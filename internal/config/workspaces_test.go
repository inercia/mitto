package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// ---- WorkspaceSettings helper method tests ----

func TestWorkspaceSettings_HasDedicatedAuxiliary(t *testing.T) {
	tests := []struct {
		name     string
		server   string
		wantTrue bool
	}{
		{"empty string", "", false},
		{"none lowercase", "none", false},
		{"none uppercase", "NONE", false},
		{"none mixed", "None", false},
		{"real server name", "Auggie (Sonnet 4.6)", true},
		{"another server", "claude", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &WorkspaceSettings{AuxiliaryACPServer: tc.server}
			if got := w.HasDedicatedAuxiliary(); got != tc.wantTrue {
				t.Errorf("HasDedicatedAuxiliary() = %v, want %v (server=%q)", got, tc.wantTrue, tc.server)
			}
		})
	}
}

func TestWorkspaceSettings_IsAuxiliaryDisabled(t *testing.T) {
	tests := []struct {
		name     string
		server   string
		wantTrue bool
	}{
		{"empty string", "", false},
		{"none lowercase", "none", true},
		{"none uppercase", "NONE", true},
		{"none mixed", "None", true},
		{"real server name", "Auggie (Sonnet 4.6)", false},
		{"another server", "claude", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &WorkspaceSettings{AuxiliaryACPServer: tc.server}
			if got := w.IsAuxiliaryDisabled(); got != tc.wantTrue {
				t.Errorf("IsAuxiliaryDisabled() = %v, want %v (server=%q)", got, tc.wantTrue, tc.server)
			}
		})
	}
}

func TestWorkspaceSettings_AuxiliaryACPServer_JSONRoundTrip(t *testing.T) {
	w := WorkspaceSettings{
		ACPServer:          "auggie",
		ACPCommand:         "auggie --acp",
		WorkingDir:         "/proj",
		AuxiliaryACPServer: "Auggie (Sonnet 4.6)",
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got WorkspaceSettings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.AuxiliaryACPServer != w.AuxiliaryACPServer {
		t.Errorf("AuxiliaryACPServer = %q, want %q", got.AuxiliaryACPServer, w.AuxiliaryACPServer)
	}
	if !got.HasDedicatedAuxiliary() {
		t.Error("HasDedicatedAuxiliary() should be true after round-trip")
	}
}

func TestWorkspaceSettings_AuxiliaryACPServer_JSONOmitempty(t *testing.T) {
	// When AuxiliaryACPServer is empty, it should be omitted from JSON
	w := WorkspaceSettings{
		ACPServer:  "auggie",
		ACPCommand: "auggie --acp",
		WorkingDir: "/proj",
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := raw["auxiliary_acp_server"]; ok {
		t.Error("auxiliary_acp_server should be omitted from JSON when empty")
	}
}

// ---- LoadWorkspacesFromFile tests ----

func TestLoadWorkspacesFromFile_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[
		{"acp_server":"auggie","acp_command":"auggie --acp","working_dir":"/proj1"},
		{"acp_server":"claude","acp_command":"claude --acp","working_dir":"/proj2"}
	]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(workspaces))
	}
	if workspaces[0].ACPServer != "auggie" {
		t.Errorf("workspaces[0].ACPServer = %q, want %q", workspaces[0].ACPServer, "auggie")
	}
	if workspaces[1].WorkingDir != "/proj2" {
		t.Errorf("workspaces[1].WorkingDir = %q, want %q", workspaces[1].WorkingDir, "/proj2")
	}
}

func TestLoadWorkspacesFromFile_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.yaml")
	content := "workspaces:\n  - acp_server: auggie\n    acp_command: auggie --acp\n    working_dir: /proj1\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(workspaces))
	}
	if workspaces[0].ACPServer != "auggie" {
		t.Errorf("workspaces[0].ACPServer = %q, want %q", workspaces[0].ACPServer, "auggie")
	}
}

func TestLoadWorkspacesFromFile_YML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.yml")
	content := "workspaces:\n  - acp_server: claude\n    acp_command: claude --acp\n    working_dir: /proj2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(workspaces))
	}
	if workspaces[0].ACPServer != "claude" {
		t.Errorf("workspaces[0].ACPServer = %q, want %q", workspaces[0].ACPServer, "claude")
	}
}

func TestLoadWorkspacesFromFile_NotFound(t *testing.T) {
	_, err := LoadWorkspacesFromFile("/nonexistent/path/workspaces.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadWorkspacesFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad json}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadWorkspacesFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadWorkspacesFromFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":\t:\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadWorkspacesFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadWorkspacesFromFile_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.txt")
	if err := os.WriteFile(path, []byte("something"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadWorkspacesFromFile(path)
	if err == nil {
		t.Fatal("expected error for unsupported extension, got nil")
	}
}

func TestLoadWorkspacesFromFile_UUIDGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[{"acp_server":"auggie","acp_command":"auggie --acp","working_dir":"/proj1"}]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(workspaces))
	}
	if workspaces[0].UUID == "" {
		t.Error("expected UUID to be generated, got empty string")
	}
}

func TestLoadWorkspacesFromFile_InvalidRunner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[{"acp_server":"auggie","acp_command":"auggie --acp","working_dir":"/proj1","restricted_runner":"invalid-runner"}]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadWorkspacesFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid runner type, got nil")
	}
}

func TestLoadWorkspacesFromFile_EmptyWorkspaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("got %d workspaces, want 0", len(workspaces))
	}
}

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
