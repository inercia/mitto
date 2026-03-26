//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests that a slow/stuck WebSocket client does NOT degrade message
// delivery to healthy co-observers connected to the same session.
package inprocess

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/client"
)

// TestObserverBlocking_SlowClientDoesNotDegradeOthers verifies the key invariant:
// if one WebSocket client is slow/stuck (never reads), healthy co-observers must
// still receive all agent messages with minimal added latency.
//
// The test sets up three clients on the same session:
//   - Client A (slow): raw gorilla WebSocket that sends load_events but never reads.
//     Its send buffer will eventually fill and backpressure will close it.
//   - Client B (healthy): normal client.Session tracking all received messages.
//   - Client C (healthy): normal client.Session tracking all received messages.
//
// B sends a prompt and the test asserts that both B and C receive all agent
// messages and prompt_complete within a 10-second window.
func TestObserverBlocking_SlowClientDoesNotDegradeOthers(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Client A: slow raw WebSocket – never reads after load_events ──────────
	// The API prefix "/mitto" must match what the server uses (see client.New default).
	wsURL := strings.Replace(ts.HTTPServer.URL, "http://", "ws://", 1) +
		"/mitto/api/sessions/" + session.SessionID + "/ws"
	rawConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Raw WebSocket dial failed: %v", err)
	}
	// Register client A as an observer so the server tries to push events to it.
	// After this, we deliberately never read – causing the channel/TCP buffer to fill.
	err = rawConn.WriteJSON(map[string]interface{}{
		"type": "load_events",
		"data": map[string]interface{}{"limit": 100},
	})
	if err != nil {
		t.Fatalf("Client A WriteJSON(load_events) failed: %v", err)
	}
	// We intentionally do NOT call rawConn.ReadMessage() again.
	// rawConn.Close() is deferred at the end so test cleanup is orderly.
	defer rawConn.Close()

	// ── Client B: healthy observer ────────────────────────────────────────────
	var (
		bMu         sync.Mutex
		bMessages   []string
		bComplete   int32 // atomic flag
		bCompleteCh = make(chan struct{})
	)
	bConnected := make(chan struct{})

	wsB, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client B connected: clientID=%s", clientID)
			close(bConnected)
		},
		OnAgentMessage: func(html string) {
			bMu.Lock()
			bMessages = append(bMessages, html)
			bMu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			t.Logf("Client B received prompt_complete: eventCount=%d", eventCount)
			if atomic.CompareAndSwapInt32(&bComplete, 0, 1) {
				close(bCompleteCh)
			}
		},
	})
	if err != nil {
		t.Fatalf("Client B Connect failed: %v", err)
	}
	defer wsB.Close()

	// ── Client C: healthy observer ────────────────────────────────────────────
	var (
		cMu         sync.Mutex
		cMessages   []string
		cComplete   int32 // atomic flag
		cCompleteCh = make(chan struct{})
	)
	cConnected := make(chan struct{})

	wsC, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client C connected: clientID=%s", clientID)
			close(cConnected)
		},
		OnAgentMessage: func(html string) {
			cMu.Lock()
			cMessages = append(cMessages, html)
			cMu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			t.Logf("Client C received prompt_complete: eventCount=%d", eventCount)
			if atomic.CompareAndSwapInt32(&cComplete, 0, 1) {
				close(cCompleteCh)
			}
		},
	})
	if err != nil {
		t.Fatalf("Client C Connect failed: %v", err)
	}
	defer wsC.Close()

	// Wait for healthy clients to connect before registering observers.
	select {
	case <-bConnected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Client B to connect")
	}
	select {
	case <-cConnected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for Client C to connect")
	}

	// Register B and C as observers (required before streaming events are received).
	if err := wsB.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client B LoadEvents failed: %v", err)
	}
	if err := wsC.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client C LoadEvents failed: %v", err)
	}

	// Short delay to ensure all three clients are fully registered as observers
	// before we trigger agent output (including client A via its load_events above).
	time.Sleep(300 * time.Millisecond)

	// ── Send prompt via Client B ───────────────────────────────────────────────
	start := time.Now()
	if err := wsB.SendPrompt("Hello, tell me about testing"); err != nil {
		t.Fatalf("Client B SendPrompt failed: %v", err)
	}

	// ── Assert: B and C receive prompt_complete within 10 s ───────────────────
	select {
	case <-bCompleteCh:
		t.Logf("Client B prompt_complete received in %v", time.Since(start))
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out (10s) waiting for Client B prompt_complete – slow client may be blocking")
	}
	select {
	case <-cCompleteCh:
		t.Logf("Client C prompt_complete received in %v", time.Since(start))
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out (10s) waiting for Client C prompt_complete – slow client may be blocking")
	}

	elapsed := time.Since(start)

	// ── Verify message counts ─────────────────────────────────────────────────
	bMu.Lock()
	bCount := len(bMessages)
	bMu.Unlock()

	cMu.Lock()
	cCount := len(cMessages)
	cMu.Unlock()

	if bCount == 0 {
		t.Error("Client B received no agent messages")
	}
	if cCount == 0 {
		t.Error("Client C received no agent messages")
	}

	// Healthy clients must see the same number of streaming chunks.
	if bCount != cCount {
		t.Errorf("Message count mismatch: Client B got %d, Client C got %d", bCount, cCount)
	}

	// Delivery to healthy clients should be well under 5 seconds regardless of
	// client A's slow behaviour.
	if elapsed > 5*time.Second {
		t.Errorf("Delivery to B and C took %v; expected < 5s (slow client should not block)", elapsed)
	}

	t.Logf("Client B: %d messages, Client C: %d messages, elapsed: %v", bCount, cCount, elapsed)

	// ── Verify client A's connection eventually becomes unusable ──────────────
	// Set a short read deadline and attempt a read. Because client A never drained
	// its receive side the server will eventually close the connection (or we time
	// out from the deadline). Either outcome is acceptable – the important thing is
	// that B and C were not affected.
	rawConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, readErr := rawConn.ReadMessage()
	t.Logf("Client A read after test: %v (connection closed or timed out – expected)", readErr)
}

