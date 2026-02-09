package web

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestResumeBackgroundSession_MissingPersistedID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Try to resume without a persisted ID
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "", // Empty ID
		ACPCommand:  "echo test",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail when PersistedID is empty")
	}
}

func TestResumeBackgroundSession_SessionNotInStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Try to resume a session that doesn't exist in the store
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "non-existent-session",
		ACPCommand:  "echo test",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail for non-existent session")
	}
}

func TestResumeBackgroundSession_InvalidACPCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session in the store first
	meta := session.Metadata{
		SessionID:  "test-session-resume",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume with an invalid ACP command
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "test-session-resume",
		ACPCommand:  "/nonexistent/command/that/does/not/exist",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail with invalid ACP command")
	}
}

func TestResumeBackgroundSession_EmptyACPCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session in the store first
	meta := session.Metadata{
		SessionID:  "test-session-empty-cmd",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume with an empty ACP command
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "test-session-empty-cmd",
		ACPCommand:  "",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail with empty ACP command")
	}
}

func TestBackgroundSession_GetSessionID(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session-id",
	}

	if bs.GetSessionID() != "test-session-id" {
		t.Errorf("GetSessionID = %q, want %q", bs.GetSessionID(), "test-session-id")
	}
}

func TestBackgroundSession_GetACPID(t *testing.T) {
	bs := &BackgroundSession{
		acpID: "acp-session-id",
	}

	if bs.GetACPID() != "acp-session-id" {
		t.Errorf("GetACPID = %q, want %q", bs.GetACPID(), "acp-session-id")
	}
}

func TestBackgroundSession_IsClosed(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.IsClosed() {
		t.Error("New BackgroundSession should not be closed")
	}

	bs.closed.Store(1)

	if !bs.IsClosed() {
		t.Error("BackgroundSession should be closed after setting closed flag")
	}
}

func TestBackgroundSession_IsPrompting(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.IsPrompting() {
		t.Error("New BackgroundSession should not be prompting")
	}

	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	if !bs.IsPrompting() {
		t.Error("BackgroundSession should be prompting after setting flag")
	}
}

func TestBackgroundSession_GetPromptCount(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.GetPromptCount() != 0 {
		t.Errorf("GetPromptCount = %d, want 0", bs.GetPromptCount())
	}

	bs.promptMu.Lock()
	bs.promptCount = 5
	bs.promptMu.Unlock()

	if bs.GetPromptCount() != 5 {
		t.Errorf("GetPromptCount = %d, want 5", bs.GetPromptCount())
	}
}

func TestBackgroundSession_ObserverManagement(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	if bs.HasObservers() {
		t.Error("New BackgroundSession should not have observers")
	}

	if bs.ObserverCount() != 0 {
		t.Errorf("ObserverCount = %d, want 0", bs.ObserverCount())
	}

	// Add a mock observer
	mockObserver := &mockSessionObserver{}
	bs.AddObserver(mockObserver)

	if !bs.HasObservers() {
		t.Error("BackgroundSession should have observers after AddObserver")
	}

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}

	// Add another observer
	mockObserver2 := &mockSessionObserver{}
	bs.AddObserver(mockObserver2)

	if bs.ObserverCount() != 2 {
		t.Errorf("ObserverCount = %d, want 2", bs.ObserverCount())
	}

	// Remove first observer
	bs.RemoveObserver(mockObserver)

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}

	// Remove second observer
	bs.RemoveObserver(mockObserver2)

	if bs.HasObservers() {
		t.Error("BackgroundSession should not have observers after removing all")
	}
}

// mockSessionObserver is a mock implementation of SessionObserver for testing.
type mockSessionObserver struct {
	mu                   sync.Mutex
	agentMessages        []string
	agentThoughts        []string
	toolCalls            []string
	errors               []string
	completed            bool
	queueUpdates         []queueUpdate
	queueMessagesSending []string
	queueMessagesSent    []string
}

type queueUpdate struct {
	queueLength int
	action      string
	messageID   string
}

func (m *mockSessionObserver) OnAgentMessage(seq int64, html string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentMessages = append(m.agentMessages, html)
}

func (m *mockSessionObserver) OnAgentThought(seq int64, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentThoughts = append(m.agentThoughts, text)
}

func (m *mockSessionObserver) OnToolCall(seq int64, id, title, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = append(m.toolCalls, id)
}

func (m *mockSessionObserver) OnToolUpdate(seq int64, id string, status *string) {}

func (m *mockSessionObserver) OnPlan(seq int64) {}

func (m *mockSessionObserver) OnFileWrite(seq int64, path string, size int) {}

func (m *mockSessionObserver) OnFileRead(seq int64, path string, size int) {}

func (m *mockSessionObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockSessionObserver) OnPromptComplete(eventCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = true
}

func (m *mockSessionObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs []string) {
	// No-op for tests
}

func (m *mockSessionObserver) OnError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, message)
}

func (m *mockSessionObserver) OnQueueUpdated(queueLength int, action string, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueUpdates = append(m.queueUpdates, queueUpdate{queueLength, action, messageID})
}

func (m *mockSessionObserver) OnQueueMessageSending(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueMessagesSending = append(m.queueMessagesSending, messageID)
}

func (m *mockSessionObserver) OnQueueMessageSent(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueMessagesSent = append(m.queueMessagesSent, messageID)
}

func (m *mockSessionObserver) OnQueueReordered(messages []session.QueuedMessage) {
	// no-op for testing
}

func (m *mockSessionObserver) OnActionButtons(buttons []ActionButton) {
	// no-op for testing
}

func (m *mockSessionObserver) getQueueMessagesSending() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.queueMessagesSending))
	copy(result, m.queueMessagesSending)
	return result
}

// Tests for NeedsTitle

func TestBackgroundSession_NeedsTitle_NoStore(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session",
		store:       nil, // No store
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when store is nil")
	}
}

func TestBackgroundSession_NeedsTitle_NoPersistedID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	bs := &BackgroundSession{
		persistedID: "", // Empty persisted ID
		store:       store,
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when persistedID is empty")
	}
}

func TestBackgroundSession_NeedsTitle_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	bs := &BackgroundSession{
		persistedID: "non-existent-session",
		store:       store,
	}

	// Session doesn't exist in store, GetMetadata will fail
	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when session is not found in store")
	}
}

func TestBackgroundSession_NeedsTitle_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-empty-name",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name - needs title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-empty-name",
		store:       store,
	}

	if !bs.NeedsTitle() {
		t.Error("NeedsTitle should return true when session name is empty")
	}
}

func TestBackgroundSession_NeedsTitle_HasName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "test-session-with-name",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "My Conversation", // Has a name - doesn't need title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-with-name",
		store:       store,
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when session already has a name")
	}
}

func TestBackgroundSession_NeedsTitle_AfterRename(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-rename",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name initially
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-rename",
		store:       store,
	}

	// Initially needs title
	if !bs.NeedsTitle() {
		t.Error("NeedsTitle should return true initially when name is empty")
	}

	// Simulate auto-rename or user rename
	err = store.UpdateMetadata("test-session-rename", func(m *session.Metadata) {
		m.Name = "Auto-generated Title"
	})
	if err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// After rename, should not need title
	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false after name is set")
	}
}

// Tests for SessionWSClient.sessionNeedsTitle

func TestSessionWSClient_SessionNeedsTitle_NoStore(t *testing.T) {
	client := &SessionWSClient{
		sessionID: "test-session",
		store:     nil, // No store
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when store is nil")
	}
}

func TestSessionWSClient_SessionNeedsTitle_EmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	client := &SessionWSClient{
		sessionID: "", // Empty session ID
		store:     store,
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when sessionID is empty")
	}
}

func TestSessionWSClient_SessionNeedsTitle_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-ws-empty",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name - needs title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	client := &SessionWSClient{
		sessionID: "test-session-ws-empty",
		store:     store,
	}

	if !client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return true when session name is empty")
	}
}

func TestSessionWSClient_SessionNeedsTitle_HasName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "test-session-ws-named",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Named Session", // Has a name
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	client := &SessionWSClient{
		sessionID: "test-session-ws-named",
		store:     store,
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when session already has a name")
	}
}

func TestBackgroundSession_GetEventCount_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	count := bs.GetEventCount()
	if count != 0 {
		t.Errorf("GetEventCount = %d, want 0 for nil recorder", count)
	}
}

func TestBackgroundSession_CreatedAt_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	createdAt := bs.CreatedAt()
	if !createdAt.IsZero() {
		t.Errorf("CreatedAt = %v, want zero time for nil recorder", createdAt)
	}
}

func TestBackgroundSession_NeedsTitle_NoRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	// Should not panic with nil recorder
	result := bs.NeedsTitle()
	if result {
		t.Error("NeedsTitle should return false when recorder is nil")
	}
}

func TestBackgroundSession_PersistedID(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session-123",
	}

	if bs.persistedID != "test-session-123" {
		t.Errorf("persistedID = %q, want %q", bs.persistedID, "test-session-123")
	}
}

func TestBackgroundSession_WorkingDirField(t *testing.T) {
	bs := &BackgroundSession{
		workingDir: "/test/workspace",
	}

	if bs.workingDir != "/test/workspace" {
		t.Errorf("workingDir = %q, want %q", bs.workingDir, "/test/workspace")
	}
}

func TestBackgroundSession_GetEventCount(t *testing.T) {
	bs := &BackgroundSession{}

	// Without recorder, should return 0
	count := bs.GetEventCount()
	if count != 0 {
		t.Errorf("GetEventCount = %d, want 0", count)
	}
}

func TestBackgroundSession_CreatedAt(t *testing.T) {
	bs := &BackgroundSession{}

	// Without recorder, should return zero time
	createdAt := bs.CreatedAt()
	if !createdAt.IsZero() {
		t.Errorf("CreatedAt should return zero time when recorder is nil")
	}
}

// --- Queue Processing Tests ---

func TestBackgroundSession_ProcessNextQueuedMessage_NoStore(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session",
		store:       nil, // No store
		observers:   make(map[SessionObserver]struct{}),
	}

	// Should not panic and should return early
	bs.processNextQueuedMessage()
}

func TestBackgroundSession_ProcessNextQueuedMessage_EmptyQueue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-empty-queue",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-empty-queue",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Should not panic and should not notify observers (queue is empty)
	bs.processNextQueuedMessage()

	if len(observer.getQueueMessagesSending()) != 0 {
		t.Error("Should not notify OnQueueMessageSending for empty queue")
	}
}

func TestBackgroundSession_ProcessNextQueuedMessage_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-disabled-queue",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-disabled-queue")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with queue disabled
	enabled := false
	queueConfig := &config.QueueConfig{Enabled: &enabled}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-disabled-queue",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Should not process queue when disabled
	bs.processNextQueuedMessage()

	if len(observer.getQueueMessagesSending()) != 0 {
		t.Error("Should not notify OnQueueMessageSending when queue is disabled")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_IsPrompting(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-prompting",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-prompting")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-prompting",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set isPrompting to true
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	// Should return false when prompting
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when isPrompting is true")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_IsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-closed",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-closed")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-closed",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set closed flag
	bs.closed.Store(1)

	// Should return false when closed
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when session is closed")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_DelayNotElapsed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delay",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-delay")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with 10 second delay
	queueConfig := &config.QueueConfig{DelaySeconds: 10}

	bs := &BackgroundSession{
		persistedID: "test-session-delay",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set lastResponseComplete to now (delay not elapsed)
	bs.promptMu.Lock()
	bs.lastResponseComplete = time.Now()
	bs.promptMu.Unlock()

	// Should return false when delay not elapsed
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when delay has not elapsed")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_DelayElapsed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delay-elapsed",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-delay-elapsed")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with 1 second delay
	queueConfig := &config.QueueConfig{DelaySeconds: 1}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-delay-elapsed",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Set lastResponseComplete to 2 seconds ago (delay elapsed)
	bs.promptMu.Lock()
	bs.lastResponseComplete = time.Now().Add(-2 * time.Second)
	bs.promptMu.Unlock()

	// Should pop the message (but fail to send since no ACP connection)
	// The message will be popped and observer notified, but PromptWithMeta will fail
	result := bs.TryProcessQueuedMessage()

	// Result depends on whether PromptWithMeta succeeds - it won't without ACP
	// But the message should be popped and OnQueueMessageSending should be called
	_ = result // We don't check result since PromptWithMeta will fail

	// Check that OnQueueMessageSending was called
	sending := observer.getQueueMessagesSending()
	if len(sending) != 1 {
		t.Errorf("OnQueueMessageSending called %d times, want 1", len(sending))
	}

	// Queue should be empty (message was popped)
	queueLen, _ := queue.Len()
	if queueLen != 0 {
		t.Errorf("Queue length = %d, want 0 (message should be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_ZeroDelayNoLastResponse(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-zero-delay",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-zero-delay")
	_, err = queue.Add("Test message", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-zero-delay",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
		// No queueConfig = default (no delay)
		// lastResponseComplete is zero (no previous response)
	}
	bs.AddObserver(observer)

	// Should pop the message immediately
	bs.TryProcessQueuedMessage()

	// Check that OnQueueMessageSending was called
	sending := observer.getQueueMessagesSending()
	if len(sending) != 1 {
		t.Errorf("OnQueueMessageSending called %d times, want 1", len(sending))
	}

	// Queue should be empty
	queueLen, _ := queue.Len()
	if queueLen != 0 {
		t.Errorf("Queue length = %d, want 0", queueLen)
	}
}

func TestBackgroundSession_GetLastResponseCompleteTime(t *testing.T) {
	bs := &BackgroundSession{}

	// Initially zero
	if !bs.GetLastResponseCompleteTime().IsZero() {
		t.Error("GetLastResponseCompleteTime should return zero time initially")
	}

	// Set a time
	now := time.Now()
	bs.promptMu.Lock()
	bs.lastResponseComplete = now
	bs.promptMu.Unlock()

	got := bs.GetLastResponseCompleteTime()
	if !got.Equal(now) {
		t.Errorf("GetLastResponseCompleteTime = %v, want %v", got, now)
	}
}

func TestBackgroundSession_GetQueueConfig(t *testing.T) {
	// Nil config
	bs := &BackgroundSession{}
	if bs.GetQueueConfig() != nil {
		t.Error("GetQueueConfig should return nil when not set")
	}

	// With config
	queueConfig := &config.QueueConfig{DelaySeconds: 5}
	bs.queueConfig = queueConfig
	if bs.GetQueueConfig() != queueConfig {
		t.Error("GetQueueConfig should return the configured queue config")
	}
}

// --- Queue Title Worker Tests ---

func TestQueueTitleWorker_MessageRemovedBeforeTitleGenerated(t *testing.T) {
	// This tests the corner case where a message is removed from the queue
	// (e.g., sent to the agent) before the title generation completes.
	// The worker should handle this gracefully without logging an error.

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-title-race",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-title-race")
	msg, err := queue.Add("Test message for title", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Now remove the message (simulating it being sent to the agent)
	if err := queue.Remove(msg.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Try to update the title - this should return ErrMessageNotFound
	err = queue.UpdateTitle(msg.ID, "Generated Title")
	if err == nil {
		t.Error("UpdateTitle should return error when message not found")
	}
	if err != session.ErrMessageNotFound {
		t.Errorf("UpdateTitle error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueueTitleWorker_UpdateTitleSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-title-success",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-title-success")
	msg, err := queue.Add("Test message for title", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Update the title while message is still in queue
	err = queue.UpdateTitle(msg.ID, "Generated Title")
	if err != nil {
		t.Errorf("UpdateTitle failed: %v", err)
	}

	// Verify the title was updated
	updatedMsg, err := queue.Get(msg.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if updatedMsg.Title != "Generated Title" {
		t.Errorf("Title = %q, want %q", updatedMsg.Title, "Generated Title")
	}
}

// TestBackgroundSession_FlushAndPersistMessages_SortsEventsBySeq verifies that events
// are sorted by sequence number before persistence. This is critical because:
// - Agent messages are buffered in MarkdownBuffer and may flush late
// - Tool calls are added to EventBuffer immediately when SafeFlush fails
// - Without sorting, tool calls would appear before agent messages in the persisted log
func TestBackgroundSession_FlushAndPersistMessages_SortsEventsBySeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-sort"
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	if err := recorder.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	// Create a session with an event buffer
	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		recorder:    recorder,
		eventBuffer: NewEventBuffer(),
	}

	// Simulate the out-of-order scenario that happens with markdown buffering:
	// - Agent message arrives first (seq 1) but gets buffered in MarkdownBuffer
	// - Tool call arrives (seq 2) and is added to EventBuffer immediately
	// - Tool call update arrives (seq 3)
	// - Agent message finally flushes from MarkdownBuffer (still seq 1)
	//
	// The EventBuffer would receive them in this order: [tool call (2), tool update (3), agent (1)]
	// But we want them persisted in seq order: [agent (1), tool call (2), tool update (3)]

	// Add events OUT OF ORDER (simulating what happens with markdown buffering)
	bs.eventBuffer.AppendToolCall(2, "tool-1", "Read file", "running")
	bs.eventBuffer.AppendToolCallUpdate(3, "tool-1", ptr("completed"))
	bs.eventBuffer.AppendAgentMessage(1, "<p>Let me read the file</p>")

	// Verify buffer order is out of sequence
	bufferEvents := bs.eventBuffer.Events()
	if len(bufferEvents) != 3 {
		t.Fatalf("Expected 3 buffered events, got %d", len(bufferEvents))
	}
	if bufferEvents[0].Seq != 2 || bufferEvents[1].Seq != 3 || bufferEvents[2].Seq != 1 {
		t.Errorf("Expected buffer in order [2,3,1], got [%d,%d,%d]",
			bufferEvents[0].Seq, bufferEvents[1].Seq, bufferEvents[2].Seq)
	}

	// Flush and persist
	bs.flushAndPersistMessages()

	// Read persisted events and verify they are in seq order
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Filter out session_start event
	var filteredEvents []session.Event
	for _, e := range events {
		if e.Type != session.EventTypeSessionStart {
			filteredEvents = append(filteredEvents, e)
		}
	}

	if len(filteredEvents) != 3 {
		t.Fatalf("Expected 3 persisted events (excluding session_start), got %d", len(filteredEvents))
	}

	// Events should be persisted in seq order, so their types should be:
	// 1. agent_message (originally seq 1, now persisted first)
	// 2. tool_call (originally seq 2, now persisted second)
	// 3. tool_call_update (originally seq 3, now persisted third)
	expectedTypes := []session.EventType{
		session.EventTypeAgentMessage,
		session.EventTypeToolCall,
		session.EventTypeToolCallUpdate,
	}
	for i, e := range filteredEvents {
		if e.Type != expectedTypes[i] {
			t.Errorf("Event %d: type = %s, want %s", i, e.Type, expectedTypes[i])
		}
	}
}

// =============================================================================
// H3: Periodic Persistence Tests
// =============================================================================

// TestPeriodicPersistence_StartStop tests that periodic persistence can be
// started and stopped without errors.
func TestPeriodicPersistence_StartStop(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 100 * time.Millisecond,
	}

	// Start should not panic
	bs.startPeriodicPersistence()

	// Timer should be set
	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if !hasTimer {
		t.Error("Expected persistTimer to be set after startPeriodicPersistence")
	}

	// Stop should not panic
	bs.stopPeriodicPersistence()

	// Timer should be nil
	bs.promptMu.Lock()
	hasTimer = bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected persistTimer to be nil after stopPeriodicPersistence")
	}
}

// TestPeriodicPersistence_ZeroInterval tests that periodic persistence is
// disabled when interval is zero.
func TestPeriodicPersistence_ZeroInterval(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 0, // Disabled
	}

	// Start should not create a timer
	bs.startPeriodicPersistence()

	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected no timer when persistInterval is 0")
	}
}

// TestPeriodicPersistence_NegativeInterval tests that periodic persistence is
// disabled when interval is negative.
func TestPeriodicPersistence_NegativeInterval(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: -1 * time.Second, // Invalid
	}

	// Start should not create a timer
	bs.startPeriodicPersistence()

	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected no timer when persistInterval is negative")
	}
}

// TestPeriodicPersistence_DoubleStart tests that starting twice replaces the timer.
func TestPeriodicPersistence_DoubleStart(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 100 * time.Millisecond,
	}

	// Start first timer
	bs.startPeriodicPersistence()

	bs.promptMu.Lock()
	firstTimer := bs.persistTimer
	bs.promptMu.Unlock()

	// Start second timer (should replace first)
	bs.startPeriodicPersistence()

	bs.promptMu.Lock()
	secondTimer := bs.persistTimer
	bs.promptMu.Unlock()

	// Timers should be different (first was stopped, new one created)
	if firstTimer == secondTimer {
		t.Error("Expected new timer after second startPeriodicPersistence")
	}

	// Cleanup
	bs.stopPeriodicPersistence()
}

// TestPeriodicPersistence_DoubleStop tests that stopping twice is safe.
func TestPeriodicPersistence_DoubleStop(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 100 * time.Millisecond,
	}

	bs.startPeriodicPersistence()
	bs.stopPeriodicPersistence()

	// Second stop should not panic
	bs.stopPeriodicPersistence()

	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected no timer after double stop")
	}
}

// TestPeriodicPersistTick_NotPrompting tests that the tick does nothing
// when not prompting.
func TestPeriodicPersistTick_NotPrompting(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 100 * time.Millisecond,
		isPrompting:     false,
	}

	// Should not panic and should not reschedule
	bs.periodicPersistTick()

	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected no timer when not prompting")
	}
}

// TestPeriodicPersistTick_Prompting tests that the tick reschedules when prompting.
func TestPeriodicPersistTick_Prompting(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 50 * time.Millisecond,
		isPrompting:     true,
	}

	// Call tick - should reschedule
	bs.periodicPersistTick()

	// Wait a bit for the timer to be set
	time.Sleep(10 * time.Millisecond)

	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if !hasTimer {
		t.Error("Expected timer to be rescheduled when prompting")
	}

	// Cleanup
	bs.stopPeriodicPersistence()
}

// TestPeriodicPersistence_Integration tests the full lifecycle of periodic persistence.
func TestPeriodicPersistence_Integration(t *testing.T) {
	bs := &BackgroundSession{
		persistInterval: 20 * time.Millisecond,
		isPrompting:     true,
	}

	// Start periodic persistence - timer should be started
	bs.startPeriodicPersistence()

	// Wait for a few ticks
	time.Sleep(100 * time.Millisecond)

	// Stop prompting
	bs.promptMu.Lock()
	bs.isPrompting = false
	bs.promptMu.Unlock()

	// Stop the timer
	bs.stopPeriodicPersistence()

	// Verify timer is stopped
	bs.promptMu.Lock()
	hasTimer := bs.persistTimer != nil
	bs.promptMu.Unlock()

	if hasTimer {
		t.Error("Expected timer to be stopped")
	}
}
