package mcpserver

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestNewServer(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server - this should not panic
	srv, err := NewServer(
		Config{Port: 0}, // Use port 0 to get a random available port
		Dependencies{
			Store:  store,
			Config: nil, // Config is optional
		},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv == nil {
		t.Fatal("NewServer returned nil")
	}

	// Verify server is not running yet
	if srv.IsRunning() {
		t.Error("Server should not be running before Start()")
	}
}

func TestServerStartStop(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Start server
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify server is running
	if !srv.IsRunning() {
		t.Error("Server should be running after Start()")
	}

	// Verify port was assigned
	port := srv.Port()
	if port == 0 {
		t.Error("Port should be assigned after Start()")
	}

	// Stop server
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Give it a moment to stop
	time.Sleep(100 * time.Millisecond)

	// Verify server is not running
	if srv.IsRunning() {
		t.Error("Server should not be running after Stop()")
	}
}

func TestListConversationsWithEmptyStore(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Start server
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	// The server should be running and tools should be registered
	if !srv.IsRunning() {
		t.Error("Server should be running")
	}
}

func TestGetRuntimeInfo(t *testing.T) {
	// Test buildRuntimeInfo directly
	info := buildRuntimeInfo()

	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.Arch == "" {
		t.Error("Arch should not be empty")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}
	if info.PID == 0 {
		t.Error("PID should not be 0")
	}
	if info.NumCPU == 0 {
		t.Error("NumCPU should not be 0")
	}
}

func TestTransportModeDefaults(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Test default mode is SSE
	srv, err := NewServer(
		Config{}, // Empty config should default to SSE
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv.Mode() != TransportModeSSE {
		t.Errorf("Default mode should be SSE, got %s", srv.Mode())
	}
}

func TestTransportModeSTDIO(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Test STDIO mode configuration
	srv, err := NewServer(
		Config{Mode: TransportModeSTDIO},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv.Mode() != TransportModeSTDIO {
		t.Errorf("Mode should be STDIO, got %s", srv.Mode())
	}

	// Port should be 0 for STDIO mode (not used)
	// Note: We don't start the server here because STDIO mode
	// would try to read from actual stdin
}

func TestConversationStartDuplicateTitle(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a parent session with can_start_conversation enabled
	parentMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create an existing session with a specific title
	existingMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Test Title",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(existingMeta); err != nil {
		t.Fatalf("Failed to create existing session: %v", err)
	}

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register the parent session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	// Try to create a new conversation with the same title
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID: parentMeta.SessionID,
		Title:  "Test Title", // Same as existing session
	}

	_, _, err = srv.handleConversationStart(ctx, nil, input)
	if err == nil {
		t.Fatal("Expected error when creating conversation with duplicate title, got nil")
	}

	// Verify error message mentions the duplicate title
	expectedMsg := "a conversation with the title 'Test Title' already exists"
	if !contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error to contain '%s', got: %v", expectedMsg, err)
	}

	// Verify error message includes the existing session ID
	if !contains(err.Error(), existingMeta.SessionID) {
		t.Errorf("Expected error to include session ID '%s', got: %v", existingMeta.SessionID, err)
	}
}

func TestConversationStartUniqueTitleAllowed(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a parent session with can_start_conversation enabled
	parentMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create an existing session with a different title
	existingMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Existing Title",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(existingMeta); err != nil {
		t.Fatalf("Failed to create existing session: %v", err)
	}

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register the parent session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	// Try to create a new conversation with a unique title
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID: parentMeta.SessionID,
		Title:  "Unique Title", // Different from existing session
	}

	_, output, err := srv.handleConversationStart(ctx, nil, input)
	if err != nil {
		t.Fatalf("Expected no error when creating conversation with unique title, got: %v", err)
	}

	// Verify the new session was created
	if output.SessionID == "" {
		t.Error("Expected session ID in output")
	}

	// Verify the title matches
	if output.Title != "Unique Title" {
		t.Errorf("Expected title 'Unique Title', got: %s", output.Title)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockSessionManager is a mock implementation of SessionManager for testing.
type mockSessionManager struct {
	broadcastCalls []broadcastCall
}

type broadcastCall struct {
	sessionID       string
	name            string
	acpServer       string
	workingDir      string
	parentSessionID string
}

func (m *mockSessionManager) GetSession(sessionID string) BackgroundSession {
	return nil
}

func (m *mockSessionManager) ListRunningSessions() []string {
	return nil
}

func (m *mockSessionManager) CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool {
	return true
}

func (m *mockSessionManager) CloseSession(sessionID, reason string) {
}

func (m *mockSessionManager) ResumeSession(sessionID, sessionName, workingDir string) (BackgroundSession, error) {
	return nil, nil
}

func (m *mockSessionManager) GetWorkspacesForFolder(folder string) []config.WorkspaceSettings {
	return nil
}

func (m *mockSessionManager) BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID string) {
	m.broadcastCalls = append(m.broadcastCalls, broadcastCall{
		sessionID:       sessionID,
		name:            name,
		acpServer:       acpServer,
		workingDir:      workingDir,
		parentSessionID: parentSessionID,
	})
}

func TestConversationStartBroadcastsEvent(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a parent session with can_start_conversation enabled
	parentMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create mock session manager to track broadcasts
	mockSM := &mockSessionManager{}

	// Create server with mock session manager
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{
			Store:          store,
			SessionManager: mockSM,
		},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register the parent session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	// Create a new conversation
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID: parentMeta.SessionID,
		Title:  "Test Conversation",
	}

	_, output, err := srv.handleConversationStart(ctx, nil, input)
	if err != nil {
		t.Fatalf("handleConversationStart failed: %v", err)
	}

	// Verify the session was created
	if output.SessionID == "" {
		t.Fatal("Expected session ID in output")
	}

	// Verify BroadcastSessionCreated was called
	if len(mockSM.broadcastCalls) != 1 {
		t.Fatalf("Expected 1 broadcast call, got %d", len(mockSM.broadcastCalls))
	}

	call := mockSM.broadcastCalls[0]
	if call.sessionID != output.SessionID {
		t.Errorf("Expected broadcast sessionID %s, got %s", output.SessionID, call.sessionID)
	}
	if call.name != "Test Conversation" {
		t.Errorf("Expected broadcast name 'Test Conversation', got %s", call.name)
	}
	if call.acpServer != "test-server" {
		t.Errorf("Expected broadcast acpServer 'test-server', got %s", call.acpServer)
	}
	if call.workingDir != "/test/dir" {
		t.Errorf("Expected broadcast workingDir '/test/dir', got %s", call.workingDir)
	}
	if call.parentSessionID != parentMeta.SessionID {
		t.Errorf("Expected broadcast parentSessionID %s, got %s", parentMeta.SessionID, call.parentSessionID)
	}
}
