package processors

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// newCloseRouterApplyTestStore creates a real *session.Store with sessionID
// already present, mirroring internal/session's own
// newCloseRouterTestStore helper (mitto-3od.1) so the orchestration in
// apply.go is exercised against the real sidecar I/O path, not a fake.
func newCloseRouterApplyTestStore(t *testing.T, sessionID string) *session.Store {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(session.Metadata{SessionID: sessionID}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	return store
}

// waitForCloseRouterState polls close-router.json until cond is satisfied or
// a bounded deadline passes. Needed because ApplyOnClose's knowledge-router
// dispatch runs asynchronously (dispatchPromptBatch's single-prompt path
// wraps dispatchWithRetry in a goroutine), so the sidecar write-back from
// applyCloseRouterCompletion happens on its own schedule relative to the
// test goroutine.
func waitForCloseRouterState(t *testing.T, store *session.Store, sessionID string, cond func(session.CloseRouterState) bool) session.CloseRouterState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := session.ReadCloseRouterState(store, sessionID)
		if err != nil {
			t.Fatalf("ReadCloseRouterState: %v", err)
		}
		if cond(state) {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for close-router state condition; last state = %+v", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- buildCloseRouterStateBlock ---------------------------------------------------

func TestBuildCloseRouterStateBlock_NoRuns_ReturnsEmpty(t *testing.T) {
	if got := buildCloseRouterStateBlock(session.CloseRouterState{}, "hash-1"); got != "" {
		t.Fatalf("block = %q, want empty for a zero-run state", got)
	}
}

func TestBuildCloseRouterStateBlock_NoMatchingHash_ReturnsEmpty(t *testing.T) {
	state := session.CloseRouterState{Runs: []session.CloseRouterRun{{
		RunID: "run-1", HistorySnapshotHash: "other-hash",
		Findings: []session.CloseRouterFinding{{LogicalKey: "k1", Destination: "memory", Written: true}},
	}}}
	if got := buildCloseRouterStateBlock(state, "hash-1"); got != "" {
		t.Fatalf("block = %q, want empty when no run matches the snapshot hash", got)
	}
}

func TestBuildCloseRouterStateBlock_MatchingRunAllUnwritten_ReturnsEmpty(t *testing.T) {
	state := session.CloseRouterState{Runs: []session.CloseRouterRun{{
		RunID: "run-1", HistorySnapshotHash: "hash-1",
		Findings: []session.CloseRouterFinding{{LogicalKey: "k1", Destination: "none", Written: false}},
	}}}
	if got := buildCloseRouterStateBlock(state, "hash-1"); got != "" {
		t.Fatalf("block = %q, want empty when the matching run has no written findings", got)
	}
}

func TestBuildCloseRouterStateBlock_MatchingRunWithWritten_IncludesOnlyWritten(t *testing.T) {
	state := session.CloseRouterState{Runs: []session.CloseRouterRun{{
		RunID: "run-1", HistorySnapshotHash: "hash-1",
		Findings: []session.CloseRouterFinding{
			{LogicalKey: "written-key", Destination: "preferences", Written: true, TargetPath: ".augment/rules/90-local.md"},
			{LogicalKey: "unwritten-key", Destination: "none", Written: false},
		},
	}}}
	block := buildCloseRouterStateBlock(state, "hash-1")
	if block == "" {
		t.Fatal("block = empty, want a non-empty <mitto_close_router_state> block")
	}
	if !strings.Contains(block, "<mitto_close_router_state>") || !strings.Contains(block, "</mitto_close_router_state>") {
		t.Fatalf("block missing XML wrapper: %s", block)
	}
	if !strings.Contains(block, "hash-1") {
		t.Errorf("block missing snapshot hash: %s", block)
	}
	if !strings.Contains(block, "written-key") {
		t.Errorf("block missing the written finding's logical key: %s", block)
	}
	if strings.Contains(block, "unwritten-key") {
		t.Errorf("block leaked an unwritten finding's logical key: %s", block)
	}
}

func TestBuildCloseRouterStateBlock_UsesNewestMatchingRunOnly(t *testing.T) {
	state := session.CloseRouterState{Runs: []session.CloseRouterRun{
		{
			RunID: "run-older", HistorySnapshotHash: "hash-1",
			Findings: []session.CloseRouterFinding{{LogicalKey: "older-key", Destination: "memory", Written: true}},
		},
		{
			RunID: "run-newer", HistorySnapshotHash: "hash-1",
			Findings: []session.CloseRouterFinding{{LogicalKey: "newer-key", Destination: "memory", Written: true}},
		},
	}}
	block := buildCloseRouterStateBlock(state, "hash-1")
	if !strings.Contains(block, "newer-key") {
		t.Errorf("block missing newest run's key: %s", block)
	}
	if strings.Contains(block, "older-key") {
		t.Errorf("block must consult only the newest matching run, but leaked an older key: %s", block)
	}
}

// --- parseCloseRouterFindings ------------------------------------------------------

func TestParseCloseRouterFindings_EmptyMessage_Errors(t *testing.T) {
	if _, err := parseCloseRouterFindings(""); err == nil {
		t.Fatal("expected an error for an empty completion message")
	}
	if _, err := parseCloseRouterFindings("   \n  "); err == nil {
		t.Fatal("expected an error for a whitespace-only completion message")
	}
}

func TestParseCloseRouterFindings_InvalidJSON_Errors(t *testing.T) {
	if _, err := parseCloseRouterFindings("not json at all"); err == nil {
		t.Fatal("expected an error for a non-JSON completion message")
	}
}

func TestParseCloseRouterFindings_FencedCodeBlockRejected_NoSubstringExtraction(t *testing.T) {
	// mitto-sys.8-style strict parsing: the WHOLE trimmed body must unmarshal.
	// A fenced code block around otherwise-valid JSON must still be rejected —
	// salvaging embedded JSON via substring extraction is exactly what this
	// parser must NOT do.
	msg := "```json\n{\"findings\":[]}\n```"
	if _, err := parseCloseRouterFindings(msg); err == nil {
		t.Fatal("expected fenced JSON to be rejected (no substring/partial extraction)")
	}
}

func TestParseCloseRouterFindings_InvalidDestination_Errors(t *testing.T) {
	msg := `{"findings":[{"text":"a finding","destination":"bogus","written":false}]}`
	_, err := parseCloseRouterFindings(msg)
	if err == nil {
		t.Fatal("expected an error for an invalid destination")
	}
	if !strings.Contains(err.Error(), "invalid destination") {
		t.Errorf("error = %v, want it to mention the invalid destination", err)
	}
}

func TestParseCloseRouterFindings_EmptyFindingsArray_ReturnsEmptySlice(t *testing.T) {
	findings, err := parseCloseRouterFindings(`{"findings":[]}`)
	if err != nil {
		t.Fatalf("parseCloseRouterFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want empty", findings)
	}
}

func TestParseCloseRouterFindings_Valid_ReturnsFindings(t *testing.T) {
	msg := `{"findings":[
		{"text":"prefers dark mode","destination":"preferences","written":true,"target_path":".augment/rules/90-local.md"},
		{"text":"just noise","destination":"none","written":false}
	]}`
	findings, err := parseCloseRouterFindings(msg)
	if err != nil {
		t.Fatalf("parseCloseRouterFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2", findings)
	}
	wantKey := session.CloseRouterLogicalKey("prefers dark mode")
	if findings[0].LogicalKey != wantKey {
		t.Errorf("LogicalKey = %q, want %q", findings[0].LogicalKey, wantKey)
	}
	if findings[0].Destination != "preferences" || !findings[0].Written || findings[0].TargetPath != ".augment/rules/90-local.md" {
		t.Errorf("finding[0] = %+v, unexpected", findings[0])
	}
	if findings[1].Destination != "none" || findings[1].Written {
		t.Errorf("finding[1] = %+v, want none/unwritten", findings[1])
	}
}

// --- applyCloseRouterCompletion ----------------------------------------------------

func TestApplyCloseRouterCompletion_NilStoreOrEmptyRunID_NoOp(t *testing.T) {
	m := NewManager("", nil)
	// Must not panic for either guard condition.
	m.applyCloseRouterCompletion(nil, "s1", "run-1", PromptCompletion{}, nil)
	store := newCloseRouterApplyTestStore(t, "s1")
	m.applyCloseRouterCompletion(store, "s1", "", PromptCompletion{}, nil)
}

func TestApplyCloseRouterCompletion_DispatchError_LeavesRunInFlight(t *testing.T) {
	const sessionID = "s1"
	store := newCloseRouterApplyTestStore(t, sessionID)
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{RunID: "run-1", HistorySnapshotHash: "hash-1"}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	m := NewManager("", nil)
	m.applyCloseRouterCompletion(store, sessionID, "run-1", PromptCompletion{}, errors.New("simulated dispatch failure"))

	state, err := session.ReadCloseRouterState(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if len(state.Runs) != 1 || !state.Runs[0].CompletedAt.IsZero() || len(state.Runs[0].Findings) != 0 {
		t.Fatalf("run after dispatch error = %+v, want CompletedAt still zero and no findings", state.Runs)
	}
}

func TestApplyCloseRouterCompletion_ParseFailure_CompletesWithEmptyFindings(t *testing.T) {
	const sessionID = "s1"
	store := newCloseRouterApplyTestStore(t, sessionID)
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{RunID: "run-1", HistorySnapshotHash: "hash-1"}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	m := NewManager("", nil)
	m.applyCloseRouterCompletion(store, sessionID, "run-1", PromptCompletion{FinalMessage: "not json"}, nil)

	state, err := session.ReadCloseRouterState(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if len(state.Runs) != 1 {
		t.Fatalf("Runs = %+v, want 1", state.Runs)
	}
	if state.Runs[0].CompletedAt.IsZero() {
		t.Fatal("CompletedAt not set: a completed (but unparseable) turn must still be recorded so a retry does not double-write")
	}
	if len(state.Runs[0].Findings) != 0 {
		t.Fatalf("Findings = %+v, want empty on parse failure", state.Runs[0].Findings)
	}
}

func TestApplyCloseRouterCompletion_Success_PersistsFindings(t *testing.T) {
	const sessionID = "s1"
	store := newCloseRouterApplyTestStore(t, sessionID)
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{RunID: "run-1", HistorySnapshotHash: "hash-1"}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	m := NewManager("", nil)
	msg := `{"findings":[{"text":"prefers dark mode","destination":"preferences","written":true,"target_path":"p.md"}]}`
	m.applyCloseRouterCompletion(store, sessionID, "run-1", PromptCompletion{FinalMessage: msg}, nil)

	state, err := session.ReadCloseRouterState(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if len(state.Runs) != 1 || state.Runs[0].CompletedAt.IsZero() {
		t.Fatalf("run after success = %+v, want CompletedAt set", state.Runs)
	}
	if len(state.Runs[0].Findings) != 1 || state.Runs[0].Findings[0].Destination != "preferences" {
		t.Fatalf("Findings = %+v, want one preferences finding", state.Runs[0].Findings)
	}
}

func TestApplyCloseRouterCompletion_RunNotFound_NoOp(t *testing.T) {
	const sessionID = "s1"
	store := newCloseRouterApplyTestStore(t, sessionID)
	want := session.CloseRouterState{
		Runs: []session.CloseRouterRun{{RunID: "run-existing", HistorySnapshotHash: "hash-1"}},
	}
	if err := session.WriteCloseRouterState(store, sessionID, want); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	m := NewManager("", nil)
	m.applyCloseRouterCompletion(store, sessionID, "run-does-not-exist", PromptCompletion{FinalMessage: `{"findings":[]}`}, nil)

	got, err := session.ReadCloseRouterState(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].RunID != "run-existing" || !got.Runs[0].CompletedAt.IsZero() {
		t.Fatalf("state mutated for an unknown run ID: %+v", got.Runs)
	}
}

// --- ApplyOnClose end-to-end --------------------------------------------------------

func TestApplyOnClose_KnowledgeRouter_FullRoundTrip(t *testing.T) {
	const sessionID = "sess-router-1"
	store := newCloseRouterApplyTestStore(t, sessionID)

	proc := &Processor{
		Name:   knowledgeRouterProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "classify findings",
	}
	m := NewManager("", nil)
	m.processors = []*Processor{proc}

	var mu sync.Mutex
	var capturedPrompt string
	done := make(chan struct{}, 1)
	m.SetPromptCompletionFunc(func(_ context.Context, _, _, _, prompt string) (PromptCompletion, error) {
		mu.Lock()
		capturedPrompt = prompt
		mu.Unlock()
		done <- struct{}{}
		return PromptCompletion{FinalMessage: `{"findings":[{"text":"prefers dark mode","destination":"preferences","written":true,"target_path":".augment/rules/90-local.md"}]}`}, nil
	})

	const historySnapshot = `{"events":[{"role":"user","text":"I prefer dark mode"}]}`
	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       sessionID,
		WorkspaceUUID:   "ws-router-1",
		SessionStore:    store,
		HistorySnapshot: historySnapshot,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for promptCompletionFunc to be invoked")
	}

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 1 && !s.Runs[0].CompletedAt.IsZero()
	})
	run := state.Runs[0]
	if wantHash := session.CloseRouterSnapshotHash([]byte(historySnapshot)); run.HistorySnapshotHash != wantHash {
		t.Errorf("HistorySnapshotHash = %q, want %q", run.HistorySnapshotHash, wantHash)
	}
	if len(run.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", run.Findings)
	}
	f := run.Findings[0]
	if f.Destination != "preferences" || !f.Written || f.TargetPath != ".augment/rules/90-local.md" {
		t.Errorf("finding = %+v, unexpected", f)
	}
	if wantKey := session.CloseRouterLogicalKey("prefers dark mode"); f.LogicalKey != wantKey {
		t.Errorf("LogicalKey = %q, want %q", f.LogicalKey, wantKey)
	}

	mu.Lock()
	prompt := capturedPrompt
	mu.Unlock()
	if !strings.Contains(prompt, "<mitto_close_history_snapshot>") {
		t.Error("dispatched prompt missing the close-history snapshot block")
	}
	if strings.Contains(prompt, "<mitto_close_router_state>") {
		t.Error("a first-ever run has no prior written findings and must not inject a router-state block")
	}
}

func TestApplyOnClose_KnowledgeRouter_InjectsAlreadyWrittenStateBlock(t *testing.T) {
	const sessionID = "sess-router-2"
	store := newCloseRouterApplyTestStore(t, sessionID)

	const historySnapshot = `{"events":[{"role":"user","text":"I prefer dark mode"}]}`
	hash := session.CloseRouterSnapshotHash([]byte(historySnapshot))
	priorKey := session.CloseRouterLogicalKey("prefers dark mode")
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{
			RunID: "run-prior", HistorySnapshotHash: hash,
			CompletedAt: time.Now().Add(-time.Hour),
			Findings: []session.CloseRouterFinding{
				{LogicalKey: priorKey, Destination: "preferences", Written: true, TargetPath: ".augment/rules/90-local.md"},
			},
		}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	proc := &Processor{
		Name:   knowledgeRouterProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "classify findings",
	}
	m := NewManager("", nil)
	m.processors = []*Processor{proc}

	var mu sync.Mutex
	var capturedPrompt string
	done := make(chan struct{}, 1)
	m.SetPromptCompletionFunc(func(_ context.Context, _, _, _, prompt string) (PromptCompletion, error) {
		mu.Lock()
		capturedPrompt = prompt
		mu.Unlock()
		done <- struct{}{}
		return PromptCompletion{FinalMessage: `{"findings":[]}`}, nil
	})

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       sessionID,
		WorkspaceUUID:   "ws-router-2",
		SessionStore:    store,
		HistorySnapshot: historySnapshot,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for promptCompletionFunc to be invoked")
	}
	waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 2 && !s.Runs[1].CompletedAt.IsZero()
	})

	mu.Lock()
	prompt := capturedPrompt
	mu.Unlock()
	if !strings.Contains(prompt, "<mitto_close_router_state>") {
		t.Fatal("expected a router-state block for a run with a prior write against the same snapshot hash")
	}
	if !strings.Contains(prompt, priorKey) {
		t.Errorf("router-state block missing prior logical key %q; prompt=%s", priorKey, prompt)
	}
	if !strings.Contains(prompt, hash) {
		t.Errorf("router-state block missing snapshot hash %q; prompt=%s", hash, prompt)
	}
}

func TestApplyOnClose_KnowledgeRouter_SessionGone_SkipsDispatch(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Deliberately never Create() the session: ReadCloseRouterState must
	// observe session.ErrSessionNotFound and ApplyOnClose must skip dispatch
	// entirely rather than target a sidecar that can never be written.

	proc := &Processor{
		Name:   knowledgeRouterProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "classify findings",
	}
	m := NewManager("", nil)
	m.processors = []*Processor{proc}

	var dispatches atomic.Int32
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		dispatches.Add(1)
		return PromptCompletion{}, nil
	})

	var mu sync.Mutex
	var skipReasons []string
	m.SetRunRecorder(func(run ProcessorRun) {
		mu.Lock()
		skipReasons = append(skipReasons, run.SkipReason)
		mu.Unlock()
	})

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       "missing-session",
		WorkspaceUUID:   "ws-router-3",
		SessionStore:    store,
		HistorySnapshot: `{"events":[]}`,
	})

	// The skip happens synchronously inside ApplyOnClose (before any
	// dispatch goroutine could be spawned), but give a would-be accidental
	// async dispatch a moment to prove its absence.
	time.Sleep(50 * time.Millisecond)
	if got := dispatches.Load(); got != 0 {
		t.Fatalf("promptCompletionFunc calls = %d, want 0 (session-gone must skip dispatch entirely)", got)
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, r := range skipReasons {
		if r == string(SkipReasonSessionGone) {
			found = true
		}
	}
	if !found {
		t.Fatalf("recorded skip reasons = %v, want %q present", skipReasons, SkipReasonSessionGone)
	}
}

// TestApplyOnClose_KnowledgeRouter_SpoolDeferral_MustNotFalselyCompleteRun is
// the mitto-3k0z reproduction. ApplyOnClose dispatches the close-phase
// knowledge-router standalone via dispatchPromptBatch -> dispatchWithRetry
// (deferrableWhenBusy=true). When the shared ACP process would shed a
// proactive auxiliary session (shouldDeferDispatchFunc reports true,
// mitto-z4w), dispatchWithRetry skips the RPC entirely and persists the
// batch straight to the durable spool — but its deferred onCompletion
// callback (`defer func() { cb(completion, lastErr) }()`, apply.go:2985)
// still fires, with a zero-value PromptCompletion{} and a nil dispatchErr,
// because those locals are never updated on the early-return deferral path.
//
// applyCloseRouterCompletion cannot distinguish that from a genuine (but
// unparseable) completion: dispatchErr == nil skips the early return, the
// empty FinalMessage fails to parse, and the in-flight run is marked
// CompletedAt with empty Findings — permanently. The real findings, when the
// spooled batch is eventually delivered by FlushPendingDispatches, have no
// callback to report back to (the closure is not part of the persisted
// PendingDispatchEntry), so they are silently discarded. This is the
// "silent knowledge loss" this bead is about.
//
// Expected (post-fix) contract: a spool-deferred dispatch has NOT actually
// completed, so the run must be left in-flight (CompletedAt.IsZero()) —
// exactly like TestApplyCloseRouterCompletion_DispatchError_LeavesRunInFlight
// — so a later retry against the same snapshot hash can reclassify it.
func TestApplyOnClose_KnowledgeRouter_SpoolDeferral_MustNotFalselyCompleteRun(t *testing.T) {
	const sessionID = "sess-router-spool-defer"
	const workspaceUUID = "ws-router-spool-defer"
	store := newCloseRouterApplyTestStore(t, sessionID)
	pendingStore := &FilePendingDispatchStore{BaseDir: t.TempDir()}

	proc := &Processor{
		Name:   knowledgeRouterProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "classify findings",
	}
	m := NewManager("", nil)
	m.processors = []*Processor{proc}
	m.SetPendingDispatchStore(pendingStore)

	var completionCalls atomic.Int32
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		completionCalls.Add(1)
		return PromptCompletion{FinalMessage: `{"findings":[{"text":"prefers dark mode","destination":"preferences","written":true,"target_path":"p.md"}]}`}, nil
	})
	// Simulate the shared ACP process being busy right after the turn ends —
	// the ordinary case for close-phase dispatch (mitto-z4w) — forcing the
	// spool-first deferral branch instead of an RPC attempt.
	m.SetShouldDeferDispatchFunc(func(ws string) bool { return ws == workspaceUUID })

	const historySnapshot = `{"events":[{"role":"user","text":"I prefer dark mode"}]}`
	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       sessionID,
		WorkspaceUUID:   workspaceUUID,
		SessionStore:    store,
		HistorySnapshot: historySnapshot,
	})

	// Actively watch the sidecar for up to 500ms for the run to be (wrongly)
	// marked complete. The AppendClaimed entry lands in the spool BEFORE the
	// deferral check even runs (apply.go:3003-3018), so waiting on the spool
	// alone races ahead of the deferred onCompletion callback and produces a
	// false pass — poll the actual side effect (the sidecar write) instead.
	// The buggy path resolves near-instantly (observed <30ms end-to-end for
	// this whole test), so 500ms reliably catches the regression without
	// slowing the suite when the run correctly stays in-flight.
	deadline := time.Now().Add(500 * time.Millisecond)
	var state session.CloseRouterState
	for {
		var err error
		state, err = session.ReadCloseRouterState(store, sessionID)
		if err != nil {
			t.Fatalf("ReadCloseRouterState: %v", err)
		}
		if len(state.Runs) == 1 && !state.Runs[0].CompletedAt.IsZero() {
			break // reproduced: falsely marked complete already
		}
		if time.Now().After(deadline) {
			break // stayed in-flight through the whole window
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := completionCalls.Load(); got != 0 {
		t.Fatalf("promptCompletionFunc call count = %d, want 0 (a spool-deferred dispatch must never reach the RPC)", got)
	}

	if len(state.Runs) != 1 {
		t.Fatalf("Runs = %+v, want exactly 1 in-flight run", state.Runs)
	}
	if !state.Runs[0].CompletedAt.IsZero() {
		t.Fatalf("run.CompletedAt = %v, want zero: a spool-deferred dispatch has NOT actually completed — "+
			"marking it complete with empty Findings silently discards the real findings once the spool is later flushed (mitto-3k0z)",
			state.Runs[0].CompletedAt)
	}
	if len(state.Runs[0].Findings) != 0 {
		t.Fatalf("Findings = %+v, want empty (nothing was ever classified yet)", state.Runs[0].Findings)
	}
}
