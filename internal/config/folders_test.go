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
	if fs.Name != "Project" || fs.Color != "#ff5500" || fs.Code != "PRJ" {
		t.Errorf("folder fields = %+v, want Name=Project Color=#ff5500 Code=PRJ", fs)
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

func TestDedupFolders_AllSameHoisted(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc", AutoChildren: []AutoChild{{Title: "Reviewer"}}},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc", AutoChildren: []AutoChild{{Title: "Reviewer"}}},
	}
	cleaned, folders := dedupFolders(ws)
	fs, ok := folders["/proj"]
	if !ok {
		t.Fatalf("folders missing /proj")
	}
	if fs.Name != "P" || fs.Code != "PRJ" || fs.Color != "#abc" {
		t.Errorf("hoisted folder = %+v", fs)
	}
	if len(fs.AutoChildren) != 1 || fs.AutoChildren[0].Title != "Reviewer" {
		t.Errorf("hoisted AutoChildren = %+v", fs.AutoChildren)
	}
	for i := range cleaned {
		if cleaned[i].Name != "" || cleaned[i].Code != "" || cleaned[i].Color != "" || cleaned[i].AutoChildren != nil {
			t.Errorf("cleaned[%d] not cleared: %+v", i, cleaned[i])
		}
	}
}

func TestDedupFolders_DivergentNotHoisted(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "A", Code: "PRJ"},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "B", Code: "PRJ"},
	}
	cleaned, folders := dedupFolders(ws)
	if cleaned[0].Name != "A" || cleaned[1].Name != "B" {
		t.Errorf("divergent Name should be preserved: %q, %q", cleaned[0].Name, cleaned[1].Name)
	}
	fs := folders["/proj"]
	if fs.Name != "" {
		t.Errorf("divergent Name should not be hoisted, got %q", fs.Name)
	}
	if fs.Code != "PRJ" {
		t.Errorf("matching Code should be hoisted, got %q", fs.Code)
	}
	if cleaned[0].Code != "" || cleaned[1].Code != "" {
		t.Errorf("hoisted Code should be cleared: %q, %q", cleaned[0].Code, cleaned[1].Code)
	}
}

func TestDedupFolders_SingleWorkspace(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj", Name: "P", Code: "PRJ", Color: "#abc"},
	}
	cleaned, folders := dedupFolders(ws)
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

func TestDedupFolders_EmptyWorkingDirSkipped(t *testing.T) {
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "", Name: "P", Code: "PRJ"},
	}
	cleaned, folders := dedupFolders(ws)
	if len(folders) != 0 {
		t.Errorf("empty working_dir should not be hoisted, folders = %+v", folders)
	}
	if cleaned[0].Name != "P" || cleaned[0].Code != "PRJ" {
		t.Errorf("empty working_dir workspace should be untouched: %+v", cleaned[0])
	}
}

func TestApplyFolderDefaults_PopulatesEmpties_PreservesOverrides(t *testing.T) {
	folders := map[string]FolderSettings{
		"/proj": {Name: "A"},
	}
	ws := []WorkspaceSettings{
		{ACPServer: "auggie", WorkingDir: "/proj"},
		{ACPServer: "claude", WorkingDir: "/proj", Name: "B"},
	}
	applyFolderDefaults(ws, folders)
	if ws[0].Name != "A" {
		t.Errorf("empty Name should be populated to A, got %q", ws[0].Name)
	}
	if ws[1].Name != "B" {
		t.Errorf("override Name should be preserved as B, got %q", ws[1].Name)
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
