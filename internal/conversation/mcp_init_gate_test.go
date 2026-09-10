package conversation

// mcp_init_gate_test.go covers mitto-tgx: a pre-fire MCP-init readiness gate
// on the onTasks fire path (triggerTasksFireWithRetry and its three callers)
// and the runOnStart boot pulse (fireOnStartPulses), so a slow MCP cold-start
// defers these fires instead of burning the delivery-failure ceiling.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// gatedSharedProcess is a fake SharedProcess whose MCP-init gating signals
// are controlled directly by the test, exercising
// BackgroundSession.IsMCPInitGated and its LoopRunner call sites.
type gatedSharedProcess struct {
	*alwaysFailSharedProcess
	inProgress bool
	timedOut   bool
	done       bool
}

func (p *gatedSharedProcess) MCPInitInProgress() bool { return p.inProgress }
func (p *gatedSharedProcess) MCPInitTimedOut() bool   { return p.timedOut }
func (p *gatedSharedProcess) MCPInitDone() bool       { return p.done }

func TestBackgroundSession_IsMCPInitGated_NilReceiver(t *testing.T) {
	var bs *BackgroundSession
	if bs.IsMCPInitGated() {
		t.Error("nil *BackgroundSession must fail open (not gated)")
	}
}

func TestBackgroundSession_IsMCPInitGated_NilSharedProcess(t *testing.T) {
	bs := &BackgroundSession{}
	if bs.IsMCPInitGated() {
		t.Error("BackgroundSession with nil sharedProcess must fail open (not gated)")
	}
}

// TestBackgroundSession_IsMCPInitGated tables every combination of the two
// optional signals (MCPInitInProgress, MCPInitTimedOut) plus the base
// MCPInitDone(), and the fail-open case where the shared process implements
// neither optional interface.
func TestBackgroundSession_IsMCPInitGated(t *testing.T) {
	tests := []struct {
		name      string
		sp        SharedProcess
		wantGated bool
	}{
		{"neither optional interface implemented (fail open)", &alwaysFailSharedProcess{}, false},
		{"in progress, not done -> gated", &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: false}, true},
		{"in progress, done -> not gated", &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: true}, false},
		{"not in progress, not done -> not gated", &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: false, done: false}, false},
		{"timed out, done -> gated", &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, timedOut: true, done: true}, true},
		{"timed out and in progress -> gated", &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, timedOut: true, inProgress: true, done: false}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs := &BackgroundSession{sharedProcess: tt.sp}
			if got := bs.IsMCPInitGated(); got != tt.wantGated {
				t.Errorf("IsMCPInitGated() = %v, want %v", got, tt.wantGated)
			}
		})
	}
}

// TestLoopRunner_SessionMCPInitGated_FailOpen verifies sessionMCPInitGated
// fails open when the session manager is nil or the session is not found.
func TestLoopRunner_SessionMCPInitGated_FailOpen(t *testing.T) {
	runner := NewLoopRunner(nil, nil, nil)
	if runner.sessionMCPInitGated("missing") {
		t.Error("sessionMCPInitGated() = true with nil sessionManager, want false (fail open)")
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner = NewLoopRunner(nil, sm, nil)
	if runner.sessionMCPInitGated("missing") {
		t.Error("sessionMCPInitGated() = true for an unregistered session, want false (fail open)")
	}
}

// TestLoopRunner_TriggerTasksFireWithRetry_MCPInitGated_ShortCircuitsBeforeDispatch
// pins the mitto-tgx choke point: the gate must reject BEFORE any
// prompt-resolution/dispatch attempt, so a gated fire never burns a retry
// attempt against a doomed prompt-prep call.
func TestLoopRunner_TriggerTasksFireWithRetry_MCPInitGated_ShortCircuitsBeforeDispatch(t *testing.T) {
	const sessionID = "s1"
	rawNow := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	runner, _ := newTasksRefireTestRunner(t, sessionID, rawNow)

	bs := runner.sessionManager.GetSession(sessionID)
	if bs == nil {
		t.Fatal("expected registered session")
	}
	bs.sharedProcess = &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: false}

	var resolverCalls int32
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		atomic.AddInt32(&resolverCalls, 1)
		return "iterate", nil
	})

	delta := &config.TasksDelta{Added: []map[string]any{{"id": "mitto-1"}}}
	err, exhausted := runner.triggerTasksFireWithRetry(sessionID, delta, nil)

	if !errors.Is(err, ErrMCPInitGated) {
		t.Fatalf("err = %v, want ErrMCPInitGated", err)
	}
	if exhausted {
		t.Error("exhausted = true, want false (the gate short-circuits before the retry loop)")
	}
	if got := atomic.LoadInt32(&resolverCalls); got != 0 {
		t.Errorf("promptResolver call count = %d, want 0 (gate must reject before any prompt-prep attempt)", got)
	}
}

// TestLoopRunner_ProcessTasksChange_MCPInitGated_DefersWithoutCounterBump
// drives the idle-path tasksActionFire branch (processTasksChange) while the
// registered session's shared process is MCP-init gated: the pending delta
// must be preserved (baseline left un-rebased), a re-fire must be armed, and
// — the core mitto-tgx acceptance criterion — the delivery-failure counter
// must NOT be bumped, since this is a benign defer, not a real failure.
func TestLoopRunner_ProcessTasksChange_MCPInitGated_DefersWithoutCounterBump(t *testing.T) {
	const sessionID = "s1"
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"),
	)

	runner, ps := newTasksRefireTestRunner(t, sessionID, rawNow)
	if err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	bs := runner.sessionManager.GetSession(sessionID)
	if bs == nil {
		t.Fatal("expected registered session")
	}
	bs.sharedProcess = &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: false}

	meta, err := runner.store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	runner.processTasksChange(meta, loop, ps, rawNow)

	baseline, err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawBefore) {
		t.Errorf("baseline.RawSnapshot = %s, want %s (must not rebase while gated)", baseline.RawSnapshot, rawBefore)
	}
	if got := countTasksRebaseTimers(runner); got != 1 {
		t.Errorf("tasksRebaseTimers = %d, want 1 (deferred fire must re-arm the quiescence timer)", got)
	}
	if !runner.tasksRefirePendingForTest(sessionID) {
		t.Error("tasksRefirePending must be set after a gated defer so the next quiescence tick retries")
	}
	runner.tasksRebaseTimersMu.Lock()
	failures := runner.tasksRefireDeliveryFailures[sessionID]
	runner.tasksRebaseTimersMu.Unlock()
	if failures != 0 {
		t.Errorf("tasksRefireDeliveryFailures[%s] = %d, want 0 (MCP-init gate must not count as a delivery failure)", sessionID, failures)
	}
	runner.cancelTasksRebaseTimerForTest(sessionID)
}

// TestLoopRunner_FireTasksRebase_MCPInitGated_DefersWithoutCounterBump drives
// the busy/quiescence re-fire path (fireTasksRebase -> maybeFireAccumulatedDelta)
// while gated: the baseline must stay un-rebased, a re-fire must be armed, and
// the delivery-failure counter must NOT be bumped (mirrors the
// tasksRefireCooldownDeferred self-heal shape, but without counting a strike).
func TestLoopRunner_FireTasksRebase_MCPInitGated_DefersWithoutCounterBump(t *testing.T) {
	const sessionID = "s1"
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"),
	)

	runner, ps := newTasksRefireTestRunner(t, sessionID, rawNow)
	if err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	// Long window so the re-armed quiescence timer does not fire in the
	// background during the test; we drive fireTasksRebase directly.
	runner.SetTasksQuiescenceWindow(time.Hour)

	bs := runner.sessionManager.GetSession(sessionID)
	if bs == nil {
		t.Fatal("expected registered session")
	}
	bs.sharedProcess = &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: false}

	runner.fireTasksRebase(sessionID, ps)

	baseline, err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawBefore) {
		t.Errorf("baseline.RawSnapshot = %s, want %s (must not rebase while gated)", baseline.RawSnapshot, rawBefore)
	}
	if got := countTasksRebaseTimers(runner); got != 1 {
		t.Errorf("tasksRebaseTimers = %d, want 1 (delivery deferral must re-arm)", got)
	}
	if !runner.tasksRefirePendingForTest(sessionID) {
		t.Error("tasksRefirePending must be re-set so the next quiescence tick retries")
	}
	runner.tasksRebaseTimersMu.Lock()
	failures := runner.tasksRefireDeliveryFailures[sessionID]
	runner.tasksRebaseTimersMu.Unlock()
	if failures != 0 {
		t.Errorf("tasksRefireDeliveryFailures[%s] = %d, want 0 (MCP-init gate must not count as a delivery failure)", sessionID, failures)
	}
	runner.cancelTasksRebaseTimerForTest(sessionID)
}

// TestLoopRunner_FireOnStartPulses_MCPInitGated_DefersThenFiresOnceWarm proves
// the boot-pulse gate (fireOnStartPulses): while gated the pulse must defer
// WITHOUT consuming the once-per-process guard and WITHOUT engaging the
// delivery-failure classifier (loop stays enabled); once the process warms up
// a later tick must attempt the dispatch again (guard consumed), instead of
// being short-circuited by the gate forever.
func TestLoopRunner_FireOnStartPulses_MCPInitGated_DefersThenFiresOnceWarm(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newRunOnStartSession(t, store, "s1", session.TriggerOnTasks)

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := NewTestBackgroundSessionWithCtx("s1", ctx, cancel)
	gated := &gatedSharedProcess{alwaysFailSharedProcess: &alwaysFailSharedProcess{}, inProgress: true, done: false}
	bs.sharedProcess = gated
	sm.AddSessionForTest(bs)

	runner := NewLoopRunner(store, sm, nil)

	runner.fireOnStartPulses()
	if runner.HasFiredRunOnStart("s1") {
		t.Fatal("runOnStartFired[s1] set while MCP-init gated; want deferred (guard rolled back for a later retry)")
	}
	loop, err := store.Loop("s1").Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if !loop.Enabled {
		t.Error("loop.Enabled = false after a gated defer; want true (a benign defer must never engage the delivery-failure classifier)")
	}

	// Warm up: un-gate. A later tick must now ATTEMPT the dispatch — the
	// once-per-process guard being consumed proves the gate no longer
	// short-circuits fireOnStartPulses.
	gated.inProgress = false
	gated.done = true
	runner.fireOnStartPulses()
	if !runner.HasFiredRunOnStart("s1") {
		t.Error("runOnStartFired[s1] not set once un-gated; want the pulse attempted (no longer short-circuited by the MCP-init gate)")
	}
}
