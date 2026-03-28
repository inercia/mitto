//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestKeepalive_GapDetectionAndSync verifies the full keepalive-based gap detection
// and sync recovery flow:
//
//  1. Client loads an initial set of events and records its highest known seq.
//  2. Additional events are injected while the client holds a stale view.
//  3. Client sends a keepalive with its stale last_seen_seq.
//  4. Server responds with keepalive_ack.max_seq > stale seq (gap detected).
//  5. Client syncs via LoadEvents(after_seq = stale seq) and receives missed events.
//  6. Recovered events form a contiguous range with no gaps.
func TestKeepalive_GapDetectionAndSync(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Phase 1: Inject initial events the client will load and "know about".
	// Using AppendEvent-based injection so MaxSeq stays 0 and EventCount = 6.
	// getServerMaxSeq() falls back to EventCount when MaxSeq == 0.
	inj := NewEventInjector(t, ts, sess.SessionID)
	_, initialLastSeq := inj.InjectMixed(3) // 6 events: seq 1-6

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu            sync.Mutex
		ackPayload    json.RawMessage
		syncedEvents  []client.SyncEvent
		loadCallCount int
		ackReceived   = make(chan struct{}, 1)
		initialLoaded = make(chan struct{}, 1)
		syncLoaded    = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			loadCallCount++
			count := loadCallCount
			if count == 2 {
				syncedEvents = append(syncedEvents, events...)
			}
			mu.Unlock()

			switch count {
			case 1:
				t.Logf("Initial events_loaded: %d events", len(events))
				select {
				case initialLoaded <- struct{}{}:
				default:
				}
			case 2:
				t.Logf("Sync events_loaded: %d events", len(events))
				select {
				case syncLoaded <- struct{}{}:
				default:
				}
			}
		},
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

	// Register as observer and load initial events.
	if err := ws.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("initial LoadEvents failed: %v", err)
	}
	select {
	case <-initialLoaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial events_loaded")
	}

	// clientMaxSeq is the highest seq the client knows about.
	clientMaxSeq := initialLastSeq
	t.Logf("Client knows events up to seq=%d", clientMaxSeq)

	// Phase 2: Inject more events that the client does not know about (simulates
	// activity that occurred while the client had a stale connection).
	injFirstSeq, injLastSeq := inj.InjectMixed(3) // 6 more events: seq 7-12
	t.Logf("Injected missed events: seq %d-%d", injFirstSeq, injLastSeq)

	storeMaxSeq := inj.CurrentMaxSeq()
	if storeMaxSeq <= clientMaxSeq {
		t.Fatalf("Store max_seq (%d) should be > clientMaxSeq (%d) after injection",
			storeMaxSeq, clientMaxSeq)
	}
	t.Logf("Store max_seq=%d, client max_seq=%d (gap=%d events)",
		storeMaxSeq, clientMaxSeq, storeMaxSeq-clientMaxSeq)

	// Phase 3: Send keepalive with stale seq — server should report gap via max_seq.
	if err := ws.SendKeepalive(clientMaxSeq); err != nil {
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
		MaxSeq     int64 `json:"max_seq"`
		ServerTime int64 `json:"server_time"`
		ClientTime int64 `json:"client_time"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("failed to parse keepalive_ack: %v; raw=%s", err, raw)
	}
	t.Logf("keepalive_ack: max_seq=%d server_time=%d client_time=%d",
		ack.MaxSeq, ack.ServerTime, ack.ClientTime)

	// ASSERT 1: Gap detection — server max_seq must exceed the client's stale seq.
	if ack.MaxSeq <= clientMaxSeq {
		t.Errorf("keepalive_ack.max_seq=%d should be > clientMaxSeq=%d (gap not detected)",
			ack.MaxSeq, clientMaxSeq)
	} else {
		t.Logf("Gap detected: server max_seq=%d > client max_seq=%d ✓", ack.MaxSeq, clientMaxSeq)
	}

	// Phase 4: Sync recovery — request only the missed events.
	if err := ws.LoadEvents(100, clientMaxSeq, 0); err != nil {
		t.Fatalf("sync LoadEvents failed: %v", err)
	}
	select {
	case <-syncLoaded:
		t.Log("sync events_loaded received ✓")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sync events_loaded")
	}

	mu.Lock()
	recovered := make([]client.SyncEvent, len(syncedEvents))
	copy(recovered, syncedEvents)
	mu.Unlock()

	// ASSERT 2: Sync returned at least one event.
	if len(recovered) == 0 {
		t.Fatal("sync recovery returned no events")
	}

	// ASSERT 3: All recovered events have seq > clientMaxSeq (no stale events returned).
	for _, e := range recovered {
		if e.Seq > 0 && e.Seq <= clientMaxSeq {
			t.Errorf("recovered event seq=%d should be > clientMaxSeq=%d", e.Seq, clientMaxSeq)
		}
	}

	// ASSERT 4: No gaps in recovered sequence numbers.
	seqs := make([]int64, 0, len(recovered))
	for _, e := range recovered {
		if e.Seq > 0 {
			seqs = append(seqs, e.Seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Errorf("gap in recovered events: seq %d followed by seq %d (expected %d)",
				seqs[i-1], seqs[i], seqs[i-1]+1)
		}
	}

	// ASSERT 5: All injected events are present.
	expectedCount := injLastSeq - injFirstSeq + 1
	if int64(len(seqs)) < expectedCount {
		t.Errorf("recovered %d events, expected at least %d (injected seq %d-%d)",
			len(seqs), expectedCount, injFirstSeq, injLastSeq)
	}

	if len(seqs) > 0 {
		t.Logf("Sync recovery verified: %d events recovered (seq %d-%d), no gaps ✓",
			len(seqs), seqs[0], seqs[len(seqs)-1])
	}
}
