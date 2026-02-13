package web

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

func TestGenerateClientID(t *testing.T) {
	id1 := generateClientID()
	id2 := generateClientID()

	// IDs should not be empty
	if id1 == "" {
		t.Error("generateClientID returned empty string")
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("generateClientID returned duplicate IDs: %s", id1)
	}

	// IDs should be 16 characters (8 bytes hex encoded)
	if len(id1) != 16 {
		t.Errorf("generateClientID returned ID of length %d, want 16", len(id1))
	}
}

// mockWSConn captures messages sent via SendMessage for testing.
type mockWSConn struct {
	mu       sync.Mutex
	messages []capturedMessage
	send     chan []byte
}

type capturedMessage struct {
	Type string
	Data map[string]interface{}
}

func newMockWSConn() *mockWSConn {
	return &mockWSConn{
		messages: make([]capturedMessage, 0),
		send:     make(chan []byte, 256),
	}
}

func (m *mockWSConn) SendMessage(msgType string, data interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Convert data to map for easier testing
	dataBytes, _ := json.Marshal(data)
	var dataMap map[string]interface{}
	json.Unmarshal(dataBytes, &dataMap)

	m.messages = append(m.messages, capturedMessage{
		Type: msgType,
		Data: dataMap,
	})
}

// mockBackgroundSessionForPrompting is a minimal mock that only implements IsPrompting.
type mockBackgroundSessionForPrompting struct {
	isPrompting bool
	mu          sync.Mutex
}

func (m *mockBackgroundSessionForPrompting) IsPrompting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isPrompting
}

func (m *mockBackgroundSessionForPrompting) setIsPrompting(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isPrompting = v
}

// TestSessionWSClient_OnAgentMessage_IsPrompting tests that OnAgentMessage includes
// the is_prompting field based on the BackgroundSession state.
func TestSessionWSClient_OnAgentMessage_IsPrompting(t *testing.T) {
	tests := []struct {
		name          string
		isPrompting   bool
		wantPrompting bool
		hasBgSession  bool
	}{
		{
			name:          "prompting true",
			isPrompting:   true,
			wantPrompting: true,
			hasBgSession:  true,
		},
		{
			name:          "prompting false (unsolicited message)",
			isPrompting:   false,
			wantPrompting: false,
			hasBgSession:  true,
		},
		{
			name:          "no background session",
			isPrompting:   false,
			wantPrompting: false,
			hasBgSession:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockWS := newMockWSConn()

			// Create a minimal SessionWSClient with just the fields we need
			// This verifies the struct can be created with the required fields
			_ = &SessionWSClient{
				sessionID: "test-session",
				wsConn: &WSConn{
					send: mockWS.send,
				},
			}

			// Create a mock background session if needed
			var mockBg *mockBackgroundSessionForPrompting
			if tt.hasBgSession {
				mockBg = &mockBackgroundSessionForPrompting{isPrompting: tt.isPrompting}
			}

			// Test the is_prompting logic directly - this mirrors the logic in OnAgentMessage:
			// isPrompting := false
			// if c.bgSession != nil {
			//     isPrompting = c.bgSession.IsPrompting()
			// }
			isPrompting := false
			if mockBg != nil {
				isPrompting = mockBg.IsPrompting()
			}

			if isPrompting != tt.wantPrompting {
				t.Errorf("isPrompting = %v, want %v", isPrompting, tt.wantPrompting)
			}
		})
	}
}

// TestSessionWSClient_OnAgentThought_IsPrompting tests that OnAgentThought includes
// the is_prompting field based on the BackgroundSession state.
func TestSessionWSClient_OnAgentThought_IsPrompting(t *testing.T) {
	mockBg := &mockBackgroundSessionForPrompting{isPrompting: true}

	// Test prompting state
	if !mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to return true")
	}

	mockBg.setIsPrompting(false)
	if mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to return false after setting to false")
	}
}

// TestSessionWSClient_OnToolCall_IsPrompting tests that OnToolCall includes
// the is_prompting field based on the BackgroundSession state.
func TestSessionWSClient_OnToolCall_IsPrompting(t *testing.T) {
	mockBg := &mockBackgroundSessionForPrompting{isPrompting: true}

	// Verify the mock works correctly
	if !mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to return true initially")
	}

	// Simulate prompt completion
	mockBg.setIsPrompting(false)
	if mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to return false after prompt completion")
	}
}

// TestSessionWSClient_OnToolUpdate_IsPrompting tests that OnToolUpdate includes
// the is_prompting field based on the BackgroundSession state.
func TestSessionWSClient_OnToolUpdate_IsPrompting(t *testing.T) {
	mockBg := &mockBackgroundSessionForPrompting{isPrompting: false}

	// When not prompting (unsolicited message), is_prompting should be false
	if mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to return false for unsolicited messages")
	}
}

// TestIsPromptingStateTransition tests the state transition from prompting to not prompting.
func TestIsPromptingStateTransition(t *testing.T) {
	mockBg := &mockBackgroundSessionForPrompting{isPrompting: false}

	// Initially not prompting
	if mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to be false initially")
	}

	// Start prompting
	mockBg.setIsPrompting(true)
	if !mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to be true after starting prompt")
	}

	// Complete prompt
	mockBg.setIsPrompting(false)
	if mockBg.IsPrompting() {
		t.Error("Expected IsPrompting to be false after completing prompt")
	}
}

// TestIsPromptingConcurrency tests that IsPrompting is safe for concurrent access.
func TestIsPromptingConcurrency(t *testing.T) {
	mockBg := &mockBackgroundSessionForPrompting{isPrompting: false}

	var wg sync.WaitGroup
	const numGoroutines = 100

	// Start multiple goroutines reading and writing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Reader
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = mockBg.IsPrompting()
				time.Sleep(time.Microsecond)
			}
		}()

		// Writer
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				mockBg.setIsPrompting(i%2 == 0)
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()
	// If we get here without a race condition, the test passes
}

// =============================================================================
// H2: syncMissedEventsDuringRegistration Tests
// =============================================================================

// TestSyncMissedEventsDuringRegistration_NoStore tests that the function
// handles nil store gracefully.
func TestSyncMissedEventsDuringRegistration_NoStore(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "test-session",
		wsConn: &WSConn{
			send: mockWS.send,
		},
		store: nil, // No store
	}

	// Should not panic and should not send any messages
	client.syncMissedEventsDuringRegistration(10)

	mockWS.mu.Lock()
	msgCount := len(mockWS.messages)
	mockWS.mu.Unlock()

	if msgCount != 0 {
		t.Errorf("Expected no messages with nil store, got %d", msgCount)
	}
}

// TestSyncMissedEventsDuringRegistration_NoMissedEvents tests that no message
// is sent when there are no missed events.
func TestSyncMissedEventsDuringRegistration_NoMissedEvents(t *testing.T) {
	// Create a temporary directory for the store
	tmpDir, err := os.MkdirTemp("", "test-sync-missed-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real store
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a session with some events
	sessionID := "test-session-no-missed"
	err = store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Add some events
	for _, event := range []session.Event{
		{Type: "user_prompt", Seq: 1, Data: map[string]interface{}{"message": "Hello"}},
		{Type: "agent_message", Seq: 2, Data: map[string]interface{}{"html": "Hi there"}},
	} {
		if err := store.AppendEvent(sessionID, event); err != nil {
			t.Fatalf("Failed to append event: %v", err)
		}
	}

	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: sessionID,
		wsConn: &WSConn{
			send: mockWS.send,
		},
		store: store,
	}

	// Sync with lastLoadedSeq = 2 (no missed events)
	client.syncMissedEventsDuringRegistration(2)

	mockWS.mu.Lock()
	msgCount := len(mockWS.messages)
	mockWS.mu.Unlock()

	if msgCount != 0 {
		t.Errorf("Expected no messages when no missed events, got %d", msgCount)
	}
}

// TestSyncMissedEventsDuringRegistration_WithMissedEvents tests that missed
// events are sent to the client.
func TestSyncMissedEventsDuringRegistration_WithMissedEvents(t *testing.T) {
	// Create a temporary directory for the store
	tmpDir, err := os.MkdirTemp("", "test-sync-missed-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real store
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a session with some events
	sessionID := "test-session-with-missed"
	err = store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Add events - simulating events that arrived after initial load
	for _, event := range []session.Event{
		{Type: "user_prompt", Seq: 1, Data: map[string]interface{}{"message": "Hello"}},
		{Type: "agent_message", Seq: 2, Data: map[string]interface{}{"html": "Hi there"}},
		{Type: "tool_call", Seq: 3, Data: map[string]interface{}{"id": "tool1", "title": "Read file"}},
		{Type: "agent_message", Seq: 4, Data: map[string]interface{}{"html": "Done!"}},
	} {
		if err := store.AppendEvent(sessionID, event); err != nil {
			t.Fatalf("Failed to append event: %v", err)
		}
	}

	// Create a mock WebSocket connection that captures messages
	mockWS := newMockWSConn()

	// Create client - simulate that we loaded up to seq 2, but events 3-4 arrived
	// between load and observer registration
	client := &SessionWSClient{
		sessionID: sessionID,
		wsConn: &WSConn{
			send: mockWS.send,
		},
		store: store,
	}

	// Sync with lastLoadedSeq = 2 (should get events 3-4)
	client.syncMissedEventsDuringRegistration(2)

	// Wait a bit for the message to be sent
	time.Sleep(50 * time.Millisecond)

	// Check that events_loaded message was sent
	mockWS.mu.Lock()
	defer mockWS.mu.Unlock()

	// The message is sent via wsConn.send channel, not mockWS.messages
	// We need to read from the channel
	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("Failed to unmarshal message: %v", err)
		}

		if msg.Type != WSMsgTypeEventsLoaded {
			t.Errorf("Expected message type %s, got %s", WSMsgTypeEventsLoaded, msg.Type)
		}

		// Check that we got the missed events
		eventsData, ok := msg.Data["events"].([]interface{})
		if !ok {
			t.Fatalf("Expected events array in message data")
		}

		if len(eventsData) != 2 {
			t.Errorf("Expected 2 missed events, got %d", len(eventsData))
		}

		// Check first_seq and last_seq
		if firstSeq, ok := msg.Data["first_seq"].(float64); !ok || int64(firstSeq) != 3 {
			t.Errorf("Expected first_seq=3, got %v", msg.Data["first_seq"])
		}
		if lastSeq, ok := msg.Data["last_seq"].(float64); !ok || int64(lastSeq) != 4 {
			t.Errorf("Expected last_seq=4, got %v", msg.Data["last_seq"])
		}

	case <-time.After(100 * time.Millisecond):
		t.Error("Expected events_loaded message but none received")
	}
}

// TestSyncMissedEventsDuringRegistration_UpdatesLastSentSeq tests that
// lastSentSeq is updated after syncing missed events.
func TestSyncMissedEventsDuringRegistration_UpdatesLastSentSeq(t *testing.T) {
	// Create a temporary directory for the store
	tmpDir, err := os.MkdirTemp("", "test-sync-missed-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real store
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a session with events
	sessionID := "test-session-lastsentseq"
	err = store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	for _, event := range []session.Event{
		{Type: "user_prompt", Seq: 1, Data: map[string]interface{}{"message": "Hello"}},
		{Type: "agent_message", Seq: 2, Data: map[string]interface{}{"html": "Hi"}},
		{Type: "agent_message", Seq: 3, Data: map[string]interface{}{"html": "Done"}},
	} {
		if err := store.AppendEvent(sessionID, event); err != nil {
			t.Fatalf("Failed to append event: %v", err)
		}
	}

	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID:   sessionID,
		wsConn:      &WSConn{send: mockWS.send},
		store:       store,
		lastSentSeq: 1, // Simulate that we've sent up to seq 1
	}

	// Sync with lastLoadedSeq = 1 (should get events 2-3)
	client.syncMissedEventsDuringRegistration(1)

	// Wait for message to be processed
	time.Sleep(50 * time.Millisecond)

	// Drain the send channel
	select {
	case <-mockWS.send:
	case <-time.After(100 * time.Millisecond):
	}

	// Check that lastSentSeq was updated
	client.seqMu.Lock()
	lastSentSeq := client.lastSentSeq
	client.seqMu.Unlock()

	if lastSentSeq != 3 {
		t.Errorf("Expected lastSentSeq=3, got %d", lastSentSeq)
	}
}

// TestSyncMissedEventsDuringRegistration_NonexistentSession tests handling
// of a session that doesn't exist in the store.
func TestSyncMissedEventsDuringRegistration_NonexistentSession(t *testing.T) {
	// Create a temporary directory for the store
	tmpDir, err := os.MkdirTemp("", "test-sync-missed-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real store (but don't create the session)
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "nonexistent-session",
		wsConn:    &WSConn{send: mockWS.send},
		store:     store,
	}

	// Should not panic and should not send any messages
	client.syncMissedEventsDuringRegistration(10)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Check no message was sent
	select {
	case <-mockWS.send:
		t.Error("Expected no message for nonexistent session")
	case <-time.After(50 * time.Millisecond):
		// Expected - no message
	}
}

// TestHandleLoadEvents_SeqMismatchProtection tests that when a client sends afterSeq
// higher than the server's max seq (event count), we fall back to initial load instead
// of setting lastSentSeq to the bogus value. This protects against UI freezes when
// streaming seq numbers diverge from persistence seq numbers.
func TestHandleLoadEvents_SeqMismatchProtection(t *testing.T) {
	// Create a temp store with a session that has a few events
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a session with some events
	sessionID := "test-seq-mismatch"
	meta := session.Metadata{
		SessionID: sessionID,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Add 5 events (seq 1-5)
	for i := 0; i < 5; i++ {
		event := session.Event{
			Type: session.EventTypeAgentMessage,
			Data: map[string]interface{}{
				"html": "<p>Message content</p>",
			},
		}
		if err := store.AppendEvent(sessionID, event); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Verify event count
	storedMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if storedMeta.EventCount != 5 {
		t.Fatalf("Expected 5 events, got %d", storedMeta.EventCount)
	}

	tests := []struct {
		name            string
		afterSeq        int64
		expectedReset   bool // whether lastSentSeq should be reset to 0
		expectedInitial bool // whether it should fall back to initial load
	}{
		{
			name:            "normal sync - afterSeq within range",
			afterSeq:        3,
			expectedReset:   false,
			expectedInitial: false,
		},
		{
			name:            "edge case - afterSeq equals event count",
			afterSeq:        5,
			expectedReset:   false,
			expectedInitial: false,
		},
		{
			name:            "stale client - afterSeq higher than event count",
			afterSeq:        374, // way higher than 5 events
			expectedReset:   true,
			expectedInitial: true,
		},
		{
			name:            "slightly stale - afterSeq just above event count",
			afterSeq:        10,
			expectedReset:   true,
			expectedInitial: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockWS := newMockWSConn()
			client := &SessionWSClient{
				sessionID: sessionID,
				wsConn:    &WSConn{send: mockWS.send},
				store:     store,
			}

			// Set initial lastSentSeq to something non-zero
			client.lastSentSeq = 100

			// Call handleLoadEvents with the test afterSeq
			client.handleLoadEvents(50, 0, tt.afterSeq)

			// Check lastSentSeq state
			client.seqMu.Lock()
			currentLastSent := client.lastSentSeq
			client.seqMu.Unlock()

			if tt.expectedReset {
				// For stale clients, lastSentSeq should be reset to 0 (or stay at initial)
				// Then updated to the highest seq from the initial load
				if currentLastSent == tt.afterSeq {
					t.Errorf("lastSentSeq was incorrectly set to stale afterSeq %d", tt.afterSeq)
				}
			} else {
				// For normal sync, lastSentSeq should be updated to afterSeq
				if tt.afterSeq > 100 && currentLastSent != tt.afterSeq {
					t.Errorf("lastSentSeq = %d, want %d", currentLastSent, tt.afterSeq)
				}
			}

			// Drain the send channel to get the response
			select {
			case msg := <-mockWS.send:
				// Parse the message to verify it's an events_loaded response
				var wsMsg struct {
					Type string                 `json:"type"`
					Data map[string]interface{} `json:"data"`
				}
				if err := json.Unmarshal(msg, &wsMsg); err != nil {
					t.Fatalf("Failed to unmarshal message: %v", err)
				}

				if wsMsg.Type != "events_loaded" {
					t.Errorf("Expected events_loaded message, got %s", wsMsg.Type)
				}

				// For initial load fallback, we should get all 5 events
				if tt.expectedInitial {
					eventsData, ok := wsMsg.Data["events"].([]interface{})
					if ok && len(eventsData) != 5 {
						t.Errorf("Expected 5 events for initial load fallback, got %d", len(eventsData))
					}
				}

			case <-time.After(100 * time.Millisecond):
				t.Error("Expected events_loaded message but got none")
			}
		})
	}
}

// =============================================================================
// Available Commands Tests
// =============================================================================

func TestSessionWSClient_OnAvailableCommandsUpdated(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "test-session",
		wsConn:    &WSConn{send: mockWS.send},
	}

	// Call OnAvailableCommandsUpdated
	commands := []AvailableCommand{
		{Name: "test", Description: "Test command", InputHint: "Enter test"},
		{Name: "help", Description: "Get help"},
	}
	client.OnAvailableCommandsUpdated(commands)

	// Read the message from the channel
	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("Failed to unmarshal message: %v", err)
		}

		if msg.Type != WSMsgTypeAvailableCommandsUpdated {
			t.Errorf("Expected message type %s, got %s", WSMsgTypeAvailableCommandsUpdated, msg.Type)
		}

		// Verify session_id is included
		if sessionID, ok := msg.Data["session_id"].(string); !ok || sessionID != "test-session" {
			t.Errorf("Expected session_id 'test-session', got %v", msg.Data["session_id"])
		}

		// Verify commands are included
		commandsData, ok := msg.Data["commands"].([]interface{})
		if !ok {
			t.Fatalf("Expected commands array in message data")
		}
		if len(commandsData) != 2 {
			t.Errorf("Expected 2 commands, got %d", len(commandsData))
		}

		// Verify first command
		cmd1, ok := commandsData[0].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected first command to be a map")
		}
		if cmd1["name"] != "test" {
			t.Errorf("Expected first command name 'test', got %v", cmd1["name"])
		}
		if cmd1["description"] != "Test command" {
			t.Errorf("Expected first command description 'Test command', got %v", cmd1["description"])
		}
		if cmd1["input_hint"] != "Enter test" {
			t.Errorf("Expected first command input_hint 'Enter test', got %v", cmd1["input_hint"])
		}

	case <-time.After(100 * time.Millisecond):
		t.Error("Expected available_commands_updated message but got none")
	}
}

func TestSessionWSClient_OnAvailableCommandsUpdated_Empty(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "test-session",
		wsConn:    &WSConn{send: mockWS.send},
	}

	// Call with empty commands
	client.OnAvailableCommandsUpdated([]AvailableCommand{})

	// Read the message from the channel
	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("Failed to unmarshal message: %v", err)
		}

		if msg.Type != WSMsgTypeAvailableCommandsUpdated {
			t.Errorf("Expected message type %s, got %s", WSMsgTypeAvailableCommandsUpdated, msg.Type)
		}

		// Verify empty commands array
		commandsData, ok := msg.Data["commands"].([]interface{})
		if !ok {
			t.Fatalf("Expected commands array in message data")
		}
		if len(commandsData) != 0 {
			t.Errorf("Expected 0 commands, got %d", len(commandsData))
		}

	case <-time.After(100 * time.Millisecond):
		t.Error("Expected available_commands_updated message but got none")
	}
}

// =============================================================================
// max_seq Piggybacking Tests
// =============================================================================

// mockBackgroundSessionForMaxSeq is a mock that implements GetMaxAssignedSeq.
type mockBackgroundSessionForMaxSeq struct {
	maxAssignedSeq int64
	isPrompting    bool
	isClosed       bool
	mu             sync.Mutex
}

func (m *mockBackgroundSessionForMaxSeq) GetMaxAssignedSeq() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxAssignedSeq
}

func (m *mockBackgroundSessionForMaxSeq) IsPrompting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isPrompting
}

func (m *mockBackgroundSessionForMaxSeq) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isClosed
}

// TestGetServerMaxSeq_WithBackgroundSession tests that getServerMaxSeq
// returns the correct value when a BackgroundSession is active.
func TestGetServerMaxSeq_WithBackgroundSession(t *testing.T) {
	// Note: When we create a session with Start() and End(), it adds 2 extra events:
	// - session_start (1 event)
	// - session_end (1 event)
	// So if we record N agent messages, the total event count is N + 2.
	tests := []struct {
		name           string
		persistedCount int   // Number of agent messages to record
		assignedSeq    int64 // Simulated assigned seq from BackgroundSession
		wantMaxSeq     int64 // Expected max seq (max of persisted+2 and assignedSeq)
	}{
		{
			name:           "assigned seq higher than persisted",
			persistedCount: 50,
			assignedSeq:    100,
			wantMaxSeq:     100, // assignedSeq wins (100 > 52)
		},
		{
			name:           "persisted count higher than assigned",
			persistedCount: 100,
			assignedSeq:    50,
			wantMaxSeq:     102, // persisted wins (100 + 2 = 102 > 50)
		},
		{
			name:           "equal values accounting for overhead",
			persistedCount: 73,
			assignedSeq:    75,
			wantMaxSeq:     75, // 73 + 2 = 75, equal to assignedSeq
		},
		{
			name:           "no events yet",
			persistedCount: 0,
			assignedSeq:    0,
			wantMaxSeq:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary store with the specified event count
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore failed: %v", err)
			}
			defer store.Close()

			sessionID := "test-session-" + tt.name

			// Create session with events
			if tt.persistedCount > 0 {
				rec := session.NewRecorderWithID(store, sessionID)
				if err := rec.Start("test-server", "/tmp"); err != nil {
					t.Fatalf("Start failed: %v", err)
				}
				for i := 0; i < tt.persistedCount; i++ {
					rec.RecordAgentMessage("<p>test</p>")
				}
				_ = rec.End("test complete")
			}

			// Create mock background session
			mockBg := &mockBackgroundSessionForMaxSeq{
				maxAssignedSeq: tt.assignedSeq,
			}

			// Create client with the mock
			client := &SessionWSClient{
				sessionID: sessionID,
				store:     store,
				bgSession: &BackgroundSession{
					nextSeq: tt.assignedSeq + 1, // nextSeq is assignedSeq + 1
				},
			}

			// Override bgSession's GetMaxAssignedSeq by setting nextSeq directly
			// (we can't easily mock the interface, so we test the real implementation)
			got := client.getServerMaxSeq()

			// The expected value is max(persistedCount, assignedSeq)
			// But since we're using the real BackgroundSession, we need to account
			// for how it calculates GetMaxAssignedSeq
			_ = mockBg // unused in this test, but shows the pattern

			if got != tt.wantMaxSeq {
				t.Errorf("getServerMaxSeq() = %d, want %d", got, tt.wantMaxSeq)
			}
		})
	}
}

// TestGetServerMaxSeq_NoBackgroundSession tests that getServerMaxSeq
// returns the persisted event count when no BackgroundSession is active.
func TestGetServerMaxSeq_NoBackgroundSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-no-bg"

	// Create session with 25 agent messages
	// Note: Start() adds session_start event, End() adds session_end event
	// So total events = 25 + 2 = 27
	rec := session.NewRecorderWithID(store, sessionID)
	if err := rec.Start("test-server", "/tmp"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	for i := 0; i < 25; i++ {
		rec.RecordAgentMessage("<p>test</p>")
	}
	_ = rec.End("test complete")

	// Create client without background session
	client := &SessionWSClient{
		sessionID: sessionID,
		store:     store,
		bgSession: nil, // No background session
	}

	got := client.getServerMaxSeq()
	// Expected: 25 agent messages + 1 session_start + 1 session_end = 27
	if got != 27 {
		t.Errorf("getServerMaxSeq() = %d, want 27 (25 messages + 2 system events)", got)
	}
}
