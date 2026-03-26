//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// SleepWakeScenario orchestrates a "client sleeps, server advances, client wakes" test.
// It simulates a mobile client that disconnects (sleeps) while the server generates
// events by injecting them directly into the store. When the client wakes up and
// reconnects, it sends load_events with after_seq to retrieve exactly the missed events.
//
// Because events are injected directly into the store (bypassing the BackgroundSession),
// the BackgroundSession's in-memory sequence counter is not updated. The load_events
// handler reads from the store directly, so the reconnecting client still receives
// the injected events correctly.
type SleepWakeScenario struct {
	// Server is the test server to use.
	Server *TestServer

	// SessionID is the session to run the scenario against.
	SessionID string

	// RoundsBeforeSleep is the number of mixed event rounds to inject before sleeping.
	// Each round produces 2 events (user_prompt + agent_message).
	RoundsBeforeSleep int

	// RoundsDuringSleep is the number of mixed event rounds to inject while the client
	// is disconnected. These are the "missed" events the client must catch up on.
	RoundsDuringSleep int

	// SleepDuration is how long to pause between disconnect and reconnect.
	// Default 0 means disconnect/reconnect with no real delay.
	SleepDuration time.Duration

	// LoadEventsLimit is the page size passed to LoadEvents after reconnect.
	// Default 0 uses 200.
	LoadEventsLimit int
}

// SleepWakeResult holds the outcome of a SleepWakeScenario.Run() call.
type SleepWakeResult struct {
	// LastSeqBeforeSleep is the highest seq in the store after pre-sleep injection.
	// This is the after_seq value sent by the reconnecting client.
	LastSeqBeforeSleep int64

	// LastSeqAfterSleep is the highest seq in the store after during-sleep injection.
	LastSeqAfterSleep int64

	// EventsAfterReconnect contains all events received in the events_loaded response
	// after reconnection with after_seq = LastSeqBeforeSleep.
	EventsAfterReconnect []client.SyncEvent

	// ReceivedCorrectEvents is true when the client received exactly the missed events:
	// count == RoundsDuringSleep*2 and all have seq > LastSeqBeforeSleep.
	ReceivedCorrectEvents bool

	// HasMore is the hasMore flag from the first events_loaded response after reconnect.
	// True means the server has additional events beyond the page returned.
	HasMore bool
}

// Run executes the sleep/wake scenario and returns the result.
// It calls t.Fatalf on unrecoverable errors; assertion failures are recorded on
// SleepWakeResult.ReceivedCorrectEvents and logged.
func (s *SleepWakeScenario) Run(t *testing.T) *SleepWakeResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	injector := NewEventInjector(t, s.Server, s.SessionID)
	result := &SleepWakeResult{}

	// ── Phase 1: Connect a client and register as observer ──────────────────
	connected1 := make(chan struct{})
	sess1, err := s.Server.Client.Connect(ctx, s.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("SleepWake[pre-sleep]: connected clientID=%s", clientID)
			close(connected1)
		},
	})
	if err != nil {
		t.Fatalf("SleepWake: initial Connect failed: %v", err)
	}

	select {
	case <-connected1:
	case <-ctx.Done():
		t.Fatalf("SleepWake: timeout waiting for initial connection")
	}

	// Register as observer (required by server protocol)
	if err := sess1.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("SleepWake: initial LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // allow server to register observer

	// ── Phase 2: Inject events before sleep ──────────────────────────────────
	if s.RoundsBeforeSleep > 0 {
		injector.InjectMixed(s.RoundsBeforeSleep)
	}
	result.LastSeqBeforeSleep = injector.CurrentMaxSeq()
	t.Logf("SleepWake: lastSeqBeforeSleep=%d", result.LastSeqBeforeSleep)

	// ── Phase 3: Disconnect (sleep) ───────────────────────────────────────────
	_ = sess1.Close() // ignore close error; connection may already be half-closed
	time.Sleep(100 * time.Millisecond)

	if s.SleepDuration > 0 {
		t.Logf("SleepWake: sleeping for %v", s.SleepDuration)
		time.Sleep(s.SleepDuration)
	}

	// ── Phase 4: Inject events while client is sleeping ──────────────────────
	if s.RoundsDuringSleep > 0 {
		injector.InjectMixed(s.RoundsDuringSleep)
	}
	result.LastSeqAfterSleep = injector.CurrentMaxSeq()
	t.Logf("SleepWake: lastSeqAfterSleep=%d", result.LastSeqAfterSleep)

	// ── Phase 5: Reconnect and request missed events ──────────────────────────
	var mu sync.Mutex
	connected2 := make(chan struct{})
	eventsLoadedDone := make(chan struct{}, 1)

	sess2, err := s.Server.Client.Connect(ctx, s.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("SleepWake[post-sleep]: reconnected clientID=%s", clientID)
			close(connected2)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			result.EventsAfterReconnect = append(result.EventsAfterReconnect, events...)
			result.HasMore = hasMore
			mu.Unlock()
			t.Logf("SleepWake: events_loaded: %d events (hasMore=%v)", len(events), hasMore)
			select {
			case eventsLoadedDone <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("SleepWake: reconnect Connect failed: %v", err)
	}
	defer sess2.Close()

	select {
	case <-connected2:
	case <-ctx.Done():
		t.Fatalf("SleepWake: timeout waiting for reconnection")
	}

	// Request only the missed events by setting after_seq = LastSeqBeforeSleep.
	// Use LoadEventsLimit when set, otherwise fall back to a generous 200.
	limit := int64(s.LoadEventsLimit)
	if limit <= 0 {
		limit = 200
	}
	if err := sess2.LoadEvents(limit, result.LastSeqBeforeSleep, 0); err != nil {
		t.Fatalf("SleepWake: LoadEvents (after reconnect) failed: %v", err)
	}

	select {
	case <-eventsLoadedDone:
	case <-ctx.Done():
		t.Fatalf("SleepWake: timeout waiting for events_loaded after reconnect")
	}
	time.Sleep(100 * time.Millisecond) // drain any trailing messages

	// ── Phase 6: Validate ────────────────────────────────────────────────────
	mu.Lock()
	received := result.EventsAfterReconnect
	mu.Unlock()

	expectedCount := int64(s.RoundsDuringSleep * 2) // 2 events per round
	receivedCount := int64(len(received))

	allAfterSleep := true
	for _, e := range received {
		if e.Seq <= result.LastSeqBeforeSleep {
			t.Logf("SleepWake: received stale event seq=%d (≤ lastSeqBeforeSleep=%d)", e.Seq, result.LastSeqBeforeSleep)
			allAfterSleep = false
		}
	}

	result.ReceivedCorrectEvents = (receivedCount == expectedCount) && allAfterSleep
	t.Logf("SleepWake: expected=%d received=%d allAfterSleep=%v → ReceivedCorrectEvents=%v",
		expectedCount, receivedCount, allAfterSleep, result.ReceivedCorrectEvents)

	return result
}

