package conversation

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

func TestSessionManager_NewSessionManager(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	if sm == nil {
		t.Fatal("NewSessionManager returned nil")
	}

	// Check default workspace is set correctly
	ws := sm.GetDefaultWorkspace()
	if ws == nil {
		t.Fatal("GetDefaultWorkspace returned nil")
	}
	// CLI command is stored as ACPCommandOverride (per-workspace override)
	if ws.ACPCommandOverride != "echo test" {
		t.Errorf("defaultWorkspace.ACPCommandOverride = %q, want %q", ws.ACPCommandOverride, "echo test")
	}
	if ws.ACPServer != "test-server" {
		t.Errorf("defaultWorkspace.ACPServer = %q, want %q", ws.ACPServer, "test-server")
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
	mockBS := NewTestBackgroundSession(BackgroundSessionTestOpts{SessionID: "test-session-123", ACPID: "acp-123"})
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

func TestNewSessionManagerWithOptions(t *testing.T) {
	workspaces := []config.WorkspaceSettings{
		{ACPServer: "server1", WorkingDir: "/path1"},
		{ACPServer: "server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true,
	})

	if sm == nil {
		t.Fatal("NewSessionManagerWithOptions returned nil")
	}

	// Check that workspaces are stored
	if len(sm.wsRegistry.workspaces) != 2 {
		t.Errorf("workspaces count = %d, want 2", len(sm.wsRegistry.workspaces))
	}

	// Check default workspace (command is resolved from global config at runtime, not stored here)
	if sm.wsRegistry.defaultWorkspace == nil {
		t.Fatal("defaultWorkspace should not be nil")
	}
	if sm.wsRegistry.defaultWorkspace.ACPServer != "server1" {
		t.Errorf("defaultWorkspace.ACPServer = %q, want %q", sm.wsRegistry.defaultWorkspace.ACPServer, "server1")
	}
}

func TestSessionManager_GetWorkspaces(t *testing.T) {
	workspaces := []config.WorkspaceSettings{
		{ACPServer: "server1", WorkingDir: "/path1"},
		{ACPServer: "server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true,
	})

	got := sm.GetWorkspaces()
	if len(got) != 2 {
		t.Errorf("GetWorkspaces count = %d, want 2", len(got))
	}
}

func TestSessionManager_GetWorkspace(t *testing.T) {
	workspaces := []config.WorkspaceSettings{
		{ACPServer: "server1", WorkingDir: "/path1"},
		{ACPServer: "server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true,
	})

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
	workspaces := []config.WorkspaceSettings{
		{ACPServer: "server1", WorkingDir: "/path1"},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true,
	})

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
	// CLI command is stored as ACPCommandOverride
	if ws.ACPCommandOverride != "echo legacy" {
		t.Errorf("default workspace ACPCommandOverride = %q, want %q", ws.ACPCommandOverride, "echo legacy")
	}
}

func TestSessionManager_GetWorkspaces_NoConfig(t *testing.T) {
	// Create session manager with no workspaces and no legacy config
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  []config.WorkspaceSettings{},
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
	if len(sm.wsRegistry.workspaces) != 0 {
		t.Errorf("initial workspaces count = %d, want 0", len(sm.wsRegistry.workspaces))
	}

	// Add a workspace
	ws := config.WorkspaceSettings{
		ACPServer:  "new-server",
		WorkingDir: "/path/to/project",
	}
	sm.AddWorkspace(ws)

	// Check it was added
	if len(sm.wsRegistry.workspaces) != 1 {
		t.Errorf("workspaces count = %d, want 1", len(sm.wsRegistry.workspaces))
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
	workspaces := []config.WorkspaceSettings{
		{UUID: "uuid-1", ACPServer: "server1", WorkingDir: "/path1"},
		{UUID: "uuid-2", ACPServer: "server2", WorkingDir: "/path2"},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true,
	})

	// Remove first workspace by UUID
	sm.RemoveWorkspace("uuid-1")

	// Check it was removed
	if len(sm.wsRegistry.workspaces) != 1 {
		t.Errorf("workspaces count = %d, want 1", len(sm.wsRegistry.workspaces))
	}

	// Check it's no longer retrievable by UUID
	if ws := sm.GetWorkspaceByUUID("uuid-1"); ws != nil {
		t.Error("GetWorkspaceByUUID should return nil for removed workspace")
	}

	// Check it's no longer retrievable by working directory
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

	// Should not panic when removing non-existent workspace by UUID
	sm.RemoveWorkspace("non-existent-uuid")

	if len(sm.wsRegistry.workspaces) != 0 {
		t.Errorf("workspaces count = %d, want 0", len(sm.wsRegistry.workspaces))
	}
}

func TestSessionManager_ResumeSession_UsesGlobalConfigACPCommand(t *testing.T) {
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

	// Set up a global config with the ACP server
	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "test-server", Command: "echo hello"},
		},
	})

	// Create a session in the store with only the ACP server name (no command)
	// The command should be looked up from global config
	meta := session.Metadata{
		SessionID:  "test-session-with-server",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume the session - it should use the ACP command from global config
	// Note: This will fail to actually start the ACP process (echo is not a valid ACP server)
	// but we're testing that the command is retrieved from global config
	_, err = sm.ResumeSession("test-session-with-server", "Test Session", "/tmp")

	// The error should be about failing to start the ACP server, NOT "empty ACP command"
	if err != nil {
		errStr := err.Error()
		if errStr == "empty ACP command" {
			t.Error("ResumeSession should have used ACP command from global config, but got 'empty ACP command' error")
		}
		// Other errors (like "failed to start ACP server") are expected since "echo hello" is not a valid ACP server
	}
}

func TestSessionManager_ResumeSession_OrphanedServer_RescuesWithFolderWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// A workspace for /tmp using the CURRENT (renamed) ACP server.
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/tmp", ACPServer: "new-server"},
		},
	})
	sm.SetStore(store)

	// Global config only knows about the new server — the old one was removed.
	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "new-server", Command: "echo hello"},
		},
	})

	// Orphaned session: its stored ACP server name no longer exists in config.
	meta := session.Metadata{
		SessionID:  "orphaned-session",
		ACPServer:  "old-removed-server",
		WorkingDir: "/tmp",
		Name:       "Orphaned Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Resume should rescue the conversation by adopting the /tmp workspace's
	// server, so it must NOT fail with an empty-command error.
	_, err = sm.ResumeSession("orphaned-session", "Orphaned Session", "/tmp")
	if err != nil && strings.Contains(err.Error(), "empty command") {
		t.Errorf("ResumeSession should have rescued the orphaned conversation with the folder workspace, but got empty-command error: %v", err)
	}

	// After a successful rescue, the stored ACP server name must be persisted so
	// the next resume resolves directly (no repeated orphaned-rescue WARN).
	updated, err := store.GetMetadata("orphaned-session")
	if err != nil {
		t.Fatalf("GetMetadata after rescue failed: %v", err)
	}
	if updated.ACPServer != "new-server" {
		t.Errorf("expected rescued metadata acp_server to be persisted as %q, got %q", "new-server", updated.ACPServer)
	}
}

func TestSessionManager_ResumeSession_OrphanedServer_NoWorkspace_Fails(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// No workspaces and no default ACP command for the folder.
	sm := NewSessionManager("", "", true, nil)
	sm.SetStore(store)
	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "some-other-server", Command: "echo hello"},
		},
	})

	// Orphaned session in a folder with no configured workspace/server.
	meta := session.Metadata{
		SessionID:  "orphaned-no-rescue",
		ACPServer:  "old-removed-server",
		WorkingDir: "/no/such/workspace/dir",
		Name:       "Orphaned No Rescue",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// With nothing to rescue with, resume must fail (no agent available).
	_, err = sm.ResumeSession("orphaned-no-rescue", "Orphaned No Rescue", "/no/such/workspace/dir")
	if err == nil {
		t.Error("ResumeSession should fail when the stored ACP server is gone and no workspace exists for the folder")
	}
}

func TestSessionManager_ApplyACPServerRenames_MigratesMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("", "", true, nil)
	sm.SetStore(store)

	// Two sessions reference the old server name; one references a different one.
	metas := []session.Metadata{
		{SessionID: "s1", ACPServer: "Auggie (Opus 4.6)", WorkingDir: "/tmp", Name: "S1"},
		{SessionID: "s2", ACPServer: "Auggie (Opus 4.6)", WorkingDir: "/tmp", Name: "S2"},
		{SessionID: "s3", ACPServer: "Other Server", WorkingDir: "/tmp", Name: "S3"},
	}
	for _, m := range metas {
		if err := store.Create(m); err != nil {
			t.Fatalf("Create(%s) failed: %v", m.SessionID, err)
		}
	}

	result, err := sm.ApplyACPServerRenames(map[string]string{
		"Auggie (Opus 4.6)": "Auggie (Opus)",
	})
	if err != nil {
		t.Fatalf("ApplyACPServerRenames failed: %v", err)
	}
	if result == nil || len(result.UpdatedSessionIDs) != 2 {
		t.Fatalf("expected 2 updated sessions, got %+v", result)
	}

	// Migrated sessions adopt the new server name.
	for _, id := range []string{"s1", "s2"} {
		m, err := store.GetMetadata(id)
		if err != nil {
			t.Fatalf("GetMetadata(%s) failed: %v", id, err)
		}
		if m.ACPServer != "Auggie (Opus)" {
			t.Errorf("session %s: expected ACPServer %q, got %q", id, "Auggie (Opus)", m.ACPServer)
		}
	}

	// Unrelated session is untouched.
	m3, err := store.GetMetadata("s3")
	if err != nil {
		t.Fatalf("GetMetadata(s3) failed: %v", err)
	}
	if m3.ACPServer != "Other Server" {
		t.Errorf("session s3 should be untouched, got %q", m3.ACPServer)
	}
}

func TestSessionManager_ApplyACPServerRenames_NoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("", "", true, nil)
	sm.SetStore(store)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "Server A", WorkingDir: "/tmp", Name: "S1",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Renaming a server that no session references is a no-op.
	result, err := sm.ApplyACPServerRenames(map[string]string{"Unused": "New"})
	if err != nil {
		t.Fatalf("ApplyACPServerRenames failed: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when nothing matches, got %+v", result)
	}

	m, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if m.ACPServer != "Server A" {
		t.Errorf("session s1 should be untouched, got %q", m.ACPServer)
	}
}

func TestSessionManager_GetWorkspacePrompts_NilCache(t *testing.T) {
	sm := &SessionManager{
		wsRegistry: &WorkspaceRegistry{workspaceRCCache: nil},
	}

	prompts := sm.GetWorkspacePrompts("/test")
	if prompts != nil {
		t.Error("GetWorkspacePrompts should return nil when cache is nil")
	}
}

func TestSessionManager_GetWorkspacePrompts_EmptyDir(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	prompts := sm.GetWorkspacePrompts("")
	if prompts != nil {
		t.Error("GetWorkspacePrompts should return nil for empty dir")
	}
}

func TestSessionManager_HasWorkspaces(t *testing.T) {
	// No workspaces
	sm := NewSessionManager("", "", false, nil)
	if sm.HasWorkspaces() {
		t.Error("HasWorkspaces should return false when no workspaces")
	}

	// With workspaces
	sm.AddWorkspace(config.WorkspaceSettings{
		WorkingDir: "/test",
		ACPServer:  "server",
	})
	if !sm.HasWorkspaces() {
		t.Error("HasWorkspaces should return true when workspaces exist")
	}
}

func TestSessionManager_SessionCount(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	if sm.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", sm.SessionCount())
	}

	// Add a mock session
	sm.mu.Lock()
	sm.sessions["test-1"] = NewMinimalBackgroundSession("test-1", "", "")
	sm.mu.Unlock()

	if sm.SessionCount() != 1 {
		t.Errorf("SessionCount = %d, want 1", sm.SessionCount())
	}
}

func TestSessionManager_ListRunningSessions(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Initially empty
	sessions := sm.ListRunningSessions()
	if len(sessions) != 0 {
		t.Errorf("ListRunningSessions = %d, want 0", len(sessions))
	}

	// Add mock sessions
	sm.mu.Lock()
	sm.sessions["test-1"] = NewMinimalBackgroundSession("test-1", "", "")
	sm.sessions["test-2"] = NewMinimalBackgroundSession("test-2", "", "")
	sm.mu.Unlock()

	sessions = sm.ListRunningSessions()
	if len(sessions) != 2 {
		t.Errorf("ListRunningSessions = %d, want 2", len(sessions))
	}
}

func TestSessionManager_GetSession(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Add a mock session
	bs := NewMinimalBackgroundSession("test-1", "", "")
	sm.mu.Lock()
	sm.sessions["test-1"] = bs
	sm.mu.Unlock()

	// Get existing session
	result := sm.GetSession("test-1")
	if result != bs {
		t.Error("GetSession should return the session")
	}

	// Get non-existent session
	result = sm.GetSession("nonexistent")
	if result != nil {
		t.Error("GetSession should return nil for non-existent session")
	}
}

func TestSessionManager_GetActiveWorkingDirs(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Initially empty
	dirs := sm.GetActiveWorkingDirs()
	if len(dirs) != 0 {
		t.Errorf("GetActiveWorkingDirs = %d, want 0", len(dirs))
	}

	// Add sessions with different working dirs
	sm.mu.Lock()
	sm.sessions["test-1"] = NewMinimalBackgroundSession("test-1", "/workspace1", "")
	sm.sessions["test-2"] = NewMinimalBackgroundSession("test-2", "/workspace2", "")
	sm.sessions["test-3"] = NewMinimalBackgroundSession("test-3", "/workspace1", "") // Duplicate
	sm.sessions["test-4"] = NewMinimalBackgroundSession("test-4", "", "")            // Empty
	sm.mu.Unlock()

	dirs = sm.GetActiveWorkingDirs()
	// Should have 2 unique directories (excluding empty)
	if len(dirs) != 2 {
		t.Errorf("GetActiveWorkingDirs = %d, want 2", len(dirs))
	}

	// Check that both expected dirs are present
	found := make(map[string]bool)
	for _, dir := range dirs {
		found[dir] = true
	}
	if !found["/workspace1"] {
		t.Error("GetActiveWorkingDirs should include /workspace1")
	}
	if !found["/workspace2"] {
		t.Error("GetActiveWorkingDirs should include /workspace2")
	}
}

func TestSessionManager_ResolveWorkspaceIdentifier(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", false, nil)

	// Get the default workspace UUID
	defaultWS := sm.GetDefaultWorkspace()
	if defaultWS == nil {
		t.Fatal("Expected default workspace")
	}
	defaultUUID := defaultWS.UUID

	// Initially, the default workspace has empty WorkingDir
	workingDir, found := sm.ResolveWorkspaceIdentifier(defaultUUID)
	if !found {
		t.Error("ResolveWorkspaceIdentifier should find default workspace UUID")
	}
	if workingDir != "" {
		t.Errorf("Expected empty WorkingDir for default workspace, got %q", workingDir)
	}

	// Add an active session with the default workspace UUID but a specific working dir
	sm.mu.Lock()
	sm.sessions["test-session"] = NewMinimalBackgroundSession("test-session", "/my/project/dir", defaultUUID)
	sm.mu.Unlock()

	// Now ResolveWorkspaceIdentifier should return the session's working dir
	workingDir, found = sm.ResolveWorkspaceIdentifier(defaultUUID)
	if !found {
		t.Error("ResolveWorkspaceIdentifier should find UUID from active session")
	}
	if workingDir != "/my/project/dir" {
		t.Errorf("Expected /my/project/dir, got %q", workingDir)
	}

	// Test with a registered workspace (should prefer workspace over session)
	wsUUID := "registered-ws-uuid"
	sm.wsRegistry.mu.Lock()
	sm.wsRegistry.workspaces["/registered/workspace"] = &config.WorkspaceSettings{
		UUID:       wsUUID,
		WorkingDir: "/registered/workspace",
		ACPServer:  "test",
	}
	sm.wsRegistry.mu.Unlock()

	workingDir, found = sm.ResolveWorkspaceIdentifier(wsUUID)
	if !found {
		t.Error("ResolveWorkspaceIdentifier should find registered workspace UUID")
	}
	if workingDir != "/registered/workspace" {
		t.Errorf("Expected /registered/workspace, got %q", workingDir)
	}

	// Test with unknown UUID
	_, found = sm.ResolveWorkspaceIdentifier("unknown-uuid")
	if found {
		t.Error("ResolveWorkspaceIdentifier should not find unknown UUID")
	}
}

func TestSessionManager_SetWorkspaces(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	workspaces := []config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
		{WorkingDir: "/workspace2", ACPServer: "server2"},
	}

	sm.SetWorkspaces(workspaces)

	result := sm.GetWorkspaces()
	if len(result) != 2 {
		t.Errorf("GetWorkspaces() = %d, want 2", len(result))
	}
}

func TestSessionManager_IsFromCLI(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Default should be false
	if sm.IsFromCLI() {
		t.Error("IsFromCLI should return false by default")
	}

	// Create with FromCLI option
	sm2 := NewSessionManagerWithOptions(SessionManagerOptions{
		FromCLI: true,
	})

	if !sm2.IsFromCLI() {
		t.Error("IsFromCLI should return true when FromCLI option is set")
	}
}

func TestSessionManager_SetProcessorManager(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Should not panic with nil
	sm.SetProcessorManager(nil)
}

func TestSessionManager_SetGlobalConversations(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Should not panic with nil
	sm.SetGlobalConversations(nil)
}

func TestSessionManager_RemoveWorkspace_NilWorkspaces(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	// Remove from nil workspaces (should not panic) - using UUID now
	sm.RemoveWorkspace("nonexistent-uuid")
}

func TestSessionManager_AddWorkspace_SameDirectoryDifferentACP(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/workspace1", ACPServer: "server1"},
	})

	// Add workspace with same directory but different ACP server
	// This is now allowed (e.g., same project folder with Claude vs Gemini)
	sm.AddWorkspace(config.WorkspaceSettings{
		UUID:       "uuid-2",
		WorkingDir: "/workspace1",
		ACPServer:  "server2",
	})

	workspaces := sm.GetWorkspaces()
	if len(workspaces) != 2 {
		t.Errorf("GetWorkspaces() = %d, want 2 (same dir, different ACP servers allowed)", len(workspaces))
	}

	// GetWorkspaceByDirAndACP should find the correct one
	ws1 := sm.GetWorkspaceByDirAndACP("/workspace1", "server1")
	if ws1 == nil {
		t.Error("GetWorkspaceByDirAndACP should find workspace with server1")
	}
	ws2 := sm.GetWorkspaceByDirAndACP("/workspace1", "server2")
	if ws2 == nil {
		t.Error("GetWorkspaceByDirAndACP should find workspace with server2")
	}
}

// =============================================================================
// M3: Health Check Helper Methods Tests
// =============================================================================

// TestSessionManager_ActiveSessionCount tests the ActiveSessionCount method.
func TestSessionManager_ActiveSessionCount(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Initially should be 0
	if count := sm.ActiveSessionCount(); count != 0 {
		t.Errorf("ActiveSessionCount() = %d, want 0", count)
	}

	// Add a mock session
	mockSession := NewMinimalBackgroundSession("test-session-1", "", "")
	sm.mu.Lock()
	sm.sessions["test-session-1"] = mockSession
	sm.mu.Unlock()

	// Should be 1 (not closed)
	if count := sm.ActiveSessionCount(); count != 1 {
		t.Errorf("ActiveSessionCount() = %d, want 1", count)
	}

	// Close the session (using atomic store)
	mockSession.SimulateClose()

	// Should be 0 (closed)
	if count := sm.ActiveSessionCount(); count != 0 {
		t.Errorf("ActiveSessionCount() after close = %d, want 0", count)
	}
}

// TestSessionManager_PromptingSessionCount tests the PromptingSessionCount method.
func TestSessionManager_PromptingSessionCount(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Initially should be 0
	if count := sm.PromptingSessionCount(); count != 0 {
		t.Errorf("PromptingSessionCount() = %d, want 0", count)
	}

	// Add a mock session that is prompting
	mockSession := NewMinimalBackgroundSessionPrompting("test-session-1", true)
	sm.mu.Lock()
	sm.sessions["test-session-1"] = mockSession
	sm.mu.Unlock()

	// Should be 1
	if count := sm.PromptingSessionCount(); count != 1 {
		t.Errorf("PromptingSessionCount() = %d, want 1", count)
	}

	// Stop prompting
	mockSession.SimulatePromptComplete()

	// Should be 0
	if count := sm.PromptingSessionCount(); count != 0 {
		t.Errorf("PromptingSessionCount() after stop = %d, want 0", count)
	}
}

// TestSessionManager_ActiveAndPromptingCounts tests both counts together.
func TestSessionManager_ActiveAndPromptingCounts(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Add multiple sessions with different states
	session1 := NewMinimalBackgroundSessionPrompting("s1", true)
	session2 := NewMinimalBackgroundSessionPrompting("s2", false)
	session3 := NewMinimalBackgroundSessionPrompting("s3", true)

	sm.mu.Lock()
	sm.sessions["s1"] = session1
	sm.sessions["s2"] = session2
	sm.sessions["s3"] = session3
	sm.mu.Unlock()

	// 3 active, 2 prompting
	if count := sm.ActiveSessionCount(); count != 3 {
		t.Errorf("ActiveSessionCount() = %d, want 3", count)
	}
	if count := sm.PromptingSessionCount(); count != 2 {
		t.Errorf("PromptingSessionCount() = %d, want 2", count)
	}

	// Close one session (using atomic store)
	session1.SimulateClose()

	// 2 active (s1 is closed so not counted in active)
	// Note: PromptingSessionCount still counts s1 because it only checks isPrompting,
	// not whether the session is closed. In practice, closed sessions shouldn't be prompting.
	if count := sm.ActiveSessionCount(); count != 2 {
		t.Errorf("ActiveSessionCount() after close = %d, want 2", count)
	}

	// Also stop prompting on s1 to simulate proper cleanup
	session1.SimulatePromptComplete()

	// Now only 1 prompting (s3)
	if count := sm.PromptingSessionCount(); count != 1 {
		t.Errorf("PromptingSessionCount() after close = %d, want 1", count)
	}
}

// TestSessionManager_ConnectedWSClientCount tests the ConnectedWSClientCount
// method (mitto-x3x), which feeds the periodic goroutine gauge's
// "connected_ws_clients" contention counter.
func TestSessionManager_ConnectedWSClientCount(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// No sessions: 0.
	if count := sm.ConnectedWSClientCount(); count != 0 {
		t.Errorf("ConnectedWSClientCount() = %d, want 0", count)
	}

	session1 := NewMinimalBackgroundSession("s1", "", "")
	session2 := NewMinimalBackgroundSession("s2", "", "")
	sm.mu.Lock()
	sm.sessions["s1"] = session1
	sm.sessions["s2"] = session2
	sm.mu.Unlock()

	// Sessions present but no connected clients yet: still 0.
	if count := sm.ConnectedWSClientCount(); count != 0 {
		t.Errorf("ConnectedWSClientCount() with no clients = %d, want 0", count)
	}

	// Attach 2 clients to s1 and 1 to s2: summed across sessions.
	session1.AddConnectedClient()
	session1.AddConnectedClient()
	session2.AddConnectedClient()
	if count := sm.ConnectedWSClientCount(); count != 3 {
		t.Errorf("ConnectedWSClientCount() = %d, want 3", count)
	}

	// Detach one client from s1: count follows.
	session1.RemoveConnectedClient()
	if count := sm.ConnectedWSClientCount(); count != 2 {
		t.Errorf("ConnectedWSClientCount() after detach = %d, want 2", count)
	}
}

// =============================================================================
// CloseSessionGracefully Tests
// =============================================================================

// TestSessionManager_CloseSessionGracefully_NotRunning tests that CloseSessionGracefully
// returns true immediately when the session is not running.
func TestSessionManager_CloseSessionGracefully_NotRunning(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	start := time.Now()
	result := sm.CloseSessionGracefully("non-existent-session", "test", 5*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("CloseSessionGracefully should return true for non-existent session")
	}

	// Should return almost immediately
	if elapsed > 100*time.Millisecond {
		t.Errorf("CloseSessionGracefully took %v, expected < 100ms for non-existent session", elapsed)
	}
}

// TestSessionManager_CloseSessionGracefully_NotPrompting tests that CloseSessionGracefully
// returns true immediately when the session is not prompting.
func TestSessionManager_CloseSessionGracefully_NotPrompting(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Add a mock session that is not prompting
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := NewTestBackgroundSessionPromptingWithCtx("test-session", false, ctx, cancel)

	sm.mu.Lock()
	sm.sessions["test-session"] = mockSession
	sm.mu.Unlock()

	start := time.Now()
	result := sm.CloseSessionGracefully("test-session", "test", 5*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("CloseSessionGracefully should return true when not prompting")
	}

	// Should return almost immediately
	if elapsed > 100*time.Millisecond {
		t.Errorf("CloseSessionGracefully took %v, expected < 100ms when not prompting", elapsed)
	}

	// Session should be removed
	if sm.GetSession("test-session") != nil {
		t.Error("Session should be removed after CloseSessionGracefully")
	}
}

// TestSessionManager_CloseSessionGracefully_WaitsForPrompt tests that CloseSessionGracefully
// waits for the prompt to complete before closing.
func TestSessionManager_CloseSessionGracefully_WaitsForPrompt(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Add a mock session that is prompting
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := NewTestBackgroundSessionPromptingWithCtx("test-session", true, ctx, cancel)

	sm.mu.Lock()
	sm.sessions["test-session"] = mockSession
	sm.mu.Unlock()

	// Simulate prompt completion after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		mockSession.SimulatePromptComplete()
	}()

	start := time.Now()
	result := sm.CloseSessionGracefully("test-session", "test", 5*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("CloseSessionGracefully should return true when prompt completes")
	}

	// Should wait for prompt to complete (~100ms)
	if elapsed < 50*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("CloseSessionGracefully took %v, expected ~100ms", elapsed)
	}

	// Session should be removed
	if sm.GetSession("test-session") != nil {
		t.Error("Session should be removed after CloseSessionGracefully")
	}
}

// TestSessionManager_CloseSessionGracefully_Timeout tests that CloseSessionGracefully
// returns false when the timeout expires.
func TestSessionManager_CloseSessionGracefully_Timeout(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	// Add a mock session that is prompting and won't complete
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Clean up
	mockSession := NewTestBackgroundSessionPromptingWithCtx("test-session", true, ctx, cancel)

	sm.mu.Lock()
	sm.sessions["test-session"] = mockSession
	sm.mu.Unlock()

	start := time.Now()
	result := sm.CloseSessionGracefully("test-session", "test", 100*time.Millisecond)
	elapsed := time.Since(start)

	if result {
		t.Error("CloseSessionGracefully should return false on timeout")
	}

	// Should timeout around 100ms
	if elapsed < 80*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("CloseSessionGracefully took %v, expected ~100ms", elapsed)
	}

	// Session should NOT be removed (timeout means we didn't close it)
	if sm.GetSession("test-session") == nil {
		t.Error("Session should NOT be removed after CloseSessionGracefully timeout")
	}
}

// =============================================================================
// ProcessPendingQueues Archive Tests
// =============================================================================

// TestSessionManager_ProcessPendingQueues_SkipsArchivedSessions tests that
// ProcessPendingQueues does not resume archived sessions even if they have
// pending queue items.
func TestSessionManager_ProcessPendingQueues_SkipsArchivedSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create an archived session with a queued message
	meta := session.Metadata{
		SessionID:  "archived-session",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Archived Session",
		Archived:   true,
		ArchivedAt: time.Now(),
		Status:     session.SessionStatusActive, // Still "active" status but archived
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("archived-session")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create a non-archived session with a queued message for comparison
	meta2 := session.Metadata{
		SessionID:  "active-session",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Active Session",
		Archived:   false,
		Status:     session.SessionStatusActive,
	}
	if err := store.Create(meta2); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	queue2 := store.Queue("active-session")
	_, err = queue2.Add("Test message 2", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session manager
	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	// ProcessPendingQueues should skip the archived session
	// Note: This will try to resume the active session but fail because
	// the ACP command is just "echo test". That's fine - we just want to
	// verify the archived session is skipped.
	sm.ProcessPendingQueues()

	// The archived session should NOT be in the running sessions
	if sm.GetSession("archived-session") != nil {
		t.Error("Archived session should not be resumed by ProcessPendingQueues")
	}

	// Note: The active session might or might not be running depending on
	// whether ResumeSession succeeded. We don't check that here.
}

// Tests for plan state cache

func TestSessionManager_PlanStateCache_SetAndGet(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "completed"},
		{Content: "Task 2", Priority: "medium", Status: "in_progress"},
		{Content: "Task 3", Priority: "low", Status: "pending"},
	}

	// Initially should be nil
	result := sm.GetCachedPlanState(sessionID)
	if result != nil {
		t.Errorf("GetCachedPlanState should return nil for non-existent session, got %v", result)
	}

	// Set plan state
	sm.SetCachedPlanState(sessionID, entries)

	// Get should return the entries
	result = sm.GetCachedPlanState(sessionID)
	if result == nil {
		t.Fatal("GetCachedPlanState returned nil after SetCachedPlanState")
	}
	if len(result) != len(entries) {
		t.Errorf("GetCachedPlanState returned %d entries, want %d", len(result), len(entries))
	}

	// Verify entries match
	for i, entry := range result {
		if entry.Content != entries[i].Content {
			t.Errorf("entry[%d].Content = %q, want %q", i, entry.Content, entries[i].Content)
		}
		if entry.Priority != entries[i].Priority {
			t.Errorf("entry[%d].Priority = %q, want %q", i, entry.Priority, entries[i].Priority)
		}
		if entry.Status != entries[i].Status {
			t.Errorf("entry[%d].Status = %q, want %q", i, entry.Status, entries[i].Status)
		}
	}
}

func TestSessionManager_PlanStateCache_ReturnsCopy(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "pending"},
	}

	sm.SetCachedPlanState(sessionID, entries)

	// Get the result and modify it
	result := sm.GetCachedPlanState(sessionID)
	result[0].Content = "Modified"

	// Get again - should still have original value
	result2 := sm.GetCachedPlanState(sessionID)
	if result2[0].Content != "Task 1" {
		t.Errorf("GetCachedPlanState should return a copy, but modification affected original: got %q", result2[0].Content)
	}
}

func TestSessionManager_PlanStateCache_Clear(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "pending"},
	}

	sm.SetCachedPlanState(sessionID, entries)

	// Verify it's set
	if sm.GetCachedPlanState(sessionID) == nil {
		t.Fatal("Plan state should be set")
	}

	// Clear it
	sm.ClearCachedPlanState(sessionID)

	// Should be nil now
	result := sm.GetCachedPlanState(sessionID)
	if result != nil {
		t.Errorf("GetCachedPlanState should return nil after ClearCachedPlanState, got %v", result)
	}
}

func TestSessionManager_PlanStateCache_SetEmptyClears(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "pending"},
	}

	sm.SetCachedPlanState(sessionID, entries)

	// Setting empty slice should clear
	sm.SetCachedPlanState(sessionID, []PlanEntry{})

	result := sm.GetCachedPlanState(sessionID)
	if result != nil {
		t.Errorf("GetCachedPlanState should return nil after setting empty slice, got %v", result)
	}

	// Setting nil should also clear
	sm.SetCachedPlanState(sessionID, entries)
	sm.SetCachedPlanState(sessionID, nil)

	result = sm.GetCachedPlanState(sessionID)
	if result != nil {
		t.Errorf("GetCachedPlanState should return nil after setting nil, got %v", result)
	}
}

func TestSessionManager_PlanStateCache_MultipleSessions(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	session1 := "session-1"
	session2 := "session-2"

	entries1 := []PlanEntry{{Content: "Session 1 Task", Priority: "high", Status: "pending"}}
	entries2 := []PlanEntry{{Content: "Session 2 Task", Priority: "low", Status: "completed"}}

	sm.SetCachedPlanState(session1, entries1)
	sm.SetCachedPlanState(session2, entries2)

	// Each session should have its own state
	result1 := sm.GetCachedPlanState(session1)
	result2 := sm.GetCachedPlanState(session2)

	if result1[0].Content != "Session 1 Task" {
		t.Errorf("Session 1 has wrong content: %q", result1[0].Content)
	}
	if result2[0].Content != "Session 2 Task" {
		t.Errorf("Session 2 has wrong content: %q", result2[0].Content)
	}

	// Clearing one shouldn't affect the other
	sm.ClearCachedPlanState(session1)

	if sm.GetCachedPlanState(session1) != nil {
		t.Error("Session 1 should be cleared")
	}
	if sm.GetCachedPlanState(session2) == nil {
		t.Error("Session 2 should still have state")
	}
}

func TestSessionManager_PlanStateCache_ConcurrentAccess(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "pending"},
	}

	// Run concurrent operations
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			sm.SetCachedPlanState(sessionID, entries)
		}()

		go func() {
			defer wg.Done()
			sm.GetCachedPlanState(sessionID)
		}()

		go func() {
			defer wg.Done()
			sm.ClearCachedPlanState(sessionID)
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics occurred
}

func TestSessionManager_CloseSession_ClearsPlanState(t *testing.T) {
	sm := NewSessionManager("echo test", "test-server", true, nil)

	sessionID := "test-session-123"
	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "pending"},
	}

	// Set plan state
	sm.SetCachedPlanState(sessionID, entries)

	// Verify it's set
	if sm.GetCachedPlanState(sessionID) == nil {
		t.Fatal("Plan state should be set")
	}

	// Close the session (even if it doesn't exist as a running session)
	sm.CloseSession(sessionID, "test")

	// Plan state should be cleared
	result := sm.GetCachedPlanState(sessionID)
	if result != nil {
		t.Errorf("CloseSession should clear plan state, got %v", result)
	}
}

// Tests for per-workspace auto-approve feature

func TestSessionManager_WorkspaceAutoApprove_Enabled(t *testing.T) {
	// Test that when workspace has AutoApprove=true, sessions created in that workspace
	// should have autoApprove enabled even if global autoApprove is false
	autoApproveTrue := true
	workspaces := []config.WorkspaceSettings{
		{
			UUID:        "ws-auto-approve",
			ACPServer:   "test-server",
			WorkingDir:  "/path/auto-approve",
			AutoApprove: &autoApproveTrue, // Per-workspace auto-approve enabled
		},
		{
			UUID:       "ws-no-auto-approve",
			ACPServer:  "test-server",
			WorkingDir: "/path/no-auto-approve",
			// AutoApprove is nil (not set)
		},
	}

	// Create session manager with global autoApprove=false
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: false, // Global auto-approve disabled
	})

	// Verify the workspace with auto-approve has the flag set
	ws := sm.GetWorkspaceByUUID("ws-auto-approve")
	if ws == nil {
		t.Fatal("GetWorkspaceByUUID should find ws-auto-approve")
	}
	if ws.AutoApprove == nil || !*ws.AutoApprove {
		t.Error("Workspace ws-auto-approve should have AutoApprove=true")
	}

	// Verify the workspace without auto-approve has the flag unset
	ws2 := sm.GetWorkspaceByUUID("ws-no-auto-approve")
	if ws2 == nil {
		t.Fatal("GetWorkspaceByUUID should find ws-no-auto-approve")
	}
	if ws2.AutoApprove != nil {
		t.Errorf("Workspace ws-no-auto-approve should have AutoApprove=nil, got %v", *ws2.AutoApprove)
	}
}

func TestSessionManager_WorkspaceAutoApprove_Disabled(t *testing.T) {
	// Test that when workspace has AutoApprove=false explicitly,
	// the setting is respected
	autoApproveFalse := false
	workspaces := []config.WorkspaceSettings{
		{
			UUID:        "ws-explicit-false",
			ACPServer:   "test-server",
			WorkingDir:  "/path/explicit-false",
			AutoApprove: &autoApproveFalse, // Explicitly disabled
		},
	}

	// Create session manager with global autoApprove=true
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: true, // Global auto-approve enabled
	})

	// Verify the workspace has the flag explicitly set to false
	ws := sm.GetWorkspaceByUUID("ws-explicit-false")
	if ws == nil {
		t.Fatal("GetWorkspaceByUUID should find ws-explicit-false")
	}
	if ws.AutoApprove == nil {
		t.Error("Workspace ws-explicit-false should have AutoApprove set (not nil)")
	} else if *ws.AutoApprove != false {
		t.Error("Workspace ws-explicit-false should have AutoApprove=false")
	}
}

func TestSessionManager_WorkspaceAutoApprove_SetWorkspaces(t *testing.T) {
	// Test that SetWorkspaces preserves the AutoApprove field
	autoApproveTrue := true
	sm := NewSessionManager("echo test", "test-server", false, nil)

	// Set workspaces with auto-approve enabled
	workspaces := []config.WorkspaceSettings{
		{
			UUID:        "ws-with-auto",
			ACPServer:   "test-server",
			WorkingDir:  "/path/with-auto",
			AutoApprove: &autoApproveTrue,
		},
	}
	sm.SetWorkspaces(workspaces)

	// Verify the workspace retained the auto-approve setting
	ws := sm.GetWorkspaceByUUID("ws-with-auto")
	if ws == nil {
		t.Fatal("GetWorkspaceByUUID should find ws-with-auto")
	}
	if ws.AutoApprove == nil || !*ws.AutoApprove {
		t.Error("SetWorkspaces should preserve AutoApprove=true")
	}
}

func TestSessionManager_WorkspaceAutoApprove_AddWorkspace(t *testing.T) {
	// Test that AddWorkspace preserves the AutoApprove field
	autoApproveTrue := true
	sm := NewSessionManager("echo test", "test-server", false, nil)

	// Add a workspace with auto-approve enabled
	ws := config.WorkspaceSettings{
		UUID:        "ws-added",
		ACPServer:   "test-server",
		WorkingDir:  "/path/added",
		AutoApprove: &autoApproveTrue,
	}
	sm.AddWorkspace(ws)

	// Verify the workspace was added with auto-approve setting
	addedWs := sm.GetWorkspaceByUUID("ws-added")
	if addedWs == nil {
		t.Fatal("GetWorkspaceByUUID should find ws-added")
	}
	if addedWs.AutoApprove == nil || !*addedWs.AutoApprove {
		t.Error("AddWorkspace should preserve AutoApprove=true")
	}
}

// =============================================================================
// ResumeSession TOCTOU Race-Prevention Tests
// =============================================================================

// TestSessionManager_ResumeSession_WaitsForPending verifies that concurrent callers
// of ResumeSession for the same session ID wait on the pending resume channel and
// receive the result produced by the primary goroutine — without launching a second
// ACP subprocess.
func TestSessionManager_ResumeSession_WaitsForPending(t *testing.T) {
	sm := NewSessionManager("", "test-server", true, nil)

	sessionID := "pending-resume-session"
	expectedBS := NewMinimalBackgroundSession(sessionID, "", "")

	// Pre-register a pending resume entry as if a primary goroutine had already
	// acquired the lock and registered it, but hasn't finished yet.
	pr := &pendingResumeResult{done: make(chan struct{})}
	sm.mu.Lock()
	sm.pendingResumes[sessionID] = pr
	sm.mu.Unlock()

	const numWaiters = 5
	results := make([]*BackgroundSession, numWaiters)
	errs := make([]error, numWaiters)

	var wg sync.WaitGroup
	for i := 0; i < numWaiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = sm.ResumeSession(sessionID, "Test", "/tmp")
		}(i)
	}

	// Give goroutines time to find the pendingResumes entry and block on <-pr.done.
	time.Sleep(50 * time.Millisecond)

	// Signal completion — set fields before closing to satisfy the happens-before guarantee.
	pr.bs = expectedBS
	pr.err = nil
	close(pr.done)

	// All goroutines must complete without deadlocking.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines deadlocked waiting for pending resume")
	}

	// Every goroutine should have received the same BackgroundSession pointer.
	for i, result := range results {
		if result != expectedBS {
			t.Errorf("goroutine %d: got BackgroundSession %p, want %p (err=%v)",
				i, result, expectedBS, errs[i])
		}
		if errs[i] != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, errs[i])
		}
	}
}

// TestSessionManager_ResumeSession_ConcurrentNoDeadlock verifies that concurrent
// ResumeSession calls for the same session ID complete without deadlocking.
// Because "echo test" is not a valid ACP server, all calls are expected to fail,
// but they must fail consistently and without blocking.
func TestSessionManager_ResumeSession_ConcurrentNoDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session in the store so the early "session not found" check passes.
	meta := session.Metadata{
		SessionID:  "race-test-session",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Race Test",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create failed: %v", err)
	}

	// "echo test" is not a valid ACP server — ResumeSession will fail quickly.
	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	const goroutines = 8
	results := make([]*BackgroundSession, goroutines)
	errs := make([]error, goroutines)

	// Release all goroutines simultaneously to maximise race likelihood.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = sm.ResumeSession("race-test-session", "Race Test", "/tmp")
		}(i)
	}
	close(start)

	// All goroutines must complete — a deadlock would trip this timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Concurrent ResumeSession calls deadlocked or timed out")
	}

	// With pendingResumes coalescing, every goroutine must receive the same result.
	// (All will be nil / error since "echo test" is not a valid ACP server.)
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("goroutine %d got different BackgroundSession than goroutine 0 (%p vs %p)",
				i, results[i], results[0])
		}
		// Errors must match in nil-ness (exact pointer may differ for non-coalesced first run)
		if (errs[i] == nil) != (errs[0] == nil) {
			t.Errorf("goroutine %d error nil-ness mismatch: got %v, goroutine 0 got %v",
				i, errs[i], errs[0])
		}
	}

	// No session should have been registered (all resume attempts failed).
	if sm.SessionCount() != 0 {
		t.Errorf("SessionCount = %d after failed resumes, want 0", sm.SessionCount())
	}
}

func TestSessionManager_WorkspaceAutoApprove_GetWorkspaces(t *testing.T) {
	// Test that GetWorkspaces returns workspaces with AutoApprove field intact
	autoApproveTrue := true
	autoApproveFalse := false
	workspaces := []config.WorkspaceSettings{
		{
			UUID:        "ws-1",
			ACPServer:   "server1",
			WorkingDir:  "/path1",
			AutoApprove: &autoApproveTrue,
		},
		{
			UUID:        "ws-2",
			ACPServer:   "server2",
			WorkingDir:  "/path2",
			AutoApprove: &autoApproveFalse,
		},
		{
			UUID:       "ws-3",
			ACPServer:  "server3",
			WorkingDir: "/path3",
			// AutoApprove is nil
		},
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: false,
	})

	got := sm.GetWorkspaces()
	if len(got) != 3 {
		t.Fatalf("GetWorkspaces count = %d, want 3", len(got))
	}

	// Find each workspace and verify AutoApprove
	var foundWs1, foundWs2, foundWs3 bool
	for _, ws := range got {
		switch ws.UUID {
		case "ws-1":
			foundWs1 = true
			if ws.AutoApprove == nil || !*ws.AutoApprove {
				t.Error("ws-1 should have AutoApprove=true")
			}
		case "ws-2":
			foundWs2 = true
			if ws.AutoApprove == nil || *ws.AutoApprove {
				t.Error("ws-2 should have AutoApprove=false")
			}
		case "ws-3":
			foundWs3 = true
			if ws.AutoApprove != nil {
				t.Error("ws-3 should have AutoApprove=nil")
			}
		}
	}

	if !foundWs1 || !foundWs2 || !foundWs3 {
		t.Error("GetWorkspaces should return all workspaces")
	}
}

// =============================================================================
// DeleteChildSessions Tests
// =============================================================================

// TestSessionManager_DeleteChildSessions tests that DeleteChildSessions
// permanently deletes all direct children of a parent session.
func TestSessionManager_DeleteChildSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create parent
	if err := store.Create(session.Metadata{
		SessionID:  "parent-1",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Parent",
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create two children
	if err := store.Create(session.Metadata{
		SessionID:       "child-1",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Child 1",
		ParentSessionID: "parent-1",
	}); err != nil {
		t.Fatalf("Create child1 failed: %v", err)
	}

	if err := store.Create(session.Metadata{
		SessionID:       "child-2",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Child 2",
		ParentSessionID: "parent-1",
	}); err != nil {
		t.Fatalf("Create child2 failed: %v", err)
	}

	// Create an unrelated session (should NOT be deleted)
	if err := store.Create(session.Metadata{
		SessionID:  "unrelated-1",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Unrelated",
	}); err != nil {
		t.Fatalf("Create unrelated failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	// Delete child sessions of parent
	sm.DeleteChildSessions("parent-1")

	// Children should be deleted
	if store.Exists("child-1") {
		t.Error("child-1 should be deleted")
	}
	if store.Exists("child-2") {
		t.Error("child-2 should be deleted")
	}

	// Parent and unrelated should still exist
	if !store.Exists("parent-1") {
		t.Error("parent-1 should still exist")
	}
	if !store.Exists("unrelated-1") {
		t.Error("unrelated-1 should still exist")
	}
}

// TestSessionManager_DeleteChildSessions_NoArchiveChildStillDeleted pins
// mitto-yvel.3 epic decision 3: deleting protected (NoArchive) children along
// with an archived parent is unaffected by the archive guard — the parent
// cascade is pure deletion, never archiving, so NoArchive children are
// deleted exactly like any other child.
func TestSessionManager_DeleteChildSessions_NoArchiveChildStillDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{
		SessionID:  "cascade-parent",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Parent",
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	if err := store.Create(session.Metadata{
		SessionID:       "cascade-protected-child",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Protected Child",
		ParentSessionID: "cascade-parent",
		NoArchive:       true,
	}); err != nil {
		t.Fatalf("Create protected child failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	sm.DeleteChildSessions("cascade-parent")

	if store.Exists("cascade-protected-child") {
		t.Error("NoArchive child should still be deleted by the parent cascade (deletion is always allowed, epic decision 3)")
	}
}

// TestSessionManager_DeleteSessionAndChildren tests that deleteSessionAndChildren
// (used by the self-destruct path) permanently removes the target session and all
// of its descendants while leaving unrelated sessions intact.
func TestSessionManager_DeleteSessionAndChildren(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create the self-destructing session
	if err := store.Create(session.Metadata{
		SessionID:  "selfdestruct-1",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Self Destruct",
	}); err != nil {
		t.Fatalf("Create selfdestruct session failed: %v", err)
	}

	// Create a child and a grandchild (descendants that must be removed)
	if err := store.Create(session.Metadata{
		SessionID:       "child-1",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Child 1",
		ParentSessionID: "selfdestruct-1",
	}); err != nil {
		t.Fatalf("Create child1 failed: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID:       "grandchild-1",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Grandchild 1",
		ParentSessionID: "child-1",
	}); err != nil {
		t.Fatalf("Create grandchild1 failed: %v", err)
	}

	// Create an unrelated session (should NOT be deleted)
	if err := store.Create(session.Metadata{
		SessionID:  "unrelated-1",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Unrelated",
	}); err != nil {
		t.Fatalf("Create unrelated failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	sm.deleteSessionAndChildren("selfdestruct-1", "self_destructed")

	// The session and all its descendants should be deleted
	if store.Exists("selfdestruct-1") {
		t.Error("selfdestruct-1 should be deleted")
	}
	if store.Exists("child-1") {
		t.Error("child-1 should be deleted")
	}
	if store.Exists("grandchild-1") {
		t.Error("grandchild-1 should be deleted")
	}

	// Unrelated session should still exist
	if !store.Exists("unrelated-1") {
		t.Error("unrelated-1 should still exist")
	}
}

// TestBuildProcessorArgOverrides verifies the helper that converts []ProcessorOverride
// into the map[procName]map[argName]value form (mitto-5g2v.2 wiring).
func TestBuildProcessorArgOverrides(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := buildProcessorArgOverrides(nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		if got := buildProcessorArgOverrides([]config.ProcessorOverride{}); got != nil {
			t.Errorf("expected nil for empty slice, got %v", got)
		}
	})

	t.Run("overrides with no Arguments are skipped", func(t *testing.T) {
		overrides := []config.ProcessorOverride{
			{Name: "proc-a", Enabled: nil, Arguments: nil},
		}
		if got := buildProcessorArgOverrides(overrides); got != nil {
			t.Errorf("expected nil (no Arguments), got %v", got)
		}
	})

	t.Run("empty-value argument entries are dropped", func(t *testing.T) {
		overrides := []config.ProcessorOverride{
			{Name: "proc-a", Arguments: map[string]string{"keep": "value", "drop": ""}},
		}
		got := buildProcessorArgOverrides(overrides)
		if got == nil {
			t.Fatal("expected non-nil map")
		}
		args := got["proc-a"]
		if args["keep"] != "value" {
			t.Errorf(`args["keep"] = %q, want "value"`, args["keep"])
		}
		if _, exists := args["drop"]; exists {
			t.Error("empty-value key 'drop' should have been dropped")
		}
	})

	t.Run("valid overrides build correct nested map", func(t *testing.T) {
		overrides := []config.ProcessorOverride{
			{Name: "auggie-manage-rules", Arguments: map[string]string{"filename": "AGENTS.md", "mode": "append"}},
			{Name: "only-enabled", Enabled: boolPtrSMTest(true), Arguments: nil},
			{Name: "multi-arg", Arguments: map[string]string{"a": "1", "b": "2"}},
		}
		got := buildProcessorArgOverrides(overrides)
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 entries (only-enabled skipped), got %d: %v", len(got), got)
		}
		if got["auggie-manage-rules"]["filename"] != "AGENTS.md" {
			t.Errorf("filename = %q, want AGENTS.md", got["auggie-manage-rules"]["filename"])
		}
		if got["auggie-manage-rules"]["mode"] != "append" {
			t.Errorf("mode = %q, want append", got["auggie-manage-rules"]["mode"])
		}
		if got["multi-arg"]["a"] != "1" || got["multi-arg"]["b"] != "2" {
			t.Errorf("multi-arg = %v, want {a:1 b:2}", got["multi-arg"])
		}
	})

	t.Run("processor with only empty-value args produces nil map", func(t *testing.T) {
		overrides := []config.ProcessorOverride{
			{Name: "all-empty", Arguments: map[string]string{"x": ""}},
		}
		if got := buildProcessorArgOverrides(overrides); got != nil {
			t.Errorf("expected nil (all values empty), got %v", got)
		}
	})
}

// boolPtrSMTest is a test helper to create a *bool.
func boolPtrSMTest(v bool) *bool { return &v }

// TestProcessorArgOverrides_SeamMethods verifies that the pd* and fu* seam methods
// on BackgroundSession return the WorkspaceProcessorArgOverrides value injected via
// BackgroundSessionConfig, completing the end-to-end wiring (mitto-5g2v.2).
func TestProcessorArgOverrides_SeamMethods(t *testing.T) {
	argOverrides := map[string]map[string]string{
		"auggie-manage-rules": {"filename": "AGENTS.md"},
	}

	// pd* seam: fakePromptDeps carries the overrides and returns them via pdWorkspaceProcessorArgOverrides.
	pd := newFakePromptDeps()
	pd.workspaceProcessorArgOverrides = argOverrides
	got := pd.pdWorkspaceProcessorArgOverrides()
	if got == nil {
		t.Fatal("pdWorkspaceProcessorArgOverrides: expected non-nil map")
	}
	if got["auggie-manage-rules"]["filename"] != "AGENTS.md" {
		t.Errorf("pd seam: filename = %q, want AGENTS.md", got["auggie-manage-rules"]["filename"])
	}

	// fu* seam: fakeFollowUpDeps carries the overrides and returns them via fuWorkspaceProcessorArgOverrides.
	fu := newFakeFollowUpDeps()
	fu.workspaceProcessorArgOverrides = argOverrides
	got2 := fu.fuWorkspaceProcessorArgOverrides()
	if got2 == nil {
		t.Fatal("fuWorkspaceProcessorArgOverrides: expected non-nil map")
	}
	if got2["auggie-manage-rules"]["filename"] != "AGENTS.md" {
		t.Errorf("fu seam: filename = %q, want AGENTS.md", got2["auggie-manage-rules"]["filename"])
	}
}

// fakeProcessManager is a minimal ProcessManager stub for tests. It records the
// workspace UUIDs passed to EnsurePrewarmed so tests can assert the re-warm
// trigger fired (mitto-54k.7), and the PinWorkspace calls so tests can assert
// the close-phase pin fired (mitto-4is). All other interface methods are no-ops.
type fakeProcessManager struct {
	mu             sync.Mutex
	prewarmed      []string
	prewarmCh      chan string
	pinCalls       []fakePinCall
	liveWorkspaces map[string]bool
	// hasLiveDefault is returned by HasLiveProcess when the workspace UUID is
	// not listed in liveWorkspaces. Defaults to true so pre-existing tests
	// (which do not care about the reaped-process path) continue to see a
	// "live" workspace.
	hasLiveDefault bool
}

type fakePinCall struct {
	workspaceUUID string
	reason        string
	maxDuration   time.Duration
	maxPinned     int
}

func newFakeProcessManager() *fakeProcessManager {
	return &fakeProcessManager{
		prewarmCh:      make(chan string, 8),
		liveWorkspaces: map[string]bool{},
		hasLiveDefault: true,
	}
}

// setLiveProcess controls what HasLiveProcess returns for a given workspace
// UUID. Passing live=false simulates a workspace whose shared ACP process has
// already been reaped by GC (mitto-6bn.1).
func (f *fakeProcessManager) setLiveProcess(workspaceUUID string, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.liveWorkspaces[workspaceUUID] = live
}

func (f *fakeProcessManager) EnsurePrewarmed(workspaceUUID string, _ *slog.Logger) {
	f.mu.Lock()
	f.prewarmed = append(f.prewarmed, workspaceUUID)
	f.mu.Unlock()
	select {
	case f.prewarmCh <- workspaceUUID:
	default:
	}
}

func (f *fakeProcessManager) prewarmedUUIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prewarmed...)
}

func (f *fakeProcessManager) GetOrCreateProcess(*config.WorkspaceSettings, string, string, map[string]string, *runner.Runner, bool) (SharedProcess, error) {
	return nil, nil
}
func (f *fakeProcessManager) ClearGCSuspended(string)   {}
func (f *fakeProcessManager) IsGCSuspended(string) bool { return false }
func (f *fakeProcessManager) StopGC()                   {}
func (f *fakeProcessManager) Close()                    {}
func (f *fakeProcessManager) ProcessCount() int         { return 0 }
func (f *fakeProcessManager) ColdProcessCount() int     { return 0 }
func (f *fakeProcessManager) PinWorkspace(workspaceUUID, reason string, maxDuration time.Duration, maxPinned int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinCalls = append(f.pinCalls, fakePinCall{
		workspaceUUID: workspaceUUID,
		reason:        reason,
		maxDuration:   maxDuration,
		maxPinned:     maxPinned,
	})
	return true
}

func (f *fakeProcessManager) pinCallsSnapshot() []fakePinCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePinCall(nil), f.pinCalls...)
}

func (f *fakeProcessManager) HasLiveProcess(workspaceUUID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.liveWorkspaces[workspaceUUID]; ok {
		return v
	}
	return f.hasLiveDefault
}

// waitForPrewarm blocks until EnsurePrewarmed is called (up to timeout) and
// returns the workspace UUID, or "" if it was not called in time.
func (f *fakeProcessManager) waitForPrewarm(timeout time.Duration) string {
	select {
	case ws := <-f.prewarmCh:
		return ws
	case <-time.After(timeout):
		return ""
	}
}

// TestSessionManager_CloseSession_NoRewarmWhenLastSessionDeleted verifies
// that deleting the last user session for a workspace does NOT trigger
// EnsurePrewarmed (mitto-clc: proactive re-warm-on-close was inactivated).
func TestSessionManager_CloseSession_NoRewarmWhenLastSessionDeleted(t *testing.T) {
	const wsUUID = "ws-rewarm-1"

	sm := NewSessionManager("echo test", "test-server", true, nil)
	pm := newFakeProcessManager()
	sm.SetACPProcessManager(pm)

	ctx, cancel := context.WithCancel(context.Background())
	bs := NewTestBackgroundSessionWithCtx("sess-1", ctx, cancel)
	bs.workspaceUUID = wsUUID
	sm.mu.Lock()
	sm.sessions["sess-1"] = bs
	sm.mu.Unlock()

	sm.CloseSession("sess-1", "deleted")

	if got := pm.waitForPrewarm(300 * time.Millisecond); got != "" {
		t.Fatalf("EnsurePrewarmed should not fire (mitto-clc), got %q", got)
	}
	if uuids := pm.prewarmedUUIDs(); len(uuids) != 0 {
		t.Fatalf("expected no prewarm calls, got %v", uuids)
	}
}

// TestSessionManager_CloseSession_NoRewarmWhenSessionsRemain verifies that
// closing one session while others in the same workspace remain does NOT
// trigger a re-warm (the remaining sessions keep the process warm).
func TestSessionManager_CloseSession_NoRewarmWhenSessionsRemain(t *testing.T) {
	const wsUUID = "ws-rewarm-2"

	sm := NewSessionManager("echo test", "test-server", true, nil)
	pm := newFakeProcessManager()
	sm.SetACPProcessManager(pm)

	ctx1, cancel1 := context.WithCancel(context.Background())
	bs1 := NewTestBackgroundSessionWithCtx("sess-1", ctx1, cancel1)
	bs1.workspaceUUID = wsUUID
	ctx2, cancel2 := context.WithCancel(context.Background())
	bs2 := NewTestBackgroundSessionWithCtx("sess-2", ctx2, cancel2)
	bs2.workspaceUUID = wsUUID

	sm.mu.Lock()
	sm.sessions["sess-1"] = bs1
	sm.sessions["sess-2"] = bs2
	sm.mu.Unlock()

	sm.CloseSession("sess-1", "deleted")

	if got := pm.waitForPrewarm(300 * time.Millisecond); got != "" {
		t.Fatalf("EnsurePrewarmed should not fire while sessions remain, got %q", got)
	}
	if uuids := pm.prewarmedUUIDs(); len(uuids) != 0 {
		t.Fatalf("expected no prewarm calls, got %v", uuids)
	}
}

// TestSessionManager_CloseSession_NoRewarmOnArchive verifies that archive-family
// close reasons do NOT trigger a re-warm even when the workspace goes
// sessionless (archiving deliberately stops-and-reclaims).
func TestSessionManager_CloseSession_NoRewarmOnArchive(t *testing.T) {
	const wsUUID = "ws-rewarm-3"

	for _, reason := range []string{"archived", "archived_timeout", "ancestor_archived", "parent_archived_timeout", "acp_server_reconfigured"} {
		t.Run(reason, func(t *testing.T) {
			sm := NewSessionManager("echo test", "test-server", true, nil)
			pm := newFakeProcessManager()
			sm.SetACPProcessManager(pm)

			ctx, cancel := context.WithCancel(context.Background())
			bs := NewTestBackgroundSessionWithCtx("sess-1", ctx, cancel)
			bs.workspaceUUID = wsUUID
			sm.mu.Lock()
			sm.sessions["sess-1"] = bs
			sm.mu.Unlock()

			sm.CloseSession("sess-1", reason)

			if got := pm.waitForPrewarm(300 * time.Millisecond); got != "" {
				t.Fatalf("EnsurePrewarmed should not fire for reason %q, got %q", reason, got)
			}
		})
	}
}

// TestApplyOnCloseProcessors_PinsWorkspace verifies that the close-phase
// pipeline pins the workspace before dispatching its fire-and-forget goroutine,
// protecting the shared ACP process from GC while in-flight processors are
// still dispatching to auxiliary sessions (mitto-4is).
func TestApplyOnCloseProcessors_PinsWorkspace(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		sid       = "sess-pin"
		wsUUID    = "ws-pin-close"
		acpServer = "test-server"
	)
	workingDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("echo test", acpServer, true, nil)
	sm.SetStore(store)
	sm.SetProcessorManager(processors.NewManager("", nil))
	sm.AddWorkspace(config.WorkspaceSettings{
		UUID:       wsUUID,
		WorkingDir: workingDir,
		ACPServer:  acpServer,
	})
	pm := newFakeProcessManager()
	sm.SetACPProcessManager(pm)

	sm.ApplyOnCloseProcessors(sid, "deleted")

	calls := pm.pinCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("PinWorkspace call count = %d, want 1 (calls=%+v)", len(calls), calls)
	}
	got := calls[0]
	if got.workspaceUUID != wsUUID {
		t.Errorf("PinWorkspace workspaceUUID = %q, want %q", got.workspaceUUID, wsUUID)
	}
	if got.reason != "conversation_closed_processors" {
		t.Errorf("PinWorkspace reason = %q, want %q", got.reason, "conversation_closed_processors")
	}
	if got.maxDuration != 15*time.Minute {
		t.Errorf("PinWorkspace maxDuration = %v, want %v", got.maxDuration, 15*time.Minute)
	}
	if got.maxPinned != 0 {
		t.Errorf("PinWorkspace maxPinned = %d, want 0 (uncapped)", got.maxPinned)
	}
}

// TestApplyOnCloseProcessors_NoPinWithoutWorkspaceUUID verifies that when the
// session's workspace cannot be resolved from the registry, the close pipeline
// still runs but skips the pin call (pinning without a UUID would be a no-op
// on the wrong key and mask the misconfiguration).
func TestApplyOnCloseProcessors_NoPinWithoutWorkspaceUUID(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sid = "sess-pin-noresolve"
	// Note: no AddWorkspace call — resolution will return nil, so workspaceUUID
	// stays empty.
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)
	sm.SetProcessorManager(processors.NewManager("", nil))
	pm := newFakeProcessManager()
	sm.SetACPProcessManager(pm)

	sm.ApplyOnCloseProcessors(sid, "deleted")

	if calls := pm.pinCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("PinWorkspace should not fire without a resolved workspace UUID, got %+v", calls)
	}
}

// TestApplyOnCloseProcessors_SkipsWhenSharedProcessReaped verifies that when
// the workspace's shared ACP process has already been reaped by GC (e.g.
// Tier 2 idle-reap hours after the last session closed), the close pipeline
// is a clean no-op: no PinWorkspace call, no goroutine spawn, no downstream
// "no shared process for workspace ..." ERROR from getOrCreateAuxiliarySession
// (mitto-6bn.1).
func TestApplyOnCloseProcessors_SkipsWhenSharedProcessReaped(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		sid       = "sess-reaped"
		wsUUID    = "ws-reaped-close"
		acpServer = "test-server"
	)
	workingDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("echo test", acpServer, true, nil)
	sm.SetStore(store)
	sm.SetProcessorManager(processors.NewManager("", nil))
	sm.AddWorkspace(config.WorkspaceSettings{
		UUID:       wsUUID,
		WorkingDir: workingDir,
		ACPServer:  acpServer,
	})
	pm := newFakeProcessManager()
	// Simulate the reaped-process scenario: HasLiveProcess returns false.
	pm.setLiveProcess(wsUUID, false)
	sm.SetACPProcessManager(pm)

	sm.ApplyOnCloseProcessors(sid, "inactivity")

	// The pre-check must short-circuit BEFORE PinWorkspace is called.
	if calls := pm.pinCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("PinWorkspace should not fire when shared process is reaped, got %+v", calls)
	}
}

// TestSessionManager_ResumeSession_HappyPath_UsesWorkspaceMatchingDirAndACPServer
// asserts that when two workspaces share the same working directory but expose
// different ACP servers, the resume path re-resolves the workspace using both
// the directory AND the ACP server from session metadata (Case: meta.ACPServer
// != "" and resolveWorkspaceForACP finds a match). No orphan branch must fire,
// so the persisted acp_server on metadata must remain unchanged.
func TestSessionManager_ResumeSession_HappyPath_UsesWorkspaceMatchingDirAndACPServer(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/tmp", ACPServer: "agent-a"},
			{WorkingDir: "/tmp", ACPServer: "agent-b"},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo hello-a"},
			{Name: "agent-b", Command: "echo hello-b"},
		},
	})

	meta := session.Metadata{
		SessionID:  "happy-path-session",
		ACPServer:  "agent-b",
		WorkingDir: "/tmp",
		Name:       "Happy Path",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = sm.ResumeSession("happy-path-session", "Happy Path", "/tmp")
	// Workspace resolution succeeded when the error, if any, is NOT the
	// empty-command class emitted by the "no workspace + no server" branch.
	if err != nil && strings.Contains(err.Error(), "empty command") {
		t.Errorf("ResumeSession should have resolved the agent-b workspace, but got empty-command error: %v", err)
	}

	// The orphan-rescue branch (Case 2) is the only path that persists a new
	// acp_server on metadata. It must not have fired here.
	updated, err := store.GetMetadata("happy-path-session")
	if err != nil {
		t.Fatalf("GetMetadata after resume failed: %v", err)
	}
	if updated.ACPServer != "agent-b" {
		t.Errorf("expected metadata acp_server to remain %q, got %q", "agent-b", updated.ACPServer)
	}
}

// TestSessionManager_ResumeSession_ACPCommandOverride_TakesPriorityOverGlobalConfig
// asserts that when the matched workspace carries an ACPCommandOverride, the
// resume path uses ResolveWorkspaceACP (which prefers the override) rather than
// the raw command from the global ACP server config.
func TestSessionManager_ResumeSession_ACPCommandOverride_TakesPriorityOverGlobalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{
				WorkingDir:         "/tmp",
				ACPServer:          "agent-a",
				ACPCommandOverride: "echo override-cmd",
			},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo global-cmd"},
		},
	})

	meta := session.Metadata{
		SessionID:  "override-session",
		ACPServer:  "agent-a",
		WorkingDir: "/tmp",
		Name:       "Override Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = sm.ResumeSession("override-session", "Override Session", "/tmp")
	if err != nil && strings.Contains(err.Error(), "empty command") {
		t.Errorf("ResumeSession should have resolved a non-empty command via the workspace override, got: %v", err)
	}

	// Behavioral cross-check: ResolveWorkspaceACP (the exact call the resume
	// block makes) must return the override, not the global command. This
	// documents the contract the extraction must preserve.
	ws := sm.wsRegistry.GetWorkspaceByDirAndACP("/tmp", "agent-a")
	if ws == nil {
		t.Fatalf("expected workspace for /tmp + agent-a, got nil")
	}
	gotCmd, _, _ := sm.wsRegistry.ResolveWorkspaceACP(ws)
	if gotCmd != "echo override-cmd" {
		t.Errorf("ResolveWorkspaceACP command = %q, want %q (override must beat global config)", gotCmd, "echo override-cmd")
	}
}

// TestSessionManager_ResumeSession_NoWorkspaceForDir_FallsBackToDefaultWorkspace
// covers the provisional-pick fallback: no workspace matches the working dir
// and metadata carries no ACP server, so the resolver must fall back to
// GetDefaultWorkspace() to obtain a usable ACP command.
func TestSessionManager_ResumeSession_NoWorkspaceForDir_FallsBackToDefaultWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/other/dir", ACPServer: "agent-a", IsDefault: true},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo hello"},
		},
	})

	// Metadata has NO acp_server set and points at a working dir with no
	// matching workspace: only the default-workspace fallback can supply a
	// command here.
	meta := session.Metadata{
		SessionID:  "default-fallback-session",
		WorkingDir: "/nowhere",
		Name:       "Default Fallback",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = sm.ResumeSession("default-fallback-session", "Default Fallback", "/nowhere")
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "empty command") || strings.Contains(errStr, "empty ACP command") {
			t.Errorf("ResumeSession should have fallen back to the default workspace's ACP command, got empty-command error: %v", err)
		}
	}
}

// TestSessionManager_ResumeSession_NoMetadataACPServer_KeepsProvisionalWorkspacePick
// asserts that when metadata carries no acp_server, the provisional pick
// (dir-matched workspace) is kept and no orphan branch fires. In particular,
// the rescue-persist step must NOT run, so metadata's acp_server stays empty.
func TestSessionManager_ResumeSession_NoMetadataACPServer_KeepsProvisionalWorkspacePick(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/tmp", ACPServer: "agent-a"},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo hello"},
		},
	})

	meta := session.Metadata{
		SessionID:  "provisional-session",
		WorkingDir: "/tmp",
		Name:       "Provisional Pick",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = sm.ResumeSession("provisional-session", "Provisional Pick", "/tmp")
	if err != nil && strings.Contains(err.Error(), "empty command") {
		t.Errorf("ResumeSession should have used the provisional workspace pick, got empty-command error: %v", err)
	}

	// Rescue-persist only fires from the orphan branch (Case 2), which requires
	// meta.ACPServer != "". This path must NOT touch acp_server on metadata.
	updated, err := store.GetMetadata("provisional-session")
	if err != nil {
		t.Fatalf("GetMetadata after resume failed: %v", err)
	}
	if updated.ACPServer != "" {
		t.Errorf("expected metadata acp_server to remain empty (no rescue-persist on provisional path), got %q", updated.ACPServer)
	}
}

// TestSessionManager_ResumeSession_FinalRetry_SkipsACPSessionResume asserts
// that when ACPStartFailureCount reaches ACPStartFailureThreshold-1, the
// resolver clears the LOCAL acpSessionID variable (forcing a fresh session on
// the final retry) without persisting that clear to metadata. This is a
// regression fence for the eventual extraction: the persisted ACPSessionID
// must survive across resume attempts.
func TestSessionManager_ResumeSession_FinalRetry_SkipsACPSessionResume(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/tmp", ACPServer: "agent-a"},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo hello"},
		},
	})

	meta := session.Metadata{
		SessionID:            "final-retry-session",
		ACPServer:            "agent-a",
		ACPSessionID:         "acp-abc",
		WorkingDir:           "/tmp",
		Name:                 "Final Retry",
		ACPStartFailureCount: ACPStartFailureThreshold - 1,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Behavior-only: the call must return (no panic, no deadlock). We don't
	// care about the returned error — "echo hello" isn't a real ACP server.
	_, _ = sm.ResumeSession("final-retry-session", "Final Retry", "/tmp")

	// The clear of acpSessionID inside the resolution block is LOCAL to the
	// function. The persisted ACPSessionID on metadata must remain untouched
	// by that step (the resume path may still update other fields such as the
	// failure counter, but not this one).
	updated, err := store.GetMetadata("final-retry-session")
	if err != nil {
		t.Fatalf("GetMetadata after resume failed: %v", err)
	}
	if updated.ACPSessionID != "acp-abc" {
		t.Errorf("expected persisted ACPSessionID to remain %q after final-retry local clear, got %q", "acp-abc", updated.ACPSessionID)
	}
}

// TestSessionManager_ResumeSession_ACPStartFailureThreshold_SkipsArchiveForNoArchive
// pins mitto-yvel.3: a NoArchive conversation that reaches
// ACPStartFailureThreshold consecutive genuine ACP start failures still has
// its ACPStartFailureCount incremented (so the counter itself keeps working)
// but must NOT be archived — a supervisor loop that keeps failing to start
// must not be reaped.
func TestSessionManager_ResumeSession_ACPStartFailureThreshold_SkipsArchiveForNoArchive(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/tmp", ACPServer: "agent-a"},
		},
	})
	sm.SetStore(store)

	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo hello"},
		},
	})

	meta := session.Metadata{
		SessionID:            "no-archive-threshold-session",
		ACPServer:            "agent-a",
		WorkingDir:           "/tmp",
		Name:                 "No Archive Threshold",
		ACPStartFailureCount: ACPStartFailureThreshold - 1,
		NoArchive:            true,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// This resume attempt pushes the failure count to ACPStartFailureThreshold,
	// which would normally trigger auto-archive.
	_, _ = sm.ResumeSession("no-archive-threshold-session", "No Archive Threshold", "/tmp")

	updated, err := store.GetMetadata("no-archive-threshold-session")
	if err != nil {
		t.Fatalf("GetMetadata after resume failed: %v", err)
	}
	if updated.ACPStartFailureCount < ACPStartFailureThreshold {
		t.Errorf("ACPStartFailureCount = %d, want >= %d (counter must still be tracked)", updated.ACPStartFailureCount, ACPStartFailureThreshold)
	}
	if updated.Archived {
		t.Error("NoArchive session should NOT be auto-archived after reaching ACPStartFailureThreshold")
	}
}
