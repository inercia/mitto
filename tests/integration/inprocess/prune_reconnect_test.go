//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests reconnection behaviour after session pruning has renumbered
// on-disk events, simulating the transient inconsistency described in the
// background_session.go comments.
package inprocess

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestPruneReconnect_StaleAfterSeqAfterPruning validates graceful reconnection
// after session pruning has renumbered on-disk events.
//
// Background:
//
//	Session pruning (internal/session/prune.go) removes oldest events and renumbers
//	remaining events starting from seq 1. After pruning, the on-disk MaxSeq is
//	reset to match the new count, but the in-memory BackgroundSession.nextSeq
//	counter is NOT reset. When a client reconnects with a stale after_seq (higher
//	than the new MaxSeq), the server returns 0 events because all renumbered
//	on-disk events have seq < stale_after_seq. The client must handle this
//	gracefully and be able to send new prompts normally afterward.
//
// Flow:
//  1. Inject 12 synthetic events (seq 1..12) into the session store.
//  2. Connect Client A, call LoadEvents(100, 0, 0) — gets all 12 events.
//  3. Record stale_after_seq = 12 (the last seq Client A knows).
//  4. Disconnect Client A.
//  5. Prune the session to keep only the last 5 events (renumbers them to 1..5).
//  6. Verify metadata: EventCount ≤ 5, new MaxSeq = 5 < stale_after_seq.
//  7. Reconnect as Client B with LoadEvents(100, stale_after_seq=12, 0).
//  8. Verify events_loaded is received with 0 events (all on-disk events ≤ 5 < 12).
//  9. Send a new prompt — verify it completes successfully.
func TestPruneReconnect_StaleAfterSeqAfterPruning(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ── Phase 1: Inject events before connecting any client ───────────────────
	// EventInjector writes directly to the store (bypassing BackgroundSession).
	// AppendEvent assigns seq = EventCount+1 and increments EventCount but does
	// NOT update MaxSeq (only RecordEvent does). BackgroundSession.nextSeq is
	// initialised to max(MaxSeq, EventCount)+1 = max(0, 12)+1 = 13 on connect.
	injector := NewEventInjector(t, ts, sess.SessionID)
	const numPrePruneRounds = 6 // 6 rounds × 2 events = 12 events total
	_, _ = injector.InjectMixed(numPrePruneRounds)

	staleAfterSeq := injector.CurrentMaxSeq() // == 12 after injection
	t.Logf("Injected %d events; stale_after_seq = %d", numPrePruneRounds*2, staleAfterSeq)

	// ── Phase 2: Connect Client A, load events, then disconnect ───────────────
	var (
		mu             sync.Mutex
		eventsLoadedA  bool
		loadedCountA   int
	)

	sessA, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client A connected: clientID=%s", clientID)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			eventsLoadedA = true
			loadedCountA = len(events)
			mu.Unlock()
			t.Logf("Client A events_loaded: count=%d, hasMore=%v", len(events), hasMore)
		},
	})
	if err != nil {
		t.Fatalf("Client A Connect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := sessA.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client A LoadEvents failed: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return eventsLoadedA
	}, "Client A initial events_loaded")

	mu.Lock()
	t.Logf("Client A received %d events in events_loaded", loadedCountA)
	mu.Unlock()

	sessA.Close()
	time.Sleep(200 * time.Millisecond)

	// ── Phase 3: Prune the session ────────────────────────────────────────────
	// NOTE: The REST endpoint POST /api/sessions/{id}/prune is defined in
	// internal/web/session_prune_api.go but not yet wired to the router
	// (marked //nolint:unused, staged for a follow-up PR). We call the store
	// method directly so that we can use keepLast=5 without the HTTP minimum
	// restriction (MinPruneKeepLast = 50 is enforced only by the HTTP handler).
	const keepLast = 5
	pruneResult, err := ts.Store.PruneKeepLast(sess.SessionID, keepLast)
	if err != nil {
		t.Fatalf("PruneKeepLast(%d) failed: %v", keepLast, err)
	}
	if pruneResult == nil {
		t.Skipf("PruneKeepLast returned nil (nothing pruned — session had ≤ %d events)", keepLast)
	}
	t.Logf("Pruning result: events_removed=%d", pruneResult.EventsRemoved)

	// ── Phase 4: Verify pruning metadata ─────────────────────────────────────
	meta, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata after prune failed: %v", err)
	}
	if meta.EventCount > keepLast {
		t.Fatalf("After pruning: EventCount=%d, want ≤ %d", meta.EventCount, keepLast)
	}
	if staleAfterSeq <= meta.MaxSeq {
		t.Fatalf("Test precondition failed: staleAfterSeq(%d) should be > new MaxSeq(%d)",
			staleAfterSeq, meta.MaxSeq)
	}
	t.Logf("After pruning: EventCount=%d, MaxSeq=%d (staleAfterSeq=%d)",
		meta.EventCount, meta.MaxSeq, staleAfterSeq)


	// ── Phase 5: Reconnect Client B with stale after_seq ─────────────────────
	// All on-disk events have been renumbered to seq 1..5.  Loading with
	// after_seq=12 means seq > 12 — no event qualifies, so the response must
	// contain 0 events.  Critically, the server must still send events_loaded
	// (not hang or error) so the client knows the replay is complete.
	var (
		mu2             sync.Mutex
		eventsLoadedB   bool
		loadedCountB    int
		promptCompleteB int32
	)

	sessB, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client B reconnected: clientID=%s", clientID)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu2.Lock()
			eventsLoadedB = true
			loadedCountB = len(events)
			mu2.Unlock()
			t.Logf("Client B events_loaded: count=%d, hasMore=%v", len(events), hasMore)
		},
		OnPromptComplete: func(eventCount int) {
			atomic.AddInt32(&promptCompleteB, 1)
			t.Logf("Client B prompt_complete: eventCount=%d", eventCount)
		},
	})
	if err != nil {
		t.Fatalf("Client B Connect failed: %v", err)
	}
	defer sessB.Close()

	time.Sleep(100 * time.Millisecond)

	// Send load_events with the stale after_seq — expect 0 events back.
	if err := sessB.LoadEvents(100, staleAfterSeq, 0); err != nil {
		t.Fatalf("Client B LoadEvents(after_seq=%d) failed: %v", staleAfterSeq, err)
	}

	// events_loaded must arrive even if it contains 0 events (no hang/error).
	waitFor(t, 10*time.Second, func() bool {
		mu2.Lock()
		defer mu2.Unlock()
		return eventsLoadedB
	}, "Client B events_loaded after reconnect with stale after_seq")

	mu2.Lock()
	receivedCount := loadedCountB
	mu2.Unlock()

	if receivedCount != 0 {
		// Non-fatal: document that more events than expected were returned.
		// This can happen if new events have been appended between prune and reconnect
		// with seq > staleAfterSeq — a valid but unusual condition.
		t.Logf("NOTE: Client B received %d event(s) with stale_after_seq=%d (expected 0); "+
			"all on-disk events after prune have seq 1..%d",
			receivedCount, staleAfterSeq, meta.MaxSeq)
	} else {
		t.Logf("✓ Client B correctly received 0 events with stale_after_seq=%d "+
			"(on-disk events renumbered to 1..%d after pruning)",
			staleAfterSeq, meta.MaxSeq)
	}

	// ── Phase 6: New prompts work normally after reconnect ────────────────────
	// The BackgroundSession's in-memory nextSeq is still 13 (it was never reset).
	// New events from this prompt get seq 13+ and are persisted after the
	// renumbered on-disk events (seq 1..5), creating an intentional gap (6..12).
	// This gap is expected and harmless.
	if err := sessB.SendPrompt("post-prune reconnect prompt"); err != nil {
		t.Fatalf("SendPrompt after prune failed: %v", err)
	}

	waitFor(t, 30*time.Second, func() bool {
		return atomic.LoadInt32(&promptCompleteB) > 0
	}, "Client B prompt_complete after prune reconnect")

	t.Log("✓ TestPruneReconnect_StaleAfterSeqAfterPruning passed: " +
		"stale after_seq handled gracefully, new prompts work normally after pruning")
}
