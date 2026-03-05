package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
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
	for i, childID := range childIDs {
		report := json.RawMessage([]byte(`{"status": "completed", "child_index": ` + string(rune('0'+i)) + `}`))
		_, output, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
			SelfID: childID,
			Report: report,
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
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"status": "done"}`),
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
		// Second child should NOT have reported
		report1 := result.output.Reports[childIDs[1]]
		if report1.Completed {
			t.Error("Expected second child report to NOT be completed")
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
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"status": "done"}`),
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

	// Now parent calls wait — startWait clears previous reports, so
	// the child needs to re-report during the wait window. Without a
	// concurrent reporter, the wait should time out.
	_, waitOutput, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksWait returned error: %v", err)
	}
	if !waitOutput.TimedOut {
		t.Error("Expected timeout — startWait clears previous reports")
	}

	report, ok := waitOutput.Reports[childIDs[0]]
	if !ok {
		t.Fatal("Missing report for child")
	}
	if report.Completed {
		t.Error("Expected child report to be pending (previous report was cleared)")
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
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"attempt": 1}`),
	})
	if err != nil {
		t.Fatalf("First report failed: %v", err)
	}

	_, output2, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"attempt": 2}`),
	})
	if err != nil {
		t.Fatalf("Second report failed: %v", err)
	}
	if !output2.Success {
		t.Errorf("Expected success on duplicate report, got error: %s", output2.Error)
	}

	// Now report from child[1] to complete
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[1],
		Report: json.RawMessage(`{"status": "done"}`),
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
		if string(report.Report) != `{"attempt": 2}` {
			t.Errorf("Expected second report, got: %s", string(report.Report))
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
		SelfID: sessionID,
		Report: json.RawMessage(`{"status": "done"}`),
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

	// Check that the child is reported as not_running
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
		SelfID: runningChildID,
		Report: json.RawMessage(`{"status": "done"}`),
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

		// Closed child should be marked as not_running
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

	// Check that archived child is not_running
	report, ok := output.Reports[childID]
	if !ok {
		t.Fatal("Missing report for archived child")
	}
	if report.Status != "not_running" {
		t.Errorf("Expected status 'not_running', got '%s'", report.Status)
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

func TestChildrenTasksWait_ChildReportsBeforeWait(t *testing.T) {
	// Child reports BEFORE parent calls wait → startWait clears previous reports,
	// so parent blocks until child reports again during the wait window.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Child reports first (no parent waiting) — this report will be discarded by startWait
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"pre_reported": true}`),
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	// Parent calls wait — clears the early report, then child re-reports during wait
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

	// Child reports again during the active wait
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"fresh_report": true}`),
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
		if string(report.Report) != `{"fresh_report": true}` {
			t.Errorf("Expected fresh report (not the pre-wait one), got: %s", string(report.Report))
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
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"child": 0}`),
	})
	if err != nil {
		t.Fatalf("Child[0] report failed: %v", err)
	}

	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[1],
		Report: json.RawMessage(`{"child": 1}`),
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
		if !report0.Completed || string(report0.Report) != `{"child": 0}` {
			t.Errorf("Expected report for child[0], got: completed=%v report=%s", report0.Completed, string(report0.Report))
		}
		report1 := result.output.Reports[childIDs[1]]
		if !report1.Completed || string(report1.Report) != `{"child": 1}` {
			t.Errorf("Expected report for child[1], got: completed=%v report=%s", report1.Completed, string(report1.Report))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestChildrenTasksWait_ReportsClearedAcrossWaits(t *testing.T) {
	// Each wait call clears previous reports. Child reports between waits are discarded.
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// First wait: times out (child hasn't reported yet)
	_, waitOutput1, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("First wait returned error: %v", err)
	}
	if !waitOutput1.TimedOut {
		t.Error("Expected first wait to time out")
	}

	// Child reports after first wait has returned
	_, _, err = srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"between_waits": true}`),
	})
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	// Second wait: clears the report from between waits, so it should also time out
	_, waitOutput2, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("Second wait returned error: %v", err)
	}
	if !waitOutput2.TimedOut {
		t.Error("Expected second wait to time out — startWait clears reports from between cycles")
	}

	report := waitOutput2.Reports[childIDs[0]]
	if report.Completed {
		t.Error("Expected child report to be pending (cleared by startWait)")
	}
}

func TestUnregisterSession_CleansUpCollector(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Child reports (creates collector for parent)
	_, _, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID: childIDs[0],
		Report: json.RawMessage(`{"status": "done"}`),
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
func (m *mockSessionManagerForWait) BroadcastSessionArchived(string, bool)                        {}

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
