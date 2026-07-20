package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// DefaultMinLoopTasksCooldownSeconds is the default floor (seconds) applied
// to the onTasks loop trigger's cooldown between fires, preventing hot
// loops from rapid beads churn. Mirrors DefaultMinLoopCompletionDelaySeconds
// for the onCompletion trigger.
const DefaultMinLoopTasksCooldownSeconds = 30

// tasksDefaultQuiescenceWindow is the default value for tasksQuiescenceWindow.
const tasksDefaultQuiescenceWindow = 30 * time.Second

// tasksListTimeout bounds how long a single `bd list` invocation may take when
// fetching a beads snapshot for onTasks condition evaluation.
const tasksListTimeout = 30 * time.Second

// Compile-time assertion: *LoopRunner implements watcher.BeadsSubscriber.
var _ watcher.BeadsSubscriber = (*LoopRunner)(nil)

// SetBeadsClient injects the beads.Client used to list issues for onTasks
// condition evaluation. Intended for tests; production code may leave this
// unset to lazily default to beads.NewClient().
func (r *LoopRunner) SetBeadsClient(c beads.Client) {
	r.beadsClientMu.Lock()
	defer r.beadsClientMu.Unlock()
	r.beadsClient = c
}

// SetBeadsWatcher records the BeadsWatcher the runner is subscribed to as a
// BeadsSubscriber. Stop() calls Unsubscribe(r) on it so that in-flight
// debounced fan-outs during shutdown no longer route to a stopped runner
// (mitto-cbx). Safe to leave nil in tests that don't wire a watcher.
func (r *LoopRunner) SetBeadsWatcher(w *watcher.BeadsWatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beadsWatcher = w
}

// beadsClientOrDefault returns the configured beads.Client, lazily defaulting
// to beads.NewClient() on first use.
func (r *LoopRunner) beadsClientOrDefault() beads.Client {
	r.beadsClientMu.Lock()
	defer r.beadsClientMu.Unlock()
	if r.beadsClient == nil {
		r.beadsClient = beads.NewClient()
	}
	return r.beadsClient
}

// SetMinLoopTasksCooldownSeconds sets the global floor for the onTasks
// trigger's cooldown between fires. Values < 0 are clamped to 0.
func (r *LoopRunner) SetMinLoopTasksCooldownSeconds(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minTasksCooldownSeconds = n
}

// MinLoopTasksCooldownSeconds returns the current floor for the onTasks
// trigger's cooldown between fires, in seconds.
func (r *LoopRunner) MinLoopTasksCooldownSeconds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.minTasksCooldownSeconds
}

// SetTasksQuiescenceWindow sets how long the onTasks loop waits, after a busy
// conversation's whole child subtree goes idle, before rebasing the baseline.
// Intended for tests to use a short window; production uses
// tasksDefaultQuiescenceWindow.
func (r *LoopRunner) SetTasksQuiescenceWindow(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasksQuiescenceWindow = d
}

// OnBeadsChanged implements watcher.BeadsSubscriber. It is called by the
// BeadsWatcher whenever a watched .beads/ directory changes. For every
// enabled onTasks conversation whose working directory matches one of the
// changed directories, it diffs the latest beads snapshot against that
// conversation's persisted baseline, evaluates the configured CEL condition,
// and fires the conversation via TriggerNow when the guards allow it.
//
// The beads snapshot for each distinct working directory is listed at most
// once per call, regardless of how many onTasks conversations share it.
func (r *LoopRunner) OnBeadsChanged(event watcher.BeadsChangeEvent) {
	if r.store == nil || r.tasksEvaluator == nil {
		return
	}

	// Belt-and-suspenders shutdown guard (mitto-cbx): the BeadsWatcher
	// fan-out snapshots subscribers under its RLock and invokes them
	// outside the lock, so a Stop()+Unsubscribe that races with an
	// in-flight fan-out can still land here. Drop the event silently if
	// Stop() has already run — the store is likely closed too. We check
	// `stopped` (set once in Stop()) rather than `!running`, because
	// tests routinely invoke OnBeadsChanged without ever calling Start().
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}

	workingDirSet := make(map[string]struct{}, len(event.WorkingDirs))
	for _, d := range event.WorkingDirs {
		workingDirSet[d] = struct{}{}
	}
	if len(workingDirSet) == 0 {
		return
	}

	sessions, err := r.store.List()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("onTasks: failed to list sessions", "error", err)
		}
		return
	}

	rawCache := make(map[string][]byte)
	failedDirs := make(map[string]struct{})

	for _, meta := range sessions {
		if meta.Archived {
			continue
		}
		if _, ok := workingDirSet[meta.WorkingDir]; !ok {
			continue
		}

		loopStore := r.store.Loop(meta.SessionID)
		loop, err := loopStore.Get()
		if err != nil || loop == nil || !loop.Enabled || !loop.IsOnTasks() {
			continue
		}

		if _, failed := failedDirs[meta.WorkingDir]; failed {
			continue
		}
		raw, ok := rawCache[meta.WorkingDir]
		if !ok {
			ctx, cancel := context.WithTimeout(context.Background(), tasksListTimeout)
			raw, err = r.beadsClientOrDefault().List(ctx, meta.WorkingDir)
			cancel()
			if err != nil {
				failedDirs[meta.WorkingDir] = struct{}{}
				if r.logger != nil {
					r.logger.Warn("onTasks: failed to list beads",
						"working_dir", meta.WorkingDir, "error", err)
				}
				continue
			}
			rawCache[meta.WorkingDir] = raw
		}

		r.processTasksChange(meta, loop, loopStore, raw)
	}
}

// tasksAction is the outcome of evaluateTasksChange: what processTasksChange
// should do next.
type tasksAction int

const (
	// tasksActionSkip means no observable action is needed (a guard blocked
	// evaluation, the delta was not material, or the condition was false/errored).
	tasksActionSkip tasksAction = iota
	// tasksActionDeferBusy means the conversation's subtree is busy; a
	// quiescence-gated rebase should be armed instead of firing now.
	tasksActionDeferBusy
	// tasksActionInitBaseline means no baseline existed yet; one should be
	// captured now WITHOUT firing (no spurious first run).
	tasksActionInitBaseline
	// tasksActionFire means all guards passed and the condition evaluated
	// true; the conversation should be fired now via TriggerNow.
	tasksActionFire
)

// tasksDecision is the result of evaluateTasksChange.
type tasksDecision struct {
	action   tasksAction
	delta    *config.TasksDelta
	baseline *TasksBaselineStore
}

// evaluateTasksChange applies the layered onTasks loop-prevention guards and
// the CEL condition to decide what should happen for a single conversation
// given the latest beads snapshot (raw) for its working directory. It performs
// no side effects other than logging — callers (processTasksChange) act on the
// returned decision. Kept side-effect-free (besides logging) so the decision
// logic is directly unit-testable without a session manager or ACP connection.
func (r *LoopRunner) evaluateTasksChange(meta session.Metadata, loop *session.LoopPrompt, raw []byte) tasksDecision {
	sessionID := meta.SessionID

	// Layer 1 (temporal): ignore while the conversation or any delegated child
	// is active.
	if r.isTasksSubtreeBusy(sessionID) {
		return tasksDecision{action: tasksActionDeferBusy}
	}

	// Auto-stop if the wall-clock maxDuration cap is reached, exactly like the
	// other triggers (fireOnCompletion / checkSession).
	loopStore := r.store.Loop(sessionID)
	if r.autoStopIfMaxDurationReached(sessionID, loop, loopStore, time.Now()) {
		return tasksDecision{action: tasksActionSkip}
	}

	// Layer 0 (hard backstop): per-conversation cooldown floor.
	if r.tasksCooldownActive(loop) {
		return tasksDecision{action: tasksActionSkip}
	}

	baselineStore := NewTasksBaselineStore(r.store.SessionDir(sessionID))
	baseline, err := baselineStore.Get()
	if err != nil {
		// No baseline yet — initialize it now WITHOUT firing (no spurious first run).
		return tasksDecision{action: tasksActionInitBaseline, baseline: baselineStore}
	}

	prevSnap, perr := config.ParseTasksSnapshot(baseline.RawSnapshot)
	if perr != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to parse persisted baseline",
				"session_id", sessionID, "error", perr)
		}
		return tasksDecision{action: tasksActionSkip}
	}
	currSnap, perr := config.ParseTasksSnapshot(raw)
	if perr != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to parse beads snapshot",
				"session_id", sessionID, "error", perr)
		}
		return tasksDecision{action: tasksActionSkip}
	}

	delta := config.DiffTasks(prevSnap, currSnap)
	if !tasksDeltaIsMaterial(delta) {
		// Nothing actually changed relative to the baseline (e.g. a debounced
		// fs event with no real content difference) — leave the baseline as-is.
		return tasksDecision{action: tasksActionSkip}
	}

	changeCtx := &config.TasksChangeContext{Tasks: currSnap, Prev: prevSnap, Changes: delta}
	ok, evalErr := r.tasksEvaluator.Evaluate(loop.Condition, changeCtx)
	if evalErr != nil {
		// Fail-closed: a misconfigured condition must not silently fire.
		if r.logger != nil {
			r.logger.Warn("onTasks: condition evaluation failed (fail-closed, not firing)",
				"session_id", sessionID, "condition", loop.Condition, "error", evalErr)
		}
		return tasksDecision{action: tasksActionSkip, delta: delta}
	}
	if !ok {
		return tasksDecision{action: tasksActionSkip, delta: delta}
	}

	return tasksDecision{action: tasksActionFire, delta: delta, baseline: baselineStore}
}

// processTasksChange evaluates a single onTasks conversation against the
// latest beads snapshot (raw) for its working directory and acts on the
// resulting decision: arming a rebase, initializing the baseline, or firing.
func (r *LoopRunner) processTasksChange(meta session.Metadata, loop *session.LoopPrompt, loopStore *session.LoopStore, raw []byte) {
	sessionID := meta.SessionID
	decision := r.evaluateTasksChange(meta, loop, raw)

	switch decision.action {
	case tasksActionDeferBusy:
		r.armTasksRebase(sessionID, loopStore)

	case tasksActionInitBaseline:
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to initialize baseline",
				"session_id", sessionID, "error", err)
		}

	case tasksActionFire:
		// Thread the computed delta through so the loop prompt body can render
		// {{ .Trigger.OnTasks.Changes.* }} (mitto-xkn). All other TriggerNow
		// call sites pass nil via the public TriggerNow shim.
		if err := r.triggerNowWithTasksDelta(sessionID, true, decision.delta); err != nil {
			// Route ErrPromptResolveFailed through the shared 3-strike auto-pause
			// logic so onTasks loops behave the same as the scheduled path when a
			// loop_prompt_name no longer resolves (mitto-uhnc); without this
			// parity the failure counter never bumps and the loop retries silently
			// forever.
			if errors.Is(err, ErrPromptResolveFailed) {
				r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
			} else if r.logger != nil && !errors.Is(err, ErrSessionBusy) {
				r.logger.Warn("onTasks: firing failed", "session_id", sessionID, "error", err)
			}
			return
		}
		// Persist the new baseline now that the run has been kicked off. Any
		// beads edits the run itself (or a delegated child) makes while busy
		// are caught by Layer 1 and absorbed later by the idle+quiescence
		// rebase (Layer 2).
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to persist baseline after fire",
				"session_id", sessionID, "error", err)
		}

	case tasksActionSkip:
		// Nothing to do.
	}
}

// tasksDeltaIsMaterial reports whether delta represents an actual content
// change (something added, updated, or removed) as opposed to a debounced
// no-op fs event.
func tasksDeltaIsMaterial(delta *config.TasksDelta) bool {
	if delta == nil {
		return false
	}
	return len(delta.Added) > 0 || len(delta.Updated) > 0 || len(delta.Removed) > 0
}

// tasksCooldownActive returns true if firing should be skipped because the
// per-conversation cooldown (clamped to the global floor) has not elapsed
// since the last delivery.
func (r *LoopRunner) tasksCooldownActive(loop *session.LoopPrompt) bool {
	if loop.LastSentAt == nil {
		return false
	}
	r.mu.Lock()
	floor := r.minTasksCooldownSeconds
	r.mu.Unlock()

	cooldown := loop.CooldownSeconds
	if cooldown < floor {
		cooldown = floor
	}
	if cooldown <= 0 {
		return false
	}
	return time.Since(*loop.LastSentAt) < time.Duration(cooldown)*time.Second
}

// isTasksSubtreeBusy returns true if the conversation, or any conversation in
// its delegated-child subtree, is currently prompting or blocked on
// mitto_children_tasks_wait.
func (r *LoopRunner) isTasksSubtreeBusy(sessionID string) bool {
	if r.sessionManager == nil || r.store == nil {
		return false
	}
	if r.isSessionBusy(sessionID) {
		return true
	}
	children, err := r.store.FindAllChildrenRecursive(sessionID)
	if err != nil {
		return false
	}
	for _, childID := range children {
		if r.isSessionBusy(childID) {
			return true
		}
	}
	return false
}

// isSessionBusy returns true if sessionID is currently prompting or blocked on
// mitto_children_tasks_wait.
func (r *LoopRunner) isSessionBusy(sessionID string) bool {
	if bs := r.sessionManager.GetSession(sessionID); bs != nil && bs.IsPrompting() {
		return true
	}
	return r.sessionManager.IsWaitingForChildren(sessionID)
}

// armTasksRebase schedules a baseline rebase for sessionID after the
// quiescence window, replacing (and stopping) any timer already pending so at
// most one rebase is queued per session.
func (r *LoopRunner) armTasksRebase(sessionID string, loopStore *session.LoopStore) {
	r.mu.Lock()
	window := r.tasksQuiescenceWindow
	r.mu.Unlock()

	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	if existing, ok := r.tasksRebaseTimers[sessionID]; ok {
		existing.Stop()
	}
	r.tasksRebaseTimers[sessionID] = time.AfterFunc(window, func() {
		r.fireTasksRebase(sessionID, loopStore)
	})
}

// fireTasksRebase re-checks idleness and, once the subtree is confirmed idle,
// rebases the onTasks baseline to the current beads snapshot — absorbing any
// edits the conversation (or a delegated child) made to beads during its run.
// If still busy, it re-arms itself for another quiescence window.
//
// When loop.ShouldCoalesceDuringBusy() is false (mitto-dmb), a material delta
// between the pre-run baseline and the current snapshot causes exactly one
// follow-up fire (subject to Layer 0 cooldown/maxDuration and the CEL
// condition) before the baseline is rebased. The default (true) preserves the
// original silent-absorption behaviour.
func (r *LoopRunner) fireTasksRebase(sessionID string, loopStore *session.LoopStore) {
	r.tasksRebaseTimersMu.Lock()
	delete(r.tasksRebaseTimers, sessionID)
	r.tasksRebaseTimersMu.Unlock()

	if r.store == nil {
		return
	}

	if r.isTasksSubtreeBusy(sessionID) {
		r.armTasksRebase(sessionID, loopStore)
		return
	}

	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.Archived {
		return
	}

	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnTasks() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tasksListTimeout)
	raw, err := r.beadsClientOrDefault().List(ctx, meta.WorkingDir)
	cancel()
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to list beads for baseline rebase",
				"session_id", sessionID, "error", err)
		}
		return
	}

	baselineStore := NewTasksBaselineStore(r.store.SessionDir(sessionID))

	// Opt-in re-fire path (mitto-dmb): when the loop is configured NOT to
	// coalesce during busy, evaluate the accumulated delta between the pre-run
	// baseline and the current snapshot and fire once more if the guards allow.
	if !loop.ShouldCoalesceDuringBusy() {
		if r.maybeFireAccumulatedDelta(sessionID, loop, loopStore, baselineStore, raw) {
			// Fire path persisted its own baseline; nothing more to do.
			return
		}
	}

	if err := baselineStore.Set(raw); err != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to rebase baseline", "session_id", sessionID, "error", err)
		}
		return
	}
	if r.logger != nil {
		r.logger.Debug("onTasks: baseline rebased after idle+quiescence", "session_id", sessionID)
	}
}

// evaluateAccumulatedDelta computes the delta between the pre-run baseline and
// the current beads snapshot for an onTasks loop opted out of the during-busy
// coalesce (CoalesceDuringBusy=false), applies the Layer 0 guards
// (maxDuration, cooldown) and the CEL condition, and returns the delta plus
// whether the caller should fire. It performs no side effects other than the
// maxDuration auto-stop (which is itself a Layer 0 guard shared with every
// other trigger path). Kept side-effect-free (besides that guard) so the
// decision is directly unit-testable without a session manager.
//
// shouldFire=false with a non-nil delta means "material change but a guard
// blocked" (cooldown, condition false, etc.); shouldFire=false with nil delta
// means "nothing to do" (no baseline, no material change, parse error).
func (r *LoopRunner) evaluateAccumulatedDelta(sessionID string, loop *session.LoopPrompt, loopStore *session.LoopStore, baselineStore *TasksBaselineStore, raw []byte) (delta *config.TasksDelta, shouldFire bool) {
	baseline, err := baselineStore.Get()
	if err != nil {
		return nil, false
	}

	prevSnap, perr := config.ParseTasksSnapshot(baseline.RawSnapshot)
	if perr != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to parse persisted baseline for re-fire",
				"session_id", sessionID, "error", perr)
		}
		return nil, false
	}
	currSnap, perr := config.ParseTasksSnapshot(raw)
	if perr != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to parse beads snapshot for re-fire",
				"session_id", sessionID, "error", perr)
		}
		return nil, false
	}

	d := config.DiffTasks(prevSnap, currSnap)
	if !tasksDeltaIsMaterial(d) {
		return nil, false
	}

	// Layer 0: honour maxDuration and cooldown exactly like a normal fire.
	if r.autoStopIfMaxDurationReached(sessionID, loop, loopStore, time.Now()) {
		return d, false
	}
	if r.tasksCooldownActive(loop) {
		return d, false
	}

	// CEL condition (fail-closed on error) — the same rule used by
	// evaluateTasksChange for the normal event-driven fire.
	if r.tasksEvaluator != nil {
		changeCtx := &config.TasksChangeContext{Tasks: currSnap, Prev: prevSnap, Changes: d}
		ok, evalErr := r.tasksEvaluator.Evaluate(loop.Condition, changeCtx)
		if evalErr != nil {
			if r.logger != nil {
				r.logger.Warn("onTasks: re-fire condition evaluation failed (fail-closed, not firing)",
					"session_id", sessionID, "condition", loop.Condition, "error", evalErr)
			}
			return d, false
		}
		if !ok {
			return d, false
		}
	}

	return d, true
}

// maybeFireAccumulatedDelta wires evaluateAccumulatedDelta to the firing side
// effects: on a positive decision it fires via triggerNowWithTasksDelta and
// persists the new baseline. Returns true only when the fire was dispatched
// (so the caller can skip the fallback plain rebase); every "no fire" outcome
// — including TriggerNow failing — returns false so the caller falls back to
// a plain rebase.
func (r *LoopRunner) maybeFireAccumulatedDelta(sessionID string, loop *session.LoopPrompt, loopStore *session.LoopStore, baselineStore *TasksBaselineStore, raw []byte) bool {
	delta, shouldFire := r.evaluateAccumulatedDelta(sessionID, loop, loopStore, baselineStore, raw)
	if !shouldFire {
		return false
	}

	if err := r.triggerNowWithTasksDelta(sessionID, true, delta); err != nil {
		if r.logger != nil && !errors.Is(err, ErrSessionBusy) {
			r.logger.Warn("onTasks: re-fire failed", "session_id", sessionID, "error", err)
		}
		return false
	}
	if err := baselineStore.Set(raw); err != nil && r.logger != nil {
		r.logger.Warn("onTasks: failed to persist baseline after re-fire",
			"session_id", sessionID, "error", err)
	}
	if r.logger != nil {
		r.logger.Debug("onTasks: re-fired after idle+quiescence with accumulated delta",
			"session_id", sessionID,
			"added", len(delta.Added),
			"updated", len(delta.Updated),
			"removed", len(delta.Removed))
	}
	return true
}

// BootstrapTasksBaseline initializes the onTasks baseline for a session if one
// does not exist yet, WITHOUT firing — preventing a spurious first run when a
// conversation is newly enabled for onTasks or the server restarts before any
// baseline was ever captured. No-op for sessions that already have a baseline,
// are archived, are not onTasks, or are not enabled.
func (r *LoopRunner) BootstrapTasksBaseline(sessionID string) {
	if r.store == nil {
		return
	}

	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnTasks() {
		return
	}

	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.Archived {
		return
	}

	baselineStore := NewTasksBaselineStore(r.store.SessionDir(sessionID))
	if _, err := baselineStore.Get(); err == nil {
		return // already initialized
	}

	ctx, cancel := context.WithTimeout(context.Background(), tasksListTimeout)
	raw, err := r.beadsClientOrDefault().List(ctx, meta.WorkingDir)
	cancel()
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to bootstrap baseline", "session_id", sessionID, "error", err)
		}
		return
	}
	if err := baselineStore.Set(raw); err != nil && r.logger != nil {
		r.logger.Warn("onTasks: failed to persist bootstrap baseline", "session_id", sessionID, "error", err)
	}
}
