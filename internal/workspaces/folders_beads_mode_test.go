package workspaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderBeadsDatabaseMode_RoundTrip(t *testing.T) {
	for _, mode := range []BeadsDatabaseMode{BeadsDatabaseModeLocal, BeadsDatabaseModeShared} {
		t.Run(string(mode), func(t *testing.T) {
			tmpDir := setupFoldersTestDir(t)
			if err := SetFolderBeadsDatabaseMode("/proj", mode); err != nil {
				t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
			}
			got, configured, err := ConfiguredFolderBeadsDatabaseMode("/proj")
			if err != nil {
				t.Fatalf("ConfiguredFolderBeadsDatabaseMode() error = %v", err)
			}
			if !configured || got != mode {
				t.Fatalf("ConfiguredFolderBeadsDatabaseMode() = (%q, %v), want (%q, true)", got, configured, mode)
			}
			raw, err := os.ReadFile(filepath.Join(tmpDir, "folders.json"))
			if err != nil {
				t.Fatalf("ReadFile(folders.json) error = %v", err)
			}
			var file FoldersFile
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatalf("json.Unmarshal(folders.json) error = %v", err)
			}
			if got := file.Folders["/proj"].Beads.DatabaseMode; got != mode {
				t.Errorf("persisted database mode = %q, want %q", got, mode)
			}
		})
	}
}

func TestFolderBeadsDatabaseMode_Validation(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SetFolderBeadsDatabaseMode("/proj", "invalid"); err == nil {
		t.Fatal("SetFolderBeadsDatabaseMode(invalid) error = nil, want validation error")
	}
	if err := SaveFolders(map[string]FolderSettings{
		"/proj": {Beads: &BeadsFolderSettings{DatabaseMode: "invalid"}},
	}); err == nil {
		t.Fatal("SaveFolders(invalid mode) error = nil, want validation error")
	}

	for _, tc := range []struct {
		name, ext, body string
	}{
		{"json", ".json", `{"folders":{"/proj":{"beads":{"databaseMode":"invalid"}}}}`},
		{"yaml", ".yaml", "folders:\n  /proj:\n    beads:\n      databaseMode: invalid\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "folders"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			if _, err := LoadFoldersFromFile(path); err == nil {
				t.Fatal("LoadFoldersFromFile(invalid mode) error = nil, want validation error")
			}
		})
	}
}

func TestLoadFoldersFromFile_DatabaseMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		ext  string
		body string
		want BeadsDatabaseMode
	}{
		{"json local", ".json", `{"folders":{"/proj":{"beads":{"databaseMode":"local"}}}}`, BeadsDatabaseModeLocal},
		{"json shared", ".json", `{"folders":{"/proj":{"beads":{"databaseMode":"shared"}}}}`, BeadsDatabaseModeShared},
		{"yaml local", ".yaml", "folders:\n  /proj:\n    beads:\n      databaseMode: local\n", BeadsDatabaseModeLocal},
		{"yaml shared", ".yaml", "folders:\n  /proj:\n    beads:\n      databaseMode: shared\n", BeadsDatabaseModeShared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "folders"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			folders, err := LoadFoldersFromFile(path)
			if err != nil {
				t.Fatalf("LoadFoldersFromFile() error = %v", err)
			}
			if got := folders["/proj"].Beads.DatabaseMode; got != tc.want {
				t.Errorf("database mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetFolderBeadsDatabaseMode_PreservesUpstreamConfiguration(t *testing.T) {
	setupFoldersTestDir(t)
	pullArgs := map[string]string{"IssueID": "mitto-1"}
	if err := SetFolderBeadsPromptUpstream("/proj", "Pull", "Push", "Sync", pullArgs, nil, nil); err != nil {
		t.Fatalf("SetFolderBeadsPromptUpstream() error = %v", err)
	}
	if err := SetFolderBeadsDatabaseMode("/proj", BeadsDatabaseModeLocal); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	folders, err := LoadFolders()
	if err != nil {
		t.Fatalf("LoadFolders() error = %v", err)
	}
	beads := folders["/proj"].Beads
	if beads == nil || beads.DatabaseMode != BeadsDatabaseModeLocal || beads.Upstream != "prompts" ||
		beads.PullPrompt != "Pull" || beads.PushPrompt != "Push" || beads.SyncPrompt != "Sync" ||
		beads.PullPromptArgs["IssueID"] != "mitto-1" {
		t.Fatalf("beads settings not preserved: %+v", beads)
	}

	if err := SetFolderBeadsUpstream("/proj", "none"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream(none) error = %v", err)
	}
	mode, configured, err := ConfiguredFolderBeadsDatabaseMode("/proj")
	if err != nil || !configured || mode != BeadsDatabaseModeLocal {
		t.Fatalf("mode after clearing upstream = (%q, %v, %v), want (local, true, nil)", mode, configured, err)
	}
}

func TestSaveWorkspaces_PreservesSharedFolderDatabaseMode(t *testing.T) {
	setupFoldersTestDir(t)
	const workingDir = "/proj"
	workspaces := []WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: workingDir, Name: "Project"},
		{UUID: "u2", ACPServer: "claude", WorkingDir: workingDir, Name: "Project"},
	}
	if err := SaveWorkspaces(workspaces); err != nil {
		t.Fatalf("SaveWorkspaces(initial) error = %v", err)
	}
	if err := SetFolderBeadsDatabaseMode(workingDir, BeadsDatabaseModeShared); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	if err := SaveWorkspaces(workspaces); err != nil {
		t.Fatalf("SaveWorkspaces(second) error = %v", err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len(LoadWorkspaces()) = %d, want 2 ACP workspaces", len(loaded))
	}
	for _, workspace := range loaded {
		mode, configured, err := ConfiguredFolderBeadsDatabaseMode(workspace.WorkingDir)
		if err != nil || !configured || mode != BeadsDatabaseModeShared {
			t.Errorf("workspace %s mode = (%q, %v, %v), want shared", workspace.UUID, mode, configured, err)
		}
	}
}

func TestConfiguredFolderBeadsDatabaseMode_LegacyMissingValue(t *testing.T) {
	setupFoldersTestDir(t)
	if err := SaveFolders(map[string]FolderSettings{"/proj": {Beads: &BeadsFolderSettings{Upstream: "jira"}}}); err != nil {
		t.Fatalf("SaveFolders() error = %v", err)
	}
	mode, configured, err := ConfiguredFolderBeadsDatabaseMode("/proj")
	if err != nil || configured || mode != "" {
		t.Fatalf("ConfiguredFolderBeadsDatabaseMode() = (%q, %v, %v), want (empty, false, nil)", mode, configured, err)
	}
}
