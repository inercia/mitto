package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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

	// Create a parent session with can_start_conversation enabled and other flags
	parentMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation:   true,
			session.FlagCanPromptUser:          true,
			session.FlagAutoApprovePermissions: true,
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

	// Verify the child inherited all of the parent's flags
	// Note: children are prevented from starting conversations by the ParentSessionID
	// check in handleConversationStart, not by flags.
	childMeta, err := store.GetMetadata(output.SessionID)
	if err != nil {
		t.Fatalf("Failed to get child metadata: %v", err)
	}
	if !session.GetFlagValue(childMeta.AdvancedSettings, session.FlagCanStartConversation) {
		t.Error("Child should have inherited can_start_conversation=true from parent")
	}
	if !session.GetFlagValue(childMeta.AdvancedSettings, session.FlagCanPromptUser) {
		t.Error("Child should have inherited can_prompt_user=true from parent")
	}
	if !session.GetFlagValue(childMeta.AdvancedSettings, session.FlagAutoApprovePermissions) {
		t.Error("Child should have inherited auto_approve_permissions=true from parent")
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
	broadcastCalls      []broadcastCall
	workspacesForFolder []config.WorkspaceSettings
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
	return m.workspacesForFolder
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

func (m *mockSessionManager) BroadcastSessionArchived(sessionID string, archived bool) {}

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
	mockSM := &mockSessionManager{
		workspacesForFolder: []config.WorkspaceSettings{
			{ACPServer: "test-server", WorkingDir: "/test/dir"},
		},
	}

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

func TestConversationStart_NoWorkspaceForACPServer(t *testing.T) {
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
		ACPServer:  "server-a",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Mock session manager with only server-a workspace for this folder
	mockSM := &mockSessionManager{
		workspacesForFolder: []config.WorkspaceSettings{
			{ACPServer: "server-a", WorkingDir: "/test/dir"},
		},
	}

	// Create server with mock session manager and a second ACP server in config
	appConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "server-a", Command: "echo a"},
			{Name: "server-b", Command: "echo b"},
		},
	}
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{
			Store:          store,
			Config:         appConfig,
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

	// Try to create a conversation with an ACP server that has no workspace for this folder
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID:    parentMeta.SessionID,
		Title:     "Should Fail",
		ACPServer: "server-b", // No workspace for this server + folder pair
	}

	_, _, err = srv.handleConversationStart(ctx, nil, input)
	if err == nil {
		t.Fatal("Expected error when creating conversation with ACP server that has no workspace, got nil")
	}

	// Verify error message
	if !contains(err.Error(), "no workspace configured") {
		t.Errorf("Expected error to contain 'no workspace configured', got: %v", err)
	}
	if !contains(err.Error(), "server-b") {
		t.Errorf("Expected error to mention requested server 'server-b', got: %v", err)
	}
}

func TestConversationStart_WorkspaceExistsForACPServer(t *testing.T) {
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
		ACPServer:  "server-a",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Mock session manager with both server-a and server-b workspaces for this folder
	mockSM := &mockSessionManager{
		workspacesForFolder: []config.WorkspaceSettings{
			{ACPServer: "server-a", WorkingDir: "/test/dir"},
			{ACPServer: "server-b", WorkingDir: "/test/dir"},
		},
	}

	// Create server with mock session manager
	appConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "server-a", Command: "echo a"},
			{Name: "server-b", Command: "echo b"},
		},
	}
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{
			Store:          store,
			Config:         appConfig,
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

	// Create a conversation with server-b — should succeed because workspace exists
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID:    parentMeta.SessionID,
		Title:     "Should Succeed",
		ACPServer: "server-b",
	}

	_, output, err := srv.handleConversationStart(ctx, nil, input)
	if err != nil {
		t.Fatalf("Expected no error when creating conversation with valid workspace, got: %v", err)
	}

	if output.SessionID == "" {
		t.Error("Expected session ID in output")
	}
	if output.ACPServer != "server-b" {
		t.Errorf("Expected ACP server 'server-b', got: %s", output.ACPServer)
	}
}

func TestConversationStart_InheritedServerRequiresWorkspace(t *testing.T) {
	// Test that even when inheriting the parent's ACP server (no explicit acp_server),
	// the workspace validation still applies
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a parent session
	parentMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent Session",
		ACPServer:  "orphan-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Mock session manager returns NO workspaces for this folder
	mockSM := &mockSessionManager{
		workspacesForFolder: []config.WorkspaceSettings{},
	}

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

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	// Try to create a conversation — no explicit acp_server, inherits "orphan-server"
	ctx := context.Background()
	input := ConversationStartInput{
		SelfID: parentMeta.SessionID,
		Title:  "Should Fail Too",
	}

	_, _, err = srv.handleConversationStart(ctx, nil, input)
	if err == nil {
		t.Fatal("Expected error when inheriting ACP server with no workspace, got nil")
	}
	if !contains(err.Error(), "no workspace configured") {
		t.Errorf("Expected error to contain 'no workspace configured', got: %v", err)
	}
}

// =============================================================================
// Parent-Child Task Coordination Tests
// =============================================================================

// setupParentChildSessions creates a server with a parent session and N child sessions.
// Returns the server, store, parent session ID, and child session IDs.
func setupParentChildSessions(t *testing.T, numChildren int) (*Server, *session.Store, string, []string) {
	t.Helper()

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Create parent session
	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create child sessions
	childIDs := make([]string, numChildren)
	for i := 0; i < numChildren; i++ {
		childID := session.GenerateSessionID()
		childMeta := session.Metadata{
			SessionID:       childID,
			Name:            "Child Session",
			ACPServer:       "test-server",
			WorkingDir:      "/test/dir",
			ParentSessionID: parentID,
		}
		if err := store.Create(childMeta); err != nil {
			t.Fatalf("Failed to create child session %d: %v", i, err)
		}
		childIDs[i] = childID
	}

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register all sessions
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}
	for _, childID := range childIDs {
		if err := srv.RegisterSession(childID, nil, logger); err != nil {
			t.Fatalf("Failed to register child session: %v", err)
		}
	}

	return srv, store, parentID, childIDs
}

func TestChildrenTasksWait_AllReport(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 2)
	ctx := context.Background()

	// Start wait in a goroutine (it blocks)
	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			Prompt:         "What is your progress?",
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give the wait handler time to set up the coordination
	time.Sleep(100 * time.Millisecond)

	// Report from both children
	for _, childID := range childIDs {
		_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
			SelfID:  childID,
			Status:  "completed",
			Summary: "Child task completed",
		})
		if err != nil {
			t.Fatalf("handleChildrenTasksReport failed for child %s: %v", childID, err)
		}
		if !output.Success {
			t.Errorf("Expected success for child report, got error: %s", output.Error)
		}
		if output.ParentSessionID != parentID {
			t.Errorf("Expected parent ID %s, got %s", parentID, output.ParentSessionID)
		}
	}

	// Wait for the result
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.Success {
			t.Fatalf("Expected success, got error: %s", result.output.Error)
		}
		if result.output.TimedOut {
			t.Error("Expected no timeout")
		}
		if len(result.output.Reports) != 2 {
			t.Errorf("Expected 2 reports, got %d", len(result.output.Reports))
		}
		for _, childID := range childIDs {
			report, ok := result.output.Reports[childID]
			if !ok {
				t.Errorf("Missing report for child %s", childID)
				continue
			}
			if !report.Completed {
				t.Errorf("Expected child %s report to be completed", childID)
			}
			if report.Report == nil {
				t.Errorf("Expected non-nil report for child %s", childID)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for handleChildrenTasksWait to return")
	}
}

func TestChildrenTasksWait_Timeout(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 2)
	ctx := context.Background()

	// Start wait with a very short timeout
	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TimeoutSeconds: 1, // 1 second timeout
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give time for coordination setup
	time.Sleep(100 * time.Millisecond)

	// Only report from one child
	_, _, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport failed: %v", err)
	}

	// Wait for timeout
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.Success {
			t.Fatalf("Expected success (even on timeout), got error: %s", result.output.Error)
		}
		if !result.output.TimedOut {
			t.Error("Expected timeout")
		}
		// First child should have reported
		report0 := result.output.Reports[childIDs[0]]
		if !report0.Completed {
			t.Error("Expected first child report to be completed")
		}
		if report0.Reason != "" {
			t.Errorf("Expected no reason for completed child, got '%s'", report0.Reason)
		}
		// Second child should NOT have reported — with diagnostic reason
		report1 := result.output.Reports[childIDs[1]]
		if report1.Completed {
			t.Error("Expected second child report to NOT be completed")
		}
		if report1.Reason != "no_report_received" {
			t.Errorf("Expected reason 'no_report_received' for timed-out child, got '%s'", report1.Reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for handleChildrenTasksWait to return")
	}
}

func TestChildrenTasksReport_NoParentWaiting(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Report without parent waiting — the report call itself should succeed
	_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !output.Success {
		t.Errorf("Expected success, got error: %s", output.Error)
	}
	if output.ParentSessionID != parentID {
		t.Errorf("Expected parent ID %s, got %s", parentID, output.ParentSessionID)
	}

	// Now parent calls wait with the same (empty) task_id — report is preserved,
	// so wait returns immediately with the existing report.
	_, waitOutput, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}
	if waitOutput.TimedOut {
		t.Error("Should not time out — report was preserved for same task")
	}

	report, ok := waitOutput.Reports[childIDs[0]]
	if !ok {
		t.Fatal("Missing report for child")
	}
	if !report.Completed {
		t.Error("Expected child report to be completed (preserved from same task)")
	}
}

func TestChildrenTasksReport_DuplicateReport(t *testing.T) {
	// Use 2 children so the first child can report twice before completion
	srv, _, parentID, childIDs := setupParentChildSessions(t, 2)
	ctx := context.Background()

	// Start wait in goroutine
	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	time.Sleep(100 * time.Millisecond)

	// Report twice from child[0] (second overwrites first)
	_, _, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "in_progress",
		Summary: "Attempt 1",
	})
	if err != nil {
		t.Fatalf("First report failed: %v", err)
	}

	_, output2, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Attempt 2",
	})
	if err != nil {
		t.Fatalf("Second report failed: %v", err)
	}
	if !output2.Success {
		t.Errorf("Expected success on duplicate report, got error: %s", output2.Error)
	}

	// Now report from child[1] to complete
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[1],
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("Child[1] report failed: %v", err)
	}

	// Wait for result - child[0] should have the second (overwritten) report
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		report := result.output.Reports[childIDs[0]]
		if !report.Completed {
			t.Error("Expected completed report")
		}
		// The report should be the second one (overwritten)
		if report.Report == nil || report.Report.Summary != "Attempt 2" {
			t.Errorf("Expected second report with summary 'Attempt 2', got: %+v", report.Report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestChildrenTasksWait_InvalidChildren(t *testing.T) {
	srv, _, parentID, _ := setupParentChildSessions(t, 0)
	ctx := context.Background()

	// Try to wait with non-existent children
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{"non-existent-child"},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	if output.Success {
		t.Error("Expected failure for non-existent children")
	}
	if output.Error == "" {
		t.Error("Expected error message")
	}
}

func TestChildrenTasksWait_NotParent(t *testing.T) {
	srv, store, parentID, _ := setupParentChildSessions(t, 0)
	ctx := context.Background()

	// Create a session that is NOT a child of the parent
	otherID := session.GenerateSessionID()
	otherMeta := session.Metadata{
		SessionID:  otherID,
		Name:       "Other Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		// No ParentSessionID - not a child of parentID
	}
	if err := store.Create(otherMeta); err != nil {
		t.Fatalf("Failed to create other session: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(otherID, nil, logger); err != nil {
		t.Fatalf("Failed to register other session: %v", err)
	}

	// Try to wait for a non-child session
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{otherID},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	// Should fail because otherID is not a child of parentID
	if output.Success {
		t.Error("Expected failure for non-child session")
	}
}

func TestChildrenTasksWait_EmptyChildrenList(t *testing.T) {
	srv, _, parentID, _ := setupParentChildSessions(t, 0)
	ctx := context.Background()

	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:       parentID,
		ChildrenList: []string{},
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	if output.Success {
		t.Error("Expected failure for empty children list")
	}
}

func TestChildrenTasksReport_NoParentSession(t *testing.T) {
	// Create a session with no parent
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	sessionID := session.GenerateSessionID()
	meta := session.Metadata{
		SessionID:  sessionID,
		Name:       "Orphan Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		// No ParentSessionID
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(sessionID, nil, logger); err != nil {
		t.Fatalf("Failed to register session: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  sessionID,
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}

	if output.Success {
		t.Error("Expected failure for session with no parent")
	}
	if output.Error == "" {
		t.Error("Expected error about no parent session")
	}
}

func TestChildrenTasksReport_SizeLimits(t *testing.T) {
	srv, _, _, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	t.Run("summary too large", func(t *testing.T) {
		longSummary := strings.Repeat("x", maxReportSummaryBytes+1)
		_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
			SelfID:  childIDs[0],
			Status:  "completed",
			Summary: longSummary,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if output.Success {
			t.Fatal("expected failure for oversized summary")
		}
		if !strings.Contains(output.Error, "summary is too long") {
			t.Errorf("expected 'summary is too long' in error, got: %s", output.Error)
		}
	})

	t.Run("details too large", func(t *testing.T) {
		longDetails := strings.Repeat("y", maxReportDetailsBytes+1)
		_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
			SelfID:  childIDs[0],
			Status:  "completed",
			Summary: "Short summary",
			Details: longDetails,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if output.Success {
			t.Fatal("expected failure for oversized details")
		}
		if !strings.Contains(output.Error, "details is too long") {
			t.Errorf("expected 'details is too long' in error, got: %s", output.Error)
		}
	})

	t.Run("at exact limit succeeds", func(t *testing.T) {
		exactSummary := strings.Repeat("s", maxReportSummaryBytes)
		exactDetails := strings.Repeat("d", maxReportDetailsBytes)
		_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
			SelfID:  childIDs[0],
			Status:  "completed",
			Summary: exactSummary,
			Details: exactDetails,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !output.Success {
			t.Errorf("expected success at exact limit, got error: %s", output.Error)
		}
	})
}

func TestChildrenTasksWait_PromptEnqueued(t *testing.T) {
	srv, store, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Start wait in goroutine with short timeout
	go func() {
		srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			Prompt:         "What is your status?",
			TimeoutSeconds: 1,
		})
	}()

	// Give time for the prompt to be enqueued
	time.Sleep(200 * time.Millisecond)

	// Check that the prompt was enqueued to the child
	queue := store.Queue(childIDs[0])
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Failed to list queue: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("Expected 1 queued message, got %d", len(messages))
	}

	// Verify the prompt contains the user's text and the report instruction
	msg := messages[0].Message
	if !contains(msg, "What is your status?") {
		t.Error("Expected prompt to contain user's text")
	}
	if !contains(msg, "mitto_children_tasks_report") {
		t.Error("Expected prompt to contain report instruction")
	}
}

func TestChildrenTasksWait_AllChildrenNotRunning(t *testing.T) {
	// Create parent + children, but DON'T register children (they are "closed")
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Closed Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Only register parent, NOT child (simulates child being closed)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{childID},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	// Should return immediately with success (no blocking since no running children)
	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}
	if output.TimedOut {
		t.Error("Should not have timed out - should return immediately")
	}

	// Check that the child is reported as not_running with reason
	report, ok := output.Reports[childID]
	if !ok {
		t.Fatal("Missing report for child")
	}
	if report.Completed {
		t.Error("Expected not completed for closed child")
	}
	if report.Status != "not_running" {
		t.Errorf("Expected status 'not_running', got '%s'", report.Status)
	}
	if report.Reason != "session_closed" {
		t.Errorf("Expected reason 'session_closed' for closed child, got '%s'", report.Reason)
	}

	// Check warnings
	if len(output.Warnings) == 0 {
		t.Error("Expected warnings about not-running children")
	}
}

func TestChildrenTasksWait_MixedRunningAndClosed(t *testing.T) {
	// Create parent + 2 children, only register one child
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	runningChildID := session.GenerateSessionID()
	closedChildID := session.GenerateSessionID()
	for _, childID := range []string{runningChildID, closedChildID} {
		childMeta := session.Metadata{
			SessionID:       childID,
			Name:            "Child Session",
			ACPServer:       "test-server",
			WorkingDir:      "/test/dir",
			ParentSessionID: parentID,
		}
		if err := store.Create(childMeta); err != nil {
			t.Fatalf("Failed to create child session: %v", err)
		}
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register parent and only the running child
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent: %v", err)
	}
	if err := srv.RegisterSession(runningChildID, nil, logger); err != nil {
		t.Fatalf("Failed to register running child: %v", err)
	}
	// closedChildID is NOT registered

	ctx := context.Background()

	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   []string{runningChildID, closedChildID},
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give the wait handler time to set up
	time.Sleep(100 * time.Millisecond)

	// Report from the running child only
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  runningChildID,
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport failed: %v", err)
	}

	// Wait for result
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.Success {
			t.Fatalf("Expected success, got error: %s", result.output.Error)
		}
		if result.output.TimedOut {
			t.Error("Should not have timed out")
		}

		// Running child should have completed report
		runningReport, ok := result.output.Reports[runningChildID]
		if !ok {
			t.Fatal("Missing report for running child")
		}
		if !runningReport.Completed {
			t.Error("Expected running child report to be completed")
		}
		if runningReport.Status != "completed" {
			t.Errorf("Expected status 'completed', got '%s'", runningReport.Status)
		}

		// Closed child should be marked as not_running with reason
		closedReport, ok := result.output.Reports[closedChildID]
		if !ok {
			t.Fatal("Missing report for closed child")
		}
		if closedReport.Completed {
			t.Error("Expected closed child report to NOT be completed")
		}
		if closedReport.Status != "not_running" {
			t.Errorf("Expected status 'not_running', got '%s'", closedReport.Status)
		}
		if closedReport.Reason != "session_closed" {
			t.Errorf("Expected reason 'session_closed' for closed child, got '%s'", closedReport.Reason)
		}

		// Check warnings
		if len(result.output.Warnings) == 0 {
			t.Error("Expected warnings about not-running child")
		}

		// Reports should include both children
		if len(result.output.Reports) != 2 {
			t.Errorf("Expected 2 reports, got %d", len(result.output.Reports))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for handleChildrenTasksWait to return")
	}
}

func TestChildrenTasksWait_ArchivedChild(t *testing.T) {
	// Create parent + archived child
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Archived Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
		Archived:        true,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Only register parent (archived child would NOT be registered)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{childID},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}

	// Check that archived child is not_running with reason "archived"
	report, ok := output.Reports[childID]
	if !ok {
		t.Fatal("Missing report for archived child")
	}
	if report.Status != "not_running" {
		t.Errorf("Expected status 'not_running', got '%s'", report.Status)
	}
	if report.Reason != "archived" {
		t.Errorf("Expected reason 'archived' for archived child, got '%s'", report.Reason)
	}

	// Check warnings mention "archived"
	if len(output.Warnings) == 0 {
		t.Fatal("Expected warnings about archived child")
	}
	found := false
	for _, w := range output.Warnings {
		if contains(w, "archived") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected a warning mentioning 'archived', got: %v", output.Warnings)
	}
}

func TestChildrenTasksWait_ChildReportsBeforeWait_SameTask(t *testing.T) {
	// Child reports BEFORE parent calls wait with the same task_id →
	// report is preserved and wait returns immediately.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Child reports first (no parent waiting) with task_id
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Pre-reported",
		TaskID:  "investigate-failures",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	// Parent calls wait with the SAME task_id → pre-report is preserved, returns immediately
	_, waitOutput, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "investigate-failures",
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}
	if !waitOutput.Success {
		t.Fatalf("Expected success, got error: %s", waitOutput.Error)
	}
	if waitOutput.TimedOut {
		t.Error("Should not time out — child already reported for this task")
	}

	report, ok := waitOutput.Reports[childIDs[0]]
	if !ok {
		t.Fatal("Missing report for child")
	}
	if !report.Completed {
		t.Error("Expected child report to be completed")
	}
	if report.Report == nil || report.Report.Summary != "Pre-reported" {
		t.Errorf("Expected pre-reported summary, got: %+v", report.Report)
	}
}

func TestChildrenTasksWait_ChildReportsBeforeWait_DifferentTask(t *testing.T) {
	// Child reports BEFORE parent calls wait with a DIFFERENT task_id →
	// report is cleared, parent blocks until child re-reports.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Child reports first with task_id "task-A"
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Old task report",
		TaskID:  "task-A",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	// Parent calls wait with DIFFERENT task_id "task-B" → old report is cleared
	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TaskID:         "task-B",
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give wait handler time to set up
	time.Sleep(100 * time.Millisecond)

	// Child reports again with the new task_id
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "New task report",
		TaskID:  "task-B",
	})
	if err != nil {
		t.Fatalf("Second report failed: %v", err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.Success {
			t.Fatalf("Expected success, got error: %s", result.output.Error)
		}
		if result.output.TimedOut {
			t.Error("Should not time out — child reported during wait")
		}

		report, ok := result.output.Reports[childIDs[0]]
		if !ok {
			t.Fatal("Missing report for child")
		}
		if !report.Completed {
			t.Error("Expected child report to be completed")
		}
		if report.Report == nil || report.Report.Summary != "New task report" {
			t.Errorf("Expected new task report, got: %+v", report.Report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestChildrenTasksWait_BothReportDuringWait(t *testing.T) {
	// 2 children both report during the active wait window → parent unblocks when both are done
	srv, _, parentID, childIDs := setupParentChildSessions(t, 2)
	ctx := context.Background()

	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give wait handler time to set up
	time.Sleep(100 * time.Millisecond)

	// Both children report during the wait
	_, _, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Child 0 done",
	})
	if err != nil {
		t.Fatalf("Child[0] report failed: %v", err)
	}

	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[1],
		Status:  "completed",
		Summary: "Child 1 done",
	})
	if err != nil {
		t.Fatalf("Child[1] report failed: %v", err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.Success {
			t.Fatalf("Expected success, got error: %s", result.output.Error)
		}
		if result.output.TimedOut {
			t.Error("Should not time out")
		}

		// Both reports should be present
		report0 := result.output.Reports[childIDs[0]]
		if !report0.Completed || report0.Report == nil || report0.Report.Summary != "Child 0 done" {
			t.Errorf("Expected report for child[0], got: completed=%v report=%+v", report0.Completed, report0.Report)
		}
		report1 := result.output.Reports[childIDs[1]]
		if !report1.Completed || report1.Report == nil || report1.Report.Summary != "Child 1 done" {
			t.Errorf("Expected report for child[1], got: completed=%v report=%+v", report1.Completed, report1.Report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestChildrenTasksWait_ReportsPreservedSameTask(t *testing.T) {
	// Same task_id across waits → reports are preserved.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// First wait with task_id: times out (child hasn't reported yet)
	_, waitOutput1, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "investigate",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("First wait returned error: %v", err)
	}
	if !waitOutput1.TimedOut {
		t.Error("Expected first wait to time out")
	}

	// Child reports after first wait has returned, with same task_id
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Between waits",
		TaskID:  "investigate",
	})
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	// Second wait with SAME task_id → report is preserved, returns immediately
	_, waitOutput2, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "investigate",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("Second wait returned error: %v", err)
	}
	if waitOutput2.TimedOut {
		t.Error("Should not time out — report was preserved from same task")
	}

	report := waitOutput2.Reports[childIDs[0]]
	if !report.Completed {
		t.Error("Expected child report to be completed (preserved across same-task waits)")
	}
}

func TestChildrenTasksWait_ReportsClearedOnNewTask(t *testing.T) {
	// Different task_id across waits → reports are cleared.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// First wait with task_id "task-A": times out
	_, waitOutput1, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-A",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("First wait returned error: %v", err)
	}
	if !waitOutput1.TimedOut {
		t.Error("Expected first wait to time out")
	}

	// Child reports after first wait with task_id "task-A"
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Task A result",
		TaskID:  "task-A",
	})
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	// Second wait with DIFFERENT task_id "task-B" → old report is cleared
	_, waitOutput2, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-B",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("Second wait returned error: %v", err)
	}
	if !waitOutput2.TimedOut {
		t.Error("Expected second wait to time out — different task clears old reports")
	}

	report := waitOutput2.Reports[childIDs[0]]
	if report.Completed {
		t.Error("Expected child report to be pending (cleared by new task)")
	}
}

func TestUnregisterSession_CleansUpCollector(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Child reports (creates collector for parent)
	_, _, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	// Verify collector exists
	srv.childReportCollectorsMu.Lock()
	_, exists := srv.childReportCollectors[parentID]
	srv.childReportCollectorsMu.Unlock()
	if !exists {
		t.Fatal("Expected collector to exist after child report")
	}

	// Unregister parent session
	srv.UnregisterSession(parentID)

	// Verify collector was cleaned up
	srv.childReportCollectorsMu.Lock()
	_, exists = srv.childReportCollectors[parentID]
	srv.childReportCollectorsMu.Unlock()
	if exists {
		t.Error("Expected collector to be cleaned up after unregistering parent session")
	}
}

func TestChildrenTasksWait_EmptyPromptSkipsSending(t *testing.T) {
	// When prompt is empty, no message should be enqueued (wait-only mode).
	srv, store, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Start wait with empty prompt and short timeout
	go func() {
		srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			Prompt:         "", // empty = wait-only
			TimeoutSeconds: 1,
		})
	}()

	// Give time for the wait to set up
	time.Sleep(200 * time.Millisecond)

	// Check that NO prompt was enqueued to the child
	queue := store.Queue(childIDs[0])
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Failed to list queue: %v", err)
	}

	if len(messages) != 0 {
		t.Errorf("Expected 0 queued messages (wait-only mode), got %d", len(messages))
	}
}

func TestChildrenTasksWait_DeduplicatesQueuedPrompts(t *testing.T) {
	// When a child already has a pending message from the parent,
	// a second wait call should NOT enqueue another message.
	srv, store, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// First wait: enqueues a prompt, then times out
	_, waitOutput1, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		Prompt:         "Report your progress please.",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("First wait returned error: %v", err)
	}
	if !waitOutput1.TimedOut {
		t.Error("Expected first wait to time out")
	}

	// Verify first message was enqueued
	queue := store.Queue(childIDs[0])
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Failed to list queue: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("Expected 1 queued message after first wait, got %d", len(messages))
	}

	// Second wait: should NOT enqueue because the first message is still pending
	_, waitOutput2, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		Prompt:         "Report your progress please.",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("Second wait returned error: %v", err)
	}
	if !waitOutput2.TimedOut {
		t.Error("Expected second wait to time out")
	}

	// Verify still only 1 message in queue (dedup prevented the second)
	messages, err = queue.List()
	if err != nil {
		t.Fatalf("Failed to list queue after second wait: %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("Expected 1 queued message (dedup should prevent duplicate), got %d", len(messages))
	}
}

// =============================================================================
// mitto_conversation_delete tests
// =============================================================================

func TestConversationDelete_Success(t *testing.T) {
	srv, store, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Delete child conversation
	_, output, err := srv.handleDeleteConversation(ctx, nil, DeleteConversationInput{
		SelfID:         parentID,
		ConversationID: childIDs[0],
	})
	if err != nil {
		t.Fatalf("handleDeleteConversation returned error: %v", err)
	}
	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}
	if output.ConversationID != childIDs[0] {
		t.Errorf("Expected conversation ID %s, got %s", childIDs[0], output.ConversationID)
	}

	// Verify the child is archived
	meta, err := store.GetMetadata(childIDs[0])
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if !meta.Archived {
		t.Error("Expected child to be archived after delete")
	}
}

func TestConversationDelete_NotParent(t *testing.T) {
	srv, store, parentID, _ := setupParentChildSessions(t, 0)
	ctx := context.Background()

	// Create an independent session (no parent)
	otherID := session.GenerateSessionID()
	otherMeta := session.Metadata{
		SessionID:  otherID,
		Name:       "Other Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(otherMeta); err != nil {
		t.Fatalf("Failed to create other session: %v", err)
	}

	// Parent tries to delete a non-child conversation → permission denied
	_, output, err := srv.handleDeleteConversation(ctx, nil, DeleteConversationInput{
		SelfID:         parentID,
		ConversationID: otherID,
	})
	if err != nil {
		t.Fatalf("handleDeleteConversation returned error: %v", err)
	}
	if output.Success {
		t.Error("Expected failure when deleting non-child conversation")
	}
	if output.Error != "permission denied: can only delete your own child conversations" {
		t.Errorf("Expected permission denied error, got: %s", output.Error)
	}
}

func TestConversationDelete_NonExistent(t *testing.T) {
	srv, _, parentID, _ := setupParentChildSessions(t, 0)
	ctx := context.Background()

	_, output, err := srv.handleDeleteConversation(ctx, nil, DeleteConversationInput{
		SelfID:         parentID,
		ConversationID: "non-existent-session",
	})
	if err != nil {
		t.Fatalf("handleDeleteConversation returned error: %v", err)
	}
	if output.Success {
		t.Error("Expected failure for non-existent conversation")
	}
	if !strings.Contains(output.Error, "conversation not found") {
		t.Errorf("Expected 'conversation not found' error, got: %s", output.Error)
	}
}

func TestListConversationsFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create sessions with different attributes
	sessions := []session.Metadata{
		{
			SessionID:  "session-workspace-a-1",
			Name:       "WA Session 1",
			ACPServer:  "auggie",
			WorkingDir: "/workspace/a",
		},
		{
			SessionID:  "session-workspace-a-2",
			Name:       "WA Session 2",
			ACPServer:  "claude-code",
			WorkingDir: "/workspace/a",
		},
		{
			SessionID:  "session-workspace-b-1",
			Name:       "WB Session 1",
			ACPServer:  "auggie",
			WorkingDir: "/workspace/b",
		},
		{
			SessionID:  "session-archived",
			Name:       "Archived Session",
			ACPServer:  "auggie",
			WorkingDir: "/workspace/a",
			Archived:   true,
		},
	}
	for _, meta := range sessions {
		if err := store.Create(meta); err != nil {
			t.Fatalf("Failed to create session %s: %v", meta.SessionID, err)
		}
	}

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()
	handler := srv.createListConversationsHandler(nil)

	t.Run("no filters returns all", func(t *testing.T) {
		_, output, err := handler(ctx, nil, ListConversationsInput{})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 4 {
			t.Errorf("expected 4 conversations, got %d", len(output.Conversations))
		}
	})

	t.Run("filter by working_dir", func(t *testing.T) {
		wd := "/workspace/a"
		_, output, err := handler(ctx, nil, ListConversationsInput{WorkingDir: &wd})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 3 {
			t.Errorf("expected 3 conversations in /workspace/a, got %d", len(output.Conversations))
		}
		for _, c := range output.Conversations {
			if c.WorkingDir != "/workspace/a" {
				t.Errorf("expected working_dir /workspace/a, got %s", c.WorkingDir)
			}
		}
	})

	t.Run("filter by archived=false", func(t *testing.T) {
		archived := false
		_, output, err := handler(ctx, nil, ListConversationsInput{Archived: &archived})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 3 {
			t.Errorf("expected 3 non-archived conversations, got %d", len(output.Conversations))
		}
		for _, c := range output.Conversations {
			if c.Archived {
				t.Errorf("expected non-archived, got archived: %s", c.SessionID)
			}
		}
	})

	t.Run("filter by archived=true", func(t *testing.T) {
		archived := true
		_, output, err := handler(ctx, nil, ListConversationsInput{Archived: &archived})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 1 {
			t.Errorf("expected 1 archived conversation, got %d", len(output.Conversations))
		}
		if len(output.Conversations) > 0 && output.Conversations[0].SessionID != "session-archived" {
			t.Errorf("expected session-archived, got %s", output.Conversations[0].SessionID)
		}
	})

	t.Run("filter by acp_server", func(t *testing.T) {
		acp := "claude-code"
		_, output, err := handler(ctx, nil, ListConversationsInput{ACPServer: &acp})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 1 {
			t.Errorf("expected 1 claude-code conversation, got %d", len(output.Conversations))
		}
		if len(output.Conversations) > 0 && output.Conversations[0].ACPServer != "claude-code" {
			t.Errorf("expected claude-code, got %s", output.Conversations[0].ACPServer)
		}
	})

	t.Run("exclude_self", func(t *testing.T) {
		exclude := "session-workspace-a-1"
		_, output, err := handler(ctx, nil, ListConversationsInput{ExcludeSelf: &exclude})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 3 {
			t.Errorf("expected 3 conversations (excluding self), got %d", len(output.Conversations))
		}
		for _, c := range output.Conversations {
			if c.SessionID == "session-workspace-a-1" {
				t.Error("excluded session should not appear in results")
			}
		}
	})

	t.Run("combined filters: working_dir + archived=false + exclude_self", func(t *testing.T) {
		wd := "/workspace/a"
		archived := false
		exclude := "session-workspace-a-1"
		_, output, err := handler(ctx, nil, ListConversationsInput{
			WorkingDir:  &wd,
			Archived:    &archived,
			ExcludeSelf: &exclude,
		})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		// /workspace/a has 3 sessions, minus 1 archived, minus 1 excluded = 1
		if len(output.Conversations) != 1 {
			t.Errorf("expected 1 conversation, got %d", len(output.Conversations))
		}
		if len(output.Conversations) > 0 && output.Conversations[0].SessionID != "session-workspace-a-2" {
			t.Errorf("expected session-workspace-a-2, got %s", output.Conversations[0].SessionID)
		}
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		wd := "/workspace/nonexistent"
		_, output, err := handler(ctx, nil, ListConversationsInput{WorkingDir: &wd})
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if len(output.Conversations) != 0 {
			t.Errorf("expected 0 conversations, got %d", len(output.Conversations))
		}
	})
}

func TestConversationDelete_ChildOfDifferentParent(t *testing.T) {
	// Create parent1 with a child, then try to delete child from parent2
	srv, store, _, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Create a second parent session
	parent2ID := session.GenerateSessionID()
	parent2Meta := session.Metadata{
		SessionID:  parent2ID,
		Name:       "Parent 2",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(parent2Meta); err != nil {
		t.Fatalf("Failed to create parent2 session: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parent2ID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent2: %v", err)
	}

	// Parent2 tries to delete Parent1's child → permission denied
	_, output, err := srv.handleDeleteConversation(ctx, nil, DeleteConversationInput{
		SelfID:         parent2ID,
		ConversationID: childIDs[0],
	})
	if err != nil {
		t.Fatalf("handleDeleteConversation returned error: %v", err)
	}
	if output.Success {
		t.Error("Expected failure when different parent tries to delete child")
	}
	if output.Error != "permission denied: can only delete your own child conversations" {
		t.Errorf("Expected permission denied error, got: %s", output.Error)
	}
}

// =============================================================================
// Conversation Wait Tests
// =============================================================================

// mockBackgroundSessionForWait implements BackgroundSession for testing the wait tool.
type mockBackgroundSessionForWait struct {
	prompting     atomic.Bool
	waitCompleted chan struct{} // close to simulate prompt completion
}

func newMockBackgroundSessionForWait(prompting bool) *mockBackgroundSessionForWait {
	m := &mockBackgroundSessionForWait{
		waitCompleted: make(chan struct{}),
	}
	m.prompting.Store(prompting)
	return m
}

func (m *mockBackgroundSessionForWait) IsPrompting() bool             { return m.prompting.Load() }
func (m *mockBackgroundSessionForWait) GetEventCount() int            { return 0 }
func (m *mockBackgroundSessionForWait) GetMaxAssignedSeq() int64      { return 0 }
func (m *mockBackgroundSessionForWait) TryProcessQueuedMessage() bool { return false }
func (m *mockBackgroundSessionForWait) WaitForResponseComplete(timeout time.Duration) bool {
	if !m.prompting.Load() {
		return true
	}
	select {
	case <-m.waitCompleted:
		return true
	case <-time.After(timeout):
		return false
	}
}

// mockSessionManagerForWait implements SessionManager for testing the wait tool.
type mockSessionManagerForWait struct {
	sessions map[string]BackgroundSession
}

func (m *mockSessionManagerForWait) GetSession(sessionID string) BackgroundSession {
	bs, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	return bs
}

func (m *mockSessionManagerForWait) ListRunningSessions() []string { return nil }
func (m *mockSessionManagerForWait) CloseSessionGracefully(string, string, time.Duration) bool {
	return true
}
func (m *mockSessionManagerForWait) CloseSession(string, string) {}
func (m *mockSessionManagerForWait) ResumeSession(string, string, string) (BackgroundSession, error) {
	return nil, nil
}
func (m *mockSessionManagerForWait) GetWorkspacesForFolder(string) []config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForWait) BroadcastSessionCreated(string, string, string, string, string) {}
func (m *mockSessionManagerForWait) BroadcastSessionArchived(string, bool)                          {}

// setupServerForWait creates a server with a SessionManager mock for wait tool tests.
func setupServerForWait(t *testing.T, targetID string, targetBS BackgroundSession) (*Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Create caller session
	callerID := session.GenerateSessionID()
	callerMeta := session.Metadata{
		SessionID:  callerID,
		Name:       "Caller Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(callerMeta); err != nil {
		t.Fatalf("Failed to create caller session: %v", err)
	}

	// Create target session
	targetMeta := session.Metadata{
		SessionID:  targetID,
		Name:       "Target Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(targetMeta); err != nil {
		t.Fatalf("Failed to create target session: %v", err)
	}

	// Create mock session manager
	sm := &mockSessionManagerForWait{
		sessions: map[string]BackgroundSession{
			targetID: targetBS,
		},
	}

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{
			Store:          store,
			SessionManager: sm,
		},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register both sessions
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(callerID, nil, logger); err != nil {
		t.Fatalf("Failed to register caller session: %v", err)
	}
	if err := srv.RegisterSession(targetID, nil, logger); err != nil {
		t.Fatalf("Failed to register target session: %v", err)
	}

	return srv, callerID
}

func TestConversationWait_AgentResponded_NotPrompting(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false) // not prompting
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         callerID,
		ConversationID: targetID,
		What:           "agent_responded",
	})
	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error != "" {
		t.Fatalf("Expected no error, got: %s", output.Error)
	}
	if !output.Success {
		t.Error("Expected success")
	}
	if output.TimedOut {
		t.Error("Expected no timeout")
	}
	if output.What != "agent_responded" {
		t.Errorf("Expected what='agent_responded', got %q", output.What)
	}
}

func TestConversationWait_AgentResponded_Completes(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(true) // currently prompting
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	type waitResult struct {
		output ConversationWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
			SelfID:         callerID,
			ConversationID: targetID,
			What:           "agent_responded",
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Simulate the agent finishing after a short delay
	time.Sleep(100 * time.Millisecond)
	mockBS.prompting.Store(false)
	close(mockBS.waitCompleted)

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleConversationWait returned error: %v", result.err)
		}
		if result.output.Error != "" {
			t.Fatalf("Expected no error, got: %s", result.output.Error)
		}
		if !result.output.Success {
			t.Error("Expected success")
		}
		if result.output.TimedOut {
			t.Error("Expected no timeout")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Test timed out waiting for handleConversationWait")
	}
}

func TestConversationWait_AgentResponded_Timeout(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(true) // prompting, never completes
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	start := time.Now()
	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         callerID,
		ConversationID: targetID,
		What:           "agent_responded",
		TimeoutSeconds: 1, // very short timeout
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error != "" {
		t.Fatalf("Expected no error, got: %s", output.Error)
	}
	if !output.Success {
		t.Error("Expected success (timed_out is still a successful return)")
	}
	if !output.TimedOut {
		t.Error("Expected timed_out=true")
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("Expected to wait ~1s, but only waited %v", elapsed)
	}
}

func TestConversationWait_InvalidWhat(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false)
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         callerID,
		ConversationID: targetID,
		What:           "unknown_condition",
	})
	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("Expected error for unknown condition")
	}
	if !strings.Contains(output.Error, "unsupported wait condition") {
		t.Errorf("Expected 'unsupported wait condition' error, got: %s", output.Error)
	}
}

func TestConversationWait_SessionNotRunning(t *testing.T) {
	targetID := session.GenerateSessionID()
	// Create server with no mock for the target (not in SessionManager)
	mockBS := newMockBackgroundSessionForWait(false)
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	// Unregister the target from SessionManager by using a different ID
	nonExistentID := session.GenerateSessionID()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         callerID,
		ConversationID: nonExistentID,
		What:           "agent_responded",
	})
	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("Expected error for non-running session")
	}
	if !strings.Contains(output.Error, "not running") {
		t.Errorf("Expected 'not running' error, got: %s", output.Error)
	}
}

func TestConversationWait_MissingSelfID(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false)
	srv, _ := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         "",
		ConversationID: targetID,
		What:           "agent_responded",
	})
	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("Expected error for missing self_id")
	}
	if !strings.Contains(output.Error, "self_id is required") {
		t.Errorf("Expected 'self_id is required' error, got: %s", output.Error)
	}
}

func TestConversationWait_MissingWhat(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false)
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:         callerID,
		ConversationID: targetID,
		What:           "",
	})
	if err != nil {
		t.Fatalf("handleConversationWait returned error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("Expected error for missing what")
	}
	if !strings.Contains(output.Error, "what is required") {
		t.Errorf("Expected 'what is required' error, got: %s", output.Error)
	}
}

// =============================================================================
// Pending Request FIFO Queue Tests
// =============================================================================

func TestPendingRequestFIFO(t *testing.T) {
	// Create a minimal server with FIFO queue
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register three pending requests with the same key ("init")
	srv.RegisterPendingRequest("init", "session-A")
	srv.RegisterPendingRequest("init", "session-B")
	srv.RegisterPendingRequest("init", "session-C")

	// WaitForPendingRequest should return them in FIFO order
	resultA := srv.WaitForPendingRequest("init")
	if resultA != "session-A" {
		t.Errorf("Expected session-A first (FIFO), got: %s", resultA)
	}

	resultB := srv.WaitForPendingRequest("init")
	if resultB != "session-B" {
		t.Errorf("Expected session-B second (FIFO), got: %s", resultB)
	}

	resultC := srv.WaitForPendingRequest("init")
	if resultC != "session-C" {
		t.Errorf("Expected session-C third (FIFO), got: %s", resultC)
	}

	// Queue should now be empty — next call should return "" (timeout)
	resultEmpty := srv.WaitForPendingRequest("init")
	if resultEmpty != "" {
		t.Errorf("Expected empty string after queue drained, got: %s", resultEmpty)
	}
}

func TestPendingRequestSingleEntry(t *testing.T) {
	// Single entry should behave same as before (backward compat)
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	srv.RegisterPendingRequest("init", "my-session")

	result := srv.WaitForPendingRequest("init")
	if result != "my-session" {
		t.Errorf("Expected my-session, got: %s", result)
	}

	// Key should be fully cleaned up (deleted, not empty slice)
	srv.pendingRequestsMu.Lock()
	queue, exists := srv.pendingRequests["init"]
	srv.pendingRequestsMu.Unlock()
	if exists {
		t.Errorf("Expected key 'init' to be deleted after last entry consumed, but found queue of len %d", len(queue))
	}
}

func TestPendingRequestCleanupExpired(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Manually insert expired entries
	srv.pendingRequestsMu.Lock()
	srv.pendingRequests["init"] = []*pendingRequest{
		{sessionID: "expired-1", registeredAt: time.Now().Add(-2 * time.Minute)},
		{sessionID: "fresh", registeredAt: time.Now()},
		{sessionID: "expired-2", registeredAt: time.Now().Add(-3 * time.Minute)},
	}
	srv.pendingRequestsMu.Unlock()

	// Trigger cleanup
	srv.pendingRequestsMu.Lock()
	srv.cleanupExpiredPendingRequestsLocked()
	srv.pendingRequestsMu.Unlock()

	// Only "fresh" should remain
	srv.pendingRequestsMu.Lock()
	queue := srv.pendingRequests["init"]
	srv.pendingRequestsMu.Unlock()

	if len(queue) != 1 {
		t.Fatalf("Expected 1 entry after cleanup, got %d", len(queue))
	}
	if queue[0].sessionID != "fresh" {
		t.Errorf("Expected 'fresh' to survive cleanup, got: %s", queue[0].sessionID)
	}
}

// =============================================================================
// MCP Session Cache Tests
// =============================================================================

func TestMCPSessionCache(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Cache a mapping
	srv.cacheMCPSession("mcp-session-abc", "mitto-session-123")

	// Lookup should find it
	result := srv.lookupMCPSession("mcp-session-abc")
	if result != "mitto-session-123" {
		t.Errorf("Expected mitto-session-123, got: %s", result)
	}

	// Unknown key should return empty
	result = srv.lookupMCPSession("unknown-key")
	if result != "" {
		t.Errorf("Expected empty for unknown key, got: %s", result)
	}
}

func TestMCPSessionCacheCleanupOnUnregister(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Cache multiple MCP sessions pointing to the same Mitto session
	srv.cacheMCPSession("mcp-1", "session-to-remove")
	srv.cacheMCPSession("mcp-2", "session-to-remove")
	srv.cacheMCPSession("mcp-3", "session-to-keep")

	// Register and then unregister the session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	sessionMeta := session.Metadata{
		SessionID:  "session-to-remove",
		Name:       "Test",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(sessionMeta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	if err := srv.RegisterSession("session-to-remove", nil, logger); err != nil {
		t.Fatalf("Failed to register session: %v", err)
	}
	srv.UnregisterSession("session-to-remove")

	// mcp-1 and mcp-2 should be cleaned up
	if result := srv.lookupMCPSession("mcp-1"); result != "" {
		t.Errorf("Expected mcp-1 to be cleaned up, got: %s", result)
	}
	if result := srv.lookupMCPSession("mcp-2"); result != "" {
		t.Errorf("Expected mcp-2 to be cleaned up, got: %s", result)
	}

	// mcp-3 should still be there (different session)
	if result := srv.lookupMCPSession("mcp-3"); result != "session-to-keep" {
		t.Errorf("Expected session-to-keep to remain, got: %s", result)
	}
}

func TestResolveSelfIDWithMCP_DirectLookup(t *testing.T) {
	// Phase 1: Direct session ID lookup should work without MCP session
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register a session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	sessionMeta := session.Metadata{
		SessionID:  "known-session",
		Name:       "Test",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(sessionMeta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	if err := srv.RegisterSession("known-session", nil, logger); err != nil {
		t.Fatalf("Failed to register session: %v", err)
	}

	// Direct lookup (Phase 1) should resolve even with nil req
	result := srv.resolveSelfIDWithMCP("known-session", nil)
	if result != "known-session" {
		t.Errorf("Expected known-session via Phase 1, got: %s", result)
	}
}

func TestResolveSelfIDWithMCP_Phase3CacheFallback(t *testing.T) {
	// Phase 3: When Phase 1+2 fail, MCP session cache should resolve
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Pre-populate the MCP session cache (simulates a prior get_current success)
	srv.cacheMCPSession("mcp-protocol-session-xyz", "resolved-mitto-session")

	// resolveSelfIDWithMCP with a wrong self_id and nil req should fail
	result := srv.resolveSelfIDWithMCP("wrong-id", nil)
	if result != "" {
		t.Errorf("Expected empty with wrong ID and nil req, got: %s", result)
	}

	// We can't easily test Phase 3 with a real mcp.CallToolRequest because
	// creating a ServerSession requires the full MCP SDK. Instead, verify
	// the cache lookup works directly.
	cached := srv.lookupMCPSession("mcp-protocol-session-xyz")
	if cached != "resolved-mitto-session" {
		t.Errorf("Expected resolved-mitto-session from cache, got: %s", cached)
	}
}

// =============================================================================
// childReportCollector Unit Tests
// =============================================================================

func TestChildReportCollector_GetPendingAndReported(t *testing.T) {
	collector := &childReportCollector{
		parentSessionID: "parent-1",
		reports:         make(map[string]*childReport),
	}

	// Start a wait with 3 children
	childIDs := []string{"child-a", "child-b", "child-c"}
	collector.startWait("test-task", childIDs)

	// Report from only one child
	collector.addReport("child-b", "test-task", []byte(`{"status":"completed"}`))

	pending, reported := collector.getPendingAndReported()

	if len(reported) != 1 {
		t.Errorf("Expected 1 reported child, got %d", len(reported))
	}
	if len(reported) == 1 && reported[0] != "child-b" {
		t.Errorf("Expected reported child to be 'child-b', got '%s'", reported[0])
	}
	if len(pending) != 2 {
		t.Errorf("Expected 2 pending children, got %d", len(pending))
	}
	// Verify pending contains child-a and child-c (order may vary)
	pendingSet := make(map[string]bool)
	for _, id := range pending {
		pendingSet[id] = true
	}
	if !pendingSet["child-a"] || !pendingSet["child-c"] {
		t.Errorf("Expected pending to contain child-a and child-c, got %v", pending)
	}
}

func TestChildReportCollector_IsWaiting(t *testing.T) {
	collector := &childReportCollector{
		parentSessionID: "parent-1",
		reports:         make(map[string]*childReport),
	}

	// Initially not waiting
	if collector.isWaiting() {
		t.Error("Expected isWaiting() to be false initially")
	}

	// Start a wait
	collector.startWait("test-task", []string{"child-1"})
	if !collector.isWaiting() {
		t.Error("Expected isWaiting() to be true during active wait")
	}

	// Clear the wait
	collector.clearWait()
	if collector.isWaiting() {
		t.Error("Expected isWaiting() to be false after clearWait")
	}
}

// =============================================================================
// Orphaned Report Detection Tests
// =============================================================================

func TestChildrenTasksReport_OrphanedParent(t *testing.T) {
	// Set up parent + child, then unregister parent before child reports.
	// The child should still be able to report without error — the report is stored
	// but the parent is no longer registered (orphaned report).
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Unregister the parent session
	srv.UnregisterSession(parentID)

	// Child reports — should succeed (no panic, no error)
	_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Done, but parent is gone",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}
	if output.ParentSessionID != parentID {
		t.Errorf("Expected parent_session_id '%s', got '%s'", parentID, output.ParentSessionID)
	}
}

// =============================================================================
// Session Health Diagnostic Reason Tests
// =============================================================================

// mockSessionManagerForChildren implements SessionManager for testing children wait diagnostics.
type mockSessionManagerForChildren struct {
	sessions map[string]BackgroundSession
}

func (m *mockSessionManagerForChildren) GetSession(sessionID string) BackgroundSession {
	bs, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	return bs
}

func (m *mockSessionManagerForChildren) ListRunningSessions() []string { return nil }
func (m *mockSessionManagerForChildren) CloseSessionGracefully(string, string, time.Duration) bool {
	return true
}
func (m *mockSessionManagerForChildren) CloseSession(string, string) {}
func (m *mockSessionManagerForChildren) ResumeSession(string, string, string) (BackgroundSession, error) {
	return nil, nil
}
func (m *mockSessionManagerForChildren) GetWorkspacesForFolder(string) []config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForChildren) BroadcastSessionCreated(string, string, string, string, string) {
}
func (m *mockSessionManagerForChildren) BroadcastSessionArchived(string, bool) {}

func TestChildrenTasksWait_TimeoutWithStillProcessing(t *testing.T) {
	// Set up parent + child, child is prompting (still processing).
	// On timeout, the child's reason should be "still_processing".
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Busy Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	// Create mock that reports child as prompting
	mockBS := newMockBackgroundSessionForWait(true) // IsPrompting() == true
	sm := &mockSessionManagerForChildren{
		sessions: map[string]BackgroundSession{
			childID: mockBS,
		},
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent: %v", err)
	}
	if err := srv.RegisterSession(childID, nil, logger); err != nil {
		t.Fatalf("Failed to register child: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{childID},
		TimeoutSeconds: 1, // Short timeout — child never reports
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}
	if !output.Success {
		t.Fatalf("Expected success (even on timeout), got error: %s", output.Error)
	}
	if !output.TimedOut {
		t.Error("Expected timeout")
	}

	report, ok := output.Reports[childID]
	if !ok {
		t.Fatal("Missing report for child")
	}
	if report.Completed {
		t.Error("Expected child to NOT have completed")
	}
	if report.Reason != "still_processing" {
		t.Errorf("Expected reason 'still_processing', got '%s'", report.Reason)
	}
}

func TestChildrenTasksWait_TimeoutWithSessionUnregistered(t *testing.T) {
	// Set up parent + child, start a wait, then unregister the child mid-wait.
	// On timeout, the child's reason should be "session_unregistered".
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TimeoutSeconds: 2,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	// Give time for coordination setup
	time.Sleep(100 * time.Millisecond)

	// Unregister child session mid-wait (simulates crash)
	srv.UnregisterSession(childIDs[0])

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if !result.output.TimedOut {
			t.Error("Expected timeout")
		}
		report := result.output.Reports[childIDs[0]]
		if report.Completed {
			t.Error("Expected child to NOT have completed")
		}
		if report.Reason != "session_unregistered" {
			t.Errorf("Expected reason 'session_unregistered', got '%s'", report.Reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for handleChildrenTasksWait to return")
	}
}

// =============================================================================
// Auto-Resume Stored Sessions Tests
// =============================================================================

// mockBackgroundSessionForAutoResume is a minimal BackgroundSession for auto-resume tests.
type mockBackgroundSessionForAutoResume struct {
	tryProcessCalled atomic.Bool
}

func (m *mockBackgroundSessionForAutoResume) IsPrompting() bool                          { return false }
func (m *mockBackgroundSessionForAutoResume) GetEventCount() int                         { return 0 }
func (m *mockBackgroundSessionForAutoResume) GetMaxAssignedSeq() int64                   { return 0 }
func (m *mockBackgroundSessionForAutoResume) WaitForResponseComplete(time.Duration) bool { return true }
func (m *mockBackgroundSessionForAutoResume) TryProcessQueuedMessage() bool {
	m.tryProcessCalled.Store(true)
	return false
}

// mockSessionManagerForAutoResume implements SessionManager where GetSession returns nil
// for stored sessions, and ResumeSession makes the session available.
type mockSessionManagerForAutoResume struct {
	mu           sync.Mutex
	sessions     map[string]BackgroundSession // initially empty for stored sessions
	resumeCalls  []resumeCall
	resumeErr    error             // if set, ResumeSession returns this error
	resumeResult BackgroundSession // returned by ResumeSession on success
	// onResume is called after a successful resume to allow registering the session
	// with the MCP server's internal registry (simulating the real flow).
	onResume func(sessionID string)
}

type resumeCall struct {
	sessionID   string
	sessionName string
	workingDir  string
}

func (m *mockSessionManagerForAutoResume) GetSession(sessionID string) BackgroundSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	bs, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	return bs
}

func (m *mockSessionManagerForAutoResume) ListRunningSessions() []string { return nil }
func (m *mockSessionManagerForAutoResume) CloseSessionGracefully(string, string, time.Duration) bool {
	return true
}
func (m *mockSessionManagerForAutoResume) CloseSession(string, string) {}

func (m *mockSessionManagerForAutoResume) ResumeSession(sessionID, sessionName, workingDir string) (BackgroundSession, error) {
	m.mu.Lock()
	m.resumeCalls = append(m.resumeCalls, resumeCall{sessionID, sessionName, workingDir})
	if m.resumeErr != nil {
		m.mu.Unlock()
		return nil, m.resumeErr
	}
	// Simulate resume: add the session to the map so GetSession finds it
	bs := m.resumeResult
	if bs != nil {
		m.sessions[sessionID] = bs
	}
	onResume := m.onResume
	m.mu.Unlock()
	// Call onResume outside lock to allow it to call srv.RegisterSession
	if onResume != nil {
		onResume(sessionID)
	}
	return bs, nil
}

func (m *mockSessionManagerForAutoResume) GetWorkspacesForFolder(string) []config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForAutoResume) BroadcastSessionCreated(string, string, string, string, string) {
}
func (m *mockSessionManagerForAutoResume) BroadcastSessionArchived(string, bool) {}

func TestSendPrompt_AutoResumesStoredSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Create source (parent) session
	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create target (child) session - this is "stored" (not running)
	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Stored Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	// Create mock BS that will be "resumed"
	mockBS := &mockBackgroundSessionForAutoResume{}

	// Create mock session manager: GetSession returns nil initially (stored),
	// ResumeSession succeeds and makes it available
	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{
			parentID: &mockBackgroundSessionForAutoResume{}, // parent is running
			// childID NOT in map — simulates stored session
		},
		resumeResult: mockBS,
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Register parent session
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	// Send prompt to stored child
	ctx := context.Background()
	_, output, err := srv.handleSendPromptToConversation(ctx, nil, SendPromptToConversationInput{
		SelfID:         parentID,
		ConversationID: childID,
		Prompt:         "Hello stored child",
	})
	if err != nil {
		t.Fatalf("handleSendPromptToConversation returned error: %v", err)
	}
	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}

	// Verify ResumeSession was called with correct params
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.resumeCalls) != 1 {
		t.Fatalf("Expected 1 ResumeSession call, got %d", len(sm.resumeCalls))
	}
	call := sm.resumeCalls[0]
	if call.sessionID != childID {
		t.Errorf("Expected ResumeSession for %s, got %s", childID, call.sessionID)
	}
	if call.sessionName != "Stored Child" {
		t.Errorf("Expected session name 'Stored Child', got '%s'", call.sessionName)
	}

	// Verify message was added to queue
	queueLen, _ := store.Queue(childID).Len()
	if queueLen != 1 {
		t.Errorf("Expected 1 message in queue, got %d", queueLen)
	}
}

func TestSendPrompt_DoesNotResumeArchivedSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	// Create an archived target session
	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Archived Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
		Archived:        true,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{
			parentID: &mockBackgroundSessionForAutoResume{},
		},
		resumeResult: &mockBackgroundSessionForAutoResume{},
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleSendPromptToConversation(ctx, nil, SendPromptToConversationInput{
		SelfID:         parentID,
		ConversationID: childID,
		Prompt:         "Hello archived child",
	})
	if err != nil {
		t.Fatalf("handleSendPromptToConversation returned error: %v", err)
	}
	// Prompt queuing should still succeed (message goes to queue)
	if !output.Success {
		t.Fatalf("Expected success, got error: %s", output.Error)
	}

	// Verify ResumeSession was NOT called (archived sessions should not be resumed)
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.resumeCalls) != 0 {
		t.Errorf("Expected 0 ResumeSession calls for archived session, got %d", len(sm.resumeCalls))
	}
}

func TestSendPrompt_ResumeFailureStillQueuesMessage(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Stored Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	// ResumeSession will fail
	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{
			parentID: &mockBackgroundSessionForAutoResume{},
		},
		resumeErr: fmt.Errorf("ACP process failed to start"),
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleSendPromptToConversation(ctx, nil, SendPromptToConversationInput{
		SelfID:         parentID,
		ConversationID: childID,
		Prompt:         "Hello stored child",
	})
	if err != nil {
		t.Fatalf("handleSendPromptToConversation returned error: %v", err)
	}
	// Message should still be queued even if resume fails
	if !output.Success {
		t.Fatalf("Expected success (message queued), got error: %s", output.Error)
	}

	// Verify message was added to queue despite resume failure
	queueLen, _ := store.Queue(childID).Len()
	if queueLen != 1 {
		t.Errorf("Expected 1 message in queue, got %d", queueLen)
	}
}

func TestChildrenTasksWait_AutoResumesStoredChild(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Stored Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	mockBS := &mockBackgroundSessionForAutoResume{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Mock session manager: child is initially NOT running.
	// ResumeSession makes it available and registers it with the MCP server.
	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{
			// parentID is running, childID is NOT (stored)
		},
		resumeResult: mockBS,
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Set up onResume callback to register the child with the MCP server
	// (simulates what the real SessionManager does on resume)
	sm.onResume = func(sessionID string) {
		_ = srv.RegisterSession(sessionID, nil, logger)
	}

	// Register parent session with MCP server
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}
	// Do NOT register childID — it's stored/not running

	ctx := context.Background()

	// Use a short timeout — child won't actually report
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{childID},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	// Verify ResumeSession was called for the stored child
	sm.mu.Lock()
	resumeCalls := sm.resumeCalls
	sm.mu.Unlock()

	if len(resumeCalls) != 1 {
		t.Fatalf("Expected 1 ResumeSession call, got %d", len(resumeCalls))
	}
	if resumeCalls[0].sessionID != childID {
		t.Errorf("Expected ResumeSession for %s, got %s", childID, resumeCalls[0].sessionID)
	}

	// The child should be treated as running (not in notRunningChildren)
	// Since it times out without a report, verify it was waited on (not skipped)
	if !output.TimedOut {
		t.Error("Expected timeout (child didn't report), but got success — this is fine if child reported")
	}
}

func TestChildrenTasksWait_DoesNotResumeArchivedChild(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanSendPrompt: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Failed to create parent session: %v", err)
	}

	childID := session.GenerateSessionID()
	childMeta := session.Metadata{
		SessionID:       childID,
		Name:            "Archived Child",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		ParentSessionID: parentID,
		Archived:        true,
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Failed to create child session: %v", err)
	}

	sm := &mockSessionManagerForAutoResume{
		sessions:     map[string]BackgroundSession{},
		resumeResult: &mockBackgroundSessionForAutoResume{},
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("Failed to register parent session: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   []string{childID},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}

	// Verify ResumeSession was NOT called for the archived child
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.resumeCalls) != 0 {
		t.Errorf("Expected 0 ResumeSession calls for archived child, got %d", len(sm.resumeCalls))
	}

	// The child should be in the not_running state
	if output.Reports == nil {
		t.Fatal("Expected reports in output")
	}
	report, exists := output.Reports[childID]
	if !exists {
		t.Fatal("Expected report for child in output")
	}
	if report.Status != "not_running" {
		t.Errorf("Expected status 'not_running' for archived child, got '%s'", report.Status)
	}
}
