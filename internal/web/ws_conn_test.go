package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    WSMessage
		wantErr bool
	}{
		{
			name:  "valid message with data",
			input: []byte(`{"type":"test","data":{"key":"value"}}`),
			want: WSMessage{
				Type: "test",
				Data: json.RawMessage(`{"key":"value"}`),
			},
			wantErr: false,
		},
		{
			name:  "valid message without data",
			input: []byte(`{"type":"ping"}`),
			want: WSMessage{
				Type: "ping",
				Data: nil,
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   []byte(`{invalid`),
			want:    WSMessage{},
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   []byte(``),
			want:    WSMessage{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessage(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Type != tt.want.Type {
					t.Errorf("ParseMessage() Type = %v, want %v", got.Type, tt.want.Type)
				}
				if tt.want.Data != nil && string(got.Data) != string(tt.want.Data) {
					t.Errorf("ParseMessage() Data = %v, want %v", string(got.Data), string(tt.want.Data))
				}
			}
		})
	}
}

func TestWSConn_SendRaw_Backpressure(t *testing.T) {
	// Create a WSConn with a small buffer and a mock connection for close detection.
	server, wsConnCh, cancel := setupWritePumpTestServer(t)
	defer server.Close()
	defer cancel()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server-side WSConn
	var wc *WSConn
	select {
	case wc = <-wsConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server connection")
	}

	// Fill the buffer completely
	for i := 0; i < cap(wc.send); i++ {
		wc.send <- []byte("fill")
	}

	// Next send should apply backpressure (wait briefly then close connection)
	done := make(chan bool)
	go func() {
		wc.SendRaw([]byte("overflow"))
		done <- true
	}()

	// Should complete within backpressure timeout + margin (not hang forever)
	select {
	case <-done:
		// Good - SendRaw returned after backpressure timeout
	case <-time.After(500 * time.Millisecond):
		t.Error("SendRaw blocked indefinitely instead of timing out")
	}
}

func TestWSConn_ReleaseConnectionSlot(t *testing.T) {
	tracker := NewConnectionTracker(10)
	clientIP := "192.168.1.1"

	// Add a connection
	if !tracker.TryAdd(clientIP) {
		t.Fatal("TryAdd should succeed")
	}

	w := &WSConn{
		tracker:  tracker,
		clientIP: clientIP,
	}

	// Release should remove from tracker
	w.ReleaseConnectionSlot()

	// Should be able to add again
	if !tracker.TryAdd(clientIP) {
		t.Error("TryAdd should succeed after ReleaseConnectionSlot")
	}
}

func TestWSConn_ReleaseConnectionSlot_NilTracker(t *testing.T) {
	w := &WSConn{
		tracker:  nil,
		clientIP: "192.168.1.1",
	}

	// Should not panic with nil tracker
	w.ReleaseConnectionSlot()
}

func TestWSConn_ReleaseConnectionSlot_EmptyClientIP(t *testing.T) {
	tracker := NewConnectionTracker(10)

	w := &WSConn{
		tracker:  tracker,
		clientIP: "",
	}

	// Should not panic with empty client IP
	w.ReleaseConnectionSlot()
}

func TestWSConnConfig_Fields(t *testing.T) {
	// Test that WSConnConfig fields are properly defined
	tracker := NewConnectionTracker(10)

	cfg := WSConnConfig{
		Conn:     nil,
		Config:   DefaultWebSocketSecurityConfig(),
		Logger:   nil,
		ClientIP: "192.168.1.1",
		Tracker:  tracker,
		SendSize: 128,
	}

	if cfg.ClientIP != "192.168.1.1" {
		t.Error("ClientIP not set correctly")
	}
	if cfg.SendSize != 128 {
		t.Error("SendSize not set correctly")
	}
	if cfg.Tracker != tracker {
		t.Error("Tracker not set correctly")
	}
}

func TestWSConn_SendMessage_Backpressure(t *testing.T) {
	// Create a WSConn with a small buffer and a real WebSocket connection.
	server, wsConnCh, cancel := setupWritePumpTestServer(t)
	defer server.Close()
	defer cancel()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server-side WSConn
	var wc *WSConn
	select {
	case wc = <-wsConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server connection")
	}

	// Fill the buffer completely
	for i := 0; i < cap(wc.send); i++ {
		wc.send <- []byte(`{"type":"fill"}`)
	}

	// Next send should apply backpressure (wait briefly then close connection)
	done := make(chan bool)
	go func() {
		wc.SendMessage("overflow", nil)
		done <- true
	}()

	// Should complete within backpressure timeout + margin
	select {
	case <-done:
		// Good - SendMessage returned after backpressure timeout
	case <-time.After(500 * time.Millisecond):
		t.Error("SendMessage blocked indefinitely instead of timing out")
	}
}

func TestWSConn_SendMessage_BackpressureResolves(t *testing.T) {
	// Test that backpressure resolves when the buffer drains within the timeout.
	w := &WSConn{
		send: make(chan []byte, 1),
	}

	// First send succeeds (fills buffer)
	w.SendMessage("first", map[string]string{"key": "value"})

	// Start a goroutine that will drain the buffer after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		<-w.send // drain one slot
	}()

	// Second send should succeed after the drain (within backpressure timeout)
	done := make(chan bool)
	go func() {
		w.SendMessage("second", nil)
		done <- true
	}()

	select {
	case <-done:
		// Good - backpressure resolved when buffer drained
	case <-time.After(500 * time.Millisecond):
		t.Error("SendMessage did not resolve backpressure when buffer drained")
	}

	// Verify second message is now in buffer
	msg := <-w.send
	var wsMsg WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}
	if wsMsg.Type != "second" {
		t.Errorf("Expected type 'second', got %s", wsMsg.Type)
	}
}

func TestWSConn_SendError(t *testing.T) {
	w := &WSConn{
		send: make(chan []byte, 1),
	}

	w.SendError("test error message")

	msg := <-w.send
	var wsMsg WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}

	if wsMsg.Type != WSMsgTypeError {
		t.Errorf("Expected type %q, got %q", WSMsgTypeError, wsMsg.Type)
	}

	var data map[string]string
	if err := json.Unmarshal(wsMsg.Data, &data); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}

	if data["message"] != "test error message" {
		t.Errorf("Expected message 'test error message', got %q", data["message"])
	}
}

// setupWritePumpTestServer creates a test HTTP server that upgrades to WebSocket
// and runs WritePump with the returned cancel function for context control.
// Returns the test server and a channel that receives the WSConn once connected.
func setupWritePumpTestServer(t *testing.T) (*httptest.Server, chan *WSConn, context.CancelFunc) {
	t.Helper()
	wsConnCh := make(chan *WSConn, 1)
	ctx, cancel := context.WithCancel(context.Background())

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade failed: %v", err)
			return
		}

		wc := &WSConn{
			conn: conn,
			send: make(chan []byte, 64),
			config: WebSocketSecurityConfig{
				WriteWait:  10 * time.Second,
				PingPeriod: 60 * time.Second, // Long period so it doesn't fire during test
			},
		}
		wsConnCh <- wc

		done := make(chan struct{})
		wc.WritePump(ctx, done)
	}))

	return server, wsConnCh, cancel
}

func TestWritePump_SendsCloseFrameOnContextCancellation(t *testing.T) {
	server, wsConnCh, cancel := setupWritePumpTestServer(t)
	defer server.Close()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server-side WSConn to be ready
	select {
	case <-wsConnCh:
		// Server connected
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server connection")
	}

	// Cancel the context — this should cause WritePump to send a close frame
	cancel()

	// Read from client — we should get a close message with GoingAway code
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = clientConn.ReadMessage()
	if err == nil {
		t.Fatal("Expected error (close frame), got normal message")
	}

	closeErr, ok := err.(*websocket.CloseError)
	if !ok {
		// On some platforms the close frame is received as a different error type.
		// Just verify we got an error (connection was closed), which is the minimum.
		t.Logf("Got non-CloseError: %v (type: %T) — connection was closed", err, err)
		return
	}

	if closeErr.Code != websocket.CloseGoingAway {
		t.Errorf("Expected close code %d (GoingAway), got %d: %s",
			websocket.CloseGoingAway, closeErr.Code, closeErr.Text)
	}
}

func TestWritePump_SendsCloseFrameOnChannelClose(t *testing.T) {
	server, wsConnCh, cancel := setupWritePumpTestServer(t)
	defer server.Close()
	defer cancel()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server-side WSConn to be ready
	var wc *WSConn
	select {
	case wc = <-wsConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server connection")
	}

	// Close the send channel — WritePump should send a close frame with NormalClosure
	close(wc.send)

	// Read from client — should get close frame
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = clientConn.ReadMessage()
	if err == nil {
		t.Fatal("Expected error (close frame), got normal message")
	}

	closeErr, ok := err.(*websocket.CloseError)
	if !ok {
		t.Logf("Got non-CloseError: %v (type: %T) — connection was closed", err, err)
		return
	}

	if closeErr.Code != websocket.CloseNormalClosure {
		t.Errorf("Expected close code %d (NormalClosure), got %d: %s",
			websocket.CloseNormalClosure, closeErr.Code, closeErr.Text)
	}
}

func TestWritePump_DeliversMessageBeforeClose(t *testing.T) {
	server, wsConnCh, cancel := setupWritePumpTestServer(t)
	defer server.Close()
	defer cancel()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server-side WSConn
	var wc *WSConn
	select {
	case wc = <-wsConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server connection")
	}

	// Send a message, then close the channel
	testMsg := []byte(`{"type":"test","data":{"key":"value"}}`)
	wc.send <- testMsg
	// Small delay to let WritePump pick up the message
	time.Sleep(50 * time.Millisecond)
	close(wc.send)

	// Client should receive the message first
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("Expected to receive message before close: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if msg.Type != "test" {
		t.Errorf("Expected message type 'test', got %q", msg.Type)
	}

	// Then client should receive the close frame
	_, _, err = clientConn.ReadMessage()
	if err == nil {
		t.Fatal("Expected close error after message delivery")
	}
}
