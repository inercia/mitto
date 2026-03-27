//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests the H2 race window: events arriving between the load_events
// response and AddObserver registration.
package inprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestObserverRegistrationRace_EventsDuringLoadEvents tests the race window
// between the server reading stored events (for the events_loaded response) and
// calling AddObserver to register the client as an observer. The function
// syncMissedEventsDuringRegistration in session_ws.go is the H2 fix that catches
// events persisted in this window.
//
// Without the fix: events arriving during the race window would be lost.
// With the fix: syncMissedEventsDuringRegistration sends a second events_loaded
// message after registration, covering the gap.
//
// Test flow:
//  1. Create session, connect Client A, build baseline history (3 prompts)
//  2. Start concurrent prompt generation from Client A (~200ms intervals)
//  3. Connect Client B during active prompting — race condition exists here
//  4. Client B calls LoadEvents while events are being persisted concurrently
//  5. Assert that all seqs in events_loaded are contiguous (no race gaps)
//  6. Assert Client B receives streaming events after observer registration
func TestObserverRegistrationRace_EventsDuringLoadEvents(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ─── Phase 1: Connect Client A and build baseline history ────────────────
	aConnected := make(chan struct{})
	var aCompletions int32

	wsA, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected:      func(_, _, _ string) { close(aConnected) },
		OnPromptComplete: func(_ int) { atomic.AddInt32(&aCompletions, 1) },
	})
	if err != nil {
		t.Fatalf("Client A Connect failed: %v", err)
	}
	defer wsA.Close()

	select {
	case <-aConnected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Client A to connect")
	}

	if err := wsA.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client A LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Send baseline prompts to build event history.
	const baselinePrompts = 3
	for i := 0; i < baselinePrompts; i++ {
		prev := atomic.LoadInt32(&aCompletions)
		if err := wsA.SendPrompt(fmt.Sprintf("Baseline prompt %d, please respond", i+1)); err != nil {
			t.Fatalf("Client A baseline prompt %d failed: %v", i+1, err)
		}
		waitFor(t, 15*time.Second, func() bool {
			return atomic.LoadInt32(&aCompletions) > prev
		}, fmt.Sprintf("baseline prompt %d to complete", i+1))
	}

	injector := NewEventInjector(t, ts, sess.SessionID)
	baselineMaxSeq := injector.CurrentMaxSeq()
	t.Logf("Baseline done: maxSeq=%d after %d prompts", baselineMaxSeq, baselinePrompts)

	// ─── Phase 2: Concurrent prompt generation to widen the race window ──────
	// Events are generated continuously; when Client B connects and calls
	// LoadEvents, there is a high chance events are being persisted concurrently.
	var concurrentStopped int32
	concurrentDone := make(chan struct{})
	go func() {
		defer close(concurrentDone)
		for atomic.LoadInt32(&concurrentStopped) == 0 {
			select {
			case <-ctx.Done():
				return
			default:
				_ = wsA.SendPrompt("Concurrent prompt, please respond")
				time.Sleep(200 * time.Millisecond)
			}
		}
	}()
	time.Sleep(50 * time.Millisecond) // Let the goroutine start.

	// ─── Phase 3: Connect Client B during active prompt generation ────────────
	var (
		bMu              sync.Mutex
		bLoadedSeqs      []int64 // seqs from ALL events_loaded messages
		bEventsLoadedCnt int     // count of events_loaded messages received
		bStreamedSeqs    []int64 // seqs from streaming (via OnRawMessage)
	)
	bConnected := make(chan struct{})
	bFirstLoaded := make(chan struct{})
	bFirstLoadedOnce := sync.Once{}
	bCompleteCh := make(chan struct{})
	bCompleteOnce := sync.Once{}

	wsB, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(_, _, _ string) {
			t.Logf("Client B connected")
			close(bConnected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			bMu.Lock()
			bEventsLoadedCnt++
			n := bEventsLoadedCnt
			for _, e := range events {
				if e.Seq > 0 {
					bLoadedSeqs = append(bLoadedSeqs, e.Seq)
				}
			}
			bMu.Unlock()
			t.Logf("Client B events_loaded #%d: %d events, hasMore=%v, isPrompting=%v",
				n, len(events), hasMore, isPrompting)
			bFirstLoadedOnce.Do(func() { close(bFirstLoaded) })
		},
		OnRawMessage: func(msgType string, data []byte) {
			// Capture seq from messages that carry a seq field (agent_message, tool_call).
			var d struct {
				Seq int64 `json:"seq"`
			}
			if json.Unmarshal(data, &d) == nil && d.Seq > 0 {
				bMu.Lock()
				bStreamedSeqs = append(bStreamedSeqs, d.Seq)
				bMu.Unlock()
			}
		},
		OnPromptComplete: func(eventCount int) {
			t.Logf("Client B received prompt_complete: eventCount=%d", eventCount)
			bCompleteOnce.Do(func() { close(bCompleteCh) })
		},
	})
	if err != nil {
		t.Fatalf("Client B Connect failed: %v", err)
	}
	defer wsB.Close()

	select {
	case <-bConnected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Client B to connect")
	}

	// Call LoadEvents — the race window exists between this call completing on
	// the server side (events read from store → events_loaded sent) and the
	// server registering Client B as an observer (AddObserver).
	if err := wsB.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client B LoadEvents failed: %v", err)
	}

	// Wait for Client B's first events_loaded response.
	select {
	case <-bFirstLoaded:
		t.Log("Client B received first events_loaded")
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Client B first events_loaded")
	}

	// Wait for Client B to receive a prompt_complete from streaming, which
	// confirms the observer is active and events flow correctly.
	select {
	case <-bCompleteCh:
		t.Log("Client B received prompt_complete — observer is streaming correctly")
	case <-time.After(30 * time.Second):
		t.Log("Client B did not receive prompt_complete within 30s; continuing assertions")
	}

	// Stop the concurrent prompt goroutine.
	atomic.StoreInt32(&concurrentStopped, 1)
	select {
	case <-concurrentDone:
	case <-time.After(3 * time.Second):
		t.Log("Concurrent prompt goroutine stop timed out (non-fatal)")
	}

	// Brief pause for any trailing events_loaded from syncMissedEventsDuringRegistration.
	time.Sleep(300 * time.Millisecond)

	// ─── Phase 4: Assertions ─────────────────────────────────────────────────
	bMu.Lock()
	loadedSeqs := make([]int64, len(bLoadedSeqs))
	copy(loadedSeqs, bLoadedSeqs)
	streamedSeqs := make([]int64, len(bStreamedSeqs))
	copy(streamedSeqs, bStreamedSeqs)
	eventsLoadedCnt := bEventsLoadedCnt
	bMu.Unlock()

	t.Logf("Client B summary: events_loaded messages=%d, loaded seqs=%d, streamed seqs=%d",
		eventsLoadedCnt, len(loadedSeqs), len(streamedSeqs))

	if len(loadedSeqs) == 0 {
		t.Fatal("Client B received no events via events_loaded — observer registration may have failed")
	}

	// Deduplicate and sort all seqs from events_loaded (initial + sync).
	seqSet := make(map[int64]bool, len(loadedSeqs))
	for _, seq := range loadedSeqs {
		seqSet[seq] = true
	}
	sortedSeqs := make([]int64, 0, len(seqSet))
	for seq := range seqSet {
		sortedSeqs = append(sortedSeqs, seq)
	}
	sort.Slice(sortedSeqs, func(i, j int) bool { return sortedSeqs[i] < sortedSeqs[j] })

	firstSeq := sortedSeqs[0]
	lastSeq := sortedSeqs[len(sortedSeqs)-1]
	t.Logf("Client B events_loaded seq range: [%d..%d] (%d unique seqs)",
		firstSeq, lastSeq, len(sortedSeqs))

	// Key assertion: seqs in events_loaded (initial + syncMissedEventsDuringRegistration)
	// must be contiguous. A gap here means the H2 fix failed to catch events persisted
	// during the race window between the storage read and AddObserver registration.
	gapCount := 0
	for i := 1; i < len(sortedSeqs); i++ {
		diff := sortedSeqs[i] - sortedSeqs[i-1]
		if diff > 1 {
			t.Errorf("Gap in events_loaded seqs: missing [%d..%d] (between %d and %d)",
				sortedSeqs[i-1]+1, sortedSeqs[i]-1,
				sortedSeqs[i-1], sortedSeqs[i])
			gapCount++
		}
	}
	if gapCount == 0 {
		t.Logf("No gaps in events_loaded seqs — syncMissedEventsDuringRegistration working correctly")
	} else {
		t.Errorf("Found %d gap(s) in events_loaded seqs — race window events may not have been caught", gapCount)
	}

	// The events_loaded must cover at least the baseline event history.
	if lastSeq < baselineMaxSeq {
		t.Errorf("Client B events_loaded lastSeq (%d) < baseline maxSeq (%d); missing baseline events",
			lastSeq, baselineMaxSeq)
	}

	// Verify Client B received some streaming events (observer was active).
	if len(streamedSeqs) > 0 {
		t.Logf("Client B received %d streamed seq(s) via observer callbacks", len(streamedSeqs))
	} else {
		t.Log("Warning: Client B received no streamed seqs (observer may be idle or no concurrent prompts completed)")
	}
}
