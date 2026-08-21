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
const tasksDefaultQuiescenceWindow = 10 * time.Second

// tasksListTimeout bounds how long a single `bd list` invocation may take when
// fetching a beads snapshot for onTasks condition evaluation.
const tasksListTimeout = 30 * time.Second

// maxTasksRefireDeliveryFailures bounds how many consecutive
// tasksRefireDeliveryFailed self-heal cycles fireTasksRebase will re-arm (via
// markTasksRefirePending + armTasksRebase) before giving up and logging at
// ERROR instead (mitto-rrq work item 3). Without this bound a durably-broken
// loop prompt (as opposed to a transient compile-race, which is already
// absorbed by the bounded in-process retry in triggerTasksFireWithRetry)
// could re-arm the quiescence timer forever. Mirrors the "3 consecutive
// strikes" convention used by MaxPromptResolveFailures (mitto-8bg).
const maxTasksRefireDeliveryFailures = 3

// tasksTransientRetryDelays is the backoff schedule triggerTasksFireWithRetry
// uses to retry an onTasks fire (triggerNowWithTasksDelta) that failed due to
// a transient template-compile-race (mitto-rrq work item 2, mirroring the
// mitto-omu queue-dispatcher policy — see queueTransientRetryDelays in
// queue_dispatcher.go). Entry i is the sleep BEFORE attempt i+2, so total
// attempts is 1 + len(tasksTransientRetryDelays). Package-var so tests can
// override to []time.Duration{0, 0, 0} for speed.
var tasksTransientRetryDelays = []time.Duration{
	50 * time.Millisecond,
	200 * time.Millisecond,
	500 * time.Millisecond,
}

// tasksTransientRetrySleep is the sleep function used between onTasks fire
// retry attempts. Package-var so tests can override to a no-op recorder
// without waiting.
var tasksTransientRetrySleep = time.Sleep

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

// SetTasksSettleWindow sets the runner-level default pre-fire settle/debounce
// window applied on the idle→first-fire path of the onTasks trigger
// (mitto-1uv). Values <= 0 disable the runner-level default (per-loop
// LoopPrompt.SettleWindow() still applies when set). Intended primarily for
// tests to use a short window; production loops opt in per-prompt via
// LoopPrompt.SettleWindowSeconds.
func (r *LoopRunner) SetTasksSettleWindow(d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasksSettleWindow = d
}

// effectiveTasksSettleWindow returns the effective pre-fire settle window for
// a given loop: the per-loop LoopPrompt.SettleWindow() if set (> 0), otherwise
// the runner-level default (r.tasksSettleWindow). Returns 0 when neither is
// set — the current fire-on-first-delta behaviour.
func (r *LoopRunner) effectiveTasksSettleWindow(loop *session.LoopPrompt) time.Duration {
	if loop != nil {
		if w := loop.SettleWindow(); w > 0 {
			return w
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tasksSettleWindow
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
	if r.eventCooldownActive(loop) {
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
		// A fs-watcher delta arrived during a busy window. Set the sticky
		// re-fire flag (mitto-cwg.1) so fireTasksRebase, at quiescence,
		// triggers exactly one follow-up run regardless of
		// ShouldCoalesceDuringBusy(). The flag is "mark, don't stack": any
		// number of fs events during the busy window collapses to a single
		// pending re-fire. User-driven prompt sends are unaffected — they go
		// through a different path and remain coalesced per the current
		// coalesceDuringBusy semantics.
		r.markTasksRefirePending(sessionID)
		r.armTasksRebase(sessionID, loopStore)

	case tasksActionInitBaseline:
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to initialize baseline",
				"session_id", sessionID, "error", err)
		}

	case tasksActionFire:
		// Pre-fire settle/debounce: if a settle window is configured for this
		// loop (per-prompt LoopPrompt.SettleWindow() or the runner-level
		// default), arm/reset a one-shot settle timer instead of firing now
		// (mitto-1uv). Subsequent material fs-watcher deltas during the window
		// reset the timer; when it expires, fireTasksSettle re-evaluates
		// against the current beads snapshot and dispatches a single coalesced
		// fire. This absorbs multi-step agent edits (e.g. `bd create` followed
		// by `bd update`) that would otherwise fire on the first partial
		// delta.
		if window := r.effectiveTasksSettleWindow(loop); window > 0 {
			r.armTasksSettleTimer(sessionID, loopStore, window)
			return
		}
		// A normal fire consumes any pending re-fire flag — the accumulated
		// delta is being delivered as this run's payload, so a follow-up
		// re-fire on quiescence would be redundant (mitto-cwg.1).
		r.clearTasksRefirePending(sessionID)
		// Thread the computed delta through so the loop prompt body can render
		// {{ .Trigger.OnTasks.Changes.* }} (mitto-xkn). All other TriggerNow
		// call sites pass nil via the public TriggerNow shim. mitto-rrq work
		// item 2: bounded retry absorbs a transient template-compile-race
		// instead of dropping the fire on the first hit.
		if err, exhausted := r.triggerTasksFireWithRetry(sessionID, decision.delta); err != nil {
			// Route ErrPromptResolveFailed through the shared 3-strike auto-pause
			// logic so onTasks loops behave the same as the scheduled path when a
			// loop_prompt_name no longer resolves (mitto-uhnc); without this
			// parity the failure counter never bumps and the loop retries silently
			// forever.
			if errors.Is(err, ErrPromptResolveFailed) {
				r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
			} else if errors.Is(err, ErrSessionBusy) {
				// Benign, expected outcome already owned by the busy/quiescence
				// path — no self-heal needed, no error logging.
			} else {
				// Durable delivery failure (e.g. an ACP handshake error) is
				// deferrable, not terminal (mitto-c9kp). Self-heal identically
				// to fireTasksRebase's tasksRefireDeliveryFailed outcome: mark
				// a pending re-fire and arm the quiescence timer so the delta
				// (left un-rebased below) is retried instead of dropped,
				// bounded by maxTasksRefireDeliveryFailures.
				if r.bumpTasksRefireDeliveryFailure(sessionID) < maxTasksRefireDeliveryFailures {
					r.markTasksRefirePending(sessionID)
					r.armTasksRebase(sessionID, loopStore)
					if exhausted && r.logger != nil {
						r.logger.Error("onTasks: firing failed after transient-race retries",
							"session_id", sessionID, "error", err, "retries_exhausted", true)
					} else if r.logger != nil {
						r.logger.Warn("onTasks: firing failed; self-healing via re-arm", "session_id", sessionID, "error", err)
					}
				} else {
					if r.logger != nil {
						r.logger.Error("onTasks: firing failed repeatedly; giving up self-heal without rebasing baseline",
							"session_id", sessionID, "error", err,
							"max_attempts", maxTasksRefireDeliveryFailures, "retries_exhausted", true)
					}
					r.clearTasksRefireDeliveryFailures(sessionID)
				}
			}
			return
		}
		r.clearTasksRefireDeliveryFailures(sessionID)
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

// triggerTasksFireWithRetry wraps triggerNowWithTasksDelta with the mitto-omu
// bounded retry policy for transient template-compile-race failures (mitto-rrq
// work item 2). Durable errors and ErrSessionBusy — an expected, non-noisy
// outcome the onTasks fire paths already special-case for logging — short-
// circuit on the first attempt; only isTransientPromptCompileRace errors are
// retried, exactly mirroring queueDispatcher.send's classification. Returns
// the final error (nil on success) and whether every attempt was exhausted on
// a transient error, for the retries_exhausted log marker.
func (r *LoopRunner) triggerTasksFireWithRetry(sessionID string, delta *config.TasksDelta) (err error, exhausted bool) {
	maxAttempts := 1 + len(tasksTransientRetryDelays)
	var lastAttempt int
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastAttempt = attempt
		err = r.triggerNowWithTasksDelta(sessionID, true, delta)
		if err == nil || errors.Is(err, ErrSessionBusy) || !isTransientPromptCompileRace(err) {
			return err, false
		}
		if attempt >= maxAttempts {
			break
		}
		delay := tasksTransientRetryDelays[attempt-1]
		if r.logger != nil {
			r.logger.Warn("onTasks: fire hit transient prompt compile race; retrying",
				"session_id", sessionID,
				"error", err,
				"attempt", attempt,
				"next_delay", delay)
		}
		tasksTransientRetrySleep(delay)
	}
	exhausted = isTransientPromptCompileRace(err) && lastAttempt >= maxAttempts
	return err, exhausted
}

// eventCooldownActive returns true if firing should be skipped because the
// per-conversation cooldown (clamped to the global floor) has not elapsed
// since the last delivery. Despite the "tasks"-flavoured floor knobs it reads
// (minTasksCooldownSeconds et al.), this checks loop.LastSentAt — a
// per-conversation, trigger-agnostic timestamp updated on every delivery
// regardless of which trigger fired it — so it is shared, unmodified, by both
// event-driven triggers: onTasks (evaluateTasksChange, evaluateAccumulatedDelta)
// and onChild (fireOnChild, loop_runner_child.go). This was named
// tasksCooldownActive before mitto-987y.4 generalised its call sites; the
// floor field/knob names were deliberately left alone (config-wired, and
// referenced by existing tests) since only the cooldown check itself needed
// to be trigger-neutral.
func (r *LoopRunner) eventCooldownActive(loop *session.LoopPrompt) bool {
	return r.eventCooldownRemaining(loop) > 0
}

// eventCooldownRemaining returns how long remains before another event-driven
// delivery may fire. A zero duration means the cooldown is not active.
func (r *LoopRunner) eventCooldownRemaining(loop *session.LoopPrompt) time.Duration {
	if loop.LastSentAt == nil {
		return 0
	}
	r.mu.Lock()
	floor := r.minTasksCooldownSeconds
	r.mu.Unlock()

	cooldown := loop.CooldownSeconds
	if cooldown < floor {
		cooldown = floor
	}
	if cooldown <= 0 {
		return 0
	}
	remaining := time.Until(loop.LastSentAt.Add(time.Duration(cooldown) * time.Second))
	if remaining < 0 {
		return 0
	}
	return remaining
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

// markTasksRefirePending sets the sticky per-session flag consumed by
// fireTasksRebase at quiescence (mitto-cwg.1). Called by processTasksChange
// on the tasksActionDeferBusy branch — i.e. exactly when a fs-watcher delta
// arrives during a busy window and would otherwise be silently absorbed by
// the plain rebase under the default coalesceDuringBusy=true.
func (r *LoopRunner) markTasksRefirePending(sessionID string) {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	r.tasksRefirePending[sessionID] = true
}

// clearTasksRefirePending drops any pending re-fire flag for sessionID. Called
// on a normal fire (tasksActionFire) because that fire already delivers the
// accumulated delta as its payload, and on session archival/shutdown so no
// stale entries linger. Also clears the tasksRefireDeliveryFailures self-heal
// counter (mitto-rrq work item 3) — a fresh normal fire supersedes any
// previous re-fire delivery failure for this session.
func (r *LoopRunner) clearTasksRefirePending(sessionID string) {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	delete(r.tasksRefirePending, sessionID)
	delete(r.tasksRefireDeliveryFailures, sessionID)
}

// bumpTasksRefireDeliveryFailure increments and returns the consecutive
// tasksRefireDeliveryFailed counter for sessionID (mitto-rrq work item 3).
func (r *LoopRunner) bumpTasksRefireDeliveryFailure(sessionID string) int {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	r.tasksRefireDeliveryFailures[sessionID]++
	return r.tasksRefireDeliveryFailures[sessionID]
}

// clearTasksRefireDeliveryFailures resets the consecutive
// tasksRefireDeliveryFailed counter for sessionID (mitto-rrq work item 3).
// Called on any successful re-fire dispatch (and, via clearTasksRefirePending,
// on any normal fire) so a session that recovers starts the bounded self-heal
// counter fresh the next time a delivery failure occurs.
func (r *LoopRunner) clearTasksRefireDeliveryFailures(sessionID string) {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	delete(r.tasksRefireDeliveryFailures, sessionID)
}

// armTasksRebase schedules a baseline rebase for sessionID after the
// quiescence window, replacing (and stopping) any timer already pending so at
// most one rebase is queued per session.
func (r *LoopRunner) armTasksRebase(sessionID string, loopStore *session.LoopStore) {
	r.mu.Lock()
	window := r.tasksQuiescenceWindow
	r.mu.Unlock()
	r.armTasksRebaseAfter(sessionID, loopStore, window)
}

// armTasksRebaseAfter schedules a rebase/re-fire check after delay, replacing
// any existing timer so each session retains at most one pending check.
func (r *LoopRunner) armTasksRebaseAfter(sessionID string, loopStore *session.LoopStore, delay time.Duration) {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	if existing, ok := r.tasksRebaseTimers[sessionID]; ok {
		existing.Stop()
	}
	r.tasksRebaseTimers[sessionID] = time.AfterFunc(delay, func() {
		r.fireTasksRebase(sessionID, loopStore)
	})
}

// armTasksSettleTimer schedules (or resets) the pre-fire settle timer for
// sessionID (mitto-1uv). Called from processTasksChange on tasksActionFire
// when an effective settle window is configured. Subsequent material
// fs-watcher deltas that route through tasksActionFire while the timer is
// pending reset it — so N rapid deltas collapse to a single fire once the
// window elapses without a further delta. If the subtree becomes busy during
// the settle window, evaluateTasksChange returns tasksActionDeferBusy on the
// next delta and the busy/quiescence path takes over; the settle timer, if
// still pending when it fires, is a no-op because fireTasksSettle re-checks
// busyness.
func (r *LoopRunner) armTasksSettleTimer(sessionID string, loopStore *session.LoopStore, window time.Duration) {
	r.tasksSettleTimersMu.Lock()
	defer r.tasksSettleTimersMu.Unlock()
	if existing, ok := r.tasksSettleTimers[sessionID]; ok {
		existing.Stop()
	}
	r.tasksSettleTimers[sessionID] = time.AfterFunc(window, func() {
		r.fireTasksSettle(sessionID, loopStore)
	})
}

// fireTasksSettle is invoked when a pre-fire settle timer expires without a
// further material delta. It re-fetches the current beads snapshot, re-runs
// evaluateTasksChange to re-apply all Layer 0/1/2 guards and the CEL
// condition against the freshest state, and — on a positive decision —
// dispatches the coalesced fire directly (bypassing the settle path so it
// cannot re-arm itself indefinitely). The fire may still be filtered out by
// cooldown, maxDuration, an archived session, a now-busy subtree (which
// hands off to the busy/quiescence path), or a condition that no longer
// matches (mitto-1uv).
func (r *LoopRunner) fireTasksSettle(sessionID string, loopStore *session.LoopStore) {
	r.tasksSettleTimersMu.Lock()
	delete(r.tasksSettleTimers, sessionID)
	r.tasksSettleTimersMu.Unlock()

	if r.store == nil {
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
			r.logger.Warn("onTasks: failed to list beads for settle fire",
				"session_id", sessionID, "error", err)
		}
		return
	}

	decision := r.evaluateTasksChange(meta, loop, raw)
	switch decision.action {
	case tasksActionDeferBusy:
		// Subtree became busy during the settle window — hand off to the
		// existing busy/quiescence path exactly like a fresh delta would.
		r.markTasksRefirePending(sessionID)
		r.armTasksRebase(sessionID, loopStore)

	case tasksActionInitBaseline:
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to initialize baseline on settle",
				"session_id", sessionID, "error", err)
		}

	case tasksActionFire:
		// Dispatch the coalesced fire directly. Do NOT re-arm the settle
		// timer here — that would produce an unbounded stall as long as
		// evaluateTasksChange keeps returning tasksActionFire.
		r.clearTasksRefirePending(sessionID)
		// mitto-rrq work item 2: bounded retry absorbs a transient
		// template-compile-race instead of dropping the settled fire.
		if err, exhausted := r.triggerTasksFireWithRetry(sessionID, decision.delta); err != nil {
			if errors.Is(err, ErrPromptResolveFailed) {
				r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
			} else if errors.Is(err, ErrSessionBusy) {
				// Benign, expected outcome already owned by the busy/quiescence
				// path — no self-heal needed, no error logging.
			} else {
				// Durable delivery failure (e.g. an ACP handshake error) is
				// deferrable, not terminal (mitto-c9kp). Self-heal identically
				// to fireTasksRebase's tasksRefireDeliveryFailed outcome: mark
				// a pending re-fire and arm the quiescence timer so the delta
				// (left un-rebased below) is retried instead of dropped,
				// bounded by maxTasksRefireDeliveryFailures.
				if r.bumpTasksRefireDeliveryFailure(sessionID) < maxTasksRefireDeliveryFailures {
					r.markTasksRefirePending(sessionID)
					r.armTasksRebase(sessionID, loopStore)
					if exhausted && r.logger != nil {
						r.logger.Error("onTasks: settled firing failed after transient-race retries",
							"session_id", sessionID, "error", err, "retries_exhausted", true)
					} else if r.logger != nil {
						r.logger.Warn("onTasks: settled firing failed; self-healing via re-arm", "session_id", sessionID, "error", err)
					}
				} else {
					if r.logger != nil {
						r.logger.Error("onTasks: settled firing failed repeatedly; giving up self-heal without rebasing baseline",
							"session_id", sessionID, "error", err,
							"max_attempts", maxTasksRefireDeliveryFailures, "retries_exhausted", true)
					}
					r.clearTasksRefireDeliveryFailures(sessionID)
				}
			}
			return
		}
		r.clearTasksRefireDeliveryFailures(sessionID)
		if err := decision.baseline.Set(raw); err != nil && r.logger != nil {
			r.logger.Warn("onTasks: failed to persist baseline after settled fire",
				"session_id", sessionID, "error", err)
		}

	case tasksActionSkip:
		// No-op: cooldown, maxDuration, immaterial delta, or condition-false.
	}
}

// fireTasksRebase re-checks idleness and, once the subtree is confirmed idle,
// rebases the onTasks baseline to the current beads snapshot — absorbing any
// edits the conversation (or a delegated child) made to beads during its run.
// If still busy, it re-arms itself for another quiescence window.
//
// Two paths lead to a follow-up fire instead of a plain baseline rebase:
//
//   - loop.ShouldCoalesceDuringBusy() is false (mitto-dmb) — the loop is
//     explicitly opted out of during-busy coalescing.
//   - the sticky tasksRefirePending flag is set for this session (mitto-cwg.1)
//     — a fs-watcher delta arrived during the busy window. This takes effect
//     regardless of ShouldCoalesceDuringBusy(), which continues to gate only
//     user prompt-send coalescing.
//
// In either case a material delta between the pre-run baseline and the current
// snapshot causes exactly one follow-up fire (subject to Layer 0
// cooldown/maxDuration and the CEL condition) before the baseline is rebased.
// If both paths are inactive and no re-fire is pending, the default plain
// baseline rebase runs.
func (r *LoopRunner) fireTasksRebase(sessionID string, loopStore *session.LoopStore) {
	r.tasksRebaseTimersMu.Lock()
	delete(r.tasksRebaseTimers, sessionID)
	// Read+clear the sticky re-fire flag atomically with timer cleanup so a
	// concurrent processTasksChange that re-sets the flag after this point
	// arms a fresh timer and its own follow-up (mitto-cwg.1). The flag is
	// consumed here whether or not the re-fire actually goes through. A
	// cooldown-blocked material delta is explicitly restored below; durable
	// no-fire outcomes such as condition-false still fall through to rebase.
	refirePending := r.tasksRefirePending[sessionID]
	delete(r.tasksRefirePending, sessionID)
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

	// Re-fire path: taken when either the loop is explicitly opted out of
	// during-busy coalescing (mitto-dmb — user prompt-sends and fs-watcher
	// deltas both flow through here) OR the sticky refirePending flag is set
	// for this session (mitto-cwg.1 — fs-watcher delta arrived during busy;
	// the coalesceDuringBusy default of true no longer gates fs deltas).
	// evaluateAccumulatedDelta computes the delta between the pre-run baseline
	// and the current snapshot and applies the Layer 0 guards (cooldown,
	// maxDuration, CEL condition); if it clears them, fire once more.
	//
	// mitto-rrq: maybeFireAccumulatedDelta returns a tri-state outcome so a
	// delivery failure (tasksRefireDeliveryFailed) is NEVER treated the same
	// as "no fire warranted" (tasksRefireNotWarranted) — only the latter falls
	// through to the plain baseline rebase below. A delivery failure instead
	// self-heals by re-arming the quiescence timer (bounded by
	// maxTasksRefireDeliveryFailures) so the pending delta survives to be
	// retried, instead of being silently destroyed by an unconditional rebase.
	if !loop.ShouldCoalesceDuringBusy() || refirePending {
		switch r.maybeFireAccumulatedDelta(sessionID, meta, loop, loopStore, baselineStore, raw) {
		case tasksRefireDispatched:
			// Fire path persisted its own baseline; nothing more to do.
			r.clearTasksRefireDeliveryFailures(sessionID)
			return
		case tasksRefireDeliveryFailed:
			if r.bumpTasksRefireDeliveryFailure(sessionID) < maxTasksRefireDeliveryFailures {
				r.markTasksRefirePending(sessionID)
				r.armTasksRebase(sessionID, loopStore)
				return
			}
			if r.logger != nil {
				r.logger.Error("onTasks: re-fire delivery failed repeatedly; giving up self-heal without rebasing baseline",
					"session_id", sessionID,
					"max_attempts", maxTasksRefireDeliveryFailures,
					"retries_exhausted", true)
			}
			r.clearTasksRefireDeliveryFailures(sessionID)
			return
		case tasksRefireCooldownDeferred:
			// Cooldown is temporary: preserve the pre-run baseline and sticky
			// pending marker, then retry once at the cooldown boundary instead of
			// waiting another full quiescence window (mitto-wsnv).
			r.markTasksRefirePending(sessionID)
			r.armTasksRebaseAfter(sessionID, loopStore, r.eventCooldownRemaining(loop))
			return
		case tasksRefireNotWarranted:
			// No pending problem to preserve — clear any stale counter and
			// fall through to the plain baseline rebase below.
			r.clearTasksRefireDeliveryFailures(sessionID)
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
	if r.eventCooldownActive(loop) {
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

// tasksRefireOutcome is the outcome of maybeFireAccumulatedDelta (mitto-rrq).
// It replaces a plain bool so the caller (fireTasksRebase) can distinguish
// "no fire warranted" (safe to rebase the baseline) from "a fire was
// warranted but delivery failed" (must NOT rebase — the delta must survive to
// be retried). Collapsing these two outcomes into a single false was the root
// cause of mitto-rrq: a transient template-compile-race resolve failure was
// treated identically to "nothing to do" and the baseline was rebased anyway,
// permanently destroying the triggering delta.
type tasksRefireOutcome int

const (
	// tasksRefireNotWarranted means no material change existed, a Layer 0
	// durable guard blocked firing (maxDuration, condition-false), or a parse
	// error occurred. Safe for the caller to fall back to a plain rebase.
	tasksRefireNotWarranted tasksRefireOutcome = iota
	// tasksRefireDispatched means the fire was dispatched successfully; the
	// new baseline was already persisted by maybeFireAccumulatedDelta itself.
	tasksRefireDispatched
	// tasksRefireDeliveryFailed means a fire was warranted (material delta +
	// guards passed) but triggerNowWithTasksDelta failed even after the
	// bounded transient-race retry. The caller MUST NOT rebase the baseline.
	tasksRefireDeliveryFailed
	// tasksRefireCooldownDeferred means a material delta was blocked only by
	// the temporary cooldown. The caller MUST preserve it and retry at expiry.
	tasksRefireCooldownDeferred
)

// maybeFireAccumulatedDelta wires evaluateAccumulatedDelta to the firing side
// effects: on a positive decision it fires via triggerTasksFireWithRetry
// (mitto-rrq work item 2 — bounded retry on a transient template-compile-race)
// and persists the new baseline on success. meta is threaded through purely so
// a durable ErrPromptResolveFailed can be routed to handlePromptResolveFailure
// (mitto-rrq work item 4 — mitto-uhnc parity with the other two fire paths).
func (r *LoopRunner) maybeFireAccumulatedDelta(sessionID string, meta session.Metadata, loop *session.LoopPrompt, loopStore *session.LoopStore, baselineStore *TasksBaselineStore, raw []byte) tasksRefireOutcome {
	delta, shouldFire := r.evaluateAccumulatedDelta(sessionID, loop, loopStore, baselineStore, raw)
	if !shouldFire {
		// MaxDuration is checked before cooldown and may have disabled the loop.
		// Re-read persisted state before classifying this as a temporary defer so
		// a terminal stop can never arm another retry.
		if delta != nil && r.eventCooldownActive(loop) {
			current, err := loopStore.Get()
			if err == nil && current != nil && current.Enabled {
				return tasksRefireCooldownDeferred
			}
		}
		return tasksRefireNotWarranted
	}

	err, exhausted := r.triggerTasksFireWithRetry(sessionID, delta)
	if err != nil {
		if errors.Is(err, ErrPromptResolveFailed) {
			r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
			return tasksRefireDeliveryFailed
		}
		if r.logger != nil && !errors.Is(err, ErrSessionBusy) {
			if exhausted {
				r.logger.Error("onTasks: re-fire failed after transient-race retries",
					"session_id", sessionID, "error", err, "retries_exhausted", true)
			} else {
				r.logger.Warn("onTasks: re-fire failed", "session_id", sessionID, "error", err)
			}
		}
		return tasksRefireDeliveryFailed
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
	return tasksRefireDispatched
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
