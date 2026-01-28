package web

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestSessionManager_NewSessionManager(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	if sm == nil {
		t.Fatal("NewSessionManager returned nil")
	}

	if sm.acpCommand != "echo test" {
		t.Errorf("acpCommand = %q, want %q", sm.acpCommand, "echo test")
	}

	if sm.acpServer != "test-server" {
		t.Errorf("acpServer = %q, want %q", sm.acpServer, "test-server")
	}

	if !sm.autoApprove {
		t.Error("autoApprove should be true")
	}

	if sm.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", sm.SessionCount())
	}
}

func TestSessionManager_SetStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	if sm.store != store {
		t.Error("SetStore did not set the store correctly")
	}
}

func TestSessionManager_GetSession_NotFound(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	bs := sm.GetSession("non-existent-session")
	if bs != nil {
		t.Error("GetSession should return nil for non-existent session")
	}
}

func TestSessionManager_ListRunningSessions_Empty(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessions := sm.ListRunningSessions()
	if len(sessions) != 0 {
		t.Errorf("ListRunningSessions = %v, want empty slice", sessions)
	}
}

func TestSessionManager_CloseSession_NonExistent(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Should not panic when closing non-existent session
	sm.CloseSession("non-existent-session", "test")

	if sm.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", sm.SessionCount())
	}
}

func TestSessionManager_CloseAll_Empty(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Should not panic when closing all with no sessions
	sm.CloseAll("test")

	if sm.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", sm.SessionCount())
	}
}

func TestSessionManager_ResumeSession_NoStore(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)
	// No store set

	_, err := sm.ResumeSession("test-session", "Test Session", "/tmp")
	if err == nil {
		t.Error("ResumeSession should fail when no store is set")
	}
}

func TestSessionManager_ResumeSession_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	// Try to resume a session that doesn't exist in the store
	_, err = sm.ResumeSession("non-existent-session", "Test Session", "/tmp")
	if err == nil {
		t.Error("ResumeSession should fail for non-existent session")
	}
}

func TestSessionManager_ResumeSession_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	// Create a session in the store first
	meta := session.Metadata{
		SessionID:  "test-session-123",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Manually add a mock background session to the manager
	mockBS := &BackgroundSession{
		persistedID: "test-session-123",
		acpID:       "acp-123",
	}
	sm.mu.Lock()
	sm.sessions["test-session-123"] = mockBS
	sm.mu.Unlock()

	// ResumeSession should return the existing session
	bs, err := sm.ResumeSession("test-session-123", "Test Session", "/tmp")
	if err != nil {
		t.Fatalf("ResumeSession failed: %v", err)
	}

	if bs != mockBS {
		t.Error("ResumeSession should return the existing session")
	}
}

func TestNewSessionManagerWithWorkspaces(t *testing.T) {
	workspaces := []WorkspaceConfig{
		{ACPServer: "server1", ACPCommand: "echo server1", WorkingDir: "/path1"},
		{ACPServer: "server2", ACPCommand: "echo server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithWorkspaces(workspaces, true, nil)

	if sm == nil {
		t.Fatal("NewSessionManagerWithWorkspaces returned nil")
	}

	// Check that workspaces are stored
	if len(sm.workspaces) != 2 {
		t.Errorf("workspaces count = %d, want 2", len(sm.workspaces))
	}

	// Check default workspace
	if sm.defaultWorkspace == nil {
		t.Fatal("defaultWorkspace should not be nil")
	}
	if sm.defaultWorkspace.ACPServer != "server1" {
		t.Errorf("defaultWorkspace.ACPServer = %q, want %q", sm.defaultWorkspace.ACPServer, "server1")
	}

	// Legacy fields should be set from first workspace
	if sm.acpCommand != "echo server1" {
		t.Errorf("acpCommand = %q, want %q", sm.acpCommand, "echo server1")
	}
	if sm.acpServer != "server1" {
		t.Errorf("acpServer = %q, want %q", sm.acpServer, "server1")
	}
}

func TestSessionManager_GetWorkspaces(t *testing.T) {
	workspaces := []WorkspaceConfig{
		{ACPServer: "server1", ACPCommand: "echo server1", WorkingDir: "/path1"},
		{ACPServer: "server2", ACPCommand: "echo server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithWorkspaces(workspaces, true, nil)

	got := sm.GetWorkspaces()
	if len(got) != 2 {
		t.Errorf("GetWorkspaces count = %d, want 2", len(got))
	}
}

func TestSessionManager_GetWorkspace(t *testing.T) {
	workspaces := []WorkspaceConfig{
		{ACPServer: "server1", ACPCommand: "echo server1", WorkingDir: "/path1"},
		{ACPServer: "server2", ACPCommand: "echo server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithWorkspaces(workspaces, true, nil)

	// Get existing workspace
	ws := sm.GetWorkspace("/path1")
	if ws == nil {
		t.Fatal("GetWorkspace should find /path1")
	}
	if ws.ACPServer != "server1" {
		t.Errorf("workspace.ACPServer = %q, want %q", ws.ACPServer, "server1")
	}

	// Get non-existent workspace
	ws = sm.GetWorkspace("/path3")
	if ws != nil {
		t.Error("GetWorkspace should return nil for non-existent path")
	}
}

func TestSessionManager_GetDefaultWorkspace(t *testing.T) {
	workspaces := []WorkspaceConfig{
		{ACPServer: "server1", ACPCommand: "echo server1", WorkingDir: "/path1"},
	}

	sm := NewSessionManagerWithWorkspaces(workspaces, true, nil)

	ws := sm.GetDefaultWorkspace()
	if ws == nil {
		t.Fatal("GetDefaultWorkspace should not return nil")
	}
	if ws.ACPServer != "server1" {
		t.Errorf("default workspace ACPServer = %q, want %q", ws.ACPServer, "server1")
	}
}

func TestSessionManager_GetDefaultWorkspace_Legacy(t *testing.T) {
	sm := NewSessionManager("echo legacy", "legacy-server", true, nil)

	ws := sm.GetDefaultWorkspace()
	if ws == nil {
		t.Fatal("GetDefaultWorkspace should not return nil for legacy manager")
	}
	if ws.ACPServer != "legacy-server" {
		t.Errorf("default workspace ACPServer = %q, want %q", ws.ACPServer, "legacy-server")
	}
	if ws.ACPCommand != "echo legacy" {
		t.Errorf("default workspace ACPCommand = %q, want %q", ws.ACPCommand, "echo legacy")
	}
}

func TestSessionManager_GetWorkspaces_NoConfig(t *testing.T) {
	// Create session manager with no workspaces and no legacy config
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  []WorkspaceConfig{},
		AutoApprove: true,
		Logger:      nil,
		FromCLI:     false,
	})

	// GetWorkspaces should return empty slice when no workspaces configured
	got := sm.GetWorkspaces()
	if len(got) != 0 {
		t.Errorf("GetWorkspaces count = %d, want 0 (no workspaces configured)", len(got))
	}
}

func TestSessionManager_AddWorkspace(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Initially no workspaces
	if len(sm.workspaces) != 0 {
		t.Errorf("initial workspaces count = %d, want 0", len(sm.workspaces))
	}

	// Add a workspace
	ws := WorkspaceConfig{
		ACPServer:  "new-server",
		ACPCommand: "echo new",
		WorkingDir: "/path/to/project",
	}
	sm.AddWorkspace(ws)

	// Check it was added
	if len(sm.workspaces) != 1 {
		t.Errorf("workspaces count = %d, want 1", len(sm.workspaces))
	}

	// Check it's retrievable
	got := sm.GetWorkspace("/path/to/project")
	if got == nil {
		t.Fatal("GetWorkspace should find the added workspace")
	}
	if got.ACPServer != "new-server" {
		t.Errorf("workspace ACPServer = %q, want %q", got.ACPServer, "new-server")
	}

	// First workspace becomes default
	def := sm.GetDefaultWorkspace()
	if def.WorkingDir != "/path/to/project" {
		t.Errorf("default workspace WorkingDir = %q, want %q", def.WorkingDir, "/path/to/project")
	}
}

func TestSessionManager_RemoveWorkspace(t *testing.T) {
	workspaces := []WorkspaceConfig{
		{ACPServer: "server1", ACPCommand: "echo server1", WorkingDir: "/path1"},
		{ACPServer: "server2", ACPCommand: "echo server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithWorkspaces(workspaces, true, nil)

	// Remove first workspace
	sm.RemoveWorkspace("/path1")

	// Check it was removed
	if len(sm.workspaces) != 1 {
		t.Errorf("workspaces count = %d, want 1", len(sm.workspaces))
	}

	// Check it's no longer retrievable
	if ws := sm.GetWorkspace("/path1"); ws != nil {
		t.Error("GetWorkspace should return nil for removed workspace")
	}

	// Check remaining workspace is still there
	if ws := sm.GetWorkspace("/path2"); ws == nil {
		t.Error("GetWorkspace should find remaining workspace")
	}

	// Default should have changed to remaining workspace
	def := sm.GetDefaultWorkspace()
	if def.WorkingDir != "/path2" {
		t.Errorf("default workspace WorkingDir = %q, want %q", def.WorkingDir, "/path2")
	}
}

func TestSessionManager_RemoveWorkspace_NonExistent(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Should not panic when removing non-existent workspace
	sm.RemoveWorkspace("/non/existent/path")

	if len(sm.workspaces) != 0 {
		t.Errorf("workspaces count = %d, want 0", len(sm.workspaces))
	}
}

func TestSessionManager_ResumeSession_UsesMetadataACPCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session manager with NO workspaces and NO default ACP command
	// This simulates the case where the server was restarted without --dir flags
	sm := NewSessionManager("", "", true, nil)
	sm.SetStore(store)

	// Create a session in the store with an ACP command stored in metadata
	meta := session.Metadata{
		SessionID:  "test-session-with-cmd",
		ACPServer:  "test-server",
		ACPCommand: "echo hello", // This is the key - the command is stored in metadata
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume the session - it should use the ACP command from metadata
	// Note: This will fail to actually start the ACP process (echo is not a valid ACP server)
	// but we're testing that the command is retrieved from metadata
	_, err = sm.ResumeSession("test-session-with-cmd", "Test Session", "/tmp")

	// The error should be about failing to start the ACP server, NOT "empty ACP command"
	if err != nil {
		errStr := err.Error()
		if errStr == "empty ACP command" {
			t.Error("ResumeSession should have used ACP command from metadata, but got 'empty ACP command' error")
		}
		// Other errors (like "failed to start ACP server") are expected since "echo hello" is not a valid ACP server
	}
}
