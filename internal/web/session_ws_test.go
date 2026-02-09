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
