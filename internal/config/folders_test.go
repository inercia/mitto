package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// setupFoldersTestDir points MITTO_DIR at a fresh temp dir and resets caches.
func setupFoldersTestDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return tmpDir
}

func TestLoadFolders_NoFile(t *testing.T) {
	setupFoldersTestDir(t)
	folders, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() returned error: %v", err)
	}
	if folders != nil {
		t.Errorf("LoadFolders() = %v, want nil", folders)
	}
}

func TestSaveFolders_LoadFolders_RoundTrip(t *testing.T) {
	setupFoldersTestDir(t)
	in := map[string]FolderSettings{
		"/proj": {
			Name:         "Project",
			Color:        "#ff5500",
			Code:         "PRJ",
			Group:        "development",
			AutoChildren: []AutoChild{{Title: "Reviewer"}},
		},
	}
	if err := SaveFolders(in); err != nil {
		t.Fatalf("SaveFolders() returned error: %v", err)
	}
	out, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() returned error: %v", err)
	}
	fs, ok := out["/proj"]
	if !ok {
		t.Fatalf("folder /proj missing after round-trip")
	}
	if fs.Name != "Project" || fs.Color != "#ff5500" || fs.Code != "PRJ" || fs.Group != "development" {
		t.Errorf("folder fields = %+v, want Name=Project Color=#ff5500 Code=PRJ Group=development", fs)
	}
	if len(fs.AutoChildren) != 1 || fs.AutoChildren[0].Title != "Reviewer" {
		t.Errorf("AutoChildren = %+v, want one Reviewer", fs.AutoChildren)
	}
}

func TestSaveFolders_EmptyRemovesFile(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SaveFolders(map[string]FolderSettings{"/proj": {Name: "P"}}); err != nil {
		t.Fatalf("SaveFolders() returned error: %v", err)
	}
	path, err := appdir.FoldersPath()
	if err != nil {
		t.Fatalf("FoldersPath() returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("folders.json should exist before empty save: %v", err)
	}
	if err := SaveFolders(map[string]FolderSettings{}); err != nil {
		t.Fatalf("SaveFolders(empty) returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("folders.json should be removed after empty save, stat err = %v", err)
	}
}

func TestExtractFolderSettings_AllSameHoisted(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc", Group: "development", AutoChildren: []AutoChild{{Title: "Reviewer"}}},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc", Group: "development", AutoChildren: []AutoChild{{Title: "Reviewer"}}},
	}
	cleaned, folders := extractFolderSettings(ws)
	fs, ok := folders["/proj"]
	if !ok {
		t.Fatalf("folders missing /proj")
	}
	if fs.Name != "P" || fs.Code != "PRJ" || fs.Color != "#abc" || fs.Group != "development" {
		t.Errorf("hoisted folder = %+v", fs)
	}
	if len(fs.AutoChildren) != 1 || fs.AutoChildren[0].Title != "Reviewer" {
		t.Errorf("hoisted AutoChildren = %+v", fs.AutoChildren)
	}
	for i := range cleaned {
		if cleaned[i].Name != "" || cleaned[i].Code != "" || cleaned[i].Color != "" || cleaned[i].Group != "" || cleaned[i].AutoChildren != nil {
			t.Errorf("cleaned[%d] not cleared: %+v", i, cleaned[i])
		}
	}
}

func TestExtractFolderSettings_DivergentCollapses(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "A", Code: "PRJ"},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "B", Code: "PRJ"},
	}
	cleaned, folders := extractFolderSettings(ws)
	// folders.json is authoritative: divergent values collapse onto the first
	// non-empty value and are stripped from every workspace.
	if cleaned[0].Name != "" || cleaned[1].Name != "" {
		t.Errorf("divergent Name should be stripped from workspaces: %q, %q", cleaned[0].Name, cleaned[1].Name)
	}
	fs := folders["/proj"]
	if fs.Name != "A" {
		t.Errorf("divergent Name should collapse to first non-empty (A), got %q", fs.Name)
	}
	if fs.Code != "PRJ" {
		t.Errorf("matching Code should be hoisted, got %q", fs.Code)
	}
	if cleaned[0].Code != "" || cleaned[1].Code != "" {
		t.Errorf("hoisted Code should be cleared: %q, %q", cleaned[0].Code, cleaned[1].Code)
	}
}

func TestExtractFolderSettings_SingleWorkspace(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
	}
	cleaned, folders := extractFolderSettings(ws)
	fs, ok := folders["/proj"]
	if !ok {
		t.Fatalf("folders missing /proj")
	}
	if fs.Name != "P" || fs.Code != "PRJ" || fs.Color != "#abc" {
		t.Errorf("hoisted folder = %+v", fs)
	}
	if cleaned[0].Name != "" || cleaned[0].Code != "" || cleaned[0].Color != "" {
		t.Errorf("cleaned not cleared: %+v", cleaned[0])
	}
}

func TestExtractFolderSettings_EmptyWorkingDirSkipped(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "", Name: "P", Code: "PRJ"},
	}
	cleaned, folders := extractFolderSettings(ws)
	if len(folders) != 0 {
		t.Errorf("empty working_dir should not be hoisted, folders = %+v", folders)
	}
	if cleaned[0].Name != "P" || cleaned[0].Code != "PRJ" {
		t.Errorf("empty working_dir workspace should be untouched: %+v", cleaned[0])
	}
}

func TestApplyFolderDefaults_FolderValueWins(t *testing.T) {
	folders := map[string]FolderSettings{
		"/proj": {Name: "A"},
	}
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj"},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "B"},
	}
	ApplyFolderDefaults(ws, folders)
	// folders.json is authoritative, so the folder value overwrites both the
	// empty workspace and the divergent override.
	if ws[0].Name != "A" {
		t.Errorf("empty Name should be populated to A, got %q", ws[0].Name)
	}
	if ws[1].Name != "A" {
		t.Errorf("override Name should collapse to folder value A, got %q", ws[1].Name)
	}
}

func TestLoadWorkspaces_MigratesDuplicatedFile(t *testing.T) {
	tmpDir := setupFoldersTestDir(t)
	workspacesPath := filepath.Join(tmpDir, appdir.WorkspacesFileName)
	content := `{"workspaces":[
		{"acp_server":"auggie","working_dir":"/proj","name":"P","code":"PRJ","color":"#abc"},
		{"acp_server":"claude","working_dir":"/proj","name":"P","code":"PRJ","color":"#abc"}
	]}`
	if err := os.WriteFile(workspacesPath, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() returned error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(loaded))
	}
	for i := range loaded {
		if loaded[i].Name != "P" || loaded[i].Code != "PRJ" || loaded[i].Color != "#abc" {
			t.Errorf("in-memory workspace[%d] not fully populated: %+v", i, loaded[i])
		}
	}

	// folders.json must now exist with the hoisted folder entry.
	foldersPath := filepath.Join(tmpDir, appdir.FoldersFileName)
	if _, err := os.Stat(foldersPath); err != nil {
		t.Fatalf("folders.json should exist after migration: %v", err)
	}
	folders, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() returned error: %v", err)
	}
	fs, ok := folders["/proj"]
	if !ok || fs.Name != "P" || fs.Code != "PRJ" || fs.Color != "#abc" {
		t.Errorf("folders entry wrong: %+v (ok=%v)", fs, ok)
	}

	// workspaces.json entries must have folder-level fields cleared on disk.
	var raw WorkspacesFile
	data, err := os.ReadFile(workspacesPath)
	if err != nil {
		t.Fatalf("read workspaces.json: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal workspaces.json: %v", err)
	}
	for i := range raw.Workspaces {
		if raw.Workspaces[i].Name != "" || raw.Workspaces[i].Code != "" || raw.Workspaces[i].Color != "" {
			t.Errorf("on-disk workspace[%d] not cleared: %+v", i, raw.Workspaces[i])
		}
	}
}

func TestLoadWorkspaces_Idempotent(t *testing.T) {
	tmpDir := setupFoldersTestDir(t)
	workspacesPath := filepath.Join(tmpDir, appdir.WorkspacesFileName)
	foldersPath := filepath.Join(tmpDir, appdir.FoldersFileName)
	content := `{"workspaces":[
		{"acp_server":"auggie","working_dir":"/proj","name":"P","code":"PRJ","color":"#abc"},
		{"acp_server":"claude","working_dir":"/proj","name":"P","code":"PRJ","color":"#abc"}
	]}`
	if err := os.WriteFile(workspacesPath, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := LoadWorkspaces(); err != nil {
		t.Fatalf("first LoadWorkspaces() error: %v", err)
	}
	wsBytes1, err := os.ReadFile(workspacesPath)
	if err != nil {
		t.Fatalf("read workspaces.json: %v", err)
	}
	folBytes1, err := os.ReadFile(foldersPath)
	if err != nil {
		t.Fatalf("read folders.json: %v", err)
	}

	if _, err := LoadWorkspaces(); err != nil {
		t.Fatalf("second LoadWorkspaces() error: %v", err)
	}
	wsBytes2, err := os.ReadFile(workspacesPath)
	if err != nil {
		t.Fatalf("read workspaces.json: %v", err)
	}
	folBytes2, err := os.ReadFile(foldersPath)
	if err != nil {
		t.Fatalf("read folders.json: %v", err)
	}

	if string(wsBytes1) != string(wsBytes2) {
		t.Error("workspaces.json was rewritten on idempotent load")
	}
	if string(folBytes1) != string(folBytes2) {
		t.Error("folders.json was rewritten on idempotent load")
	}
}

func TestSaveWorkspaces_DedupAndReloadPopulates(t *testing.T) {
	tmpDir := setupFoldersTestDir(t)
	ws := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
		{UUID: "u2", ACPServer: "claude", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
	}
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() returned error: %v", err)
	}
	foldersPath := filepath.Join(tmpDir, appdir.FoldersFileName)
	if _, err := os.Stat(foldersPath); err != nil {
		t.Fatalf("folders.json should exist: %v", err)
	}

	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() returned error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(loaded))
	}
	for i := range loaded {
		if loaded[i].Name != "P" || loaded[i].Code != "PRJ" || loaded[i].Color != "#abc" {
			t.Errorf("reloaded workspace[%d] not populated: %+v", i, loaded[i])
		}
	}
}

func TestSaveWorkspaces_OrphanFolderPruned(t *testing.T) {
	setupFoldersTestDir(t)
	pre := map[string]FolderSettings{
		"/orphan": {Name: "Old"},
	}
	if err := SaveFolders(pre); err != nil {
		t.Fatalf("SaveFolders() returned error: %v", err)
	}

	ws := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/proj", Name: "P"},
	}
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() returned error: %v", err)
	}

	folders, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() returned error: %v", err)
	}
	if _, ok := folders["/orphan"]; ok {
		t.Error("orphan folder entry should be pruned after SaveWorkspaces")
	}
	if _, ok := folders["/proj"]; !ok {
		t.Error("active folder /proj should be present")
	}
}

func TestSetAndGetFolderBeadsUpstream(t *testing.T) {
	setupFoldersTestDir(t)
	if got := FolderBeadsUpstream("/proj"); got != "" {
		t.Errorf("FolderBeadsUpstream() before set = %q, want empty", got)
	}
	if err := SetFolderBeadsUpstream("/proj", "jira"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}
	if got := FolderBeadsUpstream("/proj"); got != "jira" {
		t.Errorf("FolderBeadsUpstream() = %q, want jira", got)
	}
	// Setting to "none" clears the folder entry.
	if err := SetFolderBeadsUpstream("/proj", "none"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream(none) returned error: %v", err)
	}
	if got := FolderBeadsUpstream("/proj"); got != "" {
		t.Errorf("FolderBeadsUpstream() after clear = %q, want empty", got)
	}
}

func TestSetFolderBeadsUpstream_PreservesDedupFields(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SaveFolders(map[string]FolderSettings{"/proj": {Name: "P", Code: "PRJ"}}); err != nil {
		t.Fatalf("SaveFolders() returned error: %v", err)
	}
	if err := SetFolderBeadsUpstream("/proj", "github"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}
	folders, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() returned error: %v", err)
	}
	fs := folders["/proj"]
	if fs.Name != "P" || fs.Code != "PRJ" {
		t.Errorf("dedup fields lost: %+v", fs)
	}
	if fs.Beads == nil || fs.Beads.Upstream != "github" {
		t.Errorf("beads upstream not set: %+v", fs.Beads)
	}
}

// SaveWorkspaces must not wipe folder-native beads settings, since
// extractFolderSettings produces only workspace-derived fields.
func TestSaveWorkspaces_PreservesBeadsUpstream(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SetFolderBeadsUpstream("/proj", "gitlab"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}

	ws := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
		{UUID: "u2", ACPServer: "claude", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
	}
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() returned error: %v", err)
	}

	if got := FolderBeadsUpstream("/proj"); got != "gitlab" {
		t.Errorf("FolderBeadsUpstream() after SaveWorkspaces = %q, want gitlab", got)
	}
}

// Beads for a working_dir no longer referenced by any workspace is pruned.
func TestSaveWorkspaces_OrphanBeadsPruned(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SetFolderBeadsUpstream("/orphan", "jira"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}

	ws := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/proj", Name: "P"},
	}
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() returned error: %v", err)
	}

	if got := FolderBeadsUpstream("/orphan"); got != "" {
		t.Errorf("orphan beads upstream should be pruned, got %q", got)
	}
}

func TestSetFolderBeadsPromptUpstream_RoundTrip(t *testing.T) {
	setupFoldersTestDir(t)

	// Before any set, getters return empty.
	if got := FolderBeadsUpstream("/proj"); got != "" {
		t.Errorf("FolderBeadsUpstream() before set = %q, want empty", got)
	}
	pull, push, sync := FolderBeadsPrompts("/proj")
	if pull != "" || push != "" || sync != "" {
		t.Errorf("FolderBeadsPrompts() before set = (%q,%q,%q), want all empty", pull, push, sync)
	}

	// Set prompts upstream with three names.
	if err := SetFolderBeadsPromptUpstream("/proj", "My Pull", "My Push", "My Sync"); err != nil {
		t.Fatalf("SetFolderBeadsPromptUpstream() returned error: %v", err)
	}

	if got := FolderBeadsUpstream("/proj"); got != "prompts" {
		t.Errorf("FolderBeadsUpstream() = %q, want prompts", got)
	}
	pull, push, sync = FolderBeadsPrompts("/proj")
	if pull != "My Pull" || push != "My Push" || sync != "My Sync" {
		t.Errorf("FolderBeadsPrompts() = (%q,%q,%q), want (My Pull,My Push,My Sync)", pull, push, sync)
	}
}

func TestSetFolderBeadsUpstream_ClearsPromptNames(t *testing.T) {
	setupFoldersTestDir(t)

	// Set prompts upstream first.
	if err := SetFolderBeadsPromptUpstream("/proj", "Pull", "Push", "Sync"); err != nil {
		t.Fatalf("SetFolderBeadsPromptUpstream() returned error: %v", err)
	}

	// Switch to a regular tracker — prompt names must be cleared.
	if err := SetFolderBeadsUpstream("/proj", "jira"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}
	if got := FolderBeadsUpstream("/proj"); got != "jira" {
		t.Errorf("FolderBeadsUpstream() = %q, want jira", got)
	}
	pull, push, sync := FolderBeadsPrompts("/proj")
	if pull != "" || push != "" || sync != "" {
		t.Errorf("FolderBeadsPrompts() after switch to jira = (%q,%q,%q), want all empty", pull, push, sync)
	}
}

func TestSaveWorkspaces_PreservesBeadsPromptUpstream(t *testing.T) {
	setupFoldersTestDir(t)

	// Register /proj as a valid workspace directory before persisting.
	ws := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/proj", Name: "P"},
	}
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() initial returned error: %v", err)
	}

	if err := SetFolderBeadsPromptUpstream("/proj", "Pull", "Push", "Sync"); err != nil {
		t.Fatalf("SetFolderBeadsPromptUpstream() returned error: %v", err)
	}

	// A second workspace save must not wipe the prompt names.
	if err := SaveWorkspaces(ws); err != nil {
		t.Fatalf("SaveWorkspaces() second returned error: %v", err)
	}

	if got := FolderBeadsUpstream("/proj"); got != "prompts" {
		t.Errorf("FolderBeadsUpstream() after SaveWorkspaces = %q, want prompts", got)
	}
	pull, push, sync := FolderBeadsPrompts("/proj")
	if pull != "Pull" || push != "Push" || sync != "Sync" {
		t.Errorf("FolderBeadsPrompts() after SaveWorkspaces = (%q,%q,%q), want (Pull,Push,Sync)", pull, push, sync)
	}
}

// ---- LoadFoldersFromFile tests ----

func TestLoadFoldersFromFile_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folders.json")
	content := `{"folders":{"/proj":{"name":"P","code":"PRJ","beads":{"upstream":"github"}}}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	folders, err := LoadFoldersFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs, ok := folders["/proj"]
	if !ok {
		t.Fatal("expected entry for /proj")
	}
	if fs.Name != "P" {
		t.Errorf("Name = %q, want %q", fs.Name, "P")
	}
	if fs.Code != "PRJ" {
		t.Errorf("Code = %q, want %q", fs.Code, "PRJ")
	}
	if fs.Beads == nil || fs.Beads.Upstream != "github" {
		t.Errorf("Beads.Upstream = %v, want %q", fs.Beads, "github")
	}
}

func TestLoadFoldersFromFile_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folders.yaml")
	content := "folders:\n  /proj:\n    name: P\n    auto_children:\n      - title: Reviewer\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	folders, err := LoadFoldersFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs, ok := folders["/proj"]
	if !ok {
		t.Fatal("expected entry for /proj")
	}
	if fs.Name != "P" {
		t.Errorf("Name = %q, want %q", fs.Name, "P")
	}
	if len(fs.AutoChildren) != 1 {
		t.Fatalf("len(AutoChildren) = %d, want 1", len(fs.AutoChildren))
	}
	if fs.AutoChildren[0].Title != "Reviewer" {
		t.Errorf("AutoChildren[0].Title = %q, want %q", fs.AutoChildren[0].Title, "Reviewer")
	}
}

func TestLoadFoldersFromFile_YML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folders.yml")
	content := "folders:\n  /proj:\n    name: P\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	folders, err := LoadFoldersFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := folders["/proj"]; !ok {
		t.Fatal("expected entry for /proj")
	}
}

func TestLoadFoldersFromFile_NotFound(t *testing.T) {
	_, err := LoadFoldersFromFile("/nonexistent/path/folders.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadFoldersFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad json}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadFoldersFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadFoldersFromFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":\t:\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadFoldersFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadFoldersFromFile_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folders.txt")
	if err := os.WriteFile(path, []byte("something"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadFoldersFromFile(path)
	if err == nil {
		t.Fatal("expected error for unsupported extension, got nil")
	}
}

func TestLoadFoldersFromFile_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folders.json")
	if err := os.WriteFile(path, []byte(`{"folders":{}}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	folders, err := LoadFoldersFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("expected empty map, got len=%d", len(folders))
	}
}
