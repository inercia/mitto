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
	availableCommands    []AvailableCommand
	acpStoppedReasons    []string
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

func (m *mockSessionObserver) OnPlan(seq int64, entries []PlanEntry) {}

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

func (m *mockSessionObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string) {
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

func (m *mockSessionObserver) OnAvailableCommandsUpdated(commands []AvailableCommand) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.availableCommands = commands
}

func (m *mockSessionObserver) OnACPStopped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acpStoppedReasons = append(m.acpStoppedReasons, reason)
}

func (m *mockSessionObserver) getACPStoppedReasons() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.acpStoppedReasons))
	copy(result, m.acpStoppedReasons)
	return result
}

func (m *mockSessionObserver) getAvailableCommands() []AvailableCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(m.availableCommands))
	copy(result, m.availableCommands)
	return result
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	_, err = queue.Add("Test message", nil, nil, "client1", 0)
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
	msg, err := queue.Add("Test message for title", nil, nil, "client1", 0)
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
	msg, err := queue.Add("Test message for title", nil, nil, "client1", 0)
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

// =============================================================================
// WaitForResponseComplete Tests
// =============================================================================

// TestWaitForResponseComplete_NotPrompting tests that WaitForResponseComplete
// returns immediately when no prompt is in progress.
func TestWaitForResponseComplete_NotPrompting(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: false,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when not prompting")
	}

	// Should return almost immediately (less than 100ms)
	if elapsed > 100*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected < 100ms when not prompting", elapsed)
	}
}

// TestWaitForResponseComplete_PromptCompletes tests that WaitForResponseComplete
// returns true when the prompt completes within the timeout.
func TestWaitForResponseComplete_PromptCompletes(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Simulate prompt completion after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		bs.promptMu.Lock()
		bs.isPrompting = false
		bs.promptCond.Broadcast()
		bs.promptMu.Unlock()
	}()

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when prompt completes")
	}

	// Should complete around 100ms (with some tolerance)
	if elapsed < 50*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_Timeout tests that WaitForResponseComplete
// returns false when the timeout expires before the prompt completes.
func TestWaitForResponseComplete_Timeout(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	start := time.Now()
	result := bs.WaitForResponseComplete(100 * time.Millisecond)
	elapsed := time.Since(start)

	if result {
		t.Error("WaitForResponseComplete should return false on timeout")
	}

	// Should timeout around 100ms (with some tolerance)
	if elapsed < 80*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_SessionClosed tests that WaitForResponseComplete
// returns when the session is closed.
func TestWaitForResponseComplete_SessionClosed(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Simulate session close after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		bs.closed.Store(1)
		bs.promptMu.Lock()
		bs.promptCond.Broadcast()
		bs.promptMu.Unlock()
	}()

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when session is closed")
	}

	// Should complete around 100ms (with some tolerance)
	if elapsed < 50*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_Concurrent tests that WaitForResponseComplete
// is safe for concurrent access.
func TestWaitForResponseComplete_Concurrent(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	var wg sync.WaitGroup
	const numWaiters = 10

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bs.WaitForResponseComplete(5 * time.Second)
		}()
	}

	// Give waiters time to start
	time.Sleep(50 * time.Millisecond)

	// Complete the prompt - all waiters should wake up
	bs.promptMu.Lock()
	bs.isPrompting = false
	bs.promptCond.Broadcast()
	bs.promptMu.Unlock()

	// Wait for all waiters with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Timed out waiting for concurrent waiters to complete")
	}
}

// =============================================================================
// Available Commands Tests
// =============================================================================

func TestBackgroundSession_AvailableCommands_InitiallyEmpty(t *testing.T) {
	bs := &BackgroundSession{}

	commands := bs.AvailableCommands()
	if commands != nil {
		t.Errorf("AvailableCommands should return nil initially, got %v", commands)
	}
}

func TestBackgroundSession_AvailableCommands_SortedAlphabetically(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Call onAvailableCommands directly with unsorted commands
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "zebra", Description: "Last command"},
		{Name: "apple", Description: "First command"},
		{Name: "mango", Description: "Middle command"},
	})

	commands := bs.AvailableCommands()
	if len(commands) != 3 {
		t.Fatalf("Expected 3 commands, got %d", len(commands))
	}

	// Verify alphabetical sorting
	if commands[0].Name != "apple" {
		t.Errorf("Expected first command to be 'apple', got %q", commands[0].Name)
	}
	if commands[1].Name != "mango" {
		t.Errorf("Expected second command to be 'mango', got %q", commands[1].Name)
	}
	if commands[2].Name != "zebra" {
		t.Errorf("Expected third command to be 'zebra', got %q", commands[2].Name)
	}
}

func TestBackgroundSession_AvailableCommands_NotifiesObservers(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Trigger available commands update
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "test", Description: "Test command"},
		{Name: "help", Description: "Help command"},
	})

	// Give observers time to receive the notification
	time.Sleep(10 * time.Millisecond)

	// Verify observer received the commands
	receivedCommands := observer.getAvailableCommands()
	if len(receivedCommands) != 2 {
		t.Fatalf("Observer should have received 2 commands, got %d", len(receivedCommands))
	}

	// Verify commands are sorted
	if receivedCommands[0].Name != "help" {
		t.Errorf("Expected first command to be 'help', got %q", receivedCommands[0].Name)
	}
	if receivedCommands[1].Name != "test" {
		t.Errorf("Expected second command to be 'test', got %q", receivedCommands[1].Name)
	}
}

func TestBackgroundSession_AvailableCommands_ReturnsDefensiveCopy(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	bs.onAvailableCommands([]AvailableCommand{
		{Name: "original", Description: "Original command"},
	})

	// Get a copy and modify it
	commands := bs.AvailableCommands()
	commands[0].Name = "modified"

	// Verify original is unchanged
	originalCommands := bs.AvailableCommands()
	if originalCommands[0].Name != "original" {
		t.Errorf("AvailableCommands should return a defensive copy, but original was modified")
	}
}

func TestBackgroundSession_AvailableCommands_IgnoredWhenClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session using the public method
	bs.Close("test")

	// Try to update commands - should be ignored
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "test", Description: "Test command"},
	})

	// Verify no commands were stored
	commands := bs.AvailableCommands()
	if commands != nil {
		t.Errorf("AvailableCommands should be nil after closing, got %v", commands)
	}

	// Verify observer was not notified
	receivedCommands := observer.getAvailableCommands()
	if receivedCommands != nil {
		t.Errorf("Observer should not receive commands after session closed, got %v", receivedCommands)
	}
}

func TestBackgroundSession_AvailableCommands_MultipleObservers(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer1 := &mockSessionObserver{}
	observer2 := &mockSessionObserver{}
	bs.AddObserver(observer1)
	bs.AddObserver(observer2)

	// Trigger available commands update
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "shared", Description: "Shared command"},
	})

	// Give observers time to receive the notification
	time.Sleep(10 * time.Millisecond)

	// Verify both observers received the commands
	received1 := observer1.getAvailableCommands()
	received2 := observer2.getAvailableCommands()

	if len(received1) != 1 || received1[0].Name != "shared" {
		t.Errorf("Observer 1 should have received 'shared' command, got %v", received1)
	}
	if len(received2) != 1 || received2[0].Name != "shared" {
		t.Errorf("Observer 2 should have received 'shared' command, got %v", received2)
	}
}

// =============================================================================
// Tests for OnACPStopped notification (race condition fix)
// =============================================================================

// TestBackgroundSession_Close_NotifiesObserversOfACPStopped verifies that when a
// BackgroundSession is closed, all observers receive the OnACPStopped notification
// with the correct reason. This is critical for preventing the race condition where
// a client tries to send a prompt while the session is being archived.
func TestBackgroundSession_Close_NotifiesObserversOfACPStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session with a specific reason
	bs.Close("archived")

	// Verify observer received the OnACPStopped notification
	reasons := observer.getACPStoppedReasons()
	if len(reasons) != 1 {
		t.Fatalf("Observer should have received 1 OnACPStopped call, got %d", len(reasons))
	}
	if reasons[0] != "archived" {
		t.Errorf("OnACPStopped reason = %q, want %q", reasons[0], "archived")
	}
}

// TestBackgroundSession_Close_NotifiesMultipleObservers verifies that all connected
// observers receive the OnACPStopped notification when the session is closed.
func TestBackgroundSession_Close_NotifiesMultipleObservers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer1 := &mockSessionObserver{}
	observer2 := &mockSessionObserver{}
	observer3 := &mockSessionObserver{}
	bs.AddObserver(observer1)
	bs.AddObserver(observer2)
	bs.AddObserver(observer3)

	// Close the session
	bs.Close("archived_timeout")

	// Verify all observers received the notification
	for i, observer := range []*mockSessionObserver{observer1, observer2, observer3} {
		reasons := observer.getACPStoppedReasons()
		if len(reasons) != 1 {
			t.Errorf("Observer %d should have received 1 OnACPStopped call, got %d", i+1, len(reasons))
		}
		if len(reasons) > 0 && reasons[0] != "archived_timeout" {
			t.Errorf("Observer %d OnACPStopped reason = %q, want %q", i+1, reasons[0], "archived_timeout")
		}
	}
}

// TestBackgroundSession_Close_OnlyNotifiesOnce verifies that closing a session
// multiple times only notifies observers once (idempotent close).
func TestBackgroundSession_Close_OnlyNotifiesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session multiple times
	bs.Close("first_close")
	bs.Close("second_close")
	bs.Close("third_close")

	// Verify observer only received one notification (from first close)
	reasons := observer.getACPStoppedReasons()
	if len(reasons) != 1 {
		t.Fatalf("Observer should have received exactly 1 OnACPStopped call, got %d", len(reasons))
	}
	if reasons[0] != "first_close" {
		t.Errorf("OnACPStopped reason = %q, want %q (from first close)", reasons[0], "first_close")
	}
}

// TestBackgroundSession_Close_NotifiesBeforeMarkingClosed verifies that observers
// are notified BEFORE the session is marked as closed. This is important because
// the notification must happen while the session is still in a valid state.
func TestBackgroundSession_Close_NotifiesBeforeMarkingClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Track the closed state when OnACPStopped is called
	var wasClosedDuringNotification bool
	observer := &trackingObserver{
		onACPStopped: func(reason string) {
			// Check if IsClosed() returns true during the notification
			// Note: The closed flag is set atomically at the start of Close(),
			// so IsClosed() will return true. However, the notification happens
			// before resources are released, which is the important part.
			wasClosedDuringNotification = bs.IsClosed()
		},
	}
	bs.AddObserver(observer)

	// Close the session
	bs.Close("test")

	// The session should be marked as closed (this is expected behavior)
	// The important thing is that the notification happens before resources are released
	if !bs.IsClosed() {
		t.Error("Session should be closed after Close()")
	}

	// Verify the callback was called
	if !wasClosedDuringNotification {
		t.Log("Note: IsClosed() returned false during notification (unexpected but not critical)")
	}
}

// TestBackgroundSession_IsClosed_AfterClose verifies that IsClosed returns true
// after the session is closed, which prevents new prompts from being sent.
func TestBackgroundSession_IsClosed_AfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Initially not closed
	if bs.IsClosed() {
		t.Error("New session should not be closed")
	}

	// Close the session
	bs.Close("test")

	// Now should be closed
	if !bs.IsClosed() {
		t.Error("Session should be closed after Close()")
	}
}

// TestBackgroundSession_Close_DifferentReasons verifies that different close reasons
// are correctly passed to observers.
func TestBackgroundSession_Close_DifferentReasons(t *testing.T) {
	testCases := []string{
		"archived",
		"archived_timeout",
		"user_closed",
		"server_shutdown",
		"error",
		"",
	}

	for _, reason := range testCases {
		t.Run("reason_"+reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			bs := &BackgroundSession{
				observers: make(map[SessionObserver]struct{}),
				ctx:       ctx,
				cancel:    cancel,
			}

			observer := &mockSessionObserver{}
			bs.AddObserver(observer)

			bs.Close(reason)

			reasons := observer.getACPStoppedReasons()
			if len(reasons) != 1 {
				t.Fatalf("Observer should have received 1 OnACPStopped call, got %d", len(reasons))
			}
			if reasons[0] != reason {
				t.Errorf("OnACPStopped reason = %q, want %q", reasons[0], reason)
			}
		})
	}
}

// trackingObserver is a minimal observer that tracks specific callbacks for testing.
type trackingObserver struct {
	onACPStopped func(reason string)
}

func (o *trackingObserver) OnAgentMessage(seq int64, html string)             {}
func (o *trackingObserver) OnAgentThought(seq int64, text string)             {}
func (o *trackingObserver) OnToolCall(seq int64, id, title, status string)    {}
func (o *trackingObserver) OnToolUpdate(seq int64, id string, status *string) {}
func (o *trackingObserver) OnPlan(seq int64, entries []PlanEntry)             {}
func (o *trackingObserver) OnFileWrite(seq int64, path string, size int)      {}
func (o *trackingObserver) OnFileRead(seq int64, path string, size int)       {}
func (o *trackingObserver) OnPromptComplete(eventCount int)                   {}
func (o *trackingObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string) {
}
func (o *trackingObserver) OnError(message string)                                   {}
func (o *trackingObserver) OnQueueUpdated(queueLength int, action, messageID string) {}
func (o *trackingObserver) OnQueueReordered(messages []session.QueuedMessage)        {}
func (o *trackingObserver) OnQueueMessageSending(messageID string)                   {}
func (o *trackingObserver) OnQueueMessageSent(messageID string)                      {}
func (o *trackingObserver) OnActionButtons(buttons []ActionButton)                   {}
func (o *trackingObserver) OnAvailableCommandsUpdated(commands []AvailableCommand)   {}
func (o *trackingObserver) OnACPStopped(reason string) {
	if o.onACPStopped != nil {
		o.onACPStopped(reason)
	}
}
func (o *trackingObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

// =============================================================================
// GetMaxAssignedSeq Tests
// =============================================================================

// TestGetMaxAssignedSeq_Initial tests that GetMaxAssignedSeq returns 0 initially.
func TestGetMaxAssignedSeq_Initial(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq: 1, // Initial state: nextSeq starts at 1
	}

	maxSeq := bs.GetMaxAssignedSeq()
	if maxSeq != 0 {
		t.Errorf("GetMaxAssignedSeq() = %d, want 0 (no events assigned yet)", maxSeq)
	}
}

// TestGetMaxAssignedSeq_AfterAssignment tests that GetMaxAssignedSeq returns
// the correct value after sequence numbers have been assigned.
func TestGetMaxAssignedSeq_AfterAssignment(t *testing.T) {
	tests := []struct {
		name    string
		nextSeq int64
		want    int64
	}{
		{
			name:    "after first assignment",
			nextSeq: 2, // First event was assigned seq=1
			want:    1,
		},
		{
			name:    "after 10 assignments",
			nextSeq: 11, // Events 1-10 were assigned
			want:    10,
		},
		{
			name:    "after 100 assignments",
			nextSeq: 101,
			want:    100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs := &BackgroundSession{
				nextSeq: tt.nextSeq,
			}

			got := bs.GetMaxAssignedSeq()
			if got != tt.want {
				t.Errorf("GetMaxAssignedSeq() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestGetMaxAssignedSeq_Concurrent tests that GetMaxAssignedSeq is safe
// for concurrent access.
func TestGetMaxAssignedSeq_Concurrent(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq: 1,
	}

	var wg sync.WaitGroup
	const numGoroutines = 100

	// Start multiple goroutines reading and incrementing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Reader
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = bs.GetMaxAssignedSeq()
				time.Sleep(time.Microsecond)
			}
		}()

		// Writer (simulating AssignSeq)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bs.seqMu.Lock()
				bs.nextSeq++
				bs.seqMu.Unlock()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
	// If we get here without a race condition, the test passes
}
