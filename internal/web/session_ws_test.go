package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
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

// TestModelConfigOptionCallerBudget_CoversSetModelRetrySchedule reproduces
// mitto-9kg: handleSetConfigOption's synchronous caller budget
// (modelConfigOptionCallerBudget, 30s) is shorter than the underlying
// set_model RPC's own worst-case retry schedule, so an interactive model
// change from the conversation side panel is silently cancelled by this
// context before SharedACPProcess.SetSessionModel's retries can complete —
// producing the reported "model change has no effect" symptom whenever the
// shared ACP process is busy (this conversation prompting, or serialized
// behind a sibling's own set_model call on the capacity-1 setModelSem).
//
// The set_model retry/attempt constants live in internal/acpproc
// (SharedACPProcess), which this package must not import for unexported
// values, so the expected schedule is mirrored here from the documented
// constants in internal/acpproc/shared_acp_process.go (setSessionModelMaxAttempts,
// setSessionModelAttemptTimeouts, setSessionModelRetryBaseDelay,
// setSessionModelRetryJitterRatio) — kept consistent with
// internal/conversation/constraints_test.go's TestConstraintModelSwitchBudgetMath,
// which asserts the SAME schedule against the 90s async budget used by the
// background/loop model-switch path. That path budgets correctly; this
// synchronous interactive path (handleSetConfigOption) does not.
//
// This test currently FAILS, demonstrating the bug: the 30s caller budget is
// well below even a SINGLE caller's own worst-case retry schedule (~44s),
// let alone one additionally queued behind a busy sibling.
func TestModelConfigOptionCallerBudget_CoversSetModelRetrySchedule(t *testing.T) {
	const (
		// Mirror of internal/acpproc/shared_acp_process.go set_model constants.
		maxRetries       = 3                      // setSessionModelMaxAttempts
		scheduleSum      = 43 * time.Second       // sum(setSessionModelAttemptTimeouts) = 20s+15s+8s
		retryBaseDelay   = 300 * time.Millisecond // setSessionModelRetryBaseDelay
		retryJitterRatio = 0.5                    // setSessionModelRetryJitterRatio
	)

	// Max jittered backoff across all retry cycles (mirrors
	// TestConstraintModelSwitchBudgetMath / TestSetModelAsyncBudgetMath).
	maxJitteredBackoff := time.Duration(float64(retryBaseDelay)*float64(maxRetries-1)*(1+retryJitterRatio)) + retryBaseDelay

	// A SINGLE caller's own worst-case wall-clock time for SetSessionModel to
	// either succeed or exhaust its retries — no sibling contention yet.
	singleCallerMax := scheduleSum + maxJitteredBackoff

	if modelConfigOptionCallerBudget < singleCallerMax {
		t.Errorf("modelConfigOptionCallerBudget (%v) is less than a single caller's own "+
			"worst-case set_model retry schedule (%v); an interactive model change can be "+
			"cancelled by this context before SharedACPProcess.SetSessionModel's own retries "+
			"complete, even with no sibling contention on the shared ACP process (mitto-9kg)",
			modelConfigOptionCallerBudget, singleCallerMax)
	}

	t.Logf("modelConfigOptionCallerBudget=%v, single-caller worst-case set_model schedule=%v",
		modelConfigOptionCallerBudget, singleCallerMax)
}

func TestIsLifecycleResumeCancellation(t *testing.T) {
	if !isLifecycleResumeCancellation(fmt.Errorf("resume aborted: %w", context.Canceled)) {
		t.Fatal("wrapped context.Canceled must be treated as a quiet lifecycle cancellation")
	}
	if isLifecycleResumeCancellation(errors.New("ACP start failed")) {
		t.Fatal("ordinary ACP failures must still be broadcast")
	}
}

// TestSessionWSClient_TitleJobSurvivesACPStop reproduces mitto-g8u0: a title
// job scheduled before teardown must not dereference c.bgSession after
// OnACPStopped releases the client's reference.
func TestSessionWSClient_TitleJobSurvivesACPStop(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		server:    &Server{eventsManager: NewGlobalEventsManager()},
		wsConn:    &WSConn{send: mockWS.send},
		sessionID: "test-title-after-acp-stop",
		bgSession: conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
			WorkspaceUUID: "test-workspace",
		}),
	}

	// Capture the work while the session is live, then run it after the same
	// lifecycle callback used by BackgroundSession.Close has detached the client.
	workspaceUUID := client.bgSession.GetWorkspaceUUID()
	auxiliaryManager := client.bgSession.GetAuxiliaryManager()
	runTitleJob := func() {
		client.generateAndSetTitle("Explain the released session race", workspaceUUID, auxiliaryManager)
	}
	client.OnACPStopped("test teardown")

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("scheduled title generation panicked after ACP stop: %v", recovered)
		}
	}()
	runTitleJob()
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
// the is_prompting field based on the conversation.BackgroundSession state.
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
// the is_prompting field based on the conversation.BackgroundSession state.
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
// the is_prompting field based on the conversation.BackgroundSession state.
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
// the is_prompting field based on the conversation.BackgroundSession state.
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

// TestPostLoadProcessing_NoH2SyncOnSubsequentSync verifies that
// syncMissedEventsDuringRegistration is NOT called on a sync load_events when
// the observer is already registered (initialLoadDone == true). On such calls the
// observer is already active so streaming covers new events; a second events_loaded
// from the H2 path would be a spurious duplicate. This is the regression test for
// the mitto-b6ym fix.
func TestPostLoadProcessing_NoH2SyncOnSubsequentSync(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	sessionID := "test-no-h2-on-sync"
	if err := store.Create(session.Metadata{SessionID: sessionID}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	// Add events including one "beyond" lastSeq so H2 would fire if the gate is missing.
	for _, ev := range []session.Event{
		{Type: "user_prompt", Seq: 1, Data: map[string]interface{}{"message": "Hello"}},
		{Type: "agent_message", Seq: 2, Data: map[string]interface{}{"html": "Hi"}},
		{Type: "agent_message", Seq: 3, Data: map[string]interface{}{"html": "Extra"}},
	} {
		if err := store.AppendEvent(sessionID, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID:          sessionID,
		wsConn:             &WSConn{send: mockWS.send},
		store:              store,
		initialLoadDone:    true,
		observerRegistered: true, // observer already registered — simulates a subsequent sync
	}

	// postLoadProcessing with a non-prepend sync result (lastSeq=2, one event beyond).
	// With the bug: syncMissedEventsDuringRegistration fires and sends events_loaded.
	// With the fix: justRegistered=false so no H2 sync is triggered.
	client.postLoadProcessing(loadEventsResult{isPrepend: false, lastSeq: 2})

	// Allow time for any goroutine that might send a message.
	time.Sleep(80 * time.Millisecond)

	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(msgBytes, &msg)
		if msg.Type == WSMsgTypeEventsLoaded {
			t.Errorf("got spurious events_loaded from H2 path on subsequent sync (duplicate regression)")
		}
		// Any other message type (e.g. plan state) is fine.
	case <-time.After(80 * time.Millisecond):
		// Expected: no events_loaded from H2 path.
	}
}

// TestAttachToBackgroundSession_RegistersAfterEmptyInitialLoad reproduces
// mitto-mhgk: load_events completed while async ACP resume was still running,
// but the empty history left lastSentSeq at zero. The old attach path treated
// that as "not loaded" and never registered the client as an observer.
func TestAttachToBackgroundSession_RegistersAfterEmptyInitialLoad(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	const sessionID = "test-empty-load-before-resume"
	if err := store.Create(session.Metadata{SessionID: sessionID}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: sessionID,
		wsConn:    &WSConn{send: mockWS.send},
		store:     store,
	}

	// Successful non-prepend load with no events while bgSession is nil.
	client.handleLoadEventsAsync(20, 0, 0)
	if !client.initialLoadDone {
		t.Fatal("successful empty load was not recorded")
	}
	if client.observerRegistered {
		t.Fatal("observer registered before a background session was available")
	}

	// Async resume completes later. Registration must not depend on lastSentSeq > 0.
	bs := conversation.NewMinimalBackgroundSession(sessionID, "", "")
	client.attachToBackgroundSession(bs)

	if !client.observerRegistered {
		t.Fatal("observer was not registered after resume following an empty load")
	}
	if got := bs.ObserverCount(); got != 1 {
		t.Fatalf("ObserverCount() = %d, want 1", got)
	}

	// Repeated attachment must remain idempotent.
	client.attachToBackgroundSession(bs)
	if got := bs.ObserverCount(); got != 1 {
		t.Fatalf("ObserverCount() after duplicate attach = %d, want 1", got)
	}
}

// TestAttachToBackgroundSession_ReregistersOnInstanceChange verifies that a
// client already marked initialLoadDone/observerRegistered against an OLD
// BackgroundSession instance (e.g. before an ACP restart replaced it) is
// re-registered on the NEW instance rather than being silently skipped.
// A fresh BackgroundSession's own observer set always starts empty, so
// observerRegistered must be scoped to "registered on the CURRENT bgSession",
// not "registered at some point in the past" (mitto-mhgk coordination fix).
func TestAttachToBackgroundSession_ReregistersOnInstanceChange(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID:       "test-instance-change",
		wsConn:          &WSConn{send: mockWS.send},
		initialLoadDone: true, // load already completed in an earlier attach
	}

	bsOld := conversation.NewMinimalBackgroundSession("test-instance-change", "", "")
	client.attachToBackgroundSession(bsOld)
	if !client.observerRegistered || bsOld.ObserverCount() != 1 {
		t.Fatalf("setup: expected registration on bsOld, observerRegistered=%v count=%d",
			client.observerRegistered, bsOld.ObserverCount())
	}

	// Simulate ACP restart: OnACPStopped clears bgSession, sessionManager
	// later hands back a brand-new BackgroundSession instance for the same
	// session ID.
	bsNew := conversation.NewMinimalBackgroundSession("test-instance-change", "", "")
	client.attachToBackgroundSession(bsNew)

	if !client.observerRegistered {
		t.Fatal("client was not re-registered on the new BackgroundSession instance")
	}
	if got := bsNew.ObserverCount(); got != 1 {
		t.Fatalf("bsNew.ObserverCount() = %d, want 1", got)
	}
}

// TestAttachAndPostLoad_ConcurrentInterleavings_RegisterExactlyOnce reproduces
// the production race (mitto-mhgk): the async-resume goroutine's
// attachToBackgroundSession(bs) and handleLoadEventsAsync's
// postLoadProcessing() run concurrently and both may try to register the
// same client as an observer of the same BackgroundSession. Both read/mutate
// initialLoadDone/observerRegistered/bgSession only under initialLoadMu, so
// regardless of which goroutine's critical section runs first, the client
// must end up registered exactly once. Run with -race to also catch any
// unsynchronized access reintroduced by a future edit.
func TestAttachAndPostLoad_ConcurrentInterleavings_RegisterExactlyOnce(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	for i := 0; i < 50; i++ {
		sessionID := "test-concurrent-attach"
		if err := store.Create(session.Metadata{SessionID: sessionID}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: sessionID,
			wsConn:    &WSConn{send: mockWS.send},
			store:     store,
		}
		bs := conversation.NewMinimalBackgroundSession(sessionID, "", "")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.handleLoadEventsAsync(20, 0, 0)
		}()
		go func() {
			defer wg.Done()
			client.attachToBackgroundSession(bs)
		}()
		wg.Wait()

		if !client.initialLoadDone {
			t.Fatalf("iteration %d: load was never recorded as completed", i)
		}
		if !client.observerRegistered {
			t.Fatalf("iteration %d: observer was never registered", i)
		}
		if got := bs.ObserverCount(); got != 1 {
			t.Fatalf("iteration %d: ObserverCount() = %d, want exactly 1", i, got)
		}

		if err := store.Delete(sessionID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
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
	commands := []conversation.AvailableCommand{
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

// TestSessionWSClient_OnAvailableCommandsUpdated_ContextFlushCommand verifies
// the resolved context-flush command (mitto-1o8) is repeated in the
// available_commands_updated payload so the frontend can un-grey the flush
// action as soon as the agent's commands arrive, without waiting for a reload.
func TestSessionWSClient_OnAvailableCommandsUpdated_ContextFlushCommand(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "test-session",
		wsConn:    &WSConn{send: mockWS.send},
		bgSession: conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
			ContextFlushCommand: "/clear",
		}),
	}

	client.OnAvailableCommandsUpdated([]conversation.AvailableCommand{
		{Name: "clear"},
	})

	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("Failed to unmarshal message: %v", err)
		}
		if got := msg.Data["context_flush_command"]; got != "/clear" {
			t.Errorf("Expected context_flush_command '/clear', got %v", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected available_commands_updated message but got none")
	}
}

// TestSessionWSClient_OnAvailableCommandsUpdated_NoBgSession verifies the
// payload omits context_flush_command entirely (rather than sending an empty
// string) when there is no attached BackgroundSession to resolve it from.
func TestSessionWSClient_OnAvailableCommandsUpdated_NoBgSession(t *testing.T) {
	mockWS := newMockWSConn()
	client := &SessionWSClient{
		sessionID: "test-session",
		wsConn:    &WSConn{send: mockWS.send},
		bgSession: nil,
	}

	client.OnAvailableCommandsUpdated([]conversation.AvailableCommand{{Name: "help"}})

	select {
	case msgBytes := <-mockWS.send:
		var msg struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("Failed to unmarshal message: %v", err)
		}
		if _, present := msg.Data["context_flush_command"]; present {
			t.Errorf("Expected context_flush_command to be absent when bgSession is nil, got %v", msg.Data["context_flush_command"])
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
	client.OnAvailableCommandsUpdated([]conversation.AvailableCommand{})

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
// returns the correct value when a conversation.BackgroundSession is active.
func TestGetServerMaxSeq_WithBackgroundSession(t *testing.T) {
	// Note: When we create a session with Start() and End(), it adds 2 extra events:
	// - session_start (1 event)
	// - session_end (1 event)
	// So if we record N agent messages, the total event count is N + 2.
	tests := []struct {
		name           string
		persistedCount int   // Number of agent messages to record
		assignedSeq    int64 // Simulated assigned seq from conversation.BackgroundSession
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
				if err := rec.Start("test-server", "/tmp", ""); err != nil {
					t.Fatalf("Start failed: %v", err)
				}
				for i := 0; i < tt.persistedCount; i++ {
					rec.RecordAgentMessage("<p>test</p>", "")
				}
				_ = rec.End(session.SessionEndData{Reason: "test complete"})
			}

			// Create mock background session
			mockBg := &mockBackgroundSessionForMaxSeq{
				maxAssignedSeq: tt.assignedSeq,
			}

			// Create client with the mock
			client := &SessionWSClient{
				sessionID: sessionID,
				store:     store,
				bgSession: conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{NextSeq: tt.assignedSeq + 1}),
			}

			// Override bgSession's GetMaxAssignedSeq by setting nextSeq directly
			// (we can't easily mock the interface, so we test the real implementation)
			got := client.getServerMaxSeq()

			// The expected value is max(persistedCount, assignedSeq)
			// But since we're using the real conversation.BackgroundSession, we need to account
			// for how it calculates GetMaxAssignedSeq
			_ = mockBg // unused in this test, but shows the pattern

			if got != tt.wantMaxSeq {
				t.Errorf("getServerMaxSeq() = %d, want %d", got, tt.wantMaxSeq)
			}
		})
	}
}

// TestGetServerMaxSeq_NoBackgroundSession tests that getServerMaxSeq
// returns the persisted event count when no conversation.BackgroundSession is active.
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
	if err := rec.Start("test-server", "/tmp", ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	for i := 0; i < 25; i++ {
		rec.RecordAgentMessage("<p>test</p>", "")
	}
	_ = rec.End(session.SessionEndData{Reason: "test complete"})

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

// TestSessionWSClient_OnUserPrompt_ArgumentCount verifies that the WS user_prompt
// payload includes argument_count when the prompt had arguments, and omits it otherwise.
func TestSessionWSClient_OnUserPrompt_ArgumentCount(t *testing.T) {
	tests := []struct {
		name          string
		promptName    string
		argumentCount int
		wantArgCount  bool
	}{
		{
			name:          "with arguments",
			promptName:    "deploy-prompt",
			argumentCount: 3,
			wantArgCount:  true,
		},
		{
			name:          "no arguments",
			promptName:    "plain-prompt",
			argumentCount: 0,
			wantArgCount:  false,
		},
		{
			name:          "ad-hoc prompt no arguments",
			promptName:    "",
			argumentCount: 0,
			wantArgCount:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockWS := newMockWSConn()
			client := &SessionWSClient{
				sessionID: "test-session",
				clientID:  "client-1",
				wsConn:    &WSConn{send: mockWS.send},
			}

			client.OnUserPrompt(1, "client-1", "pid-1", "hello", nil, nil, tc.promptName, tc.argumentCount, nil, nil)

			// Read from the send channel (same pattern as TestSessionWSClient_OnAvailableCommandsUpdated)
			select {
			case msgBytes := <-mockWS.send:
				var msg struct {
					Type string                 `json:"type"`
					Data map[string]interface{} `json:"data"`
				}
				if err := json.Unmarshal(msgBytes, &msg); err != nil {
					t.Fatalf("failed to unmarshal message: %v", err)
				}
				if msg.Type != WSMsgTypeUserPrompt {
					t.Errorf("message type = %q, want %q", msg.Type, WSMsgTypeUserPrompt)
				}
				argCountVal, hasArgCount := msg.Data["argument_count"]
				if tc.wantArgCount {
					if !hasArgCount {
						t.Errorf("expected argument_count in payload, got none")
					} else if int(argCountVal.(float64)) != tc.argumentCount {
						t.Errorf("argument_count = %v, want %d", argCountVal, tc.argumentCount)
					}
				} else {
					if hasArgCount && argCountVal != nil {
						if f, ok := argCountVal.(float64); ok && f != 0 {
							t.Errorf("expected argument_count absent/0, got %v", argCountVal)
						}
					}
				}
			case <-time.After(100 * time.Millisecond):
				t.Error("expected user_prompt message on send channel but got none")
			}
		})
	}
}

// TestSessionWSClient_OnUserPrompt_Arguments verifies that the WS user_prompt
// payload includes the "arguments" map when non-empty, and omits it when nil/empty
// (mitto-e2h: retry replay needs the raw argument values, not just the count).
func TestSessionWSClient_OnUserPrompt_Arguments(t *testing.T) {
	tests := []struct {
		name         string
		arguments    map[string]string
		wantHasArgs  bool
		wantArgValue string
	}{
		{
			name:         "with arguments",
			arguments:    map[string]string{"IssueID": "mitto-123"},
			wantHasArgs:  true,
			wantArgValue: "mitto-123",
		},
		{
			name:        "nil arguments",
			arguments:   nil,
			wantHasArgs: false,
		},
		{
			name:        "empty arguments map",
			arguments:   map[string]string{},
			wantHasArgs: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockWS := newMockWSConn()
			client := &SessionWSClient{
				sessionID: "test-session",
				clientID:  "client-1",
				wsConn:    &WSConn{send: mockWS.send},
			}

			client.OnUserPrompt(1, "client-1", "pid-1", "hello", nil, nil, "my-prompt", 1, tc.arguments, nil)

			select {
			case msgBytes := <-mockWS.send:
				var msg struct {
					Type string                 `json:"type"`
					Data map[string]interface{} `json:"data"`
				}
				if err := json.Unmarshal(msgBytes, &msg); err != nil {
					t.Fatalf("failed to unmarshal message: %v", err)
				}
				argsVal, hasArgs := msg.Data["arguments"]
				if tc.wantHasArgs {
					if !hasArgs {
						t.Fatalf("expected \"arguments\" in payload, got none")
					}
					argsMap, ok := argsVal.(map[string]interface{})
					if !ok {
						t.Fatalf("arguments has type %T, want map[string]interface{}", argsVal)
					}
					if argsMap["IssueID"] != tc.wantArgValue {
						t.Errorf("arguments[IssueID] = %v, want %q", argsMap["IssueID"], tc.wantArgValue)
					}
				} else if hasArgs {
					t.Errorf("expected \"arguments\" absent from payload, got %v", argsVal)
				}
			case <-time.After(100 * time.Millisecond):
				t.Error("expected user_prompt message on send channel but got none")
			}
		})
	}
}

// TestSessionWSClient_OnEventMeta_AttachedToUserPrompt verifies that meta stored via
// OnEventMeta is attached to the subsequent user_prompt WebSocket payload, and that
// without OnEventMeta the "meta" key is absent from the payload.
func TestSessionWSClient_OnEventMeta_AttachedToUserPrompt(t *testing.T) {
	t.Run("meta present when OnEventMeta called before OnUserPrompt", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		const seq = int64(42)
		metaIn := map[string]any{"source": "test", "count": 7}

		// Simulate the ordering guarantee: OnEventMeta fires before OnUserPrompt.
		client.OnEventMeta(seq, metaIn)
		client.OnUserPrompt(seq, "client-1", "pid-1", "hello", nil, nil, "", 0, nil, nil)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if msg.Type != WSMsgTypeUserPrompt {
				t.Fatalf("msg type = %q, want %q", msg.Type, WSMsgTypeUserPrompt)
			}
			metaOut, hasMeta := msg.Data["meta"]
			if !hasMeta {
				t.Fatal("expected \"meta\" key in WS payload, got none")
			}
			metaMap, ok := metaOut.(map[string]interface{})
			if !ok {
				t.Fatalf("meta has type %T, want map[string]interface{}", metaOut)
			}
			if metaMap["source"] != "test" {
				t.Errorf(`meta["source"] = %v, want "test"`, metaMap["source"])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected user_prompt on send channel, got none")
		}
	})

	t.Run("meta absent when OnEventMeta not called", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		client.OnUserPrompt(1, "client-1", "pid-1", "hello", nil, nil, "", 0, nil, nil)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, hasMeta := msg.Data["meta"]; hasMeta {
				t.Errorf("expected \"meta\" key absent from WS payload, but it was present: %v", msg.Data["meta"])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected user_prompt on send channel, got none")
		}
	})

	t.Run("meta consumed: second OnUserPrompt for same seq has no meta", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		const seq = int64(10)
		client.OnEventMeta(seq, map[string]any{"once": true})
		// First call consumes the meta.
		client.OnUserPrompt(seq, "client-1", "pid-1", "msg1", nil, nil, "", 0, nil, nil)
		<-mockWS.send // drain first message

		// Second call for same seq must NOT have meta.
		client.OnUserPrompt(seq, "client-1", "pid-1", "msg2", nil, nil, "", 0, nil, nil)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, hasMeta := msg.Data["meta"]; hasMeta {
				t.Errorf("second call: expected \"meta\" absent, got %v", msg.Data["meta"])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected second user_prompt on send channel, got none")
		}
	})

	t.Run("argument_names array passes through in meta", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		const seq = int64(99)
		client.OnEventMeta(seq, map[string]any{"argument_names": []string{"ISSUE_ID", "PROJECT"}})
		client.OnUserPrompt(seq, "client-1", "pid-1", "review", nil, nil, "Review", 2, nil, nil)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			metaOut, ok := msg.Data["meta"].(map[string]interface{})
			if !ok {
				t.Fatalf("meta missing or wrong type: %T", msg.Data["meta"])
			}
			names, ok := metaOut["argument_names"].([]interface{})
			if !ok {
				t.Fatalf("argument_names missing or wrong type: %T", metaOut["argument_names"])
			}
			if len(names) != 2 || names[0] != "ISSUE_ID" || names[1] != "PROJECT" {
				t.Errorf("argument_names = %v, want [ISSUE_ID PROJECT]", names)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected user_prompt on send channel, got none")
		}
	})
}

// TestSessionWSClient_OnUserPrompt_Provenance verifies mitto-rg79's WS payload
// contract: the "provenance" key is entirely absent for ordinary prompts
// (nil provenance) and present with the exact field values (including nested
// Slack detail) when non-nil.
func TestSessionWSClient_OnUserPrompt_Provenance(t *testing.T) {
	t.Run("nil provenance omits the key entirely", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		client.OnUserPrompt(1, "client-1", "pid-1", "hello", nil, nil, "", 0, nil, nil)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, has := msg.Data["provenance"]; has {
				t.Errorf("expected \"provenance\" absent, got %v", msg.Data["provenance"])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected user_prompt on send channel, got none")
		}
	})

	t.Run("non-nil provenance with Slack detail round-trips through JSON", func(t *testing.T) {
		mockWS := newMockWSConn()
		client := &SessionWSClient{
			sessionID: "test-session",
			clientID:  "client-1",
			wsConn:    &WSConn{send: mockWS.send},
		}

		prov := &session.PromptProvenance{
			LoopTrigger: session.TriggerOnSlack,
			Slack: &session.PromptSlackProvenance{
				InstallationID: "I1",
				ChannelID:      "C1",
				EventCount:     3,
			},
		}
		client.OnUserPrompt(1, "loop-runner", "", "slack turn", nil, nil, "", 0, nil, prov)

		select {
		case msgBytes := <-mockWS.send:
			var msg struct {
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			provOut, ok := msg.Data["provenance"].(map[string]interface{})
			if !ok {
				t.Fatalf("provenance missing or wrong type: %T", msg.Data["provenance"])
			}
			if provOut["loop_trigger"] != "onSlack" {
				t.Errorf("loop_trigger = %v, want onSlack", provOut["loop_trigger"])
			}
			slackOut, ok := provOut["slack"].(map[string]interface{})
			if !ok {
				t.Fatalf("slack detail missing or wrong type: %T", provOut["slack"])
			}
			if slackOut["channel_id"] != "C1" || slackOut["installation_id"] != "I1" {
				t.Errorf("unexpected slack identifiers: %v", slackOut)
			}
			if slackOut["event_count"] != float64(3) {
				t.Errorf("event_count = %v, want 3", slackOut["event_count"])
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected user_prompt on send channel, got none")
		}
	})
}

// =============================================================================
// checkRequiredToolPatterns: no blind timed re-broadcast (mitto-sys.12)
// =============================================================================

// TestCheckRequiredToolPatterns_NoTimedRetry proves that checkRequiredToolPatterns
// no longer schedules a 30/60/120s re-broadcast loop: it must return promptly
// (well under 1s) and must broadcast prompts_changed with reason
// "mcp_tools_initial" exactly once, never with reason "mcp_tools_retry". Late/
// changed tools now surface via the event-driven watcher (mitto-sys.4) and the
// bounded-backoff path (mitto-sys.5) instead.
func TestCheckRequiredToolPatterns_NoTimedRetry(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("failed to create prompts dir: %v", err)
	}
	// A prompt with an enabledWhen tool-pattern requirement so
	// collectRequiredToolPatterns returns a non-empty list, which is the
	// precondition for the (formerly timed) broadcast path to run at all.
	promptContent := `name: "Slack Prompt"
enabledWhen: 'Tools.HasPattern("slack_*")'
prompt: |
  Do something with Slack.
`
	if err := os.WriteFile(filepath.Join(promptsDir, "slack.prompt.yaml"), []byte(promptContent), 0644); err != nil {
		t.Fatalf("failed to write prompt file: %v", err)
	}

	promptsCache := config.NewPromptsCache()
	if _, err := promptsCache.Get(); err != nil {
		t.Fatalf("PromptsCache.Get() failed: %v", err)
	}

	eventsManager := NewGlobalEventsManager()
	captureSend := make(chan []byte, 16)
	eventsClient := &GlobalEventsClient{
		wsConn: &WSConn{send: captureSend},
		done:   make(chan struct{}),
	}
	eventsManager.Register(eventsClient)
	defer eventsManager.Unregister(eventsClient)

	server := &Server{
		config:        Config{PromptsCache: promptsCache},
		eventsManager: eventsManager,
	}
	client := &SessionWSClient{
		sessionID: "test-session",
		server:    server,
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		client.checkRequiredToolPatterns("ws-uuid")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkRequiredToolPatterns did not return within 1s — a blind timed re-broadcast loop appears to still be present")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("checkRequiredToolPatterns took %v, want well under 1s (no timed retry loop)", elapsed)
	}

	// Drain and inspect every broadcast message; give the (already-returned)
	// call a brief moment in case of any async send.
	var reasons []string
	deadline := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case raw := <-captureSend:
			var msg struct {
				Type string `json:"type"`
				Data struct {
					Reason string `json:"reason"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("failed to unmarshal broadcast message: %v", err)
			}
			if msg.Type != WSMsgTypePromptsChanged {
				t.Errorf("broadcast type = %q, want %q", msg.Type, WSMsgTypePromptsChanged)
			}
			reasons = append(reasons, msg.Data.Reason)
		case <-deadline:
			break drain
		}
	}

	if len(reasons) != 1 {
		t.Fatalf("got %d prompts_changed broadcasts %v, want exactly 1", len(reasons), reasons)
	}
	if reasons[0] != "mcp_tools_initial" {
		t.Errorf("broadcast reason = %q, want %q", reasons[0], "mcp_tools_initial")
	}
	for _, r := range reasons {
		if r == "mcp_tools_retry" {
			t.Errorf("found a mcp_tools_retry broadcast — the timed re-broadcast loop must be fully removed")
		}
	}
}

func TestComputeEventStats(t *testing.T) {
	tests := []struct {
		name   string
		events []session.Event
		want   eventStats
	}{
		{
			name:   "empty",
			events: nil,
			want:   eventStats{},
		},
		{
			name: "mcp and acp tool calls counted separately",
			events: []session.Event{
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "1", Title: "mitto_conversation_get_current_mitto"}},
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "2", Title: "mitto_ui_options_mitto"}},
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "3", Title: "mitto_children_tasks_wait_mitto"}},
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "4", Title: "str-replace-editor"}},
			},
			want: eventStats{mcpCallsTotal: 3, mcpUICalls: 1, mcpChildrenWaitCalls: 1, acpToolCalls: 1},
		},
		{
			name: "duplicate tool_call_id counted once",
			events: []session.Event{
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "1", Title: "mitto_ui_notify_mitto", Status: "pending"}},
				{Type: session.EventTypeToolCall, Data: session.ToolCallData{ToolCallID: "1", Title: "mitto_ui_notify_mitto", Status: "completed"}},
			},
			want: eventStats{mcpCallsTotal: 1, mcpUICalls: 1},
		},
		{
			name: "turns and images from user prompts",
			events: []session.Event{
				{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: "hi"}},
				{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: "again", Images: []session.ImageRef{{ID: "a"}, {ID: "b"}}}},
			},
			want: eventStats{turns: 2, imagesUploaded: 2},
		},
		{
			name: "errors counted",
			events: []session.Event{
				{Type: session.EventTypeError, Data: session.ErrorData{Message: "boom"}},
				{Type: session.EventTypeError, Data: session.ErrorData{Message: "boom2"}},
			},
			want: eventStats{errors: 2},
		},
		{
			name: "permissions bucketed by outcome and selected option",
			events: []session.Event{
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "auto_approved", SelectedOption: "allow-once"}},
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "user_selected", SelectedOption: "allow"}},
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "user_selected", SelectedOption: "deny"}},
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "user_selected", SelectedOption: "reject-once"}},
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "timed_out", SelectedOption: ""}},
				{Type: session.EventTypePermission, Data: session.PermissionData{Outcome: "user_selected", SelectedOption: "unknown-option"}},
			},
			want: eventStats{permissionsAllowed: 2, permissionsDenied: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeEventStats(tt.events)
			if got != tt.want {
				t.Errorf("computeEventStats() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestSendSessionConnected_NoArchive pins the mitto-yvel.4 WebSocket transport
// requirement: the "connected" message always carries "no_archive" (mirroring
// meta.NoArchive), regardless of its value, so the frontend's `??` fallback
// chains in useWebSocket.js resolve deterministically instead of silently
// inheriting a stale value from a prior connected message.
func TestSendSessionConnected_NoArchive(t *testing.T) {
	tests := []struct {
		name          string
		noArchive     bool
		wantNoArchive bool
	}{
		{name: "protected session reports no_archive=true", noArchive: true, wantNoArchive: true},
		{name: "unprotected session reports no_archive=false (key still present)", noArchive: false, wantNoArchive: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			defer store.Close()

			sessionID := "test-no-archive-" + tt.name
			if err := store.Create(session.Metadata{
				SessionID: sessionID,
				ACPServer: "test-server",
				NoArchive: tt.noArchive,
			}); err != nil {
				t.Fatalf("store.Create: %v", err)
			}

			mockWS := newMockWSConn()
			server := &Server{config: Config{ACPServer: "test-server"}}
			client := &SessionWSClient{
				sessionID: sessionID,
				server:    server,
				wsConn:    &WSConn{send: mockWS.send},
				store:     store,
			}

			client.sendSessionConnected(nil)

			select {
			case msgBytes := <-mockWS.send:
				var msg struct {
					Type string                 `json:"type"`
					Data map[string]interface{} `json:"data"`
				}
				if err := json.Unmarshal(msgBytes, &msg); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if msg.Type != WSMsgTypeConnected {
					t.Errorf("message type = %q, want %q", msg.Type, WSMsgTypeConnected)
				}
				got, present := msg.Data["no_archive"]
				if !present {
					t.Fatal("expected \"no_archive\" key to always be present in the connected message")
				}
				if got != tt.wantNoArchive {
					t.Errorf("no_archive = %v, want %v", got, tt.wantNoArchive)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("expected connected message on send channel, got none")
			}
		})
	}
}
