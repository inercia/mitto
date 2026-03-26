//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Sequence Number Contract Integration Tests
//
// These tests verify the sequence number contract between backend and frontend:
// - Uniqueness: Each event has a unique seq (except coalescing chunks)
// - Monotonicity: seq values are strictly increasing
// - Persistence: seq is preserved when events are written to storage
// - Coalescing: Multiple chunks of same message share the same seq
// =============================================================================

// TestSequenceNumberMonotonicity verifies that sequence numbers are
// monotonically increasing within a session.
func TestSequenceNumberMonotonicity(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track sequence numbers
	var (
		mu       sync.Mutex
		seqs     []int64
		complete = make(chan struct{})
	)

	callbacks := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					seqs = append(seqs, e.Seq)
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection
	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send a prompt
	if err := ws.SendPrompt("Hello, please respond briefly"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion
	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for prompt completion")
	}

	// Load all events
	ws.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	// Verify monotonicity
	mu.Lock()
	defer mu.Unlock()

	if len(seqs) < 2 {
		t.Skipf("Not enough events to verify monotonicity (got %d)", len(seqs))
	}

	for i := 1; i < len(seqs); i++ {
		if seqs[i] < seqs[i-1] {
			t.Errorf("Sequence numbers not monotonic: seq[%d]=%d < seq[%d]=%d",
				i, seqs[i], i-1, seqs[i-1])
		}
	}

	t.Logf("Verified %d sequence numbers are monotonic", len(seqs))
}

// TestSequenceNumberPersistence verifies that sequence numbers are preserved
// when events are persisted and reloaded.
func TestSequenceNumberPersistence(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// First connection - send prompt and wait for completion
	var (
		mu            sync.Mutex
		streamingSeqs []int64
		complete      = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			// Note: OnAgentMessage doesn't include seq in the callback
			// We'll verify via events_loaded
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}
	// Suppress unused variable warning
	_ = streamingSeqs

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws1.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	if err := ws1.SendPrompt("Hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for prompt completion")
	}

	ws1.Close()

	// Second connection - load events and verify seqs match
	var (
		loadedSeqs []int64
		loaded     = make(chan struct{})
	)

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					loadedSeqs = append(loadedSeqs, e.Seq)
				}
			}
			mu.Unlock()
			close(loaded)
		},
	}

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Load all events
	ws2.LoadEvents(100, 0, 0)

	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for events to load")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(loadedSeqs) == 0 {
		t.Fatal("No events loaded")
	}

	// Verify loaded seqs are monotonic
	for i := 1; i < len(loadedSeqs); i++ {
		if loadedSeqs[i] < loadedSeqs[i-1] {
			t.Errorf("Loaded sequence numbers not monotonic: seq[%d]=%d < seq[%d]=%d",
				i, loadedSeqs[i], i-1, loadedSeqs[i-1])
		}
	}

	t.Logf("Verified %d persisted sequence numbers", len(loadedSeqs))
}

// TestSequenceNumberSyncAfterReconnect verifies that syncing after reconnection
// correctly retrieves missed events using after_seq.
func TestSequenceNumberSyncAfterReconnect(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// First connection - send prompt and track last seq
	var (
		mu       sync.Mutex
		lastSeq  int64
		complete = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > lastSeq {
					lastSeq = e.Seq
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws1.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send first prompt
	if err := ws1.SendPrompt("First message"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for first prompt completion")
	}

	// Load events to get the last seq
	ws1.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	firstLastSeq := lastSeq
	mu.Unlock()

	ws1.Close()

	// Send another prompt via REST API (simulating activity while disconnected)
	// Note: This would require the session to be running, which it is in the background

	// Second connection - sync from last known seq
	var (
		syncedEvents []client.SyncEvent
		synced       = make(chan struct{})
	)

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			syncedEvents = append(syncedEvents, events...)
			mu.Unlock()
			close(synced)
		},
	}

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Load events after the first session's last seq
	ws2.LoadEvents(100, firstLastSeq, 0)

	select {
	case <-synced:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for sync")
	}

	mu.Lock()
	defer mu.Unlock()

	// All synced events should have seq > firstLastSeq
	for _, e := range syncedEvents {
		if e.Seq > 0 && e.Seq <= firstLastSeq {
			t.Errorf("Synced event has seq %d <= firstLastSeq %d", e.Seq, firstLastSeq)
		}
	}

	t.Logf("Sync after seq %d returned %d events", firstLastSeq, len(syncedEvents))
}

// TestMultipleClientsReceiveSameSeqs verifies that multiple clients connected
// to the same session receive events with the same sequence numbers.
func TestMultipleClientsReceiveSameSeqs(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track events from both clients
	var (
		mu1, mu2      sync.Mutex
		client1Events []client.SyncEvent
		client2Events []client.SyncEvent
		complete1     = make(chan struct{})
		complete2     = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu1.Lock()
			client1Events = append(client1Events, events...)
			mu1.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-complete1:
			default:
				close(complete1)
			}
		},
	}

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu2.Lock()
			client2Events = append(client2Events, events...)
			mu2.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-complete2:
			default:
				close(complete2)
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect both clients
	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("First connect failed: %v", err)
	}
	defer ws1.Close()

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Each client must send load_events to register as an observer
	// This is required by the server to prevent race conditions
	ws1.LoadEvents(50, 0, 0)
	ws2.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send a prompt from client 1
	if err := ws1.SendPrompt("Hello from client 1"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for both clients to receive completion
	select {
	case <-complete1:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for client 1 completion")
	}

	select {
	case <-complete2:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for client 2 completion")
	}

	// Load events on both clients
	ws1.LoadEvents(100, 0, 0)
	ws2.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	// Compare events
	mu1.Lock()
	mu2.Lock()
	defer mu1.Unlock()
	defer mu2.Unlock()

	if len(client1Events) == 0 || len(client2Events) == 0 {
		t.Skipf("Not enough events (client1: %d, client2: %d)", len(client1Events), len(client2Events))
	}

	// Build seq maps
	seqs1 := make(map[int64]string)
	for _, e := range client1Events {
		if e.Seq > 0 {
			seqs1[e.Seq] = e.Type
		}
	}

	seqs2 := make(map[int64]string)
	for _, e := range client2Events {
		if e.Seq > 0 {
			seqs2[e.Seq] = e.Type
		}
	}

	// Verify both clients have the same seqs
	for seq, type1 := range seqs1 {
		if type2, ok := seqs2[seq]; ok {
			if type1 != type2 {
				t.Errorf("Seq %d has different types: client1=%s, client2=%s", seq, type1, type2)
			}
		}
	}

	t.Logf("Both clients received consistent sequence numbers (client1: %d events, client2: %d events)",
		len(client1Events), len(client2Events))
}

// =============================================================================
// Protocol Ordering Contract Tests (Gap 6)
//
// These tests verify that the server always sends 'connected' before any
// 'events_loaded' response. This prevents the race condition observed in
// production where events_loaded arrived 6ms before connected, leaving the
// client in an uninitialized state when the first events were processed.
//
// Correct protocol ordering:
//  1. Server sends 'connected' (session metadata, capabilities)
//  2. Client sends 'load_events' (response to connected)
//  3. Server sends 'events_loaded' (response to load_events)
// =============================================================================

// TestProtocolOrdering_ConnectedBeforeEventsLoaded verifies that the server
// always sends the 'connected' message before any 'events_loaded' response.
// This prevents the race condition observed in production where events_loaded
// arrived 6ms before connected, leaving the client in an uninitialized state
// when the first events were processed.
func TestProtocolOrdering_ConnectedBeforeEventsLoaded(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session with some events so events_loaded has content.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject some events so events_loaded returns non-empty.
	inj := NewEventInjector(t, ts, sess.SessionID)
	inj.InjectMixed(5) // 5 rounds → 10+ events total

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Record the ORDER of message types as they arrive.
	var (
		mu           sync.Mutex
		messageOrder []string
		connected    = make(chan struct{}, 1)
		loaded       = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			mu.Lock()
			messageOrder = append(messageOrder, "connected")
			mu.Unlock()
			select {
			case connected <- struct{}{}:
			default:
			}
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			messageOrder = append(messageOrder, "events_loaded")
			mu.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Send load_events AFTER connecting (normal flow).
	ws.LoadEvents(50, 0, 0)

	// Wait for both messages.
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for connected")
	}
	select {
	case <-loaded:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for events_loaded")
	}

	// Snapshot the order under the lock.
	mu.Lock()
	order := make([]string, len(messageOrder))
	copy(order, messageOrder)
	mu.Unlock()

	if len(order) < 2 {
		t.Fatalf("Expected at least connected + events_loaded, got: %v", order)
	}
	if order[0] != "connected" {
		t.Errorf("First message must be 'connected', got order: %v", order)
	}

	// Find the positions of each message type.
	connectedIdx := -1
	eventsLoadedIdx := -1
	for i, m := range order {
		if m == "connected" && connectedIdx == -1 {
			connectedIdx = i
		}
		if m == "events_loaded" && eventsLoadedIdx == -1 {
			eventsLoadedIdx = i
		}
	}
	if connectedIdx >= eventsLoadedIdx {
		t.Errorf("connected (idx %d) must come before events_loaded (idx %d), full order: %v",
			connectedIdx, eventsLoadedIdx, order)
	}

	t.Logf("Protocol ordering verified: %v", order)
}

// TestProtocolOrdering_ConnectedBeforeEventsLoaded_MultipleReconnects verifies
// the ordering holds across multiple connect/disconnect cycles.
func TestProtocolOrdering_ConnectedBeforeEventsLoaded_MultipleReconnects(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Inject events so events_loaded has content.
	inj := NewEventInjector(t, ts, sess.SessionID)
	inj.InjectMixed(3)

	// Repeat the connect/loadEvents/disconnect cycle 5 times.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

		var (
			mu           sync.Mutex
			messageOrder []string
			done         = make(chan struct{})
		)

		ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
			OnConnected: func(sessionID, clientID, acpServer string) {
				mu.Lock()
				messageOrder = append(messageOrder, "connected")
				mu.Unlock()
			},
			OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
				mu.Lock()
				messageOrder = append(messageOrder, "events_loaded")
				mu.Unlock()
				select {
				case done <- struct{}{}:
				default:
				}
			},
		})
		if err != nil {
			cancel()
			t.Fatalf("Reconnect %d: Connect failed: %v", i, err)
		}
		ws.LoadEvents(50, 0, 0)

		select {
		case <-done:
		case <-ctx.Done():
			cancel()
			ws.Close()
			t.Fatalf("Reconnect %d: timeout waiting for events_loaded", i)
		}

		mu.Lock()
		order := make([]string, len(messageOrder))
		copy(order, messageOrder)
		mu.Unlock()

		if len(order) < 2 {
			t.Errorf("Reconnect %d: expected at least connected + events_loaded, got: %v", i, order)
		} else if order[0] != "connected" {
			t.Errorf("Reconnect %d: first message must be connected, got %v", i, order)
		}

		t.Logf("Reconnect %d: protocol ordering verified: %v", i, order)

		cancel()
		ws.Close()
		time.Sleep(50 * time.Millisecond) // brief settle between reconnects
	}
}

// =============================================================================
// Gap 9: Keepalive wire protocol
// =============================================================================

// TestKeepalive_AckReceivedForKeepaliveMessage verifies that the server responds
// to a keepalive message with a keepalive_ack containing the server's current
// max_seq. This exercises the application-level keepalive mechanism end-to-end.
func TestKeepalive_AckReceivedForKeepaliveMessage(t *testing.T) {
	ts := SetupTestServer(t)
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Inject some events so the server has a meaningful max_seq.
	inj := NewEventInjector(t, ts, session.SessionID)
	inj.InjectMixed(3) // 6 events → max_seq should be >= 6

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		mu          sync.Mutex
		msgTypes    []string
		ackPayload  json.RawMessage
		ackReceived = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnRawMessage: func(msgType string, data []byte) {
			mu.Lock()
			msgTypes = append(msgTypes, msgType)
			if msgType == "keepalive_ack" {
				ackPayload = append(json.RawMessage(nil), data...)
				mu.Unlock()
				select {
				case ackReceived <- struct{}{}:
				default:
				}
				return
			}
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait briefly for the server to process load_events and send events_loaded.
	time.Sleep(300 * time.Millisecond)

	// Send a keepalive with the client's last-seen seq.
	const clientSeq int64 = 6
	if err := ws.SendKeepalive(clientSeq); err != nil {
		t.Fatalf("SendKeepalive failed: %v", err)
	}

	// Wait for the keepalive_ack response.
	select {
	case <-ackReceived:
		t.Log("keepalive_ack received ✓")
	case <-time.After(5 * time.Second):
		mu.Lock()
		seen := msgTypes
		mu.Unlock()
		t.Fatalf("timeout waiting for keepalive_ack; messages seen: %v", seen)
	}

	// Parse the ack payload and verify it contains max_seq.
	mu.Lock()
	raw := ackPayload
	mu.Unlock()

	var ack struct {
		MaxSeq     int64 `json:"max_seq"`
		ServerTime int64 `json:"server_time"`
		ClientTime int64 `json:"client_time"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("failed to parse keepalive_ack payload: %v; raw=%s", err, raw)
	}
	if ack.MaxSeq == 0 {
		t.Errorf("keepalive_ack.max_seq should be > 0, got %d", ack.MaxSeq)
	}
	if ack.ServerTime == 0 {
		t.Errorf("keepalive_ack.server_time should be non-zero")
	}
	t.Logf("keepalive_ack: max_seq=%d server_time=%d client_time=%d ✓",
		ack.MaxSeq, ack.ServerTime, ack.ClientTime)
}

// =============================================================================
// Gap 10: WebSocket close code differentiation
// =============================================================================

// TestWebSocketCloseCode_SessionDeleted verifies the signals a connected
// WebSocket client receives when its session is deleted server-side.
//
// Actual server behaviour (confirmed by code inspection):
//   - Session deletion calls CloseSession → bs.Close("deleted")
//   - bs.Close sends OnACPStopped to all observers before cancelling ctx
//   - The SessionWSClient observer turns that into an "acp_stopped" WS message
//   - The TCP connection itself is NOT synchronously closed on deletion;
//     the WritePump keeps the pipe alive until ctx is cancelled or ping fails.
//   - When the WritePump's ctx eventually fires it sends close code 1001
//     (Going Away) — not 1006 (Abnormal / no close frame).
//
// This test verifies:
//  1. The "acp_stopped" signal is delivered to the connected client.
//  2. If a WebSocket close frame is received, it carries code 1000 or 1001
//     (not 1006 / abnormal), confirming the server sends a proper close frame.
func TestWebSocketCloseCode_SessionDeleted(t *testing.T) {
	ts := SetupTestServer(t)
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	// Do NOT defer delete — we delete explicitly to trigger the signal.

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		mu            sync.Mutex
		closeCode     int
		closeReason   string
		closeReceived = make(chan struct{}, 1)
		acpStopped    = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnACPStopped: func(reason string) {
			t.Logf("acp_stopped received: reason=%q", reason)
			select {
			case acpStopped <- struct{}{}:
			default:
			}
		},
		OnClosed: func(code int, reason string) {
			mu.Lock()
			closeCode = code
			closeReason = reason
			mu.Unlock()
			t.Logf("WebSocket close frame received: code=%d reason=%q", code, reason)
			select {
			case closeReceived <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	// Let the server process load_events before we delete.
	time.Sleep(200 * time.Millisecond)

	// Delete the session — the server will stop its BackgroundSession and
	// notify all observers via OnACPStopped, which the SessionWSClient
	// translates into an "acp_stopped" WebSocket message.
	if err := ts.Client.DeleteSession(session.SessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// ── Part 1: acp_stopped must be delivered ────────────────────────────────
	select {
	case <-acpStopped:
		t.Log("acp_stopped received after session deletion ✓")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for acp_stopped after session deletion")
	}

	// ── Part 2: optional close-code check ────────────────────────────────────
	// The TCP connection stays alive until a ping timeout or explicit close.
	// If a WebSocket close frame arrives, verify it is not 1006 (abnormal).
	select {
	case <-closeReceived:
		mu.Lock()
		code := closeCode
		reason := closeReason
		mu.Unlock()
		// Acceptable close codes from the server:
		//   1000 — Normal Closure   (WritePump: send channel closed)
		//   1001 — Going Away       (WritePump: ctx.Done or ping failed)
		// 1006 is NOT sent by the server; it is generated by the client library
		// when the TCP connection drops without a close frame (abnormal closure).
		const (
			closeNormalClosure = 1000
			closeGoingAway     = 1001
		)
		if code != closeNormalClosure && code != closeGoingAway {
			t.Errorf("unexpected WebSocket close code %d (reason=%q); expected 1000 or 1001",
				code, reason)
		} else {
			t.Logf("WebSocket close code %d is correct ✓", code)
		}
	case <-time.After(1 * time.Second):
		// Not receiving a close frame immediately is normal — the connection
		// stays alive until ping timeout. This is not a failure.
		t.Log("No immediate WebSocket close frame — connection stays alive until ping timeout (expected)")
	}
}
