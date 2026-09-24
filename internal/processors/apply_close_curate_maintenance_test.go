package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/bdexec"
	"github.com/inercia/mitto/internal/session"
)

// newCurateMemoriesTestManager builds a Manager with a single, bare
// curate-memories-on-close processor (no EnabledWhen — the CEL gate is
// exercised separately by the YAML-loader tests in processors_test.go; these
// tests target only the mitto-1kl gating orchestration in ApplyOnClose).
func newCurateMemoriesTestManager() *Manager {
	m := NewManager("", nil)
	m.processors = []*Processor{{
		Name:   curateMemoriesProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "curate the memory store",
	}}
	return m
}

// isolateMemoryCurationDir points appdir.MemoryCurationStateDir() at a fresh
// temp directory for the duration of the test, since ApplyOnClose's gating
// branch hardcodes baseDir="" (production always uses the real appdir path).
func isolateMemoryCurationDir(t *testing.T) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

// installFakeBdMemoryCount points PATH at a fake `bd` script that answers
// `bd --readonly memories --json` with an object containing exactly count
// keys, mirroring the fakeBd pattern used throughout processors_test.go.
func installFakeBdMemoryCount(t *testing.T, count int) {
	t.Helper()
	keys := make(map[string]any, count)
	for i := 0; i < count; i++ {
		keys[fmt.Sprintf("key-%d", i)] = struct{}{}
	}
	body, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then printf 'bd version 1.2.2 (test)\\n'; exit 0; fi\n" +
		"cat <<'MITTO_BD_EOF'\n" + string(body) + "\nMITTO_BD_EOF\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(fake bd): %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// blockingPromptCompletionFunc returns a PromptCompletionFunc that blocks
// until release is closed, then reports success. Used to make the in-flight
// coalescing window (between dispatch and completion) deterministic instead
// of racing a goroutine.
func blockingPromptCompletionFunc(release <-chan struct{}, calls *atomic.Int32) PromptCompletionFunc {
	return func(context.Context, string, string, string, string) (PromptCompletion, error) {
		calls.Add(1)
		<-release
		return PromptCompletion{FinalMessage: "done"}, nil
	}
}

// waitForCompletionCalls polls until calls reaches want or the deadline
// elapses, avoiding a fixed sleep for the async dispatch goroutine.
func waitForCompletionCalls(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("dispatch calls = %d, want >= %d within timeout", calls.Load(), want)
}

// recordingRunsRecorder is a concurrency-safe ProcessorRun sink for
// SetRunRecorder in these tests.
type recordingRunsRecorder struct {
	mu   sync.Mutex
	runs []ProcessorRun
}

func (r *recordingRunsRecorder) record(run ProcessorRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, run)
}

func (r *recordingRunsRecorder) snapshot() []ProcessorRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProcessorRun, len(r.runs))
	copy(out, r.runs)
	return out
}

// --- Empty / first-ever run -------------------------------------------------

// TestApplyOnClose_CurateMemories_FirstRun_DispatchesAndPersistsCount covers
// the AC's "empty" case: a workspace with no prior ledger must dispatch on
// the very first close (the zero-value LastRunAt trivially satisfies the
// interval gate) and, on completion, persist the observed memory count.
func TestApplyOnClose_CurateMemories_FirstRun_DispatchesAndPersistsCount(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 10)
	const workspaceUUID = "ws-curate-first-run"

	m := newCurateMemoriesTestManager()
	var calls atomic.Int32
	release := make(chan struct{})
	close(release) // let this run's dispatch complete immediately
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     "s1",
		WorkspaceUUID: workspaceUUID,
		WorkingDir:    t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1)
	// Give the deferred onCompletion callback (runs right after the tracked
	// dispatch returns) a moment to persist the ledger.
	deadline := time.Now().Add(2 * time.Second)
	var state session.MemoryCurationState
	for time.Now().Before(deadline) {
		var err error
		state, err = session.ReadMemoryCurationState("", workspaceUUID)
		if err != nil {
			t.Fatalf("ReadMemoryCurationState() error = %v", err)
		}
		if state.InFlightRunID == "" && !state.LastRunAt.IsZero() {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if state.InFlightRunID != "" {
		t.Errorf("InFlightRunID = %q, want cleared after completion", state.InFlightRunID)
	}
	if state.LastRunMemoryCount != 10 {
		t.Errorf("LastRunMemoryCount = %d, want 10 (observed count)", state.LastRunMemoryCount)
	}
	if state.LastRunAt.IsZero() {
		t.Error("LastRunAt is zero, want set to completion time")
	}
}

// --- Below interval AND below threshold: skip -------------------------------

// TestApplyOnClose_CurateMemories_BelowThresholds_Skips covers the AC's
// "below-threshold" case: a recent LastRunAt and a small memory-count delta
// must skip dispatch entirely with SkipReasonMaintenanceBelowThreshold.
func TestApplyOnClose_CurateMemories_BelowThresholds_Skips(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 11) // delta of 1 vs LastRunMemoryCount=10, below default MinChangedMemories=5
	const workspaceUUID = "ws-curate-below-threshold"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		LastRunAt:          fixedNow.Add(-1 * time.Hour), // well below default MinInterval=24h
		LastRunMemoryCount: 10,
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	m := newCurateMemoriesTestManager()
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     "s1",
		WorkspaceUUID: workspaceUUID,
		WorkingDir:    t.TempDir(),
	})

	// The skip is synchronous inside ApplyOnClose; give an accidental async
	// dispatch a moment to prove its absence before asserting.
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("dispatch calls = %d, want 0 (below both thresholds must not dispatch)", got)
	}

	runs := rec.snapshot()
	if len(runs) != 1 || runs[0].Outcome != "skipped" || runs[0].SkipReason != string(SkipReasonMaintenanceBelowThreshold) {
		t.Fatalf("runs = %+v, want exactly one skipped run with SkipReason=%s", runs, SkipReasonMaintenanceBelowThreshold)
	}
}

// waitForLedgerSettled polls the ledger until InFlightRunID clears (the
// completion callback has run) or the deadline elapses.
func waitForLedgerSettled(t *testing.T, workspaceUUID string) session.MemoryCurationState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var state session.MemoryCurationState
	for time.Now().Before(deadline) {
		var err error
		state, err = session.ReadMemoryCurationState("", workspaceUUID)
		if err != nil {
			t.Fatalf("ReadMemoryCurationState() error = %v", err)
		}
		if state.InFlightRunID == "" {
			return state
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("ledger for %s never settled (InFlightRunID stayed %q)", workspaceUUID, state.InFlightRunID)
	return state
}

// --- Threshold crossing: dispatch despite a recent interval -----------------

// TestApplyOnClose_CurateMemories_ThresholdCrossing_DispatchesDespiteRecentRun
// covers the AC's "threshold-crossing" case: a large enough memory-count
// delta forces a dispatch even though MinInterval has not elapsed.
func TestApplyOnClose_CurateMemories_ThresholdCrossing_DispatchesDespiteRecentRun(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 20) // delta of 10 vs LastRunMemoryCount=10, crosses default MinChangedMemories=5
	const workspaceUUID = "ws-curate-threshold-crossing"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		LastRunAt:          fixedNow.Add(-1 * time.Hour), // well below default MinInterval=24h
		LastRunMemoryCount: 10,
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	m := newCurateMemoriesTestManager()
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	close(release)
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     "s1",
		WorkspaceUUID: workspaceUUID,
		WorkingDir:    t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1)
	state := waitForLedgerSettled(t, workspaceUUID)
	if state.LastRunMemoryCount != 20 {
		t.Errorf("LastRunMemoryCount = %d, want 20", state.LastRunMemoryCount)
	}
	runs := rec.snapshot()
	if len(runs) != 1 || runs[0].Outcome != "ok" {
		t.Fatalf("runs = %+v, want exactly one dispatched (ok) run", runs)
	}
}

// --- Concurrent closes coalesce into at most one dispatch -------------------

// TestApplyOnClose_CurateMemories_ConcurrentCloses_CoalesceIntoOneDispatch
// covers the AC's "concurrent" case: a second close arriving while the first
// run is still in flight must skip with SkipReasonMaintenanceInFlight rather
// than dispatching a second run, and the first run's eventual completion
// must still persist normally.
func TestApplyOnClose_CurateMemories_ConcurrentCloses_CoalesceIntoOneDispatch(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 10)
	const workspaceUUID = "ws-curate-concurrent"

	m := newCurateMemoriesTestManager()
	var calls atomic.Int32
	release := make(chan struct{}) // held closed until we've observed the coalesced skip
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	// First close: the in-flight marker is written synchronously (before the
	// async dispatch goroutine even starts), so it is guaranteed visible to
	// the second call below regardless of goroutine scheduling.
	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "s1", WorkspaceUUID: workspaceUUID, WorkingDir: t.TempDir(),
	})

	// Second, concurrent close for the same workspace: must coalesce.
	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "s2", WorkspaceUUID: workspaceUUID, WorkingDir: t.TempDir(),
	})

	runs := rec.snapshot()
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want exactly 2 recorded (one dispatched, one coalesced)", runs)
	}
	if runs[0].Outcome != "ok" {
		t.Errorf("first run = %+v, want Outcome=ok", runs[0])
	}
	if runs[1].Outcome != "skipped" || runs[1].SkipReason != string(SkipReasonMaintenanceInFlight) {
		t.Errorf("second run = %+v, want Outcome=skipped SkipReason=%s", runs[1], SkipReasonMaintenanceInFlight)
	}

	// Release the first (only) dispatch and confirm it still completes and
	// persists normally — coalescing must not corrupt the winning run.
	close(release)
	waitForCompletionCalls(t, &calls, 1)
	state := waitForLedgerSettled(t, workspaceUUID)
	if state.LastRunMemoryCount != 10 {
		t.Errorf("LastRunMemoryCount = %d, want 10", state.LastRunMemoryCount)
	}
}

// --- Retry after crash: a stale in-flight marker is overwritten -------------

// TestApplyOnClose_CurateMemories_StaleInFlightMarker_TreatedAsCrashedAndRedispatches
// covers the AC's "retry" case: an in-flight marker whose lease has expired
// (the prior run crashed mid-dispatch) must be treated as not-in-flight and a
// fresh run dispatched, overwriting the stale RunID.
func TestApplyOnClose_CurateMemories_StaleInFlightMarker_TreatedAsCrashedAndRedispatches(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 15)
	const workspaceUUID = "ws-curate-stale-inflight"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		InFlightRunID:     "stale-run-from-a-crash",
		InFlightStartedAt: fixedNow.Add(-20 * time.Minute), // past memoryCurationInFlightLease=15m
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	m := newCurateMemoriesTestManager()
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	close(release)
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "s1", WorkspaceUUID: workspaceUUID, WorkingDir: t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1)
	state := waitForLedgerSettled(t, workspaceUUID)
	if state.LastRunMemoryCount != 15 {
		t.Errorf("LastRunMemoryCount = %d, want 15 (fresh run completed)", state.LastRunMemoryCount)
	}
	runs := rec.snapshot()
	if len(runs) != 1 || runs[0].Outcome != "ok" {
		t.Fatalf("runs = %+v, want exactly one dispatched (ok) run (stale marker must not skip)", runs)
	}
}

// --- Count failure fails open ------------------------------------------------

// TestApplyOnClose_CurateMemories_CountFailsOpen_DispatchesAnyway documents
// the deliberate fail-open behavior when the memory count cannot be
// determined (e.g. bd is missing): gating must dispatch rather than silently
// starve maintenance forever, and completion must preserve the last-known
// count instead of persisting a misleading 0.
func TestApplyOnClose_CurateMemories_CountFailsOpen_DispatchesAnyway(t *testing.T) {
	isolateMemoryCurationDir(t)
	t.Setenv("PATH", "") // bd is unresolvable: exec.LookPath("bd") fails
	const workspaceUUID = "ws-curate-count-fails-open"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		LastRunAt:          fixedNow.Add(-1 * time.Hour), // recent: interval NOT elapsed
		LastRunMemoryCount: 10,
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	m := newCurateMemoriesTestManager()
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	close(release)
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "s1", WorkspaceUUID: workspaceUUID, WorkingDir: t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1)
	state := waitForLedgerSettled(t, workspaceUUID)
	// countKnown is false, so finalCount preserves the prior LastRunMemoryCount
	// rather than persisting a misleading 0.
	if state.LastRunMemoryCount != 10 {
		t.Errorf("LastRunMemoryCount = %d, want 10 (preserved when the count is unknown)", state.LastRunMemoryCount)
	}
	runs := rec.snapshot()
	if len(runs) != 1 || runs[0].Outcome != "ok" {
		t.Fatalf("runs = %+v, want exactly one dispatched (ok) run (count failure must fail open)", runs)
	}
}

// --- Interval crossing: dispatch despite a small delta ----------------------

// TestApplyOnClose_CurateMemories_IntervalCrossing_DispatchesDespiteLowDelta
// covers the AC's "interval" case: MinInterval elapsed forces a dispatch even
// though the memory-count delta is below MinChangedMemories.
func TestApplyOnClose_CurateMemories_IntervalCrossing_DispatchesDespiteLowDelta(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 11) // delta of 1, below default MinChangedMemories=5
	const workspaceUUID = "ws-curate-interval-crossing"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		LastRunAt:          fixedNow.Add(-25 * time.Hour), // past default MinInterval=24h
		LastRunMemoryCount: 10,
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	m := newCurateMemoriesTestManager()
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	close(release)
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))
	rec := &recordingRunsRecorder{}
	m.SetRunRecorder(rec.record)

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     "s1",
		WorkspaceUUID: workspaceUUID,
		WorkingDir:    t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1)
	state := waitForLedgerSettled(t, workspaceUUID)
	if state.LastRunMemoryCount != 11 {
		t.Errorf("LastRunMemoryCount = %d, want 11", state.LastRunMemoryCount)
	}
	runs := rec.snapshot()
	if len(runs) != 1 || runs[0].Outcome != "ok" {
		t.Fatalf("runs = %+v, want exactly one dispatched (ok) run", runs)
	}
}

// --- Count timeout (mitto-bitr): must not be logged as WARN -----------------

// occupyAllBdSlots calls bdexec.Acquire once per process-wide execution slot
// (bdexec.MaxConcurrent) against distinct scratch directories (so only the
// shared global `slots` channel is contended, not any per-database mutex)
// and never releases them for the duration of the test. This reproduces the
// LITERAL "context deadline exceeded" error bdexec.Acquire returns when its
// own `case <-ctx.Done()` branch wins — the actual production path for
// mitto-bitr — as opposed to killing a slow subprocess mid-flight, which
// instead surfaces as "signal: killed".
func occupyAllBdSlots(t *testing.T) {
	t.Helper()
	for i := 0; i < bdexec.MaxConcurrent; i++ {
		release, err := bdexec.Acquire(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("bdexec.Acquire(warm-up slot %d) error = %v", i, err)
		}
		t.Cleanup(release)
	}
}

// TestApplyOnClose_CurateMemories_CountTimeout_NotLoggedAsWarn reproduces
// mitto-bitr: countBdMemories derives its context via
// context.WithTimeout(ctx, 10*time.Second), which inherits whichever
// deadline is EARLIER, and then contends for bdexec's process-wide execution
// slots. When every slot is already held (busy host, other bd invocations in
// flight) and the caller's own context has a short deadline, bdexec.Acquire's
// `case <-ctx.Done()` branch wins and returns the exact
// "context deadline exceeded" error family reported by the bug — before bd
// is even invoked, so bd itself is healthy and would have answered. The gate
// must still fail open (dispatch anyway — safe, unchanged by this bug), but
// per the bead's acceptance criteria the timeout must no longer be logged at
// WARN. This test currently FAILS (the WARN fires) until the fix demotes the
// log level and/or gives the count its own independent deadline.
func TestApplyOnClose_CurateMemories_CountTimeout_NotLoggedAsWarn(t *testing.T) {
	isolateMemoryCurationDir(t)
	installFakeBdMemoryCount(t, 10) // bd itself is healthy; only the slot wait times out
	occupyAllBdSlots(t)
	const workspaceUUID = "ws-curate-count-timeout"

	fixedNow := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if err := session.WriteMemoryCurationState("", workspaceUUID, session.MemoryCurationState{
		LastRunAt:          fixedNow.Add(-1 * time.Hour), // interval NOT elapsed
		LastRunMemoryCount: 10,
	}); err != nil {
		t.Fatalf("WriteMemoryCurationState() error = %v", err)
	}

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.processors = []*Processor{{
		Name:   curateMemoriesProcessorName,
		When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "curate the memory store",
	}}
	m.SetClock(func() time.Time { return fixedNow })
	var calls atomic.Int32
	release := make(chan struct{})
	close(release)
	m.SetPromptCompletionFunc(blockingPromptCompletionFunc(release, &calls))

	// A short-lived context: countBdMemories' inner
	// context.WithTimeout(ctx, 10*time.Second) inherits this 300ms deadline
	// instead of its own 10s ceiling. With every bdexec slot already held
	// (occupyAllBdSlots above), Acquire blocks on this deadline and returns
	// ctx.Err() — literally "context deadline exceeded".
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	m.ApplyOnClose(ctx, CloseProcessorInput{
		SessionID: "s1", WorkspaceUUID: workspaceUUID, WorkingDir: t.TempDir(),
	})

	waitForCompletionCalls(t, &calls, 1) // fail-open: still dispatches despite the count failure

	var found bool
	for _, rec := range handler.snapshot() {
		if !strings.Contains(rec.Message, "memory count failed") {
			continue
		}
		found = true
		errAttr := fmt.Sprint(rec.Attrs["error"])
		if !strings.Contains(errAttr, "deadline exceeded") {
			t.Fatalf("count-failure log record error attr = %q, want to contain %q (test setup did not reproduce a genuine timeout)",
				errAttr, "deadline exceeded")
		}
		if rec.Level == slog.LevelWarn {
			t.Errorf("count timeout logged at WARN (msg=%q attrs=%v); mitto-bitr requires the timeout to no longer be logged as WARN once countBdMemories fails with a context deadline",
				rec.Message, rec.Attrs)
		}
	}
	if !found {
		t.Fatalf("no %q log record captured — test setup did not reproduce the count failure", "memory count failed")
	}
}
