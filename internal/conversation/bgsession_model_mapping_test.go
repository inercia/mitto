package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// --- resolveClosestModel: pure-function unit tests ---

func modelMappingCatalog() *SessionModelState {
	return &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "m-1"},
			{ModelId: "m-2", Name: "m-2"},
			{ModelId: "claude-sonnet-4-5-20260101", Name: "Claude Sonnet 4.5"},
		},
	}
}

func TestResolveClosestModel_ExactIDMatch(t *testing.T) {
	got := resolveClosestModel("m-2", modelMappingCatalog())
	if got != "m-2" {
		t.Fatalf("resolveClosestModel() = %q, want %q", got, "m-2")
	}
}

func TestResolveClosestModel_ExactIDMatch_CaseInsensitive(t *testing.T) {
	got := resolveClosestModel("M-2", modelMappingCatalog())
	if got != "m-2" {
		t.Fatalf("resolveClosestModel() = %q, want %q", got, "m-2")
	}
}

// TestResolveClosestModel_LookAlike pins the bead's own example: a previous
// agent's dot-separated raw id ("claude-sonnet-4.5") must match a target
// catalog entry displayed with a hyphen-separated version number ("Claude
// Sonnet 4.5" is the *display name* here, but the id itself uses a
// different separator convention too) via the normalized lookAlike path.
func TestResolveClosestModel_LookAlike(t *testing.T) {
	got := resolveClosestModel("claude-sonnet-4.5", modelMappingCatalog())
	if got != "claude-sonnet-4-5-20260101" {
		t.Fatalf("resolveClosestModel() = %q, want %q", got, "claude-sonnet-4-5-20260101")
	}
}

func TestResolveClosestModel_LookAlike_HyphenatedID(t *testing.T) {
	// The previous id itself may already be hyphen-separated; normalization
	// must still split it into independent tokens rather than treating the
	// whole hyphenated string as one opaque blob (lookAlike's own Fields()
	// split only understands whitespace).
	got := resolveClosestModel("claude-sonnet-4-5", modelMappingCatalog())
	if got != "claude-sonnet-4-5-20260101" {
		t.Fatalf("resolveClosestModel() = %q, want %q", got, "claude-sonnet-4-5-20260101")
	}
}

func TestResolveClosestModel_NoMatch(t *testing.T) {
	got := resolveClosestModel("totally-unrelated-model", modelMappingCatalog())
	if got != "" {
		t.Fatalf("resolveClosestModel() = %q, want \"\" (no match)", got)
	}
}

func TestResolveClosestModel_EmptyPendingOrNilModels(t *testing.T) {
	if got := resolveClosestModel("", modelMappingCatalog()); got != "" {
		t.Fatalf("empty pendingFrom must resolve to \"\", got %q", got)
	}
	if got := resolveClosestModel("m-1", nil); got != "" {
		t.Fatalf("nil models must resolve to \"\", got %q", got)
	}
	if got := resolveClosestModel("m-1", &SessionModelState{}); got != "" {
		t.Fatalf("empty AvailableModels must resolve to \"\", got %q", got)
	}
}

// --- cbApplyPendingModelMapping: full BackgroundSession integration tests ---

// newModelMappingSession builds a store-backed BackgroundSession whose
// persisted metadata already carries pendingFrom in PendingModelMappingFrom
// (simulating a just-completed MoveSessionToAgent), wired to a fake shared
// process so a SetSessionModel RPC (if any) can be observed and answered.
func newModelMappingSession(t *testing.T, pendingFrom string) (*BackgroundSession, *modelTurnProcess, *session.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sessionID = "mm-session"
	if err := store.Create(session.Metadata{
		SessionID:               sessionID,
		ACPServer:               "agent-b",
		WorkingDir:              tmpDir,
		PendingModelMappingFrom: pendingFrom,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &modelTurnProcess{
		fakeSharedProcess: newFakeSharedProcess(),
		models:            make(chan modelTurnRPC, 16), prompts: make(chan modelTurnRPC, 16),
		cancels: make(chan string, 16), stop: make(chan struct{}),
	}
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, persistedID: sessionID, acpID: "acp-" + sessionID,
		workingDir: tmpDir, sharedProcess: p, observers: make(map[SessionObserver]struct{}),
		pendingConfig: make(map[string]string), isFirstPrompt: true,
		store:       store,
		mittoConfig: &config.Config{},
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	t.Cleanup(func() {
		cancel()
		close(p.stop)
		bs.waitForStartupConfigConstraints()
	})
	return bs, p, store
}

// waitForBaselineModel polls store's persisted BaselineModel for sessionID
// until it equals want, or fails the test after a bounded timeout. Needed
// because cbApplyPendingModelMapping's RPC-issuing branch runs in an
// unmanaged goroutine (mirroring cbApplyConfigConstraintsAsync's own
// fire-and-forget pattern) with no WaitGroup a test can join.
func waitForBaselineModel(t *testing.T, store *session.Store, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		meta, err := store.GetMetadata(sessionID)
		if err == nil && meta.BaselineModel == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	meta, _ := store.GetMetadata(sessionID)
	t.Fatalf("timed out waiting for baseline model %q, got %q", want, meta.BaselineModel)
}

func TestCbApplyPendingModelMapping_NoOpWhenNothingPending(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	modelTurnNoRPC(t, p.models)
	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.BaselineModel != "m-1" {
		t.Fatalf("baseline model = %q, want agent default %q", meta.BaselineModel, "m-1")
	}
}

func TestCbApplyPendingModelMapping_ExactMatch_AppliesAndRecordsBaseline(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "m-2")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	call := modelTurnReceive(t, p.models)
	if call.model != "m-2" {
		t.Fatalf("SetSessionModel model = %q, want %q", call.model, "m-2")
	}
	call.reply <- nil

	waitForBaselineModel(t, store, bs.persistedID, "m-2")
	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.PendingModelMappingFrom != "" {
		t.Fatalf("PendingModelMappingFrom = %q, want cleared", meta.PendingModelMappingFrom)
	}
	if got := bs.cmGetCurrentModelID(); got != "m-2" {
		t.Fatalf("active model = %q, want %q", got, "m-2")
	}
}

func TestCbApplyPendingModelMapping_LookAlikeMatch_Applies(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "claude-sonnet-4.5")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	call := modelTurnReceive(t, p.models)
	if call.model != "claude-sonnet-4-5-20260101" {
		t.Fatalf("SetSessionModel model = %q, want %q", call.model, "claude-sonnet-4-5-20260101")
	}
	call.reply <- nil

	waitForBaselineModel(t, store, bs.persistedID, "claude-sonnet-4-5-20260101")
}

func TestCbApplyPendingModelMapping_NoMatch_FallsBackToDefaultWithoutError(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "totally-unrelated-model")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	modelTurnNoRPC(t, p.models)
	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.BaselineModel != "m-1" {
		t.Fatalf("baseline model = %q, want agent default %q (no match, no error)", meta.BaselineModel, "m-1")
	}
	if meta.PendingModelMappingFrom != "" {
		t.Fatalf("PendingModelMappingFrom = %q, want cleared even on no-match", meta.PendingModelMappingFrom)
	}
}

func TestCbApplyPendingModelMapping_AlreadyActive_PromotesWithoutRPC(t *testing.T) {
	// pendingFrom already equals the agent's own current/default model: no
	// RPC should be needed, but the value must still be promoted to the
	// persisted baseline (mirrors ApplyModelTag's mitto-1yo already-matches
	// short-circuit) and PendingModelMappingFrom must be cleared.
	bs, p, store := newModelMappingSession(t, "m-1")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	modelTurnNoRPC(t, p.models)
	waitForBaselineModel(t, store, bs.persistedID, "m-1")
	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.PendingModelMappingFrom != "" {
		t.Fatalf("PendingModelMappingFrom = %q, want cleared", meta.PendingModelMappingFrom)
	}
}

func TestCbApplyPendingModelMapping_RunsOnlyOnce(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "m-2")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	call := modelTurnReceive(t, p.models)
	call.reply <- nil
	waitForBaselineModel(t, store, bs.persistedID, "m-2")

	// A manual change away from the mapped model... SetConfigOption blocks
	// synchronously on the RPC reply, so it must run concurrently with the
	// channel drain below (it cannot return before we reply to its call).
	setErrCh := make(chan error, 1)
	go func() {
		setErrCh <- bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, "m-1")
	}()
	call = modelTurnReceive(t, p.models)
	call.reply <- nil
	if err := <-setErrCh; err != nil {
		t.Fatalf("SetConfigOption failed: %v", err)
	}
	waitForBaselineModel(t, store, bs.persistedID, "m-1")

	// ...followed by a second models callback (e.g. a resume) must NOT
	// re-fire the mapping RPC, since PendingModelMappingFrom was already
	// consumed and cleared on the first call.
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()
	modelTurnNoRPC(t, p.models)

	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.BaselineModel != "m-1" {
		t.Fatalf("baseline model = %q, want manual choice %q preserved (mapping must not re-fire)", meta.BaselineModel, "m-1")
	}
}

// TestCbApplyPendingModelMapping_ExplicitModelTagAppliedAfterward_TagWins
// pins the mitto-9eci ordering requirement: an explicit model_tag request
// issued after the mapping hook has already run/applied must win — see
// cbApplyPendingModelMapping's doc comment for why ApplyModelTag can only
// ever run at or after this hook (it requires bs.AgentModels() != nil).
func TestCbApplyPendingModelMapping_ExplicitModelTagAppliedAfterward_TagWins(t *testing.T) {
	bs, p, store := newModelMappingSession(t, "m-2")
	bs.setAgentModels(modelMappingCatalog())
	bs.waitForStartupConfigConstraints()

	// The mapping hook resolves and applies "m-2".
	call := modelTurnReceive(t, p.models)
	if call.model != "m-2" {
		t.Fatalf("mapping SetSessionModel model = %q, want %q", call.model, "m-2")
	}
	call.reply <- nil
	waitForBaselineModel(t, store, bs.persistedID, "m-2")

	// An explicit model_tag pin now requested for a DIFFERENT model must win.
	bs.mittoConfig.Models = []config.ModelProfile{
		{Name: "Pinned", Tags: []string{"pinned"}, Criteria: &config.ACPServerConstraint{Pattern: "Claude Sonnet 4.5", MatchMode: "exact"}},
	}
	resultCh := make(chan struct {
		resolved string
		err      error
	}, 1)
	go func() {
		resolved, err := bs.ApplyModelTag(context.Background(), "pinned")
		resultCh <- struct {
			resolved string
			err      error
		}{resolved, err}
	}()
	tagCall := modelTurnReceive(t, p.models)
	if tagCall.model != "claude-sonnet-4-5-20260101" {
		t.Fatalf("ApplyModelTag SetSessionModel model = %q, want %q", tagCall.model, "claude-sonnet-4-5-20260101")
	}
	tagCall.reply <- nil
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("ApplyModelTag returned error: %v", res.err)
	}

	waitForBaselineModel(t, store, bs.persistedID, "claude-sonnet-4-5-20260101")
	if got := bs.cmGetCurrentModelID(); got != "claude-sonnet-4-5-20260101" {
		t.Fatalf("active model = %q, want tag-pinned model to win", got)
	}
}

// TestCallbackSink_SetAgentModels_InvokesPendingModelMapping is a
// callback-sink-level test (fakeCallbackDeps harness, matching the sibling
// tests in acp_callback_sink_test.go) confirming setAgentModels always
// delegates to cbApplyPendingModelMapping with the received models, right
// after kicking off the startup constraint goroutine.
func TestCallbackSink_SetAgentModels_InvokesPendingModelMapping(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	models := &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
		},
	}
	s.setAgentModels(d, models)

	if len(d.pendingMappingCalls) != 1 || d.pendingMappingCalls[0] != models {
		t.Fatalf("expected cbApplyPendingModelMapping called once with models, got %v", d.pendingMappingCalls)
	}
}
