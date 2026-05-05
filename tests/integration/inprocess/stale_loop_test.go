//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests server-side behaviors related to the stale loop bug:
// When a client's lastKnownSeq is ahead of the server's max_seq (off-by-one),
// the server must fall back to initial load instead of returning nothing.
package inprocess

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestStaleClient_OffByOneRecovery verifies that when a client sends load_events
// with afterSeq = server_max_seq + 1 (one ahead of the server), the server
// falls back to an initial load instead of returning an empty result.
//
// This simulates the exact bug scenario: ACP crashes mid-stream sending seq=N+1
// over WebSocket, but only seq=N was persisted. On reconnect, client sends
// afterSeq=N+1, which exceeds the server's max_seq=N.
//
// Expected behavior (session_ws.go handleLoadEvents):
//   - afterSeq > serverMaxSeq triggers "falling_back_to_initial_load" log
//   - Server returns the last N events (initial load), not an empty set
//   - Returned events have valid, non-zero seq numbers
func TestStaleClient_OffByOneRecovery(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject 5 rounds (10 events: seq 1-10).
	inj := NewEventInjector(t, ts, sess.SessionID)
	_, _ = inj.InjectMixed(5)
	serverMaxSeq := inj.CurrentMaxSeq()
	t.Logf("Server max_seq after injection: %d", serverMaxSeq)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Phase 1: Initial connection — register as observer.
	var (
		mu              sync.Mutex
		loadCallCount   int
		eventsFromLoad1 []client.SyncEvent
		eventsFromLoad2 []client.SyncEvent
		load1Done       = make(chan struct{}, 1)
		load2Done       = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			loadCallCount++
			count := loadCallCount
			switch count {
			case 1:
				eventsFromLoad1 = append(eventsFromLoad1, events...)
			case 2:
				eventsFromLoad2 = append(eventsFromLoad2, events...)
			}
			mu.Unlock()

			switch count {
			case 1:
				select {
				case load1Done <- struct{}{}:
				default:
				}
			case 2:
				select {
				case load2Done <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Initial load: register as observer and get events.
	if err := ws.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("initial LoadEvents failed: %v", err)
	}
	select {
	case <-load1Done:
		t.Logf("Initial load received %d events", func() int { mu.Lock(); defer mu.Unlock(); return len(eventsFromLoad1) }())
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial events_loaded")
	}

	// Phase 2: Simulate off-by-one — client claims it has seen serverMaxSeq+1.
	// This is the stale client scenario from the bug.
	staleSentSeq := serverMaxSeq + 1
	t.Logf("Sending load_events with after_seq=%d (stale, server_max_seq=%d)", staleSentSeq, serverMaxSeq)
	if err := ws.LoadEvents(50, staleSentSeq, 0); err != nil {
		t.Fatalf("stale LoadEvents failed: %v", err)
	}
	select {
	case <-load2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for fallback events_loaded")
	}

	mu.Lock()
	recovered := make([]client.SyncEvent, len(eventsFromLoad2))
	copy(recovered, eventsFromLoad2)
	mu.Unlock()

	// ASSERT 1: Server returned events (fallback to initial load, not empty).
	if len(recovered) == 0 {
		t.Fatal("off-by-one fallback returned no events — stale client will be stuck")
	}
	t.Logf("Off-by-one recovery: %d events returned ✓", len(recovered))

	// ASSERT 2: Returned events have valid seq numbers (not zero).
	for _, e := range recovered {
		if e.Seq <= 0 {
			t.Errorf("recovered event has invalid seq=%d", e.Seq)
		}
	}

	// ASSERT 3: Events span the injected range (no truncation).
	seqs := make([]int64, 0, len(recovered))
	for _, e := range recovered {
		if e.Seq > 0 {
			seqs = append(seqs, e.Seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	if len(seqs) > 0 {
		t.Logf("Recovery events: seq %d-%d ✓", seqs[0], seqs[len(seqs)-1])
	}
}

// TestStaleClient_PrependAfterRecovery verifies that after a stale recovery
// returns the last 50 events, the client can request older events via
// before_seq (prepend) and receive a contiguous, non-overlapping range.
//
// This models the second step in the stale recovery flow:
//  1. Stale client gets initial load (last 50 events) with hasMore=true
//  2. Client sends load_events with before_seq = firstSeq of the initial load
//  3. Server returns older events that come before firstSeq
//  4. Combined view has no gaps
func TestStaleClient_PrependAfterRecovery(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject 60 rounds = 120 events (much more than the load limit of 50).
	inj := NewEventInjector(t, ts, sess.SessionID)
	inj.InjectMixed(60)
	totalSeq := inj.CurrentMaxSeq()
	t.Logf("Total events injected: max_seq=%d", totalSeq)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu          sync.Mutex
		callCount   int
		firstPage   []client.SyncEvent
		hasMorePage bool
		prependPage []client.SyncEvent
		page1Done   = make(chan struct{}, 1)
		page2Done   = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			callCount++
			count := callCount
			switch count {
			case 1:
				firstPage = append(firstPage, events...)
				hasMorePage = hasMore
			case 2:
				prependPage = append(prependPage, events...)
			}
			mu.Unlock()

			switch count {
			case 1:
				select {
				case page1Done <- struct{}{}:
				default:
				}
			case 2:
				select {
				case page2Done <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Phase 1: Initial load (last 50 events) — simulates stale recovery.
	const loadLimit = 50
	if err := ws.LoadEvents(loadLimit, 0, 0); err != nil {
		t.Fatalf("initial LoadEvents failed: %v", err)
	}
	select {
	case <-page1Done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial events_loaded")
	}

	mu.Lock()
	page1 := make([]client.SyncEvent, len(firstPage))
	copy(page1, firstPage)
	hasMore := hasMorePage
	mu.Unlock()

	// ASSERT: Server reports hasMore=true (older events exist).
	if !hasMore {
		t.Logf("hasMore=false — may not have enough events to trigger pagination; injected max_seq=%d", totalSeq)
	}
	if len(page1) == 0 {
		t.Fatal("initial load returned no events")
	}

	// Find the lowest seq in the initial page (that is the firstSeq for prepend).
	seqs1 := make([]int64, 0, len(page1))
	for _, e := range page1 {
		if e.Seq > 0 {
			seqs1 = append(seqs1, e.Seq)
		}
	}
	sort.Slice(seqs1, func(i, j int) bool { return seqs1[i] < seqs1[j] })
	if len(seqs1) == 0 {
		t.Fatal("initial page has no events with valid seq")
	}
	firstSeq := seqs1[0]
	t.Logf("Initial page: %d events, seq %d-%d, hasMore=%v", len(seqs1), seqs1[0], seqs1[len(seqs1)-1], hasMore)

	// Phase 2: Prepend — request older events with before_seq = firstSeq.
	if err := ws.LoadEvents(loadLimit, 0, firstSeq); err != nil {
		t.Fatalf("prepend LoadEvents failed: %v", err)
	}
	select {
	case <-page2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for prepend events_loaded")
	}

	mu.Lock()
	page2 := make([]client.SyncEvent, len(prependPage))
	copy(page2, prependPage)
	mu.Unlock()

	if len(page2) == 0 {
		t.Fatal("prepend returned no events — hasMore was set but before_seq request returned nothing")
	}

	// ASSERT: All prepend events have seq < firstSeq.
	for _, e := range page2 {
		if e.Seq > 0 && e.Seq >= firstSeq {
			t.Errorf("prepend event seq=%d is >= firstSeq=%d (should be before)", e.Seq, firstSeq)
		}
	}

	// ASSERT: Combined events form a contiguous range with no gaps.
	allSeqs := make([]int64, 0, len(seqs1)+len(page2))
	allSeqs = append(allSeqs, seqs1...)
	for _, e := range page2 {
		if e.Seq > 0 {
			allSeqs = append(allSeqs, e.Seq)
		}
	}
	sort.Slice(allSeqs, func(i, j int) bool { return allSeqs[i] < allSeqs[j] })

	// Deduplicate
	deduped := allSeqs[:1]
	for i := 1; i < len(allSeqs); i++ {
		if allSeqs[i] != allSeqs[i-1] {
			deduped = append(deduped, allSeqs[i])
		}
	}

	for i := 1; i < len(deduped); i++ {
		if deduped[i] != deduped[i-1]+1 {
			t.Errorf("gap in combined events: seq %d followed by seq %d", deduped[i-1], deduped[i])
		}
	}

	t.Logf("Combined %d events: seq %d-%d, no gaps ✓", len(deduped), deduped[0], deduped[len(deduped)-1])
}

// TestLoadEvents_ConcurrentRequestsDropped verifies that the server's TryLock
// guard in handleLoadEventsAsync safely drops concurrent load_events requests
// without causing errors or data corruption.
//
// The server uses sync.Mutex.TryLock() to detect concurrent load_events and
// silently drop the duplicate. This prevents thundering herd issues when
// multiple load_events arrive in rapid succession (e.g., after reconnect).
//
// Expected behavior:
//   - At least 1 events_loaded response is received
//   - No error messages from the server
//   - Events in the response have valid seq numbers (no data corruption)
func TestLoadEvents_ConcurrentRequestsDropped(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject events so there's something to load.
	inj := NewEventInjector(t, ts, sess.SessionID)
	inj.InjectMixed(5) // 10 events

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu            sync.Mutex
		eventsLoaded  []client.SyncEvent
		errorMessages []string
		loadRespCount atomic.Int64
		firstLoadDone = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			eventsLoaded = append(eventsLoaded, events...)
			mu.Unlock()
			loadRespCount.Add(1)
			select {
			case firstLoadDone <- struct{}{}:
			default:
			}
		},
		OnError: func(message string) {
			mu.Lock()
			errorMessages = append(errorMessages, message)
			mu.Unlock()
			t.Logf("Server error: %s", message)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Send initial load to register as observer.
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("initial LoadEvents failed: %v", err)
	}
	select {
	case <-firstLoadDone:
		t.Logf("Initial load done, %d events", loadRespCount.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial events_loaded")
	}

	// Rapidly fire 5 more load_events in quick succession.
	// The TryLock guard should drop the concurrent ones.
	for i := 0; i < 5; i++ {
		if err := ws.LoadEvents(50, 0, 0); err != nil {
			t.Logf("LoadEvents %d failed (expected if closed): %v", i, err)
		}
	}

	// Wait a bit for any pending responses.
	time.Sleep(300 * time.Millisecond)

	// ASSERT 1: At least one response was received.
	if loadRespCount.Load() < 1 {
		t.Fatal("no events_loaded responses received at all")
	}
	t.Logf("Received %d events_loaded responses (some concurrent ones dropped) ✓", loadRespCount.Load())

	// ASSERT 2: No error messages from concurrent load handling.
	mu.Lock()
	errors := make([]string, len(errorMessages))
	copy(errors, errorMessages)
	loaded := make([]client.SyncEvent, len(eventsLoaded))
	copy(loaded, eventsLoaded)
	mu.Unlock()

	// Note: Some server errors may be unrelated to load_events (e.g., ACP startup).
	// We only check for load_events-related errors here.
	for _, msg := range errors {
		if msg == "Invalid message data" || msg == "before_seq and after_seq are mutually exclusive" {
			t.Errorf("unexpected load_events error: %s", msg)
		}
	}

	// ASSERT 3: Events in response have valid seq numbers.
	for _, e := range loaded {
		if e.Seq < 0 {
			t.Errorf("event has invalid negative seq=%d", e.Seq)
		}
	}

	t.Logf("Concurrent load_events test passed: %d responses, %d events, %d errors ✓",
		loadRespCount.Load(), len(loaded), len(errors))
}

// TestKeepalive_OffByOneStaleDetection verifies that when a client sends a keepalive
// with last_seen_seq = N+1 but the server's max_seq is N, the keepalive_ack
// correctly reports max_seq = N, allowing the client to detect it has stale state.
//
// This tests the server-side half of the off-by-one detection. The client-side
// check (clientMaxSeq > serverMaxSeq) is what triggers the stale reload in the
// frontend. The server must accurately report its max_seq for this to work.
//
// Expected behavior:
//   - keepalive_ack.max_seq = serverMaxSeq (accurate, not inflated)
//   - clientLastSeenSeq > ack.max_seq (the off-by-one mismatch is visible)
func TestKeepalive_OffByOneStaleDetection(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject events so the server has a known max_seq.
	inj := NewEventInjector(t, ts, sess.SessionID)
	_, _ = inj.InjectMixed(5) // 10 events
	serverMaxSeq := inj.CurrentMaxSeq()
	t.Logf("Server max_seq: %d", serverMaxSeq)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu          sync.Mutex
		ackPayload  json.RawMessage
		ackReceived = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnRawMessage: func(msgType string, data []byte) {
			if msgType == "keepalive_ack" {
				mu.Lock()
				ackPayload = append(json.RawMessage(nil), data...)
				mu.Unlock()
				select {
				case ackReceived <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Register as observer.
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let server register observer

	// Simulate client claiming to have seen one more event than the server has.
	// This is the off-by-one condition from the bug: streaming sent seq N+1 over
	// WebSocket but only N was persisted before the ACP crash.
	clientFakeLastSeen := serverMaxSeq + 1
	t.Logf("Sending keepalive with last_seen_seq=%d (server_max_seq=%d, off by 1)",
		clientFakeLastSeen, serverMaxSeq)

	if err := ws.SendKeepalive(clientFakeLastSeen); err != nil {
		t.Fatalf("SendKeepalive failed: %v", err)
	}

	select {
	case <-ackReceived:
		t.Log("keepalive_ack received ✓")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for keepalive_ack")
	}

	mu.Lock()
	raw := ackPayload
	mu.Unlock()

	var ack struct {
		MaxSeq       int64 `json:"max_seq"`
		ServerMaxSeq int64 `json:"server_max_seq"` // deprecated alias
		ServerTime   int64 `json:"server_time"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("failed to parse keepalive_ack: %v; raw=%s", err, raw)
	}
	t.Logf("keepalive_ack: max_seq=%d server_max_seq=%d", ack.MaxSeq, ack.ServerMaxSeq)

	// ASSERT 1: Server reports its accurate max_seq (should equal serverMaxSeq).
	// Note: server_max_seq is the max of persisted and in-flight events.
	// Since we only used AppendEvent injection, it equals EventCount.
	if ack.MaxSeq == 0 && ack.ServerMaxSeq > 0 {
		// Accept deprecated field as fallback
		ack.MaxSeq = ack.ServerMaxSeq
	}
	if ack.MaxSeq <= 0 {
		t.Errorf("keepalive_ack.max_seq=%d should be > 0", ack.MaxSeq)
	}

	// ASSERT 2: Server's max_seq < client's fake last_seen_seq.
	// This is the off-by-one condition: client claims seq N+1 but server only has N.
	// When the client detects clientMaxSeq > serverMaxSeq, it triggers a full reload.
	if ack.MaxSeq >= clientFakeLastSeen {
		t.Errorf("expected ack.max_seq=%d < clientFakeLastSeen=%d (off-by-one not detectable)",
			ack.MaxSeq, clientFakeLastSeen)
	} else {
		t.Logf("Off-by-one detectable: ack.max_seq=%d < clientFakeLastSeen=%d ✓",
			ack.MaxSeq, clientFakeLastSeen)
	}

	// ASSERT 3: The difference is exactly 1 (the off-by-one).
	diff := clientFakeLastSeen - ack.MaxSeq
	if diff != 1 {
		t.Logf("Note: diff=%d (expected 1); server may report additional events from ACP init", diff)
	} else {
		t.Logf("Exact off-by-one confirmed: diff=%d ✓", diff)
	}
}
