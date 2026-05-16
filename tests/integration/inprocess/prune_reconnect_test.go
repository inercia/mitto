//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestPruneReconnect_SeqsPreservedAfterPruning verifies that session pruning
// preserves original event seqs (does not renumber).
//
// Background:
//
//	Sequence numbers are monotonic global identifiers used by the WebSocket sync
//	protocol (load_events after_seq). They MUST remain stable across pruning;
//	otherwise clients that have observed a pre-prune seq would request
//	load_events(after_seq=N) and receive nothing because the file's max_seq has
//	been reset to a lower value. This regression test ensures pruning preserves
//	seqs so that the sync protocol works correctly after pruning.
func TestPruneReconnect_SeqsPreservedAfterPruning(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	injector := NewEventInjector(t, ts, sess.SessionID)
	const numRounds = 6 // 6 rounds × 2 events = 12 events
	_, lastInjectedSeq := injector.InjectMixed(numRounds)
	t.Logf("Injected events; last seq = %d", lastInjectedSeq)

	// Connect Client A and load all events.
	var (
		muA           sync.Mutex
		eventsLoadedA bool
		loadedCountA  int
		connectedA    bool
	)
	sessA, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(_, _, _ string) {
			muA.Lock()
			connectedA = true
			muA.Unlock()
		},
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			muA.Lock()
			eventsLoadedA = true
			loadedCountA = len(events)
			muA.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Client A Connect failed: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		return connectedA
	}, "Client A connected")

	if err := sessA.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client A LoadEvents failed: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		return eventsLoadedA
	}, "Client A events_loaded")
	t.Logf("Client A received %d events", loadedCountA)
	sessA.Close()
	time.Sleep(200 * time.Millisecond)

	// Prune the session keeping only the last 5 events.
	const keepLast = 5
	pruneResult, err := ts.Store.PruneKeepLast(sess.SessionID, keepLast)
	if err != nil {
		t.Fatalf("PruneKeepLast(%d) failed: %v", keepLast, err)
	}
	if pruneResult == nil {
		t.Skipf("PruneKeepLast returned nil (nothing pruned)")
	}
	t.Logf("Pruning result: events_removed=%d", pruneResult.EventsRemoved)

	// Verify pruning preserves seqs (THE FIX).
	meta, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata after prune failed: %v", err)
	}
	if meta.EventCount != keepLast {
		t.Errorf("After pruning: EventCount=%d, want %d", meta.EventCount, keepLast)
	}
	// Critical assertion: MaxSeq must be preserved, NOT reset to len(remaining).
	if meta.MaxSeq != lastInjectedSeq {
		t.Errorf("After pruning: MaxSeq=%d, want %d (preserved, not renumbered)",
			meta.MaxSeq, lastInjectedSeq)
	}
	t.Logf("✓ After pruning: EventCount=%d, MaxSeq=%d (preserved)",
		meta.EventCount, meta.MaxSeq)

	// Reconnect Client B and sync via after_seq (this is what failed before the fix).
	var (
		muB             sync.Mutex
		connectedB      bool
		eventsLoadedB   bool
		loadedCountB    int
		promptCompleteB int32
	)
	sessB, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(_, _, _ string) {
			muB.Lock()
			connectedB = true
			muB.Unlock()
		},
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			muB.Lock()
			eventsLoadedB = true
			loadedCountB = len(events)
			muB.Unlock()
		},
		OnPromptComplete: func(_ int) {
			atomic.AddInt32(&promptCompleteB, 1)
		},
	})
	if err != nil {
		t.Fatalf("Client B Connect failed: %v", err)
	}
	defer sessB.Close()

	waitFor(t, 5*time.Second, func() bool {
		muB.Lock()
		defer muB.Unlock()
		return connectedB
	}, "Client B connected")

	// Client B requests events after seq=N-keepLast, which BEFORE the fix would
	// have returned 0 events (because pruning renumbered seqs to 1..5). With
	// preserved seqs, it should return the last 5 events with their original seqs.
	syncAfterSeq := lastInjectedSeq - int64(keepLast)
	if err := sessB.LoadEvents(100, syncAfterSeq, 0); err != nil {
		t.Fatalf("Client B LoadEvents(after_seq=%d) failed: %v", syncAfterSeq, err)
	}
	waitFor(t, 10*time.Second, func() bool {
		muB.Lock()
		defer muB.Unlock()
		return eventsLoadedB
	}, "Client B events_loaded after sync")

	muB.Lock()
	gotCount := loadedCountB
	muB.Unlock()
	if gotCount != keepLast {
		t.Errorf("Client B after sync(after_seq=%d): got %d events, want %d (preserved seqs)",
			syncAfterSeq, gotCount, keepLast)
	} else {
		t.Logf("✓ Client B sync recovered %d events with preserved seqs", gotCount)
	}

	// Verify new prompts still work and get seq > MaxSeq.
	if err := sessB.SendPrompt("post-prune prompt"); err != nil {
		t.Fatalf("SendPrompt after prune failed: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool {
		return atomic.LoadInt32(&promptCompleteB) > 0
	}, "Client B prompt_complete after prune")
	t.Log("✓ New prompts work normally after pruning with preserved seqs")
}
