package conversation

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// --- buildPromptTriggerContext (pure function) — pins onChild threading
// without requiring a full ACP-connected BackgroundSession (mitto-qvlh). ---

func TestBuildPromptTriggerContext_NilWhenAllEmpty(t *testing.T) {
	if got := buildPromptTriggerContext(nil, nil, nil); got != nil {
		t.Errorf("expected nil PromptTriggerContext, got %+v", got)
	}
}

func TestBuildPromptTriggerContext_OnChild_EndResponse(t *testing.T) {
	onChild := &LoopDispatchOnChild{ChildID: "child-1", Event: session.ChildEventAnyEndResponse}
	got := buildPromptTriggerContext(nil, nil, onChild)
	if got == nil {
		t.Fatal("expected non-nil PromptTriggerContext")
	}
	if got.OnChild == nil {
		t.Fatal("expected non-nil OnChild")
	}
	if got.OnChild.ChildID != "child-1" {
		t.Errorf("ChildID = %q, want %q", got.OnChild.ChildID, "child-1")
	}
	if got.OnChild.Event != session.ChildEventAnyEndResponse {
		t.Errorf("Event = %q, want %q", got.OnChild.Event, session.ChildEventAnyEndResponse)
	}
	if got.OnChild.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", got.OnChild.StoppedReason)
	}
	if got.OnTasks != nil || got.OnSlack != nil || got.Slack != nil {
		t.Errorf("expected OnTasks/OnSlack/Slack to remain nil, got %+v", got)
	}
}

func TestBuildPromptTriggerContext_OnChild_Deleted(t *testing.T) {
	onChild := &LoopDispatchOnChild{ChildID: "child-2", Event: session.ChildEventAnyDeleted}
	got := buildPromptTriggerContext(nil, nil, onChild)
	if got == nil || got.OnChild == nil {
		t.Fatal("expected non-nil PromptTriggerContext.OnChild")
	}
	if got.OnChild.Event != session.ChildEventAnyDeleted {
		t.Errorf("Event = %q, want %q", got.OnChild.Event, session.ChildEventAnyDeleted)
	}
	if got.OnChild.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty for anyDeleted", got.OnChild.StoppedReason)
	}
}

func TestBuildPromptTriggerContext_OnChild_LoopStopped_CarriesReason(t *testing.T) {
	onChild := &LoopDispatchOnChild{
		ChildID:       "child-3",
		Event:         session.ChildEventAnyLoopStopped,
		StoppedReason: session.StoppedReasonMaxDuration,
	}
	got := buildPromptTriggerContext(nil, nil, onChild)
	if got == nil || got.OnChild == nil {
		t.Fatal("expected non-nil PromptTriggerContext.OnChild")
	}
	if got.OnChild.Event != session.ChildEventAnyLoopStopped {
		t.Errorf("Event = %q, want %q", got.OnChild.Event, session.ChildEventAnyLoopStopped)
	}
	if got.OnChild.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", got.OnChild.StoppedReason, session.StoppedReasonMaxDuration)
	}
}

// TestBuildPromptTriggerContext_OnChild_DoesNotClobberOnTasksOrSlack verifies
// that OnChild, OnTasks, and OnSlack/Slack can coexist without one clobbering
// another's presence (mirrors the pre-existing OnTasks+Slack coexistence
// pin in processors_test.go's TestBuildCELContext_TriggerSlack case 3).
func TestBuildPromptTriggerContext_OnChild_DoesNotClobberOnTasksOrSlack(t *testing.T) {
	onChild := &LoopDispatchOnChild{ChildID: "child-4", Event: session.ChildEventAnyEndResponse}
	slackEvents := []PromptSlackEvent{{InstallationID: "I1", ChannelID: "C1"}}
	got := buildPromptTriggerContext(nil, slackEvents, onChild)
	if got == nil {
		t.Fatal("expected non-nil PromptTriggerContext")
	}
	if got.OnChild == nil || got.OnChild.ChildID != "child-4" {
		t.Errorf("expected OnChild populated, got %+v", got.OnChild)
	}
	if got.OnSlack == nil || len(got.OnSlack.Events) != 1 {
		t.Errorf("expected OnSlack populated, got %+v", got.OnSlack)
	}
	if got.Slack == nil {
		t.Error("expected deprecated Slack alias populated alongside OnChild")
	}
}

// --- fireOnChild / OnChild* public entry points: log-based smoke tests
// mirroring the existing TestLoopRunner_FireOnChild_HappyPath_* idiom. There
// is no ACP connection in this test harness (bs.acpConn == nil), so
// PromptWithMeta returns synchronously before any observer sees the actual
// PromptMeta — the exact field-level ChildID/Event/StoppedReason threading is
// pinned directly above via buildPromptTriggerContext. These tests instead
// confirm the guard chain is passed and dispatch reaches triggerNowFull. ---

func TestFireOnChild_EndResponse_ThreadsChildIDAndEvent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnChildSession(t, store, "parent", nil)
	if err := store.Create(session.Metadata{
		SessionID: "child1", ACPServer: "test", WorkingDir: "/tmp", ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", false))

	logger, buf := captureDebugLogger()
	runner := NewLoopRunner(store, sm, logger)

	runner.OnChildEndResponse("child1")

	out := buf.String()
	if !strings.Contains(out, "Triggering immediate loop delivery") || !strings.Contains(out, "fired_by=onChild") {
		t.Errorf("expected the onChild-fired dispatch to reach triggerNowFull, got:\n%s", out)
	}
}

func TestFireOnChild_Deleted_ThreadsChildIDAndEvent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnChildSession(t, store, "parent", nil)

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", false))

	logger, buf := captureDebugLogger()
	runner := NewLoopRunner(store, sm, logger)

	runner.OnChildDeleted("child1", "parent")

	out := buf.String()
	if !strings.Contains(out, "Triggering immediate loop delivery") || !strings.Contains(out, "fired_by=onChild") {
		t.Errorf("expected the onChild-fired dispatch to reach triggerNowFull, got:\n%s", out)
	}
}

func TestFireOnChild_LoopStopped_ThreadsChildIDEventAndReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// anyLoopStopped is opt-in only (not in DefaultChildEvents).
	newOnChildSession(t, store, "parent", []session.ChildEvent{session.ChildEventAnyLoopStopped})
	if err := store.Create(session.Metadata{
		SessionID: "child1", ACPServer: "test", WorkingDir: "/tmp", ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", false))

	logger, buf := captureDebugLogger()
	runner := NewLoopRunner(store, sm, logger)

	runner.OnChildLoopStopped("child1", session.StoppedReasonMaxDuration)

	out := buf.String()
	if !strings.Contains(out, "Triggering immediate loop delivery") || !strings.Contains(out, "fired_by=onChild") {
		t.Errorf("expected the onChild-fired dispatch to reach triggerNowFull, got:\n%s", out)
	}
	// OnChildLoopStopped's own Debug log records the reason (pre-existing;
	// mitto-qvlh additionally forwards it into PromptMeta.Trigger.OnChild,
	// pinned directly against buildPromptTriggerContext above).
	if !strings.Contains(out, "onChild: child loop stopped") || !strings.Contains(out, "reason=maxDuration") {
		t.Errorf("expected the child-loop-stopped debug log with reason=maxDuration, got:\n%s", out)
	}
}

// TestFireOnChild_CoalescedLoser_LeavesNoOnChildContext verifies that a
// coalesced onChild fire never reaches deliverPrompt's triggerCtx
// construction. triggerNowFull logs "Triggering immediate loop delivery"
// BEFORE calling deliverPrompt (which is where the dispatch-claim coalescing
// check and buildPromptTriggerContext call actually live — see
// deliverPrompt's claimDispatch call preceding buildPromptTriggerContext), so
// that Info log alone does not prove a PromptTriggerContext.OnChild was
// built. The authoritative signal is the "Loop dispatch coalesced" Debug log
// emitted by claimDispatch's failure branch (inside deliverPrompt, ahead of
// the triggerCtx build) together with the caller-side coalesced-fire log, and
// the resulting ErrLoopDispatchCoalesced surfacing through fireOnChild's own
// Debug log — never Warn/Error.
func TestFireOnChild_CoalescedLoser_LeavesNoOnChildContext(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnChildSession(t, store, "parent", nil)

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", false))

	logger, buf := captureDebugLogger()
	runner := NewLoopRunner(store, sm, logger)

	// Another trigger already owns the in-flight dispatch for this session.
	if _, ok := runner.claimDispatch("parent", session.TriggerOnTasks); !ok {
		t.Fatal("precondition: claimDispatch() failed")
	}

	runner.fireOnChild("parent", session.ChildEventAnyEndResponse, "child1", "")

	out := buf.String()
	if !strings.Contains(out, "onChild: fire coalesced or session busy") {
		t.Errorf("expected a coalesced-fire Debug log, got:\n%s", out)
	}
	if !strings.Contains(out, "Loop dispatch coalesced - another trigger is already in flight") {
		t.Errorf("expected deliverPrompt's claimDispatch failure to be logged (proving the coalescing check ran before buildPromptTriggerContext), got:\n%s", out)
	}
	if strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
		t.Errorf("a coalescing loss must not log at Warn/Error, got:\n%s", out)
	}
}
