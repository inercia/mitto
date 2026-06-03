package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// ---- WorkspaceSettings AuxiliaryModelSelection tests ----

func TestWorkspaceSettings_AuxiliaryModelSelection_JSONRoundTrip(t *testing.T) {
	w := WorkspaceSettings{
		ACPServer:  "auggie",
		WorkingDir: "/proj",
		AuxiliaryModelSelection: &ACPServerConstraint{
			MatchMode: "contains",
			Pattern:   "Haiku",
		},
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got WorkspaceSettings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.AuxiliaryModelSelection == nil {
		t.Fatal("AuxiliaryModelSelection should not be nil after round-trip")
	}
	if got.AuxiliaryModelSelection.MatchMode != "contains" {
		t.Errorf("MatchMode = %q, want %q", got.AuxiliaryModelSelection.MatchMode, "contains")
	}
	if got.AuxiliaryModelSelection.Pattern != "Haiku" {
		t.Errorf("Pattern = %q, want %q", got.AuxiliaryModelSelection.Pattern, "Haiku")
	}
}

func TestWorkspaceSettings_AuxiliaryModelSelection_JSONOmitempty(t *testing.T) {
	// When AuxiliaryModelSelection is nil, it should be omitted from JSON
	w := WorkspaceSettings{
		ACPServer:  "auggie",
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
	if _, ok := raw["auxiliary_model_selection"]; ok {
		t.Error("auxiliary_model_selection should be omitted from JSON when nil")
	}
	// Also confirm old field is gone
	if _, ok := raw["auxiliary_acp_server"]; ok {
		t.Error("auxiliary_acp_server should not appear in serialized workspace JSON")
	}
}

// ---- LoadWorkspacesFromFile tests ----

func TestLoadWorkspacesFromFile_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[
		{"acp_server":"auggie","working_dir":"/proj1"},
		{"acp_server":"claude","working_dir":"/proj2"}
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
	content := "workspaces:\n  - acp_server: auggie\n    working_dir: /proj1\n"
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
	content := "workspaces:\n  - acp_server: claude\n    working_dir: /proj2\n"
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
	content := `{"workspaces":[{"acp_server":"auggie","working_dir":"/proj1"}]}`
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
	content := `{"workspaces":[{"acp_server":"auggie","working_dir":"/proj1","restricted_runner":"invalid-runner"}]}`
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
			{"acp_server": "auggie", "working_dir": "/path/to/project1"},
			{"acp_server": "claude", "working_dir": "/path/to/project2"}
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
		{ACPServer: "test-server", WorkingDir: "/test/path"},
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

// TestWorkspaceSettings_RedundantFields_NotSerialized verifies that removed fields
// (acp_command, acp_cwd, acp_env) are NOT present in the serialized JSON output.
// These fields were removed in favour of runtime resolution from global ACP server config.
func TestWorkspaceSettings_RedundantFields_NotSerialized(t *testing.T) {
	w := WorkspaceSettings{
		ACPServer:  "auggie",
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
	for _, forbidden := range []string{"acp_command", "acp_cwd", "acp_env"} {
		if _, ok := raw[forbidden]; ok {
			t.Errorf("%q should not appear in serialized workspace JSON", forbidden)
		}
	}
}

// TestLoadWorkspacesFromFile_OldFormatIgnored verifies that workspaces.json files
// containing the legacy acp_command/acp_env fields (from before the runtime-resolution
// refactor) load without error and those fields are silently ignored.
func TestLoadWorkspacesFromFile_OldFormatIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"workspaces":[{
		"acp_server":"auggie",
		"acp_command":"auggie --acp",
		"working_dir":"/proj1",
		"acp_env":{"NODE_OPTIONS":"--max-old-space-size=8192"}
	}]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	workspaces, err := LoadWorkspacesFromFile(path)
	if err != nil {
		t.Fatalf("old-format JSON must load without error: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(workspaces))
	}
	// The important fields are preserved; the legacy cached fields are discarded.
	if workspaces[0].ACPServer != "auggie" {
		t.Errorf("ACPServer = %q, want %q", workspaces[0].ACPServer, "auggie")
	}
	if workspaces[0].WorkingDir != "/proj1" {
		t.Errorf("WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/proj1")
	}
}
