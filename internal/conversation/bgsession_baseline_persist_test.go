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

// TestSetAgentModels_ResumeWithoutConstraint_DoesNotReapplyPersistedBaseline is
// the reproduction test for mitto-1yo: on session resume, when metadata
// already carries a BaselineModel that differs from the agent's reported
// CurrentModelId and NO ACP-server model constraint governs the session, the
// persisted baseline is loaded into bs.baselineModel (cbInitBaselineModelIfEmpty)
// but is NEVER reflected back into the model config option's CurrentValue nor
// re-applied to the agent. The UI chip therefore shows the agent's default
// model instead of the user's prior manual selection, which is silently lost
// on every conversation switch.
//
// This test exercises setAgentModels end-to-end (the real production call
// path used on session/resume and session/load, see bgsession_acp_process.go)
// with a real session.Store carrying a persisted BaselineModel and no
// ACP-server constraint. It currently FAILS — CurrentValue stays at the
// agent's default rather than the persisted baseline — and will PASS once the
// fix restores the persisted baseline to the config option's CurrentValue (and
// re-applies it to the agent) on resume in the absence of a constraint.
func TestSetAgentModels_ResumeWithoutConstraint_DoesNotReapplyPersistedBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sid = "test-session-resume-baseline-lost"
	const persistedBaseline = "model-A" // user's earlier manual pick, persisted in metadata
	const agentDefault = "model-B"      // what the agent reports as CurrentModelId on resume

	if err := store.Create(session.Metadata{
		SessionID:     sid,
		ACPServer:     "test-server",
		WorkingDir:    "/tmp",
		Name:          "Test",
		BaselineModel: persistedBaseline,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A fresh BackgroundSession object handling a conversation-switch resume,
	// with NO ACP-server model constraint configured (the common case that
	// triggers the bug — a configured constraint would win via
	// applyConfigConstraints and mask this gap).
	bs := &BackgroundSession{persistedID: sid, store: store}

	models := &SessionModelState{
		CurrentModelId: agentDefault,
		AvailableModels: []ModelInfo{
			{ModelId: persistedBaseline, Name: "Model A"},
			{ModelId: agentDefault, Name: "Model B"},
		},
	}
	bs.setAgentModels(models)
	bs.waitForStartupConfigConstraints()

	opt, ok := bs.cmFindByCategory(ConfigOptionCategoryModel)
	if !ok {
		t.Fatalf("expected a model config option to be set")
	}

	// EXPECTED (post-fix): the persisted baseline must win when there is no
	// ACP-server constraint, mirroring how a constraint match is pre-applied
	// at acp_callback_sink.go:632-642 for the constraint case.
	if opt.CurrentValue != persistedBaseline {
		t.Errorf("model config option CurrentValue = %q, want %q "+
			"(persisted BaselineModel must be restored to the UI chip on resume "+
			"when no ACP-server model constraint governs the session — mitto-1yo)",
			opt.CurrentValue, persistedBaseline)
	}

	// In-memory baseline already reflects the persisted value (this part
	// works today via cbInitBaselineModelIfEmpty) — the bug is that it goes
	// no further than memory.
	if got := bs.GetBaselineModel(); got != persistedBaseline {
		t.Errorf("in-memory baselineModel = %q, want %q", got, persistedBaseline)
	}
}
