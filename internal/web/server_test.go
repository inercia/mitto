package web

import (
	"log/slog"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestConfig_GetWorkspaces_WithWorkspaces(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1", ACPServer: "server1"},
			{WorkingDir: "/workspace2", ACPServer: "server2"},
		},
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 2 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 2", len(workspaces))
	}

	if workspaces[0].WorkingDir != "/workspace1" {
		t.Errorf("workspaces[0].WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/workspace1")
	}
}

func TestConfig_GetWorkspaces_LegacyFields(t *testing.T) {
	cfg := &Config{
		ACPServer:         "legacy-server",
		ACPCommand:        "legacy-command",
		DefaultWorkingDir: "/legacy/dir",
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 1 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 1", len(workspaces))
	}

	if workspaces[0].ACPServer != "legacy-server" {
		t.Errorf("ACPServer = %q, want %q", workspaces[0].ACPServer, "legacy-server")
	}
	if workspaces[0].ACPCommand != "legacy-command" {
		t.Errorf("ACPCommand = %q, want %q", workspaces[0].ACPCommand, "legacy-command")
	}
	if workspaces[0].WorkingDir != "/legacy/dir" {
		t.Errorf("WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/legacy/dir")
	}
}

func TestConfig_GetWorkspaces_EmptyWorkingDir(t *testing.T) {
	cfg := &Config{
		ACPServer:         "server",
		DefaultWorkingDir: "", // Empty - should use current directory
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 1 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 1", len(workspaces))
	}

	// WorkingDir should be set to current directory (not empty)
	if workspaces[0].WorkingDir == "" {
		t.Error("WorkingDir should not be empty when DefaultWorkingDir is empty")
	}
}

func TestConfig_GetDefaultWorkspace(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/first", ACPServer: "server1"},
			{WorkingDir: "/second", ACPServer: "server2"},
		},
	}

	ws := cfg.GetDefaultWorkspace()

	if ws == nil {
		t.Fatal("GetDefaultWorkspace() returned nil")
	}

	if ws.WorkingDir != "/first" {
		t.Errorf("WorkingDir = %q, want %q", ws.WorkingDir, "/first")
	}
}

func TestConfig_GetDefaultWorkspace_Empty(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{},
	}

	// When Workspaces is empty, GetWorkspaces creates a legacy workspace
	ws := cfg.GetDefaultWorkspace()

	// Should return the legacy workspace, not nil
	if ws == nil {
		t.Fatal("GetDefaultWorkspace() returned nil")
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := &Config{
		AutoApprove:    true,
		Debug:          true,
		FromCLI:        true,
		ConfigReadOnly: true,
		RCFilePath:     "/path/to/rc",
	}

	if !cfg.AutoApprove {
		t.Error("AutoApprove should be true")
	}
	if !cfg.Debug {
		t.Error("Debug should be true")
	}
	if !cfg.FromCLI {
		t.Error("FromCLI should be true")
	}
	if !cfg.ConfigReadOnly {
		t.Error("ConfigReadOnly should be true")
	}
	if cfg.RCFilePath != "/path/to/rc" {
		t.Errorf("RCFilePath = %q, want %q", cfg.RCFilePath, "/path/to/rc")
	}
}

func TestConfig_GetWorkspaceByDir(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1", ACPServer: "server1"},
			{WorkingDir: "/workspace2", ACPServer: "server2"},
		},
	}

	// Find existing workspace
	ws := cfg.GetWorkspaceByDir("/workspace1")
	if ws == nil {
		t.Fatal("GetWorkspaceByDir returned nil for existing workspace")
	}
	if ws.ACPServer != "server1" {
		t.Errorf("ACPServer = %q, want %q", ws.ACPServer, "server1")
	}

	// Find non-existent workspace
	ws = cfg.GetWorkspaceByDir("/nonexistent")
	if ws != nil {
		t.Error("GetWorkspaceByDir should return nil for non-existent workspace")
	}
}

func TestConfig_GetWorkspaceByDir_Legacy(t *testing.T) {
	cfg := &Config{
		ACPServer:         "legacy-server",
		ACPCommand:        "legacy-cmd",
		DefaultWorkingDir: "/legacy/dir",
	}

	// Find legacy workspace
	ws := cfg.GetWorkspaceByDir("/legacy/dir")
	if ws == nil {
		t.Fatal("GetWorkspaceByDir returned nil for legacy workspace")
	}
	if ws.ACPServer != "legacy-server" {
		t.Errorf("ACPServer = %q, want %q", ws.ACPServer, "legacy-server")
	}
}

func TestServer_APIPrefix(t *testing.T) {
	server := &Server{
		apiPrefix: "/api/v1",
	}

	if server.APIPrefix() != "/api/v1" {
		t.Errorf("APIPrefix() = %q, want %q", server.APIPrefix(), "/api/v1")
	}
}

func TestServer_Store(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		store: store,
	}

	if server.Store() != store {
		t.Error("Store() should return the store")
	}
}

func TestServer_Store_Nil(t *testing.T) {
	server := &Server{
		store: nil,
	}

	if server.Store() != nil {
		t.Error("Store() should return nil when store is nil")
	}
}

func TestServer_IsShutdown(t *testing.T) {
	server := &Server{}

	// Initially not shutdown
	if server.IsShutdown() {
		t.Error("IsShutdown should return false initially")
	}

	// Set shutdown
	server.mu.Lock()
	server.shutdown = true
	server.mu.Unlock()

	if !server.IsShutdown() {
		t.Error("IsShutdown should return true after setting")
	}
}

func TestServer_Logger(t *testing.T) {
	logger := slog.Default()
	server := &Server{
		logger: logger,
	}

	if server.Logger() != logger {
		t.Error("Logger() should return the logger")
	}
}

func TestServer_Logger_Nil(t *testing.T) {
	server := &Server{
		logger: nil,
	}

	if server.Logger() != nil {
		t.Error("Logger() should return nil when logger is nil")
	}
}
