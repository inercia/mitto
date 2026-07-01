package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateRenamePeriodicToLoop_RenamesFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_loop_rename_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionDir := filepath.Join(tmpDir, "session-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	oldPath := filepath.Join(sessionDir, legacyPeriodicFileName)
	content := []byte(`{"prompt":"hello","frequency":{"value":1,"unit":"hours"},"enabled":true}`)
	if err := os.WriteFile(oldPath, content, 0644); err != nil {
		t.Fatalf("failed to write periodic.json: %v", err)
	}

	modified, err := migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if modified != 1 {
		t.Errorf("expected 1 session modified, got %d", modified)
	}

	newPath := filepath.Join(sessionDir, loopFileName)
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("periodic.json should no longer exist, stat err = %v", err)
	}
	got, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("loop.json should exist: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("loop.json content = %q, want %q (content must be unchanged)", got, content)
	}
}

func TestMigrateRenamePeriodicToLoop_IdempotentReRun(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_loop_rename_idempotent")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionDir := filepath.Join(tmpDir, "session-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	oldPath := filepath.Join(sessionDir, legacyPeriodicFileName)
	content := []byte(`{"prompt":"hello"}`)
	if err := os.WriteFile(oldPath, content, 0644); err != nil {
		t.Fatalf("failed to write periodic.json: %v", err)
	}

	// First run performs the rename.
	modified, err := migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if modified != 1 {
		t.Fatalf("expected 1 session modified on first run, got %d", modified)
	}

	// Second run should be a no-op (periodic.json no longer exists).
	modified, err = migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 sessions modified on idempotent re-run, got %d", modified)
	}

	newPath := filepath.Join(sessionDir, loopFileName)
	got, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("loop.json should still exist: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("loop.json content changed across idempotent re-run: %q, want %q", got, content)
	}
}

func TestMigrateRenamePeriodicToLoop_NoOpWhenAbsent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_loop_rename_absent")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionDir := filepath.Join(tmpDir, "session-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	// No periodic.json and no loop.json present.

	modified, err := migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 sessions modified when periodic.json absent, got %d", modified)
	}

	oldPath := filepath.Join(sessionDir, legacyPeriodicFileName)
	newPath := filepath.Join(sessionDir, loopFileName)
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("periodic.json should still be absent, stat err = %v", err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("loop.json should not have been created, stat err = %v", err)
	}
}

func TestMigrateRenamePeriodicToLoop_NoClobberExistingLoop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_loop_rename_noclobber")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionDir := filepath.Join(tmpDir, "session-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	oldPath := filepath.Join(sessionDir, legacyPeriodicFileName)
	oldContent := []byte(`{"prompt":"legacy"}`)
	if err := os.WriteFile(oldPath, oldContent, 0644); err != nil {
		t.Fatalf("failed to write periodic.json: %v", err)
	}

	newPath := filepath.Join(sessionDir, loopFileName)
	newContent := []byte(`{"prompt":"already-migrated"}`)
	if err := os.WriteFile(newPath, newContent, 0644); err != nil {
		t.Fatalf("failed to write loop.json: %v", err)
	}

	modified, err := migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 sessions modified when loop.json already exists, got %d", modified)
	}

	// Both files must be left untouched: existing loop.json is not clobbered,
	// and the (now orphaned but not lost) periodic.json is left in place for
	// manual inspection rather than being silently deleted.
	gotOld, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("periodic.json should still exist: %v", err)
	}
	if string(gotOld) != string(oldContent) {
		t.Errorf("periodic.json content changed, got %q, want %q", gotOld, oldContent)
	}
	gotNew, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("loop.json should still exist: %v", err)
	}
	if string(gotNew) != string(newContent) {
		t.Errorf("loop.json content was clobbered, got %q, want %q", gotNew, newContent)
	}
}

func TestMigrateRenamePeriodicToLoop_SkipsNonDirEntries(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_loop_rename_nondir")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// A stray file directly under baseDir (e.g. migrations.json) must not
	// cause an error or be treated as a session directory.
	strayFile := filepath.Join(tmpDir, "migrations.json")
	if err := os.WriteFile(strayFile, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write stray file: %v", err)
	}

	modified, err := migrateRenamePeriodicToLoop(tmpDir, nil)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 sessions modified, got %d", modified)
	}
}
