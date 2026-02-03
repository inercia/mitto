package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

func TestLoadWorkspaces_NoFile(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Load workspaces - should return nil (not error) when file doesn't exist
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() returned error: %v", err)
	}
	if workspaces != nil {
		t.Errorf("LoadWorkspaces() = %v, want nil", workspaces)
	}
}

func TestLoadWorkspaces_ExistingFile(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create a workspaces.json file
	workspacesPath := filepath.Join(tmpDir, appdir.WorkspacesFileName)
	workspacesJSON := `{
		"workspaces": [
			{"acp_server": "auggie", "acp_command": "auggie --acp", "working_dir": "/path/to/project1"},
			{"acp_server": "claude", "acp_command": "claude --acp", "working_dir": "/path/to/project2"}
		]
	}`
	if err := os.WriteFile(workspacesPath, []byte(workspacesJSON), 0644); err != nil {
		t.Fatalf("failed to create test workspaces.json: %v", err)
	}

	// Load workspaces
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() returned error: %v", err)
	}

	if len(workspaces) != 2 {
		t.Fatalf("LoadWorkspaces() returned %d workspaces, want 2", len(workspaces))
	}

	// Check first workspace
	if workspaces[0].ACPServer != "auggie" {
		t.Errorf("workspaces[0].ACPServer = %q, want %q", workspaces[0].ACPServer, "auggie")
	}
	if workspaces[0].WorkingDir != "/path/to/project1" {
		t.Errorf("workspaces[0].WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/path/to/project1")
	}

	// Check second workspace
	if workspaces[1].ACPServer != "claude" {
		t.Errorf("workspaces[1].ACPServer = %q, want %q", workspaces[1].ACPServer, "claude")
	}
}

func TestSaveWorkspaces(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Save workspaces
	workspaces := []WorkspaceSettings{
		{ACPServer: "test-server", ACPCommand: "test-cmd", WorkingDir: "/test/path"},
	}
	if err := SaveWorkspaces(workspaces); err != nil {
		t.Fatalf("SaveWorkspaces() returned error: %v", err)
	}

	// Verify file was created
	workspacesPath := filepath.Join(tmpDir, appdir.WorkspacesFileName)
	if _, err := os.Stat(workspacesPath); err != nil {
		t.Fatalf("workspaces.json was not created: %v", err)
	}

	// Load and verify
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("LoadWorkspaces() returned %d workspaces, want 1", len(loaded))
	}
	if loaded[0].ACPServer != "test-server" {
		t.Errorf("loaded[0].ACPServer = %q, want %q", loaded[0].ACPServer, "test-server")
	}
	if loaded[0].WorkingDir != "/test/path" {
		t.Errorf("loaded[0].WorkingDir = %q, want %q", loaded[0].WorkingDir, "/test/path")
	}
}
