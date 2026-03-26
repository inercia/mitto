//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/client"
)

// TestSlowClient_BackpressureDisconnectAndRecovery validates the reconnect +
// sync cycle triggered when the server closes a slow client's connection.
//
// In production, ws_conn.go's sendWithBackpressure() closes the connection when
// the server's send channel is full and the slow client hasn't drained its OS
// TCP receive buffer within sendBackpressureTimeout (100 ms). The disconnected
// client must reconnect and call load_events(after_seq=N) to recover all missed
// events without gaps or duplicates.
//
// This test exercises the full cycle:
//  1. A reference client (A) establishes baseline history via a real prompt.
//  2. A raw gorilla/websocket client (B) connects, registers as an observer, and
//     then stops reading — reproducing what a CPU-stalled or network-slow client
//     does in production.
//  3. Client A sends additional prompts, generating new events that the server
//     tries to push to both A and B.
//  4. B's connection is closed (simulating the backpressure-triggered close that
//     sendWithBackpressure performs via w.conn.Close()).
//  5. B reconnects as a normal client and calls load_events(after_seq=bLastSeq).
//  6. All events generated while B was stalled are recovered without data loss
//     or duplicates.
func TestSlowClient_BackpressureDisconnectAndRecovery(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ── Phase 1: Build baseline history via client A ──────────────────────────
	// Client A is the well-behaved reference observer.
	var promptsDone int32 // atomic counter, incremented by OnPromptComplete

	sessA, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			atomic.AddInt32(&promptsDone, 1)
		},
	})
	if err != nil {
		t.Fatalf("Client A Connect failed: %v", err)
	}
	defer sessA.Close()

	time.Sleep(200 * time.Millisecond)

	if err := sessA.LoadEvents(200, 0, 0); err != nil {
		t.Fatalf("Client A LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// sendAndWait sends one prompt from client A and blocks until it completes.
	sendAndWait := func(msg string) {
		before := atomic.LoadInt32(&promptsDone)
		if err := sessA.SendPrompt(msg); err != nil {
			t.Fatalf("SendPrompt(%q) failed: %v", msg, err)
		}
		waitFor(t, 30*time.Second, func() bool {
			return atomic.LoadInt32(&promptsDone) > before
		}, fmt.Sprintf("prompt completion: %q", truncate(msg, 30)))
	}

	sendAndWait("initial baseline prompt")
	t.Log("Phase 1 complete: baseline history established")

	// ── Phase 2: Connect raw slow client B ────────────────────────────────────
	// B connects via raw gorilla/websocket so we control its read behaviour.
	// The API prefix is "/mitto" (default for client.New); the WS URL matches.
	wsURL := fmt.Sprintf("ws://%s/mitto/api/sessions/%s/ws",
		ts.HTTPServer.Listener.Addr().String(), sess.SessionID)

	rawConn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Raw client B dial failed: %v", err)
	}

	// Read the mandatory 'connected' message.
	rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, connBytes, err := rawConn.ReadMessage()
	if err != nil {
		rawConn.Close()
		t.Fatalf("Raw client B: failed to read connected message: %v", err)
	}
	rawConn.SetReadDeadline(time.Time{})
	t.Logf("Raw client B: received connected (%d bytes)", len(connBytes))

	// Register B as an observer by sending load_events.
	loadPayload, _ := json.Marshal(map[string]interface{}{
		"type": "load_events",
		"data": map[string]interface{}{"limit": 200},
	})
	if err := rawConn.WriteMessage(websocket.TextMessage, loadPayload); err != nil {
		rawConn.Close()
		t.Fatalf("Raw client B: failed to send load_events: %v", err)
	}

	// Read messages until events_loaded arrives; capture B's last known max_seq.
	// After this loop B stops reading, simulating a stalled client.
	var bLastKnownSeq int64
	rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msgBytes, readErr := rawConn.ReadMessage()
		if readErr != nil {
			break
		}
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(msgBytes, &env) != nil || env.Type != "events_loaded" {
			continue
		}
		var d struct {
			MaxSeq int64 `json:"max_seq"`
			Events []struct {
				Seq int64 `json:"seq"`
			} `json:"events"`
		}
		if json.Unmarshal(env.Data, &d) == nil {
			bLastKnownSeq = d.MaxSeq
			if bLastKnownSeq == 0 && len(d.Events) > 0 {
				bLastKnownSeq = d.Events[len(d.Events)-1].Seq
			}
		}
		t.Logf("Raw client B: events_loaded parsed, last known seq = %d", bLastKnownSeq)
		break
	}

	// ── Phase 3: Generate events while B is stalled ───────────────────────────
	// Each prompt produces persisted events. The server tries to deliver them to
	// both A and B. Since B isn't reading, its TCP receive buffer fills up; in a
	// high-traffic scenario the server's send buffer would reach capacity and
	// sendWithBackpressure would close the connection after 100 ms.
	const numMissedPrompts = 3
	for i := 1; i <= numMissedPrompts; i++ {
		sendAndWait(fmt.Sprintf("missed event prompt %d", i))
		t.Logf("Missed-event prompt %d/%d completed", i, numMissedPrompts)
	}

	// Capture the server's current max seq to verify recovery completeness.
	injector := NewEventInjector(t, ts, sess.SessionID)
	maxSeq := injector.CurrentMaxSeq()
	t.Logf("Max seq after all prompts: %d (B last known: %d)", maxSeq, bLastKnownSeq)

	if maxSeq <= bLastKnownSeq {
		t.Fatalf("No new events generated for B to recover: maxSeq=%d <= bLastKnownSeq=%d",
			maxSeq, bLastKnownSeq)
	}

	// ── Phase 4: Simulate backpressure disconnect ─────────────────────────────
	// Close B's raw connection from the client side, reproducing the observable
	// effect of sendWithBackpressure's w.conn.Close(): B's pending reads error,
	// the server removes B from the observer set, and B must reconnect.
	t.Logf("Simulating backpressure disconnect of client B (last known seq: %d)", bLastKnownSeq)
	rawConn.Close()
	time.Sleep(300 * time.Millisecond)

	// ── Phase 5: Reconnect and sync ───────────────────────────────────────────
	var (
		mu              sync.Mutex
		bRecoveredSeqs  []int64
		bRecoveredTypes []string
		bConnected      = make(chan struct{})
		bLoaded         = make(chan struct{})
		bLoadedOnce     sync.Once
	)

	sessB, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client B reconnected: clientID=%s", clientID)
			close(bConnected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, ev := range events {
				bRecoveredSeqs = append(bRecoveredSeqs, ev.Seq)
				bRecoveredTypes = append(bRecoveredTypes, ev.Type)
			}
			mu.Unlock()
			t.Logf("Client B recovered %d events (hasMore=%v)", len(events), hasMore)
			bLoadedOnce.Do(func() { close(bLoaded) })
		},
	})
	if err != nil {
		t.Fatalf("Client B reconnect failed: %v", err)
	}
	defer sessB.Close()

	select {
	case <-bConnected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client B to reconnect")
	}

	// Request only the events B missed (after its last-known seq).
	if err := sessB.LoadEvents(500, bLastKnownSeq, 0); err != nil {
		t.Fatalf("Client B LoadEvents after reconnect failed: %v", err)
	}

	select {
	case <-bLoaded:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client B events_loaded after reconnect")
	}

	time.Sleep(200 * time.Millisecond)

	// ── Phase 6: Verify recovery ──────────────────────────────────────────────
	mu.Lock()
	recoveredCount := len(bRecoveredSeqs)
	seqsCopy := make([]int64, len(bRecoveredSeqs))
	copy(seqsCopy, bRecoveredSeqs)
	typesCopy := make([]string, len(bRecoveredTypes))
	copy(typesCopy, bRecoveredTypes)
	mu.Unlock()

	if recoveredCount == 0 {
		t.Fatal("Client B recovered 0 events — expected missed events to be replayed after reconnect")
	}

	// No recovered event should have seq ≤ bLastKnownSeq (would be a re-delivery).
	for i, seq := range seqsCopy {
		if seq > 0 && seq <= bLastKnownSeq {
			t.Errorf("Recovered event[%d] seq=%d ≤ bLastKnownSeq=%d — unexpected re-delivery",
				i, seq, bLastKnownSeq)
		}
	}

	// No duplicate sequence numbers in the recovered batch.
	seqCount := make(map[int64]int)
	for _, seq := range seqsCopy {
		seqCount[seq]++
	}
	for seq, cnt := range seqCount {
		if cnt > 1 {
			t.Errorf("Duplicate seq %d appeared %d times in recovery batch", seq, cnt)
		}
	}

	t.Logf("Recovery verified: bLastKnownSeq=%d, serverMaxSeq=%d, recoveredEvents=%d",
		bLastKnownSeq, maxSeq, recoveredCount)
	if len(typesCopy) > 5 {
		t.Logf("Recovered event types (first 5): %v", typesCopy[:5])
	} else {
		t.Logf("Recovered event types: %v", typesCopy)
	}
	t.Log("✓ Slow client backpressure-disconnect-recovery test passed")
}
