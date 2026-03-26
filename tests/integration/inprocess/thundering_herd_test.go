//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestThunderingHerd_MultipleSessionsSync simulates N sessions all reconnecting
// simultaneously after being offline, each needing to replay a large event history.
//
// This reproduces the thundering-herd scenario observed in Mitto's logs at startup:
// when 3-5 sessions all reconnect simultaneously, each sending a load_events request,
// the server must handle concurrent replay requests without timing out.
//
// The test verifies that ALL sessions successfully sync within a reasonable time
// window — proving the server can handle concurrent load_events requests.
func TestThunderingHerd_MultipleSessionsSync(t *testing.T) {
	ts := SetupTestServer(t)

	const (
		numSessions   = 5  // simulate 5 open sessions (realistic for power users)
		roundsPerSess = 20 // 20 rounds × 2 events = 40 events per session history
	)

	// ── Phase 1: Create N sessions and inject event history into each ─────────
	sessionIDs := make([]string, numSessions)
	for i := 0; i < numSessions; i++ {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
			Name: fmt.Sprintf("thundering-herd-session-%d", i),
		})
		if err != nil {
			t.Fatalf("CreateSession[%d] failed: %v", i, err)
		}
		t.Cleanup(func() { ts.Client.DeleteSession(sess.SessionID) })
		sessionIDs[i] = sess.SessionID

		// Inject a realistic event history using the direct-injection path
		injector := NewEventInjector(t, ts, sess.SessionID)
		injector.InjectMixed(roundsPerSess) // 40 events per session
		t.Logf("Session[%d] %s: injected %d rounds (%d events)",
			i, sess.SessionID, roundsPerSess, roundsPerSess*2)
	}

	// ── Phase 2: All N clients reconnect simultaneously ───────────────────────
	// Each client connects and calls LoadEvents(50, 0, 0) — a cold-start load.
	// We measure total wall-clock time to detect serialized (thundering-herd) blocking.

	type clientResult struct {
		sessionID  string
		eventCount int
		hasMore    bool
		err        error
	}

	results := make([]clientResult, numSessions)
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	for i, sessionID := range sessionIDs {
		wg.Add(1)
		go func(i int, sessionID string) {
			defer wg.Done()

			connected := make(chan struct{})
			eventsLoaded := make(chan struct{}, 1)
			var mu sync.Mutex
			var count int
			var more bool

			sess, err := ts.Client.Connect(ctx, sessionID, client.SessionCallbacks{
				OnConnected: func(sID, clientID, acpServer string) {
					close(connected)
				},
				OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
					mu.Lock()
					count += len(events)
					more = hasMore
					mu.Unlock()
					select {
					case eventsLoaded <- struct{}{}:
					default:
					}
				},
			})
			if err != nil {
				results[i] = clientResult{sessionID: sessionID, err: fmt.Errorf("Connect: %w", err)}
				return
			}
			defer sess.Close()

			// Wait for connected message
			select {
			case <-connected:
			case <-ctx.Done():
				results[i] = clientResult{sessionID: sessionID, err: fmt.Errorf("timeout waiting for connected")}
				return
			}

			// Send load_events from seq=0 (cold start — simulates waking from sleep)
			if err := sess.LoadEvents(50, 0, 0); err != nil {
				results[i] = clientResult{sessionID: sessionID, err: fmt.Errorf("LoadEvents: %w", err)}
				return
			}

			// Wait for events_loaded response
			select {
			case <-eventsLoaded:
			case <-ctx.Done():
				results[i] = clientResult{sessionID: sessionID, err: fmt.Errorf("timeout waiting for events_loaded")}
				return
			}

			mu.Lock()
			results[i] = clientResult{sessionID: sessionID, eventCount: count, hasMore: more}
			mu.Unlock()
		}(i, sessionID)
	}

	// Wait for all clients with overall timeout
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// success — all clients finished
	case <-time.After(15 * time.Second):
		t.Fatal("thundering herd: sessions did not sync within 15s (possible serialized blocking)")
	}

	elapsed := time.Since(start)
	t.Logf("All %d sessions synced in %v", numSessions, elapsed)

	// ── Phase 3: Assertions ────────────────────────────────────────────────────
	// Wall-clock sanity: 5 sessions × 40 events each should not take longer than
	// 10s even on slow CI. Serialized handling would push this beyond 15s (timed out above).
	if elapsed > 10*time.Second {
		t.Errorf("sync took %v, expected < 10s; possible thundering-herd serialization", elapsed)
	}

	for i, r := range results {
		if r.err != nil {
			t.Errorf("Session[%d] %s error: %v", i, r.sessionID, r.err)
			continue
		}

		// Each session was injected with roundsPerSess*2 events (40 injected), but sessions
		// also have 1 initialization event created at session-creation time. We therefore
		// expect at least roundsPerSess*2 events. With limit=50 and ≤41 events, hasMore must be false.
		minExpected := roundsPerSess * 2
		if r.eventCount < minExpected {
			t.Errorf("Session[%d] %s: got %d events, expected >= %d (hasMore=%v)",
				i, r.sessionID, r.eventCount, minExpected, r.hasMore)
		} else if r.hasMore {
			t.Errorf("Session[%d] %s: hasMore=true unexpectedly with limit=50 and %d events",
				i, r.sessionID, r.eventCount)
		} else {
			t.Logf("Session[%d] %s: got %d events (hasMore=%v) ✓",
				i, r.sessionID, r.eventCount, r.hasMore)
		}
	}
}
