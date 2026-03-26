//go:build integration

package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestSequenceIsolation_IndependentPerSession verifies that sequence numbers are
// independent across sessions. Each session maintains its own counter starting at 1,
// and events from one session do not affect or leak into another session's sequence
// numbering.
//
// Approach: use TestEventInjector (directly into the store) for deterministic seq
// numbers, then verify both sessions have independent, monotonically-increasing
// sequences starting at 1.
func TestSequenceIsolation_IndependentPerSession(t *testing.T) {
	ts := SetupTestServer(t)

	// Create Session A and Session B.
	sessA, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession A failed: %v", err)
	}
	defer ts.Client.DeleteSession(sessA.SessionID)

	sessB, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession B failed: %v", err)
	}
	defer ts.Client.DeleteSession(sessB.SessionID)

	// Inject events directly into the store (bypasses ACP for determinism).
	// 3 rounds → 6 events (seq 1–6) for Session A.
	// 2 rounds → 4 events (seq 1–4) for Session B.
	injA := NewEventInjector(t, ts, sessA.SessionID)
	firstSeqA, lastSeqA := injA.InjectMixed(3)
	t.Logf("Session A: injected events seq %d–%d", firstSeqA, lastSeqA)

	injB := NewEventInjector(t, ts, sessB.SessionID)
	firstSeqB, lastSeqB := injB.InjectMixed(2)
	t.Logf("Session B: injected events seq %d–%d", firstSeqB, lastSeqB)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect Client A to Session A.
	var (
		muA     sync.Mutex
		eventsA []client.SyncEvent
		loadedA = make(chan struct{}, 1)
	)
	wsA, err := ts.Client.Connect(ctx, sessA.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			muA.Lock()
			eventsA = append(eventsA, events...)
			muA.Unlock()
			select {
			case loadedA <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect to Session A failed: %v", err)
	}
	defer wsA.Close()

	// Connect Client B to Session B.
	var (
		muB     sync.Mutex
		eventsB []client.SyncEvent
		loadedB = make(chan struct{}, 1)
	)
	wsB, err := ts.Client.Connect(ctx, sessB.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			muB.Lock()
			eventsB = append(eventsB, events...)
			muB.Unlock()
			select {
			case loadedB <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect to Session B failed: %v", err)
	}
	defer wsB.Close()

	// Register both clients as observers and load their events.
	if err := wsA.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents for Session A failed: %v", err)
	}
	if err := wsB.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents for Session B failed: %v", err)
	}

	// Wait for both sessions to deliver their events_loaded responses.
	select {
	case <-loadedA:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Session A events_loaded")
	}
	select {
	case <-loadedB:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Session B events_loaded")
	}

	// Brief settle to collect any trailing callbacks.
	time.Sleep(200 * time.Millisecond)

	// Snapshot under lock.
	muA.Lock()
	snapshotA := make([]client.SyncEvent, len(eventsA))
	copy(snapshotA, eventsA)
	muA.Unlock()

	muB.Lock()
	snapshotB := make([]client.SyncEvent, len(eventsB))
	copy(snapshotB, eventsB)
	muB.Unlock()

	// --- Assertions ---

	if len(snapshotA) == 0 {
		t.Fatal("Session A: no events loaded")
	}
	if len(snapshotB) == 0 {
		t.Fatal("Session B: no events loaded")
	}
	t.Logf("Session A loaded %d event(s); Session B loaded %d event(s)",
		len(snapshotA), len(snapshotB))

	seqsA := seqsFromEvents(snapshotA)
	seqsB := seqsFromEvents(snapshotB)

	if len(seqsA) == 0 {
		t.Fatal("Session A: no events with seq > 0")
	}
	if len(seqsB) == 0 {
		t.Fatal("Session B: no events with seq > 0")
	}

	// Both sessions must start at seq 1.
	// Each session has its own counter beginning at 1, regardless of what the other
	// session does. If a global counter were shared, Session B would start at
	// seq > maxSeqA (e.g., seq 8 if A had 7 events).
	if seqsA[0] != 1 {
		t.Errorf("Session A: first seq = %d, want 1 (independent counter must start at 1)", seqsA[0])
	}
	if seqsB[0] != 1 {
		t.Errorf("Session B: first seq = %d, want 1 (must start independently from 1, not continue Session A's counter)", seqsB[0])
	}

	// Injected events must be present (last injected seq must appear in loaded events).
	if seqsA[len(seqsA)-1] < lastSeqA {
		t.Errorf("Session A: last loaded seq = %d, want >= %d (injected events missing)", seqsA[len(seqsA)-1], lastSeqA)
	}
	if seqsB[len(seqsB)-1] < lastSeqB {
		t.Errorf("Session B: last loaded seq = %d, want >= %d (injected events missing)", seqsB[len(seqsB)-1], lastSeqB)
	}

	// Monotonicity within Session A.
	for i := 1; i < len(seqsA); i++ {
		if seqsA[i] <= seqsA[i-1] {
			t.Errorf("Session A: seq not monotonically increasing at [%d]: %d <= %d",
				i, seqsA[i], seqsA[i-1])
		}
	}

	// Monotonicity within Session B.
	for i := 1; i < len(seqsB); i++ {
		if seqsB[i] <= seqsB[i-1] {
			t.Errorf("Session B: seq not monotonically increasing at [%d]: %d <= %d",
				i, seqsB[i], seqsB[i-1])
		}
	}

	// Cross-contamination check: Session B's seq values must lie in [1, totalEventsB].
	// If a global counter were shared with Session A, Session B's seq values would be
	// offset by Session A's event count (e.g., start at 8 if A had 7 events).
	maxSeqA := seqsA[len(seqsA)-1]
	maxSeqB := seqsB[len(seqsB)-1]

	// Session B's max seq must equal its own total event count (len(seqsB)).
	// If leaking from A, it would be maxSeqA + len(seqsB) instead.
	if maxSeqB != int64(len(seqsB)) {
		t.Errorf("Session B: max seq = %d, want %d (= its own event count); would be %d if counter leaked from Session A",
			maxSeqB, int64(len(seqsB)), maxSeqA+int64(len(seqsB)))
	}

	t.Logf("Sequence isolation verified: Session A seq [1,%d] (%d events), Session B seq [1,%d] (%d events) — independent counters ✓",
		maxSeqA, len(seqsA), maxSeqB, len(seqsB))
}

// seqsFromEvents extracts sorted, deduplicated sequence numbers (seq > 0) from
// a slice of SyncEvents. Events returned by LoadEvents are already in ascending
// order, so the result preserves that order.
func seqsFromEvents(events []client.SyncEvent) []int64 {
	seen := make(map[int64]bool, len(events))
	seqs := make([]int64, 0, len(events))
	for _, e := range events {
		if e.Seq > 0 && !seen[e.Seq] {
			seen[e.Seq] = true
			seqs = append(seqs, e.Seq)
		}
	}
	return seqs
}

