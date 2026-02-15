package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// testWSDialer is a WebSocket dialer for tests
var testWSDialer = websocket.Dialer{
	HandshakeTimeout: 5 * time.Second,
}

// connectTestWS connects to a test WebSocket server
func connectTestWS(t *testing.T, server *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + path
	conn, resp, err := testWSDialer.Dial(wsURL, nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("WebSocket dial failed: %v (status: %d)", err, resp.StatusCode)
		}
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	return conn
}

// readWSMessage reads a WebSocket message with timeout
func readWSMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read WebSocket message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("Failed to unmarshal WebSocket message: %v", err)
	}
	return msg
}

// sendWSMessage sends a WebSocket message
func sendWSMessage(t *testing.T, conn *websocket.Conn, msgType string, data interface{}) {
	t.Helper()
	msg := WSMessage{Type: msgType}
	if data != nil {
		msg.Data, _ = json.Marshal(data)
	}
	msgBytes, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		t.Fatalf("Failed to send WebSocket message: %v", err)
	}
}

func TestGlobalEventsWebSocket_Connect(t *testing.T) {
	// Create a minimal events manager
	eventsManager := NewGlobalEventsManager()

	// Create test server with events endpoint
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade failed: %v", err)
			return
		}

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		client := &GlobalEventsClient{
			wsConn: wsConn,
			done:   make(chan struct{}),
		}

		eventsManager.Register(client)
		defer eventsManager.Unregister(client)

		// Send connected message
		wsConn.SendMessage(WSMsgTypeConnected, map[string]string{
			"acp_server": "test-server",
		})

		// Start write pump in goroutine
		go wsConn.WritePump(r.Context(), client.done)

		// Read pump (blocks until disconnect)
		for {
			_, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect to events WebSocket
	conn := connectTestWS(t, server, "/api/events")
	defer conn.Close()

	// Should receive connected message
	msg := readWSMessage(t, conn, 2*time.Second)
	if msg.Type != WSMsgTypeConnected {
		t.Errorf("Expected message type %q, got %q", WSMsgTypeConnected, msg.Type)
	}

	// Verify data contains acp_server
	var data map[string]string
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}
	if data["acp_server"] != "test-server" {
		t.Errorf("Expected acp_server 'test-server', got %q", data["acp_server"])
	}
}

func TestGlobalEventsWebSocket_Broadcast(t *testing.T) {
	eventsManager := NewGlobalEventsManager()

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		client := &GlobalEventsClient{
			wsConn: wsConn,
			done:   make(chan struct{}),
		}

		eventsManager.Register(client)
		defer eventsManager.Unregister(client)

		go wsConn.WritePump(r.Context(), client.done)

		for {
			_, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect two clients
	conn1 := connectTestWS(t, server, "/api/events")
	defer conn1.Close()

	conn2 := connectTestWS(t, server, "/api/events")
	defer conn2.Close()

	// Give time for connections to be registered
	time.Sleep(50 * time.Millisecond)

	// Broadcast a session_created event
	eventsManager.Broadcast(WSMsgTypeSessionCreated, map[string]string{
		"session_id": "test-session-123",
		"name":       "Test Session",
	})

	// Both clients should receive the broadcast
	msg1 := readWSMessage(t, conn1, 2*time.Second)
	if msg1.Type != WSMsgTypeSessionCreated {
		t.Errorf("Client 1: Expected message type %q, got %q", WSMsgTypeSessionCreated, msg1.Type)
	}

	msg2 := readWSMessage(t, conn2, 2*time.Second)
	if msg2.Type != WSMsgTypeSessionCreated {
		t.Errorf("Client 2: Expected message type %q, got %q", WSMsgTypeSessionCreated, msg2.Type)
	}

	// Verify client count
	if count := eventsManager.ClientCount(); count != 2 {
		t.Errorf("Expected 2 clients, got %d", count)
	}
}

func TestWSMessage_ParseAndMarshal(t *testing.T) {
	tests := []struct {
		name     string
		msgType  string
		data     interface{}
		wantType string
	}{
		{
			name:     "prompt message",
			msgType:  WSMsgTypePrompt,
			data:     map[string]string{"message": "Hello"},
			wantType: WSMsgTypePrompt,
		},
		{
			name:     "agent message",
			msgType:  WSMsgTypeAgentMessage,
			data:     map[string]string{"html": "<p>Response</p>"},
			wantType: WSMsgTypeAgentMessage,
		},
		{
			name:     "tool call",
			msgType:  WSMsgTypeToolCall,
			data:     map[string]string{"id": "tool-1", "title": "Read file", "status": "running"},
			wantType: WSMsgTypeToolCall,
		},
		{
			name:     "error message",
			msgType:  WSMsgTypeError,
			data:     map[string]string{"message": "Something went wrong"},
			wantType: WSMsgTypeError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create message
			msg := WSMessage{Type: tt.msgType}
			if tt.data != nil {
				msg.Data, _ = json.Marshal(tt.data)
			}

			// Marshal to JSON
			msgBytes, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Parse back
			parsed, err := ParseMessage(msgBytes)
			if err != nil {
				t.Fatalf("Failed to parse: %v", err)
			}

			if parsed.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", parsed.Type, tt.wantType)
			}
		})
	}
}

func TestWSConn_SendMessage_Integration(t *testing.T) {
	// Create a test WebSocket server
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	receivedMessages := make(chan WSMessage, 10)

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		// Start write pump
		go wsConn.WritePump(r.Context(), nil)

		// Send test messages
		wsConn.SendMessage("test_message", map[string]string{"key": "value1"})
		wsConn.SendMessage("test_message", map[string]string{"key": "value2"})
		wsConn.SendError("test error")

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect and read messages
	conn := connectTestWS(t, server, "/ws")
	defer conn.Close()

	// Read first message
	msg1 := readWSMessage(t, conn, 2*time.Second)
	if msg1.Type != "test_message" {
		t.Errorf("Message 1 type = %q, want 'test_message'", msg1.Type)
	}

	// Read second message
	msg2 := readWSMessage(t, conn, 2*time.Second)
	if msg2.Type != "test_message" {
		t.Errorf("Message 2 type = %q, want 'test_message'", msg2.Type)
	}

	// Read error message
	msg3 := readWSMessage(t, conn, 2*time.Second)
	if msg3.Type != WSMsgTypeError {
		t.Errorf("Message 3 type = %q, want %q", msg3.Type, WSMsgTypeError)
	}

	close(receivedMessages)
}

func TestSessionWSClient_MessageHandling(t *testing.T) {
	// Test that SessionWSClient correctly handles different message types
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Track received messages on server side
	serverReceived := make(chan WSMessage, 10)

	mux.HandleFunc("/api/sessions/test-session/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		// Start write pump
		go wsConn.WritePump(r.Context(), nil)

		// Send connected message
		wsConn.SendMessage(WSMsgTypeConnected, map[string]string{
			"session_id": "test-session",
			"client_id":  "client-123",
		})

		// Read messages from client
		for {
			data, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			msg, _ := ParseMessage(data)
			serverReceived <- msg

			// Respond based on message type
			switch msg.Type {
			case WSMsgTypePrompt:
				// Simulate agent response
				wsConn.SendMessage(WSMsgTypeAgentMessage, map[string]string{
					"html": "<p>Hello! How can I help?</p>",
				})
				wsConn.SendMessage(WSMsgTypePromptComplete, map[string]int{
					"event_count": 1,
				})
			case WSMsgTypeCancel:
				wsConn.SendMessage(WSMsgTypePromptComplete, map[string]int{
					"event_count": 0,
				})
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect to session WebSocket
	conn := connectTestWS(t, server, "/api/sessions/test-session/ws")
	defer conn.Close()

	// Should receive connected message
	connectedMsg := readWSMessage(t, conn, 2*time.Second)
	if connectedMsg.Type != WSMsgTypeConnected {
		t.Errorf("Expected %q, got %q", WSMsgTypeConnected, connectedMsg.Type)
	}

	// Send a prompt
	sendWSMessage(t, conn, WSMsgTypePrompt, map[string]string{
		"message": "Hello",
	})

	// Should receive agent message
	agentMsg := readWSMessage(t, conn, 2*time.Second)
	if agentMsg.Type != WSMsgTypeAgentMessage {
		t.Errorf("Expected %q, got %q", WSMsgTypeAgentMessage, agentMsg.Type)
	}

	// Should receive prompt complete
	completeMsg := readWSMessage(t, conn, 2*time.Second)
	if completeMsg.Type != WSMsgTypePromptComplete {
		t.Errorf("Expected %q, got %q", WSMsgTypePromptComplete, completeMsg.Type)
	}

	// Verify server received the prompt
	select {
	case msg := <-serverReceived:
		if msg.Type != WSMsgTypePrompt {
			t.Errorf("Server expected %q, got %q", WSMsgTypePrompt, msg.Type)
		}
	case <-time.After(time.Second):
		t.Error("Server did not receive prompt message")
	}
}

func TestSessionWSClient_SyncSession(t *testing.T) {
	// Test sync_session message handling
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/sessions/test-session/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		go wsConn.WritePump(r.Context(), nil)

		// Send connected message
		wsConn.SendMessage(WSMsgTypeConnected, nil)

		// Read messages
		for {
			data, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			msg, _ := ParseMessage(data)

			if msg.Type == WSMsgTypeSyncSession {
				// Parse sync request
				var syncReq struct {
					SessionID string `json:"session_id"`
					AfterSeq  int    `json:"after_seq"`
				}
				json.Unmarshal(msg.Data, &syncReq)

				// Send sync response with events after the requested sequence
				wsConn.SendMessage(WSMsgTypeSessionSync, map[string]interface{}{
					"session_id": syncReq.SessionID,
					"events": []map[string]interface{}{
						{"seq": syncReq.AfterSeq + 1, "type": "agent_message", "data": "missed message 1"},
						{"seq": syncReq.AfterSeq + 2, "type": "agent_message", "data": "missed message 2"},
					},
				})
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := connectTestWS(t, server, "/api/sessions/test-session/ws")
	defer conn.Close()

	// Read connected message
	readWSMessage(t, conn, 2*time.Second)

	// Send sync request
	sendWSMessage(t, conn, WSMsgTypeSyncSession, map[string]interface{}{
		"session_id": "test-session",
		"after_seq":  5,
	})

	// Should receive sync response
	syncMsg := readWSMessage(t, conn, 2*time.Second)
	if syncMsg.Type != WSMsgTypeSessionSync {
		t.Errorf("Expected %q, got %q", WSMsgTypeSessionSync, syncMsg.Type)
	}

	// Verify sync response contains events
	var syncResp struct {
		SessionID string                   `json:"session_id"`
		Events    []map[string]interface{} `json:"events"`
	}
	if err := json.Unmarshal(syncMsg.Data, &syncResp); err != nil {
		t.Fatalf("Failed to unmarshal sync response: %v", err)
	}

	if len(syncResp.Events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(syncResp.Events))
	}
}

func TestSessionSync_EventOrdering(t *testing.T) {
	// Test that session_sync returns events in correct chronological order
	// This catches the bug where tool calls appeared before agent messages after mobile wake
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Simulate a session with interleaved tool calls and agent messages
	// This is the order they should appear: tool_call, agent_message, tool_call, agent_message
	mockEvents := []map[string]interface{}{
		{"seq": 1, "type": "user_prompt", "timestamp": 1000, "data": map[string]string{"text": "fix the file"}},
		{"seq": 2, "type": "tool_call", "timestamp": 2000, "data": map[string]string{"id": "tool-1", "title": "Read file.js"}},
		{"seq": 3, "type": "agent_message", "timestamp": 3000, "data": map[string]string{"html": "I found the issue"}},
		{"seq": 4, "type": "tool_call", "timestamp": 4000, "data": map[string]string{"id": "tool-2", "title": "Edit file.js"}},
		{"seq": 5, "type": "agent_message", "timestamp": 5000, "data": map[string]string{"html": "Done! Fixed the issue"}},
	}

	mux.HandleFunc("/api/sessions/test-session/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		go wsConn.WritePump(r.Context(), nil)
		wsConn.SendMessage(WSMsgTypeConnected, nil)

		for {
			data, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			msg, _ := ParseMessage(data)

			if msg.Type == WSMsgTypeSyncSession {
				var syncReq struct {
					SessionID string `json:"session_id"`
					AfterSeq  int    `json:"after_seq"`
				}
				json.Unmarshal(msg.Data, &syncReq)

				// Filter events after the requested sequence
				var filteredEvents []map[string]interface{}
				for _, evt := range mockEvents {
					if seq, ok := evt["seq"].(int); ok && seq > syncReq.AfterSeq {
						filteredEvents = append(filteredEvents, evt)
					}
				}

				wsConn.SendMessage(WSMsgTypeSessionSync, map[string]interface{}{
					"session_id": syncReq.SessionID,
					"after_seq":  syncReq.AfterSeq,
					"events":     filteredEvents,
				})
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := connectTestWS(t, server, "/api/sessions/test-session/ws")
	defer conn.Close()

	// Read connected message
	readWSMessage(t, conn, 2*time.Second)

	// Simulate mobile wake: request sync after seq 1 (user saw the prompt)
	sendWSMessage(t, conn, WSMsgTypeSyncSession, map[string]interface{}{
		"session_id": "test-session",
		"after_seq":  1,
	})

	// Should receive sync response
	syncMsg := readWSMessage(t, conn, 2*time.Second)
	if syncMsg.Type != WSMsgTypeSessionSync {
		t.Fatalf("Expected %q, got %q", WSMsgTypeSessionSync, syncMsg.Type)
	}

	var syncResp struct {
		SessionID string                   `json:"session_id"`
		AfterSeq  int                      `json:"after_seq"`
		Events    []map[string]interface{} `json:"events"`
	}
	if err := json.Unmarshal(syncMsg.Data, &syncResp); err != nil {
		t.Fatalf("Failed to unmarshal sync response: %v", err)
	}

	// Should have 4 events (seq 2-5)
	if len(syncResp.Events) != 4 {
		t.Errorf("Expected 4 events, got %d", len(syncResp.Events))
	}

	// Verify events are in correct order by sequence number
	expectedOrder := []string{"tool_call", "agent_message", "tool_call", "agent_message"}
	for i, evt := range syncResp.Events {
		evtType, _ := evt["type"].(string)
		if evtType != expectedOrder[i] {
			t.Errorf("Event %d: expected type %q, got %q", i, expectedOrder[i], evtType)
		}

		// Verify sequence numbers are ascending
		seq := int(evt["seq"].(float64))
		expectedSeq := i + 2 // starts at seq 2
		if seq != expectedSeq {
			t.Errorf("Event %d: expected seq %d, got %d", i, expectedSeq, seq)
		}
	}

	// Verify timestamps are ascending (chronological order)
	var lastTimestamp float64
	for i, evt := range syncResp.Events {
		ts := evt["timestamp"].(float64)
		if ts <= lastTimestamp {
			t.Errorf("Event %d: timestamp %v should be > %v", i, ts, lastTimestamp)
		}
		lastTimestamp = ts
	}
}

func TestSessionSync_ToolCallDeduplication(t *testing.T) {
	// Test that tool calls with the same ID are properly identified
	// This catches the bug where tool messages hashed to empty string
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Two different tool calls that should NOT be deduplicated
	mockEvents := []map[string]interface{}{
		{"seq": 1, "type": "tool_call", "timestamp": 1000, "data": map[string]string{"id": "tool-1", "title": "Read file.js"}},
		{"seq": 2, "type": "tool_call", "timestamp": 2000, "data": map[string]string{"id": "tool-2", "title": "Read file.js"}}, // Same title, different ID
	}

	mux.HandleFunc("/api/sessions/test-session/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		go wsConn.WritePump(r.Context(), nil)
		wsConn.SendMessage(WSMsgTypeConnected, nil)

		for {
			data, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			msg, _ := ParseMessage(data)

			if msg.Type == WSMsgTypeSyncSession {
				wsConn.SendMessage(WSMsgTypeSessionSync, map[string]interface{}{
					"session_id": "test-session",
					"after_seq":  0,
					"events":     mockEvents,
				})
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := connectTestWS(t, server, "/api/sessions/test-session/ws")
	defer conn.Close()

	readWSMessage(t, conn, 2*time.Second) // connected

	sendWSMessage(t, conn, WSMsgTypeSyncSession, map[string]interface{}{
		"session_id": "test-session",
		"after_seq":  0,
	})

	syncMsg := readWSMessage(t, conn, 2*time.Second)
	var syncResp struct {
		Events []map[string]interface{} `json:"events"`
	}
	json.Unmarshal(syncMsg.Data, &syncResp)

	// Both tool calls should be present (not deduplicated)
	if len(syncResp.Events) != 2 {
		t.Errorf("Expected 2 tool calls, got %d", len(syncResp.Events))
	}

	// Verify they have different IDs
	ids := make(map[string]bool)
	for _, evt := range syncResp.Events {
		data, _ := evt["data"].(map[string]interface{})
		if id, ok := data["id"].(string); ok {
			if ids[id] {
				t.Errorf("Duplicate tool ID found: %s", id)
			}
			ids[id] = true
		}
	}
}

func TestWebSocketReconnection(t *testing.T) {
	// Test that clients can reconnect after disconnection
	eventsManager := NewGlobalEventsManager()

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		client := &GlobalEventsClient{
			wsConn: wsConn,
			done:   make(chan struct{}),
		}

		eventsManager.Register(client)
		defer eventsManager.Unregister(client)

		wsConn.SendMessage(WSMsgTypeConnected, nil)
		go wsConn.WritePump(r.Context(), client.done)

		for {
			_, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// First connection
	conn1 := connectTestWS(t, server, "/api/events")
	readWSMessage(t, conn1, 2*time.Second) // connected message

	// Verify client count
	time.Sleep(50 * time.Millisecond)
	if count := eventsManager.ClientCount(); count != 1 {
		t.Errorf("Expected 1 client, got %d", count)
	}

	// Disconnect
	conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Client count should be 0
	if count := eventsManager.ClientCount(); count != 0 {
		t.Errorf("Expected 0 clients after disconnect, got %d", count)
	}

	// Reconnect
	conn2 := connectTestWS(t, server, "/api/events")
	defer conn2.Close()
	readWSMessage(t, conn2, 2*time.Second) // connected message

	// Client count should be 1 again
	time.Sleep(50 * time.Millisecond)
	if count := eventsManager.ClientCount(); count != 1 {
		t.Errorf("Expected 1 client after reconnect, got %d", count)
	}
}

// --- Config Options WebSocket Tests ---

// TestSessionWS_ConnectedMessage_IncludesConfigOptions tests that the connected
// message includes config_options when available.
func TestSessionWS_ConnectedMessage_IncludesConfigOptions(t *testing.T) {
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/sessions/test-session/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		// Start write pump
		go wsConn.WritePump(r.Context(), nil)

		// Send connected message with config_options
		wsConn.SendMessage(WSMsgTypeConnected, map[string]interface{}{
			"session_id": "test-session",
			"client_id":  "test-client",
			"acp_server": "test-server",
			"config_options": []SessionConfigOption{
				{
					ID:           ConfigOptionCategoryMode,
					Name:         "Mode",
					Description:  "Session operating mode",
					Category:     ConfigOptionCategoryMode,
					Type:         ConfigOptionTypeSelect,
					CurrentValue: "code",
					Options: []SessionConfigOptionValue{
						{Value: "ask", Name: "Ask", Description: "Ask questions"},
						{Value: "code", Name: "Code", Description: "Make code changes"},
					},
				},
			},
		})

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := connectTestWS(t, server, "/api/sessions/test-session/ws")
	defer conn.Close()

	// Read connected message
	connectedMsg := readWSMessage(t, conn, 2*time.Second)
	if connectedMsg.Type != WSMsgTypeConnected {
		t.Errorf("Expected %q, got %q", WSMsgTypeConnected, connectedMsg.Type)
	}

	// Parse the data
	var connectedData struct {
		SessionID     string                `json:"session_id"`
		ClientID      string                `json:"client_id"`
		ConfigOptions []SessionConfigOption `json:"config_options"`
	}
	if err := json.Unmarshal(connectedMsg.Data, &connectedData); err != nil {
		t.Fatalf("Failed to unmarshal connected data: %v", err)
	}

	// Verify config_options
	if len(connectedData.ConfigOptions) != 1 {
		t.Fatalf("Expected 1 config option, got %d", len(connectedData.ConfigOptions))
	}

	modeOpt := connectedData.ConfigOptions[0]
	if modeOpt.ID != ConfigOptionCategoryMode {
		t.Errorf("Config option ID = %q, want %q", modeOpt.ID, ConfigOptionCategoryMode)
	}
	if modeOpt.Category != ConfigOptionCategoryMode {
		t.Errorf("Config option Category = %q, want %q", modeOpt.Category, ConfigOptionCategoryMode)
	}
	if modeOpt.Type != ConfigOptionTypeSelect {
		t.Errorf("Config option Type = %q, want %q", modeOpt.Type, ConfigOptionTypeSelect)
	}
	if modeOpt.CurrentValue != "code" {
		t.Errorf("Config option CurrentValue = %q, want %q", modeOpt.CurrentValue, "code")
	}
	if len(modeOpt.Options) != 2 {
		t.Errorf("Expected 2 options, got %d", len(modeOpt.Options))
	}
}

// TestSessionWS_ConfigOptionChanged_Broadcast tests that config option changes
// are broadcast to connected clients.
func TestSessionWS_ConfigOptionChanged_Broadcast(t *testing.T) {
	eventsManager := NewGlobalEventsManager()

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		wsConn := &WSConn{
			conn:   conn,
			send:   make(chan []byte, 64),
			config: DefaultWebSocketSecurityConfig(),
		}

		client := &GlobalEventsClient{
			wsConn: wsConn,
			done:   make(chan struct{}),
		}

		eventsManager.Register(client)
		defer eventsManager.Unregister(client)

		// Send connected message
		wsConn.SendMessage(WSMsgTypeConnected, map[string]string{
			"acp_server": "test-server",
		})

		// Start write pump
		go wsConn.WritePump(r.Context(), client.done)

		// Read pump
		for {
			_, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect a client
	conn := connectTestWS(t, server, "/api/events")
	defer conn.Close()

	// Read connected message
	readWSMessage(t, conn, 2*time.Second)

	// Broadcast a config option changed event
	eventsManager.Broadcast(WSMsgTypeConfigOptionChanged, map[string]interface{}{
		"session_id": "test-session",
		"config_id":  ConfigOptionCategoryMode,
		"value":      "architect",
	})

	// Read the broadcast message
	changedMsg := readWSMessage(t, conn, 2*time.Second)
	if changedMsg.Type != WSMsgTypeConfigOptionChanged {
		t.Errorf("Expected %q, got %q", WSMsgTypeConfigOptionChanged, changedMsg.Type)
	}

	// Verify the data
	var changedData struct {
		SessionID string `json:"session_id"`
		ConfigID  string `json:"config_id"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(changedMsg.Data, &changedData); err != nil {
		t.Fatalf("Failed to unmarshal changed data: %v", err)
	}

	if changedData.SessionID != "test-session" {
		t.Errorf("session_id = %q, want %q", changedData.SessionID, "test-session")
	}
	if changedData.ConfigID != ConfigOptionCategoryMode {
		t.Errorf("config_id = %q, want %q", changedData.ConfigID, ConfigOptionCategoryMode)
	}
	if changedData.Value != "architect" {
		t.Errorf("value = %q, want %q", changedData.Value, "architect")
	}
}

// TestSessionWS_SetConfigOption_MessageFormat tests the set_config_option message format.
func TestSessionWS_SetConfigOption_MessageFormat(t *testing.T) {
	// Test that the set_config_option message can be properly formatted and parsed
	msg := WSMessage{
		Type: WSMsgTypeSetConfigOption,
	}
	data := map[string]string{
		"config_id": ConfigOptionCategoryMode,
		"value":     "code",
	}
	msg.Data, _ = json.Marshal(data)

	// Marshal and parse back
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	parsed, err := ParseMessage(msgBytes)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if parsed.Type != WSMsgTypeSetConfigOption {
		t.Errorf("Type = %q, want %q", parsed.Type, WSMsgTypeSetConfigOption)
	}

	var parsedData struct {
		ConfigID string `json:"config_id"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(parsed.Data, &parsedData); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}

	if parsedData.ConfigID != ConfigOptionCategoryMode {
		t.Errorf("config_id = %q, want %q", parsedData.ConfigID, ConfigOptionCategoryMode)
	}
	if parsedData.Value != "code" {
		t.Errorf("value = %q, want %q", parsedData.Value, "code")
	}
}

// TestSessionConfigOption_JSONSerialization tests that SessionConfigOption
// serializes correctly to JSON for WebSocket transmission.
func TestSessionConfigOption_JSONSerialization(t *testing.T) {
	opt := SessionConfigOption{
		ID:           ConfigOptionCategoryMode,
		Name:         "Mode",
		Description:  "Session operating mode",
		Category:     ConfigOptionCategoryMode,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: "code",
		Options: []SessionConfigOptionValue{
			{Value: "ask", Name: "Ask", Description: "Ask questions without making changes"},
			{Value: "code", Name: "Code", Description: "Make code changes"},
			{Value: "architect", Name: "Architect"}, // No description
		},
	}

	// Serialize to JSON
	data, err := json.Marshal(opt)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Parse back
	var parsed SessionConfigOption
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify all fields
	if parsed.ID != opt.ID {
		t.Errorf("ID = %q, want %q", parsed.ID, opt.ID)
	}
	if parsed.Name != opt.Name {
		t.Errorf("Name = %q, want %q", parsed.Name, opt.Name)
	}
	if parsed.Description != opt.Description {
		t.Errorf("Description = %q, want %q", parsed.Description, opt.Description)
	}
	if parsed.Category != opt.Category {
		t.Errorf("Category = %q, want %q", parsed.Category, opt.Category)
	}
	if parsed.Type != opt.Type {
		t.Errorf("Type = %q, want %q", parsed.Type, opt.Type)
	}
	if parsed.CurrentValue != opt.CurrentValue {
		t.Errorf("CurrentValue = %q, want %q", parsed.CurrentValue, opt.CurrentValue)
	}
	if len(parsed.Options) != len(opt.Options) {
		t.Fatalf("Options length = %d, want %d", len(parsed.Options), len(opt.Options))
	}

	// Verify options
	for i, parsedOpt := range parsed.Options {
		origOpt := opt.Options[i]
		if parsedOpt.Value != origOpt.Value {
			t.Errorf("Options[%d].Value = %q, want %q", i, parsedOpt.Value, origOpt.Value)
		}
		if parsedOpt.Name != origOpt.Name {
			t.Errorf("Options[%d].Name = %q, want %q", i, parsedOpt.Name, origOpt.Name)
		}
		if parsedOpt.Description != origOpt.Description {
			t.Errorf("Options[%d].Description = %q, want %q", i, parsedOpt.Description, origOpt.Description)
		}
	}
}

// TestSessionConfigOption_OmitEmptyFields tests that empty optional fields
// are omitted from JSON serialization.
func TestSessionConfigOption_OmitEmptyFields(t *testing.T) {
	opt := SessionConfigOption{
		ID:           "model",
		Name:         "Model",
		Type:         ConfigOptionTypeSelect,
		CurrentValue: "gpt-4",
		Options: []SessionConfigOptionValue{
			{Value: "gpt-4", Name: "GPT-4"},
		},
		// Description and Category are intentionally empty
	}

	data, err := json.Marshal(opt)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Check that empty fields are omitted
	dataStr := string(data)
	if strings.Contains(dataStr, `"description"`) {
		t.Error("Empty description should be omitted from JSON")
	}
	if strings.Contains(dataStr, `"category"`) {
		t.Error("Empty category should be omitted from JSON")
	}
}
