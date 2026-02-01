package web

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
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
	agentMessages []string
	agentThoughts []string
	toolCalls     []string
	errors        []string
	completed     bool
}

func (m *mockSessionObserver) OnAgentMessage(html string) {
	m.agentMessages = append(m.agentMessages, html)
}

func (m *mockSessionObserver) OnAgentThought(text string) {
	m.agentThoughts = append(m.agentThoughts, text)
}

func (m *mockSessionObserver) OnToolCall(id, title, status string) {
	m.toolCalls = append(m.toolCalls, id)
}

func (m *mockSessionObserver) OnToolUpdate(id string, status *string) {}

func (m *mockSessionObserver) OnPlan() {}

func (m *mockSessionObserver) OnFileWrite(path string, size int) {}

func (m *mockSessionObserver) OnFileRead(path string, size int) {}

func (m *mockSessionObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockSessionObserver) OnPromptComplete(eventCount int) {
	m.completed = true
}

func (m *mockSessionObserver) OnUserPrompt(senderID, promptID, message string, imageIDs []string) {
	// No-op for tests
}

func (m *mockSessionObserver) OnError(message string) {
	m.errors = append(m.errors, message)
}

func (m *mockSessionObserver) OnQueueUpdated(queueLength int, action string, messageID string) {
	// No-op for tests
}

func (m *mockSessionObserver) OnQueueMessageSending(messageID string) {
	// No-op for tests
}

func (m *mockSessionObserver) OnQueueMessageSent(messageID string) {
	// No-op for tests
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
