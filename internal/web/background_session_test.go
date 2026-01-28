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

func TestBackgroundSession_ClientAttachment(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.HasAttachedClient() {
		t.Error("New BackgroundSession should not have attached client")
	}

	if bs.GetAttachedClient() != nil {
		t.Error("GetAttachedClient should return nil for new session")
	}

	// Attach a mock client (we can't easily create a real WSClient in tests)
	mockClient := &WSClient{}
	bs.AttachClient(mockClient)

	if !bs.HasAttachedClient() {
		t.Error("BackgroundSession should have attached client after AttachClient")
	}

	if bs.GetAttachedClient() != mockClient {
		t.Error("GetAttachedClient should return the attached client")
	}

	// Detach client
	bs.DetachClient()

	if bs.HasAttachedClient() {
		t.Error("BackgroundSession should not have attached client after DetachClient")
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

func (m *mockSessionObserver) OnPromptComplete() {
	m.completed = true
}

func (m *mockSessionObserver) OnError(message string) {
	m.errors = append(m.errors, message)
}
