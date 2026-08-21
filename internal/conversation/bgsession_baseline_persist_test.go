package conversation

// Reproduction tests for mitto-9yl Bug 1: cbInitBaselineModelIfEmpty seeds
// bs.baselineModel in memory but never persists to session metadata. As a
// result, backfill (which reads session.Metadata.BaselineModel from disk)
// attributes every historical token delta to the empty-string "Unknown"
// bucket for any session where the user never touched the model dropdown.
//
// These tests exercise cbInitBaselineModelIfEmpty directly with a real
// session.Store and assert what ends up persisted on disk. They fail on the
// current tree (the metadata write is missing) and will pass once Fix 1
// extends the callback to call cmPersistBaselineModel for the defaultModel
// branch.

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestCbInitBaselineModelIfEmpty_PersistsToMetadata is the primary Bug 1
// reproduction: on a fresh session (empty on-disk BaselineModel), calling
// the init callback with a non-empty default MUST both seed the in-memory
// field AND persist to metadata so downstream backfill can attribute tokens.
func TestCbInitBaselineModelIfEmpty_PersistsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sid = "test-session-baseline-persist"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bs := &BackgroundSession{persistedID: sid, store: store}
	bs.cbInitBaselineModelIfEmpty("claude-sonnet-4.5")

	// In-memory: seeded from the default. This part already works.
	if got := bs.GetBaselineModel(); got != "claude-sonnet-4.5" {
		t.Errorf("in-memory baselineModel = %q, want %q", got, "claude-sonnet-4.5")
	}

	// On-disk: MUST be persisted so backfill / resume see the same value.
	// This is the actual bug — the current callback never writes to metadata.
	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.BaselineModel != "claude-sonnet-4.5" {
		t.Errorf("persisted BaselineModel = %q, want %q "+
			"(cbInitBaselineModelIfEmpty must call cmPersistBaselineModel "+
			"when seeding from defaultModel — mitto-9yl Bug 1)",
			meta.BaselineModel, "claude-sonnet-4.5")
	}
}

// TestCbInitBaselineModelIfEmpty_DoesNotOverwritePersistedMetadata verifies
// Fix 1's guard: when metadata already carries a BaselineModel (a resumed
// session with a prior manual pick), the callback must respect it and never
// clobber it with the agent's currently-active model.
func TestCbInitBaselineModelIfEmpty_DoesNotOverwritePersistedMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sid = "test-session-baseline-preserve"
	if err := store.Create(session.Metadata{
		SessionID:     sid,
		ACPServer:     "test-server",
		WorkingDir:    "/tmp",
		Name:          "Test",
		BaselineModel: "model-A",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bs := &BackgroundSession{persistedID: sid, store: store}
	bs.cbInitBaselineModelIfEmpty("model-B")

	if got := bs.GetBaselineModel(); got != "model-A" {
		t.Errorf("in-memory baselineModel = %q, want %q (persisted value must win)", got, "model-A")
	}
	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.BaselineModel != "model-A" {
		t.Errorf("persisted BaselineModel = %q, want %q (must not be overwritten)",
			meta.BaselineModel, "model-A")
	}
}

// TestCbInitBaselineModelIfEmpty_EmptyDefaultDoesNotPersist verifies the
// guard on the persist side: if defaultModel is empty (agent didn't advertise
// a model) there is nothing meaningful to persist, so metadata stays untouched.
func TestCbInitBaselineModelIfEmpty_EmptyDefaultDoesNotPersist(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sid = "test-session-baseline-empty-default"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bs := &BackgroundSession{persistedID: sid, store: store}
	bs.cbInitBaselineModelIfEmpty("")

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.BaselineModel != "" {
		t.Errorf("persisted BaselineModel = %q, want empty (empty default must not persist)",
			meta.BaselineModel)
	}
}
