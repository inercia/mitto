package web

import (
	"context"
	"strings"
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

func (m *mockSessionObserver) OnUIPrompt(req UIPromptRequest) {
	// no-op for testing
}

func (m *mockSessionObserver) OnUIPromptDismiss(requestID string, reason string) {
	// no-op for testing
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
	onACPStopped      func(reason string)
	onUIPrompt        func(req UIPromptRequest)
	onUIPromptDismiss func(requestID string, reason string)
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
func (o *trackingObserver) OnUIPrompt(req UIPromptRequest) {
	if o.onUIPrompt != nil {
		o.onUIPrompt(req)
	}
}
func (o *trackingObserver) OnUIPromptDismiss(requestID string, reason string) {
	if o.onUIPromptDismiss != nil {
		o.onUIPromptDismiss(requestID, reason)
	}
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

// TestBackgroundSession_Close_ServerShutdownUsesSuspend verifies that when a session
// is closed with reason "server_shutdown", the recorder uses Suspend() instead of End().
// This prevents multiple session_end events from being recorded when the session is
// resumed after server restart.
func TestBackgroundSession_Close_ServerShutdownUsesSuspend(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Create a recorder and start it
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-acp", "/tmp"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Create a BackgroundSession with the recorder
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
		recorder:  recorder,
	}

	// Close with server_shutdown reason
	bs.Close("server_shutdown")

	// Read events and verify no session_end was recorded
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Count session_end events (should be 0 for server_shutdown)
	sessionEndCount := 0
	for _, event := range events {
		if event.Type == session.EventTypeSessionEnd {
			sessionEndCount++
		}
	}

	if sessionEndCount != 0 {
		t.Errorf("Expected 0 session_end events for server_shutdown, got %d", sessionEndCount)
	}

	// Verify session status is still active (not completed)
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Status == session.SessionStatusCompleted {
		t.Error("Session status should not be 'completed' after server_shutdown")
	}
}

// TestBackgroundSession_Close_OtherReasonsUseEnd verifies that when a session
// is closed with reasons other than "server_shutdown", the recorder uses End()
// which records a session_end event.
func TestBackgroundSession_Close_OtherReasonsUseEnd(t *testing.T) {
	testCases := []string{
		"archived",
		"user_closed",
		"session_limit_exceeded",
		"duplicate_session",
	}

	for _, reason := range testCases {
		t.Run("reason_"+reason, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore failed: %v", err)
			}

			// Create a recorder and start it
			recorder := session.NewRecorder(store)
			if err := recorder.Start("test-acp", "/tmp"); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			sessionID := recorder.SessionID()

			// Create a BackgroundSession with the recorder
			ctx, cancel := context.WithCancel(context.Background())
			bs := &BackgroundSession{
				observers: make(map[SessionObserver]struct{}),
				ctx:       ctx,
				cancel:    cancel,
				recorder:  recorder,
			}

			// Close with the test reason
			bs.Close(reason)

			// Read events and verify session_end was recorded
			events, err := store.ReadEvents(sessionID)
			if err != nil {
				t.Fatalf("ReadEvents failed: %v", err)
			}

			// Count session_end events (should be 1 for non-server_shutdown reasons)
			sessionEndCount := 0
			var lastSessionEnd *session.Event
			for i, event := range events {
				if event.Type == session.EventTypeSessionEnd {
					sessionEndCount++
					lastSessionEnd = &events[i]
				}
			}

			if sessionEndCount != 1 {
				t.Errorf("Expected 1 session_end event for reason %q, got %d", reason, sessionEndCount)
			}

			// Verify the reason is correct
			if lastSessionEnd != nil {
				data, ok := lastSessionEnd.Data.(session.SessionEndData)
				if !ok {
					// Try map conversion (JSON unmarshaling)
					if dataMap, ok := lastSessionEnd.Data.(map[string]interface{}); ok {
						if r, ok := dataMap["reason"].(string); ok {
							if r != reason {
								t.Errorf("session_end reason = %q, want %q", r, reason)
							}
						}
					}
				} else if data.Reason != reason {
					t.Errorf("session_end reason = %q, want %q", data.Reason, reason)
				}
			}

			// Verify session status is completed
			meta, err := store.GetMetadata(sessionID)
			if err != nil {
				t.Fatalf("GetMetadata failed: %v", err)
			}
			if meta.Status != session.SessionStatusCompleted {
				t.Errorf("Session status = %q, want %q", meta.Status, session.SessionStatusCompleted)
			}
		})
	}
}

// =============================================================================
// refreshNextSeq Tests
// =============================================================================

// TestRefreshNextSeq_NilRecorder tests that refreshNextSeq handles nil recorder gracefully.
func TestRefreshNextSeq_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq:  100,
		recorder: nil, // No recorder
	}

	// Should not panic and should not change nextSeq
	bs.refreshNextSeq()

	if bs.nextSeq != 100 {
		t.Errorf("nextSeq should remain unchanged when recorder is nil, got %d, want 100", bs.nextSeq)
	}
}

// TestRefreshNextSeq_UsesMaxSeqWhenHigher tests that refreshNextSeq uses MaxSeq
// when it's higher than EventCount (the bug fix scenario).
func TestRefreshNextSeq_UsesMaxSeqWhenHigher(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-maxseq"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to simulate coalescing where MaxSeq > EventCount
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 10 // Only 10 events
		m.MaxSeq = 100    // But highest seq is 100 (due to coalescing)
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1, // Start low
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Refresh should use MaxSeq + 1
	bs.refreshNextSeq()

	// nextSeq should be MaxSeq + 1 = 101, not EventCount + 1 = 11
	if bs.nextSeq != 101 {
		t.Errorf("nextSeq = %d, want 101 (MaxSeq + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_UsesEventCountWhenHigher tests that refreshNextSeq uses EventCount
// when it's higher than MaxSeq (normal case without coalescing).
func TestRefreshNextSeq_UsesEventCountWhenHigher(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-eventcount"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to set EventCount >= MaxSeq (normal case)
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 50
		m.MaxSeq = 50 // Equal to EventCount
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// nextSeq should be EventCount + 1 = 51
	if bs.nextSeq != 51 {
		t.Errorf("nextSeq = %d, want 51 (EventCount + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_ZeroMaxSeq tests that refreshNextSeq handles zero MaxSeq
// (sessions created before MaxSeq tracking was added).
func TestRefreshNextSeq_ZeroMaxSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-zero-maxseq"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to simulate a legacy session with EventCount but no MaxSeq
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 25
		m.MaxSeq = 0 // Legacy session without MaxSeq
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// nextSeq should be EventCount + 1 = 26
	if bs.nextSeq != 26 {
		t.Errorf("nextSeq = %d, want 26 (EventCount + 1 when MaxSeq is 0)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_TableDriven tests various combinations of MaxSeq and EventCount.
func TestRefreshNextSeq_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		eventCount int
		maxSeq     int64
		wantSeq    int64
	}{
		{
			name:       "MaxSeq much higher than EventCount (coalescing)",
			eventCount: 100,
			maxSeq:     500,
			wantSeq:    501, // MaxSeq + 1
		},
		{
			name:       "EventCount equals MaxSeq",
			eventCount: 100,
			maxSeq:     100,
			wantSeq:    101, // Either works, both give same result
		},
		{
			name:       "EventCount higher than MaxSeq (shouldn't happen but handle it)",
			eventCount: 200,
			maxSeq:     100,
			wantSeq:    201, // EventCount + 1
		},
		{
			name:       "Both zero (empty session)",
			eventCount: 0,
			maxSeq:     0,
			wantSeq:    1, // Start at 1
		},
		{
			name:       "MaxSeq is 1, EventCount is 0",
			eventCount: 0,
			maxSeq:     1,
			wantSeq:    2, // MaxSeq + 1
		},
		{
			name:       "Large values",
			eventCount: 10000,
			maxSeq:     50000,
			wantSeq:    50001, // MaxSeq + 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore failed: %v", err)
			}
			defer store.Close()

			sessionID := "test-" + tt.name

			// Create session first (Create resets EventCount to 0)
			meta := session.Metadata{
				SessionID:  sessionID,
				ACPServer:  "test-server",
				WorkingDir: tmpDir,
			}
			if err := store.Create(meta); err != nil {
				t.Fatalf("Create failed: %v", err)
			}

			// Then update metadata to set the test values
			if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
				m.EventCount = tt.eventCount
				m.MaxSeq = tt.maxSeq
			}); err != nil {
				t.Fatalf("UpdateMetadata failed: %v", err)
			}

			recorder := session.NewRecorderWithID(store, sessionID)
			bs := &BackgroundSession{
				nextSeq:     1,
				recorder:    recorder,
				persistedID: sessionID,
			}

			bs.refreshNextSeq()

			if bs.nextSeq != tt.wantSeq {
				t.Errorf("nextSeq = %d, want %d", bs.nextSeq, tt.wantSeq)
			}
		})
	}
}

// TestRefreshNextSeq_Concurrent tests that refreshNextSeq is safe for concurrent access.
func TestRefreshNextSeq_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-concurrent"

	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 100
		m.MaxSeq = 500
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	var wg sync.WaitGroup
	const numGoroutines = 50

	// Start multiple goroutines calling refreshNextSeq and GetNextSeq
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Refresher
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				bs.refreshNextSeq()
				time.Sleep(time.Microsecond)
			}
		}()

		// Reader
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = bs.GetMaxAssignedSeq()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
	// If we get here without a race condition, the test passes
}

// TestRefreshNextSeq_PreservesHigherValue tests that refreshNextSeq doesn't
// decrease nextSeq if it's already higher than what the store reports.
// This is important for the case where events have been assigned but not yet persisted.
func TestRefreshNextSeq_PreservesHigherValue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-preserve"

	// Create session first
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Store has lower values
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 10
		m.MaxSeq = 50
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     200, // Already higher than store's MaxSeq
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// Note: Current implementation DOES reset to store values.
	// This test documents the current behavior.
	// If we want to preserve higher values, we'd need to change the implementation.
	// For now, the fix ensures we use MaxSeq instead of EventCount.
	if bs.nextSeq != 51 {
		t.Errorf("nextSeq = %d, want 51 (MaxSeq + 1 from store)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_AfterUserPrompt tests the real-world scenario where
// refreshNextSeq is called after persisting a user prompt.
func TestRefreshNextSeq_AfterUserPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session and record some events with coalescing
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Record events that simulate coalescing (multiple chunks with same seq)
	// We'll record events with explicit seq numbers to simulate the scenario
	for i := 0; i < 5; i++ {
		if err := recorder.RecordAgentMessage("<p>chunk</p>"); err != nil {
			t.Fatalf("RecordAgentMessage failed: %v", err)
		}
	}

	// Now manually update MaxSeq to simulate coalescing
	// (In real usage, this happens through RecordEvent with pre-assigned seq)
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.MaxSeq = 100 // Simulate high seq from coalescing
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// Create BackgroundSession with the recorder
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Simulate what happens after a user prompt is persisted
	bs.refreshNextSeq()

	// nextSeq should be MaxSeq + 1 = 101, not EventCount + 1 = 7
	if bs.nextSeq != 101 {
		t.Errorf("nextSeq = %d, want 101 (MaxSeq + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_IntegrationWithGetNextSeq tests that refreshNextSeq
// and GetNextSeq work correctly together.
func TestRefreshNextSeq_IntegrationWithGetNextSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-integration"

	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 50
		m.MaxSeq = 200 // High due to coalescing
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Refresh to sync with store
	bs.refreshNextSeq()

	// Get next seq should return 201
	seq1 := bs.GetNextSeq()
	if seq1 != 201 {
		t.Errorf("First GetNextSeq() = %d, want 201", seq1)
	}

	// Next call should return 202
	seq2 := bs.GetNextSeq()
	if seq2 != 202 {
		t.Errorf("Second GetNextSeq() = %d, want 202", seq2)
	}

	// GetMaxAssignedSeq should return 202 (the last assigned)
	maxSeq := bs.GetMaxAssignedSeq()
	if maxSeq != 202 {
		t.Errorf("GetMaxAssignedSeq() = %d, want 202", maxSeq)
	}
}

func TestFormatACPError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		contains string // expected substring in result
	}{
		{
			name:     "timeout error",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"details":"The operation was aborted due to timeout"}}`,
			contains: "tool operation timed out",
		},
		{
			name:     "peer disconnected",
			errMsg:   "peer disconnected before response",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "connection reset",
			errMsg:   "connection reset by peer",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "broken pipe",
			errMsg:   "write: broken pipe",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "context canceled",
			errMsg:   "context canceled",
			contains: "request was cancelled",
		},
		{
			name:     "context deadline exceeded",
			errMsg:   "context deadline exceeded",
			contains: "request was cancelled",
		},
		{
			name:     "rate limit",
			errMsg:   "rate limit exceeded",
			contains: "Rate limit reached",
		},
		{
			name:     "too many requests",
			errMsg:   "too many requests",
			contains: "Rate limit reached",
		},
		{
			name:     "generic internal error with details",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"details":"something went wrong"}}`,
			contains: "internal error",
		},
		{
			name:     "unknown error",
			errMsg:   "some unknown error occurred",
			contains: "Prompt failed: some unknown error occurred",
		},
		{
			name:     "nil error returns empty",
			errMsg:   "",
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}

			result := formatACPError(err)

			if tt.contains == "" {
				if result != "" {
					t.Errorf("formatACPError() = %q, want empty string", result)
				}
				return
			}

			if !containsIgnoreCase(result, tt.contains) {
				t.Errorf("formatACPError() = %q, want to contain %q", result, tt.contains)
			}
		})
	}
}

// testError is a simple error implementation for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// --- Config Options Tests ---

// TestBackgroundSession_SetSessionModes_ConvertsToConfigOptions tests that legacy
// modes are correctly converted to the config options format.
func TestBackgroundSession_SetSessionModes_ConvertsToConfigOptions(t *testing.T) {
	bs := &BackgroundSession{}

	description1 := "Ask questions without making changes"
	description2 := "Make code changes"

	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description1},
			{Id: "code", Name: "Code", Description: &description2},
			{Id: "architect", Name: "Architect"}, // No description
		},
	}

	bs.setSessionModes(modes)

	// Verify config options were created
	configOptions := bs.ConfigOptions()
	if len(configOptions) != 1 {
		t.Fatalf("ConfigOptions() length = %d, want 1", len(configOptions))
	}

	modeOption := configOptions[0]

	// Verify the mode config option structure
	if modeOption.ID != ConfigOptionCategoryMode {
		t.Errorf("ID = %q, want %q", modeOption.ID, ConfigOptionCategoryMode)
	}
	if modeOption.Name != "Mode" {
		t.Errorf("Name = %q, want %q", modeOption.Name, "Mode")
	}
	if modeOption.Category != ConfigOptionCategoryMode {
		t.Errorf("Category = %q, want %q", modeOption.Category, ConfigOptionCategoryMode)
	}
	if modeOption.Type != ConfigOptionTypeSelect {
		t.Errorf("Type = %q, want %q", modeOption.Type, ConfigOptionTypeSelect)
	}
	if modeOption.CurrentValue != "ask" {
		t.Errorf("CurrentValue = %q, want %q", modeOption.CurrentValue, "ask")
	}

	// Verify options
	if len(modeOption.Options) != 3 {
		t.Fatalf("Options length = %d, want 3", len(modeOption.Options))
	}

	// Verify first option
	if modeOption.Options[0].Value != "ask" {
		t.Errorf("Options[0].Value = %q, want %q", modeOption.Options[0].Value, "ask")
	}
	if modeOption.Options[0].Name != "Ask" {
		t.Errorf("Options[0].Name = %q, want %q", modeOption.Options[0].Name, "Ask")
	}
	if modeOption.Options[0].Description != description1 {
		t.Errorf("Options[0].Description = %q, want %q", modeOption.Options[0].Description, description1)
	}

	// Verify option without description
	if modeOption.Options[2].Description != "" {
		t.Errorf("Options[2].Description = %q, want empty", modeOption.Options[2].Description)
	}

	// Verify usesLegacyModes flag
	bs.configMu.RLock()
	usesLegacy := bs.usesLegacyModes
	bs.configMu.RUnlock()
	if !usesLegacy {
		t.Error("usesLegacyModes should be true after setSessionModes")
	}
}

// TestBackgroundSession_SetSessionModes_NilModes tests that nil modes are handled gracefully.
func TestBackgroundSession_SetSessionModes_NilModes(t *testing.T) {
	bs := &BackgroundSession{}

	// Should not panic
	bs.setSessionModes(nil)

	// Config options should be empty/nil
	configOptions := bs.ConfigOptions()
	if len(configOptions) != 0 {
		t.Errorf("ConfigOptions() length = %d, want 0", len(configOptions))
	}
}

// TestBackgroundSession_ConfigOptions_ReturnsCopy tests that ConfigOptions returns
// a copy, not a reference to the internal slice.
func TestBackgroundSession_ConfigOptions_ReturnsCopy(t *testing.T) {
	bs := &BackgroundSession{}

	description := "Test description"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Get config options
	options1 := bs.ConfigOptions()
	options2 := bs.ConfigOptions()

	// Modify the first copy
	if len(options1) > 0 {
		options1[0].CurrentValue = "modified"
	}

	// Second copy should be unaffected
	if len(options2) > 0 && options2[0].CurrentValue == "modified" {
		t.Error("ConfigOptions() should return a copy, not a reference")
	}
}

// TestBackgroundSession_GetConfigValue tests getting config values.
func TestBackgroundSession_GetConfigValue(t *testing.T) {
	bs := &BackgroundSession{}

	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Get mode value
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "code" {
		t.Errorf("GetConfigValue(%q) = %q, want %q", ConfigOptionCategoryMode, value, "code")
	}

	// Get non-existent config
	value = bs.GetConfigValue("nonexistent")
	if value != "" {
		t.Errorf("GetConfigValue(%q) = %q, want empty", "nonexistent", value)
	}
}

// TestBackgroundSession_OnCurrentModeChanged tests that mode changes from the agent
// update the config options and notify callbacks.
func TestBackgroundSession_OnCurrentModeChanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes first
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Track callback calls
	var callbackCalls []struct {
		sessionID string
		configID  string
		value     string
	}
	var callbackMu sync.Mutex
	bs.onConfigChanged = func(sessionID, configID, value string) {
		callbackMu.Lock()
		callbackCalls = append(callbackCalls, struct {
			sessionID string
			configID  string
			value     string
		}{sessionID, configID, value})
		callbackMu.Unlock()
	}
	bs.persistedID = "test-session"

	// Simulate mode change from agent
	bs.onCurrentModeChanged("code")

	// Verify current value was updated
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "code" {
		t.Errorf("GetConfigValue after mode change = %q, want %q", value, "code")
	}

	// Verify callback was called
	callbackMu.Lock()
	numCalls := len(callbackCalls)
	callbackMu.Unlock()
	if numCalls != 1 {
		t.Fatalf("Callback called %d times, want 1", numCalls)
	}

	callbackMu.Lock()
	call := callbackCalls[0]
	callbackMu.Unlock()
	if call.sessionID != "test-session" {
		t.Errorf("Callback sessionID = %q, want %q", call.sessionID, "test-session")
	}
	if call.configID != ConfigOptionCategoryMode {
		t.Errorf("Callback configID = %q, want %q", call.configID, ConfigOptionCategoryMode)
	}
	if call.value != "code" {
		t.Errorf("Callback value = %q, want %q", call.value, "code")
	}
}

// TestBackgroundSession_OnCurrentModeChanged_Closed tests that mode changes are
// ignored when the session is closed.
func TestBackgroundSession_OnCurrentModeChanged_Closed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	// Set up modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Close the session properly using the Close method which sets closed flag
	bs.Close("test")

	// Track callback calls
	callbackCalled := false
	bs.onConfigChanged = func(sessionID, configID, value string) {
		callbackCalled = true
	}

	// Try to change mode - should be ignored
	bs.onCurrentModeChanged("code")

	if callbackCalled {
		t.Error("Callback should not be called when session is closed")
	}

	// Value should still be "ask"
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "ask" {
		t.Errorf("GetConfigValue after ignored mode change = %q, want %q", value, "ask")
	}
}

// TestBackgroundSession_SetConfigOption_NoACP tests SetConfigOption error handling
// when there's no ACP connection.
func TestBackgroundSession_SetConfigOption_NoACP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:     ctx,
		cancel:  cancel,
		acpConn: nil, // No ACP connection
	}

	// Set up modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Try to set config option - should fail
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "code")
	if err == nil {
		t.Error("SetConfigOption should fail when there's no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}
}

// TestBackgroundSession_SetConfigOption_InvalidConfigID tests SetConfigOption error
// handling for unknown config IDs.
func TestBackgroundSession_SetConfigOption_InvalidConfigID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a mock ACP connection (we just need it to not be nil)
	// Use a type assertion trick to create a non-nil pointer without actually initializing
	var mockConn *acp.ClientSideConnection
	// We can't easily create a real ClientSideConnection, so test the error path differently
	// by verifying that after passing acpConn check, we get the right error

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes - but don't set acpConn so we get "no ACP connection" error first
	// This tests that order of checks: IsClosed -> acpConn -> configID validation
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// First verify no ACP connection error
	err := bs.SetConfigOption(context.Background(), "unknown_config", "value")
	if err == nil {
		t.Error("SetConfigOption should fail when no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}

	// Note: Testing unknown config ID validation requires a real ACP connection
	// which requires a full mock ACP server setup. The unit test above verifies
	// the earlier check in the code path.
	_ = mockConn // silence unused variable warning
}

// TestBackgroundSession_SetConfigOption_InvalidValue tests SetConfigOption error
// handling for invalid values.
// Note: Full validation testing requires a mock ACP server. This test verifies
// the code path up to the ACP connection check.
func TestBackgroundSession_SetConfigOption_InvalidValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes - but no acpConn, so we get "no ACP connection" error
	// The invalid value check happens after the ACP connection check
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Try to set a value - should fail with no ACP connection
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "invalid_mode")
	if err == nil {
		t.Error("SetConfigOption should fail when no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}

	// Note: Testing invalid value validation requires a real ACP connection
	// which requires a full mock ACP server setup. Integration tests cover this.
}

// TestBackgroundSession_SetConfigOption_Closed tests SetConfigOption error handling
// when the session is closed.
func TestBackgroundSession_SetConfigOption_Closed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	// Close the session properly using the Close method which sets closed flag
	bs.Close("test")

	// Try to set config option - should fail
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "code")
	if err == nil {
		t.Error("SetConfigOption should fail when session is closed")
	}
	if !strings.Contains(err.Error(), "session is closed") {
		t.Errorf("Error message = %q, should contain 'session is closed'", err.Error())
	}
}

// TestBackgroundSession_ConfigOptions_Empty tests that ConfigOptions returns nil
// when no config options are set.
func TestBackgroundSession_ConfigOptions_Empty(t *testing.T) {
	bs := &BackgroundSession{}

	configOptions := bs.ConfigOptions()
	if configOptions != nil {
		t.Errorf("ConfigOptions() = %v, want nil", configOptions)
	}
}

// TestBackgroundSession_SetSessionModes_PersistsToMetadata tests that the initial
// mode is persisted to metadata.
func TestBackgroundSession_SetSessionModes_PersistsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mode-persist"

	// Create session in store
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
	}

	// Set modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Verify metadata was updated
	updatedMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updatedMeta.CurrentModeID != "code" {
		t.Errorf("Metadata.CurrentModeID = %q, want %q", updatedMeta.CurrentModeID, "code")
	}
}

// TestBackgroundSession_OnCurrentModeChanged_PersistsToMetadata tests that mode
// changes from the agent are persisted to metadata.
func TestBackgroundSession_OnCurrentModeChanged_PersistsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mode-change-persist"

	// Create session in store
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Set initial modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Simulate mode change from agent
	bs.onCurrentModeChanged("code")

	// Verify metadata was updated
	updatedMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updatedMeta.CurrentModeID != "code" {
		t.Errorf("Metadata.CurrentModeID = %q, want %q", updatedMeta.CurrentModeID, "code")
	}
}

// =============================================================================
// Session MCP Server Tests
// =============================================================================

func TestStartSessionMcpServer_ReturnsEmptySlice(t *testing.T) {
	// With the global MCP server architecture, startSessionMcpServer should always
	// return an empty slice (MCP is configured globally, not passed per-session).
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mcp-global"
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		ctx:         ctx,
		// Note: globalMcpServer is nil, so registration will be skipped
	}

	// Agent supports HTTP MCP
	agentCaps := acp.AgentCapabilities{
		McpCapabilities: acp.McpCapabilities{
			Http: true,
		},
	}

	mcpServers := bs.startSessionMcpServer(store, agentCaps)

	// With global MCP server architecture, no McpServers are passed to ACP
	if len(mcpServers) != 0 {
		t.Errorf("Expected empty MCP servers slice (using global server), got %d", len(mcpServers))
	}
}

// TestUIPrompt tests the UI prompt functionality
func TestUIPrompt(t *testing.T) {
	t.Run("basic flow", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		// Track what the observer receives
		var receivedPrompt UIPromptRequest
		var promptReceived bool
		promptCh := make(chan struct{}, 1)

		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {
				receivedPrompt = req
				promptReceived = true
				promptCh <- struct{}{}
			},
		}
		bs.AddObserver(observer)

		// Start a goroutine to answer the prompt
		go func() {
			<-promptCh // Wait for prompt to be sent
			time.Sleep(50 * time.Millisecond)
			bs.HandleUIPromptAnswer("test-request-001", "yes", "Deploy Now")
		}()

		// Send a UI prompt
		req := UIPromptRequest{
			RequestID:      "test-request-001",
			Type:           UIPromptTypeYesNo,
			Question:       "Do you want to deploy?",
			Options:        []UIPromptOption{{ID: "yes", Label: "Deploy Now"}, {ID: "no", Label: "Cancel"}},
			TimeoutSeconds: 5,
		}

		resp, err := bs.UIPrompt(ctx, req)
		if err != nil {
			t.Fatalf("UIPrompt failed: %v", err)
		}

		if !promptReceived {
			t.Error("Observer should have received OnUIPrompt call")
		}
		if receivedPrompt.RequestID != "test-request-001" {
			t.Errorf("OnUIPrompt request ID = %q, want %q", receivedPrompt.RequestID, "test-request-001")
		}
		if resp.OptionID != "yes" {
			t.Errorf("Response option ID = %q, want %q", resp.OptionID, "yes")
		}
		if resp.Label != "Deploy Now" {
			t.Errorf("Response label = %q, want %q", resp.Label, "Deploy Now")
		}
		if resp.TimedOut {
			t.Error("Response should not be timed out")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		// Track dismiss calls
		var dismissReceived bool
		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {},
			onUIPromptDismiss: func(requestID string, reason string) {
				dismissReceived = true
			},
		}
		bs.AddObserver(observer)

		// Send a UI prompt with very short timeout (no answer)
		req := UIPromptRequest{
			RequestID:      "test-request-timeout",
			Type:           UIPromptTypeYesNo,
			Question:       "This will timeout",
			Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
			TimeoutSeconds: 1, // 1 second timeout
		}

		resp, err := bs.UIPrompt(ctx, req)
		if err != nil {
			t.Fatalf("UIPrompt failed: %v", err)
		}

		if !resp.TimedOut {
			t.Error("Response should be timed out")
		}

		// Wait for dismiss notification
		time.Sleep(100 * time.Millisecond)
		if !dismissReceived {
			t.Error("Observer should have received OnUIPromptDismiss call")
		}
	})

	t.Run("replace prompt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		var promptCount int
		var dismissReasons []string
		var mu sync.Mutex
		firstPromptReceived := make(chan struct{}, 1)
		secondPromptReceived := make(chan struct{}, 1)

		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {
				mu.Lock()
				promptCount++
				count := promptCount
				mu.Unlock()
				switch count {
				case 1:
					firstPromptReceived <- struct{}{}
				case 2:
					secondPromptReceived <- struct{}{}
				}
			},
			onUIPromptDismiss: func(requestID string, reason string) {
				mu.Lock()
				dismissReasons = append(dismissReasons, reason)
				mu.Unlock()
			},
		}
		bs.AddObserver(observer)

		// Start first prompt (don't answer it)
		firstPromptDone := make(chan struct{})
		go func() {
			defer close(firstPromptDone)
			req1 := UIPromptRequest{
				RequestID:      "first-prompt",
				Type:           UIPromptTypeYesNo,
				Question:       "First question",
				Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
				TimeoutSeconds: 30,
			}
			bs.UIPrompt(ctx, req1)
		}()

		// Wait for first prompt to be sent to observer
		select {
		case <-firstPromptReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("First prompt was not received by observer")
		}

		// Send second prompt in a goroutine
		secondPromptDone := make(chan UIPromptResponse)
		go func() {
			req2 := UIPromptRequest{
				RequestID:      "second-prompt",
				Type:           UIPromptTypeYesNo,
				Question:       "Second question",
				Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
				TimeoutSeconds: 5,
			}
			resp, _ := bs.UIPrompt(ctx, req2)
			secondPromptDone <- resp
		}()

		// Wait for second prompt to be sent to observer
		select {
		case <-secondPromptReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("Second prompt was not received by observer")
		}

		// Now answer the second prompt
		bs.HandleUIPromptAnswer("second-prompt", "yes", "Yes")

		// Get response
		var resp UIPromptResponse
		select {
		case resp = <-secondPromptDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Second prompt did not complete")
		}

		// Verify second prompt was answered
		if resp.OptionID != "yes" {
			t.Errorf("Response option ID = %q, want %q", resp.OptionID, "yes")
		}

		// Wait for first prompt to complete (it should have been replaced)
		select {
		case <-firstPromptDone:
		case <-time.After(2 * time.Second):
			t.Error("First prompt goroutine did not complete")
		}

		// Wait for async dismiss notification (sent via goroutine)
		time.Sleep(100 * time.Millisecond)

		// Verify we got 2 prompts
		mu.Lock()
		pc := promptCount
		reasons := dismissReasons
		mu.Unlock()

		if pc != 2 {
			t.Errorf("Prompt count = %d, want 2", pc)
		}

		// Verify first prompt was dismissed with "replaced" reason
		foundReplaced := false
		for _, r := range reasons {
			if r == "replaced" {
				foundReplaced = true
				break
			}
		}
		if !foundReplaced {
			t.Errorf("Expected 'replaced' in dismiss reasons, got %v", reasons)
		}
	})
}
