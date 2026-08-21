package processors

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileStateStore_SaveLoad_RoundTrip is the baseline: Save then Load
// returns the same data when the session directory exists.
func TestFileStateStore_SaveLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileStateStore{}

	state := &ProcessorStateData{
		AgentResponseCount: 3,
		Processors: map[string]*ProcessorCadenceState{
			"my-processor": {TurnsSinceLastFire: 2, TokensSinceLastFire: 100},
		},
	}
	if err := store.Save(tmpDir, state); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(tmpDir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.AgentResponseCount != 3 {
		t.Errorf("AgentResponseCount = %d, want 3", loaded.AgentResponseCount)
	}
	if loaded.Processors["my-processor"].TurnsSinceLastFire != 2 {
		t.Errorf("TurnsSinceLastFire = %d, want 2", loaded.Processors["my-processor"].TurnsSinceLastFire)
	}
}

// TestFileStateStore_Save_EmptySessionDir mirrors the existing no-op guard
// for an unconfigured (test-only) session dir: Save must return nil and
// write nothing.
func TestFileStateStore_Save_EmptySessionDir(t *testing.T) {
	store := &FileStateStore{}
	if err := store.Save("", &ProcessorStateData{}); err != nil {
		t.Fatalf("Save with empty sessionDir should be a no-op, got error: %v", err)
	}
}

// TestFileStateStore_Save_DeletedSessionDir pins the mitto-32ef fix: when
// the session directory has been removed (simulating a concurrent
// Store.Delete racing this Save call, e.g. from apply.go's after-phase
// pipeline), Save must NOT recreate the directory. It should return nil
// (a benign no-op — apply.go's only caller merely logs a WARN on any
// error) and leave no processor_state.json orphan behind.
func TestFileStateStore_Save_DeletedSessionDir(t *testing.T) {
	tmpDir := t.TempDir()
	deletedSessionDir := filepath.Join(tmpDir, "deleted-session")
	// Note: deletedSessionDir is deliberately never created, simulating
	// Store.Delete having already run os.RemoveAll on it.

	store := &FileStateStore{}
	if err := store.Save(deletedSessionDir, &ProcessorStateData{AgentResponseCount: 1}); err != nil {
		t.Fatalf("Save on a deleted session dir should be a benign no-op, got error: %v", err)
	}

	if _, statErr := os.Stat(deletedSessionDir); !os.IsNotExist(statErr) {
		t.Errorf("expected %q to remain absent (no orphan created), but it exists", deletedSessionDir)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("tmpDir should contain no orphan entries, got: %v", entries)
	}
}

// TestFileStateStore_Load_MissingFile confirms Load's existing first-run
// contract (zero-value state, no error) is unaffected by the Save change.
func TestFileStateStore_Load_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileStateStore{}

	state, err := store.Load(tmpDir)
	if err != nil {
		t.Fatalf("Load on first run should not error, got: %v", err)
	}
	if state.AgentResponseCount != 0 {
		t.Errorf("AgentResponseCount = %d, want 0", state.AgentResponseCount)
	}
	if state.Processors == nil {
		t.Error("Processors map should be initialized, got nil")
	}
}
