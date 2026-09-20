package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMemoryCurationState_RoundTrip covers the mitto-1kl AC's "persisted
// workspace state" requirement: a written ledger is read back byte-for-byte
// for the fields that matter to the gating decision.
func TestMemoryCurationState_RoundTrip(t *testing.T) {
	baseDir := t.TempDir()
	const workspaceUUID = "ws-round-trip"
	want := MemoryCurationState{
		Version:            MemoryCurationSidecarVersion,
		LastRunAt:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		LastRunMemoryCount: 42,
	}
	if err := WriteMemoryCurationState(baseDir, workspaceUUID, want); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	got, err := ReadMemoryCurationState(baseDir, workspaceUUID)
	if err != nil {
		t.Fatalf("ReadMemoryCurationState() error = %v", err)
	}
	if !got.LastRunAt.Equal(want.LastRunAt) || got.LastRunMemoryCount != want.LastRunMemoryCount {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	path := filepath.Join(baseDir, workspaceUUID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected ledger file at %s: %v", path, err)
	}
}

// TestReadMemoryCurationState_MissingFile documents the AC's backward-
// compatible default: a workspace with no prior run (fresh install, or an
// upgrade from before mitto-1kl) must read as the zero-value state rather
// than erroring, so the very first close's interval gate passes immediately.
func TestReadMemoryCurationState_MissingFile(t *testing.T) {
	baseDir := t.TempDir()
	got, err := ReadMemoryCurationState(baseDir, "ws-never-run")
	if err != nil {
		t.Fatalf("ReadMemoryCurationState() error = %v, want nil for a missing file", err)
	}
	if got.Version != MemoryCurationSidecarVersion {
		t.Errorf("Version = %d, want current version %d", got.Version, MemoryCurationSidecarVersion)
	}
	if !got.LastRunAt.IsZero() || got.LastRunMemoryCount != 0 || got.InFlightRunID != "" {
		t.Errorf("missing-file state = %+v, want the zero value (interval/threshold gates must pass on first-ever close)", got)
	}
}

// TestReadMemoryCurationState_UpgradesZeroVersionOnRead covers reading a
// ledger written by a hypothetical pre-versioning writer (or a hand-edited
// file) with version 0: it must be normalized to the current schema version
// on read rather than propagating 0 into the gating logic.
func TestReadMemoryCurationState_UpgradesZeroVersionOnRead(t *testing.T) {
	baseDir := t.TempDir()
	const workspaceUUID = "ws-legacy"
	raw, err := json.Marshal(map[string]any{
		"last_run_memory_count": 7,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, workspaceUUID+".json"), raw, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ReadMemoryCurationState(baseDir, workspaceUUID)
	if err != nil {
		t.Fatalf("ReadMemoryCurationState() error = %v", err)
	}
	if got.Version != MemoryCurationSidecarVersion {
		t.Errorf("Version = %d, want normalized to current version %d", got.Version, MemoryCurationSidecarVersion)
	}
	if got.LastRunMemoryCount != 7 {
		t.Errorf("LastRunMemoryCount = %d, want 7 (preserved through the version normalization)", got.LastRunMemoryCount)
	}
}

// TestWriteMemoryCurationState_EmptyWorkspaceUUID_NoOp documents that an
// empty workspace UUID is a deliberate no-op (never touches disk), matching
// ReadMemoryCurationState's symmetric short-circuit for the same input.
func TestWriteMemoryCurationState_EmptyWorkspaceUUID_NoOp(t *testing.T) {
	baseDir := t.TempDir()
	if err := WriteMemoryCurationState(baseDir, "", MemoryCurationState{LastRunMemoryCount: 99}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v, want nil no-op", err)
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("baseDir has %d entries, want 0 (empty workspace UUID must not write to disk)", len(entries))
	}
}

// TestReadMemoryCurationState_EmptyWorkspaceUUID_ReturnsZeroValue mirrors the
// write-side no-op: reading with an empty workspace UUID must not touch disk
// and must return the zero-value state.
func TestReadMemoryCurationState_EmptyWorkspaceUUID_ReturnsZeroValue(t *testing.T) {
	baseDir := t.TempDir()
	got, err := ReadMemoryCurationState(baseDir, "")
	if err != nil {
		t.Fatalf("ReadMemoryCurationState() error = %v", err)
	}
	if got.Version != MemoryCurationSidecarVersion || !got.LastRunAt.IsZero() || got.LastRunMemoryCount != 0 {
		t.Errorf("empty-UUID state = %+v, want the zero value", got)
	}
}
