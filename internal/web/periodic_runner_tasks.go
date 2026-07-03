package web

import (
	"context"
	"errors"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// DefaultMinPeriodicTasksCooldownSeconds is the default floor (seconds) applied
// to the onTasks periodic trigger's cooldown between fires, preventing hot
// loops from rapid beads churn. Mirrors DefaultMinPeriodicCompletionDelaySeconds
// for the onCompletion trigger.
const DefaultMinPeriodicTasksCooldownSeconds = 30

// tasksDefaultQuiescenceWindow is the default value for tasksQuiescenceWindow.
const tasksDefaultQuiescenceWindow = 30 * time.Second

// tasksListTimeout bounds how long a single `bd list` invocation may take when
// fetching a beads snapshot for onTasks condition evaluation.
const tasksListTimeout = 30 * time.Second

// tasksNoProgressLimit is the number of consecutive onTasks fires that touch no
// issue beyond what the previous fire already touched before the circuit
// breaker (Layer 3) auto-pauses the trigger.
const tasksNoProgressLimit = 3

// Compile-time assertion: *PeriodicRunner implements config.BeadsSubscriber.
var _ config.BeadsSubscriber = (*PeriodicRunner)(nil)

// SetBeadsClient injects the beads.Client used to list issues for onTasks
// condition evaluation. Intended for tests; production code may leave this
// unset to lazily default to beads.NewClient().
func (r *PeriodicRunner) SetBeadsClient(c beads.Client) {
	r.beadsClientMu.Lock()
	defer r.beadsClientMu.Unlock()
	r.beadsClient = c
}

// beadsClientOrDefault returns the configured beads.Client, lazily defaulting
// to beads.NewClient() on first use.
func (r *PeriodicRunner) beadsClientOrDefault() beads.Client {
	r.beadsClientMu.Lock()
	defer r.beadsClientMu.Unlock()
	if r.beadsClient == nil {
		r.beadsClient = beads.NewClient()
	}
	return r.beadsClient
}

// SetMinPeriodicTasksCooldownSeconds sets the global floor for the onTasks
// trigger's cooldown between fires. Values < 0 are clamped to 0.
func (r *PeriodicRunner) SetMinPeriodicTasksCooldownSeconds(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minTasksCooldownSeconds = n
}

// MinPeriodicTasksCooldownSeconds returns the current floor for the onTasks
// trigger's cooldown between fires, in seconds.
func (r *PeriodicRunner) MinPeriodicTasksCooldownSeconds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.minTasksCooldownSeconds
}

// SetTasksQuiescenceWindow sets how long the onTasks loop waits, after a busy
// conversation's whole child subtree goes idle, before rebasing the baseline.
// Intended for tests to use a short window; production uses
// tasksDefaultQuiescenceWindow.
func (r *PeriodicRunner) SetTasksQuiescenceWindow(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasksQuiescenceWindow = d
}

// OnBeadsChanged implements config.BeadsSubscriber. It is called by the
// BeadsWatcher whenever a watched .beads/ directory changes. For every
// enabled onTasks conversation whose working directory matches one of the
// changed directories, it diffs the latest beads snapshot against that
// conversation's persisted baseline, evaluates the configured CEL condition,
// and fires the conversation via TriggerNow when the guards allow it.
//
// The beads snapshot for each distinct working directory is listed at most
// once per call, regardless of how many onTasks conversations share it.
func (r *PeriodicRunner) OnBeadsChanged(event config.BeadsChangeEvent) {
	if r.store == nil || r.tasksEvaluator == nil {
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

		periodicStore := r.store.Periodic(meta.SessionID)
		periodic, err := periodicStore.Get()
		if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnTasks() {
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

		r.processTasksChange(meta, periodic, periodicStore, raw)
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
func (r *PeriodicRunner) evaluateTasksChange(meta session.Metadata, periodic *session.PeriodicPrompt, raw []byte) tasksDecision {
	sessionID := meta.SessionID

	// Layer 1 (temporal): ignore while the conversation or any delegated child
	// is active.
	if r.isTasksSubtreeBusy(sessionID) {
		return tasksDecision{action: tasksActionDeferBusy}
	}

	// Auto-stop if the wall-clock maxDuration cap is reached, exactly like the
	// other triggers (fireOnCompletion / checkSession).
	periodicStore := r.store.Periodic(sessionID)
	if r.autoStopIfMaxDurationReached(sessionID, periodic, periodicStore, time.Now()) {
		return tasksDecision{action: tasksActionSkip}
	}

	// Layer 0 (hard backstop): per-conversation cooldown floor.
	if r.tasksCooldownActive(periodic) {
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
	ok, evalErr := r.tasksEvaluator.Evaluate(periodic.Condition, changeCtx)
	if evalErr != nil {
		// Fail-closed: a misconfigured condition must not silently fire.
		if r.logger != nil {
			r.logger.Warn("onTasks: condition evaluation failed (fail-closed, not firing)",
				"session_id", sessionID, "condition", periodic.Condition, "error", evalErr)
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
func (r *PeriodicRunner) processTasksChange(meta session.Metadata, periodic *session.PeriodicPrompt, periodicStore *session.PeriodicStore, raw []byte) {
	sessionID := meta.SessionID
	decision := r.evaluateTasksChange(meta, periodic, raw)

	switch decision.action {
	case tasksActionDeferBusy:
		r.armTasksRebase(sessionID, periodicStore)

	case tasksActionInitBaseline:
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to initialize baseline",
				"session_id", sessionID, "error", err)
		}

	case tasksActionFire:
		if err := r.TriggerNow(sessionID, true); err != nil {
			if r.logger != nil && !errors.Is(err, ErrSessionBusy) {
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
		r.recordTasksFireOutcome(sessionID, periodicStore, decision.delta)

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
func (r *PeriodicRunner) tasksCooldownActive(periodic *session.PeriodicPrompt) bool {
	if periodic.LastSentAt == nil {
		return false
	}
	r.mu.Lock()
	floor := r.minTasksCooldownSeconds
	r.mu.Unlock()

	cooldown := periodic.CooldownSeconds
	if cooldown < floor {
		cooldown = floor
	}
	if cooldown <= 0 {
		return false
	}
	return time.Since(*periodic.LastSentAt) < time.Duration(cooldown)*time.Second
}

// isTasksSubtreeBusy returns true if the conversation, or any conversation in
// its delegated-child subtree, is currently prompting or blocked on
// mitto_children_tasks_wait.
func (r *PeriodicRunner) isTasksSubtreeBusy(sessionID string) bool {
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
func (r *PeriodicRunner) isSessionBusy(sessionID string) bool {
	if bs := r.sessionManager.GetSession(sessionID); bs != nil && bs.IsPrompting() {
		return true
	}
	return r.sessionManager.IsWaitingForChildren(sessionID)
}

// armTasksRebase schedules a baseline rebase for sessionID after the
// quiescence window, replacing (and stopping) any timer already pending so at
// most one rebase is queued per session.
func (r *PeriodicRunner) armTasksRebase(sessionID string, periodicStore *session.PeriodicStore) {
	r.mu.Lock()
	window := r.tasksQuiescenceWindow
	r.mu.Unlock()

	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	if existing, ok := r.tasksRebaseTimers[sessionID]; ok {
		existing.Stop()
	}
	r.tasksRebaseTimers[sessionID] = time.AfterFunc(window, func() {
		r.fireTasksRebase(sessionID, periodicStore)
	})
}

// fireTasksRebase re-checks idleness and, once the subtree is confirmed idle,
// rebases the onTasks baseline to the current beads snapshot — absorbing any
// edits the conversation (or a delegated child) made to beads during its run.
// If still busy, it re-arms itself for another quiescence window.
func (r *PeriodicRunner) fireTasksRebase(sessionID string, periodicStore *session.PeriodicStore) {
	r.tasksRebaseTimersMu.Lock()
	delete(r.tasksRebaseTimers, sessionID)
	r.tasksRebaseTimersMu.Unlock()

	if r.store == nil {
		return
	}

	if r.isTasksSubtreeBusy(sessionID) {
		r.armTasksRebase(sessionID, periodicStore)
		return
	}

	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.Archived {
		return
	}

	periodic, err := periodicStore.Get()
	if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnTasks() {
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

// BootstrapTasksBaseline initializes the onTasks baseline for a session if one
// does not exist yet, WITHOUT firing — preventing a spurious first run when a
// conversation is newly enabled for onTasks or the server restarts before any
// baseline was ever captured. No-op for sessions that already have a baseline,
// are archived, are not onTasks, or are not enabled.
func (r *PeriodicRunner) BootstrapTasksBaseline(sessionID string) {
	if r.store == nil {
		return
	}

	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnTasks() {
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

// recordTasksFireOutcome implements the Layer 3 circuit breaker: it tracks,
// per session, the set of issue IDs touched by consecutive onTasks fires. When
// tasksNoProgressLimit consecutive fires touch no issue beyond what the
// previous fire already touched (e.g. a steady-state-true condition with no
// genuine forward progress), it auto-pauses the trigger via MarkStopped,
// mirroring the existing failure-pause patterns (handlePromptResolveFailure,
// autoStopIfMaxDurationReached).
func (r *PeriodicRunner) recordTasksFireOutcome(sessionID string, periodicStore *session.PeriodicStore, delta *config.TasksDelta) {
	curr := tasksTouchedIDs(delta)

	r.tasksNoProgressMu.Lock()
	prev := r.tasksLastTouchedIDs[sessionID]
	noProgress := tasksIsSubsetOf(curr, prev)
	if noProgress {
		r.tasksNoProgressCount[sessionID]++
	} else {
		r.tasksNoProgressCount[sessionID] = 0
	}
	count := r.tasksNoProgressCount[sessionID]
	r.tasksLastTouchedIDs[sessionID] = curr
	r.tasksNoProgressMu.Unlock()

	if count < tasksNoProgressLimit {
		return
	}

	if err := periodicStore.MarkStopped(session.StoppedReasonNoProgress); err != nil {
		if r.logger != nil {
			r.logger.Warn("onTasks: failed to auto-pause after no-progress fires",
				"session_id", sessionID, "error", err)
		}
		return
	}

	r.tasksNoProgressMu.Lock()
	delete(r.tasksNoProgressCount, sessionID)
	delete(r.tasksLastTouchedIDs, sessionID)
	r.tasksNoProgressMu.Unlock()

	if r.onPeriodicAutoStopped != nil {
		if final, err := periodicStore.Get(); err == nil {
			r.onPeriodicAutoStopped(sessionID, final)
		}
	}
	if r.logger != nil {
		r.logger.Warn("onTasks: auto-paused after repeated no-progress fires (circuit breaker)",
			"session_id", sessionID, "consecutive_no_progress", count)
	}
}

// tasksTouchedIDs extracts the set of issue IDs from delta.Touched.
func tasksTouchedIDs(delta *config.TasksDelta) map[string]struct{} {
	ids := make(map[string]struct{})
	if delta == nil {
		return ids
	}
	for _, issue := range delta.Touched {
		if id, ok := issue["id"].(string); ok && id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids
}

// tasksIsSubsetOf reports whether every id in curr is also present in prev,
// meaning curr touched nothing genuinely new relative to the previous fire.
// An empty curr is trivially a subset (no progress signal at all).
func tasksIsSubsetOf(curr, prev map[string]struct{}) bool {
	for id := range curr {
		if _, ok := prev[id]; !ok {
			return false
		}
	}
	return true
}
