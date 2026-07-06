package web

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPollInterval is the default interval between loop prompt checks.
	DefaultPollInterval = 1 * time.Minute

	// MaxLoopResumeFailures is the number of consecutive ACP resume failures
	// after which a loop session is automatically archived.
	MaxLoopResumeFailures = 3

	// MaxPromptResolveFailures is the number of consecutive prompt-name resolution
	// failures after which the loop config is auto-paused (disabled).
	MaxPromptResolveFailures = 3

	// loopScheduleBackoffBase is the initial delay applied to NextScheduledAt
	// after the first scheduled loop delivery failure. It doubles with each
	// consecutive failure, capped at loopScheduleBackoffCap. This prevents a
	// flaky transport from re-firing the same prompt on every poll tick (mitto-qal.2).
	loopScheduleBackoffBase = 30 * time.Second

	// loopScheduleBackoffCap is the maximum backoff delay for scheduled
	// loop delivery failures.
	loopScheduleBackoffCap = 15 * time.Minute

	// DefaultAutoUnarchiveRetryInterval is the default per-conversation retry
	// cadence for auto-unarchiving loop conversations archived due to broken ACP.
	DefaultAutoUnarchiveRetryInterval = 1 * time.Hour

	// DefaultAutoUnarchiveStaggerInterval is the default global minimum gap
	// between auto-unarchive attempts, preventing a retry storm when many
	// sessions become due at once.
	DefaultAutoUnarchiveStaggerInterval = 10 * time.Minute
)

// loopScheduleBackoff returns the delay to defer the next scheduled run after
// `failures` consecutive delivery failures. It grows exponentially from
// loopScheduleBackoffBase, doubling on each failure, capped at
// loopScheduleBackoffCap.
func loopScheduleBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := loopScheduleBackoffBase
	for i := 1; i < failures; i++ {
		delay *= 2
		if delay >= loopScheduleBackoffCap {
			return loopScheduleBackoffCap
		}
	}
	if delay > loopScheduleBackoffCap {
		delay = loopScheduleBackoffCap
	}
	return delay
}

// Errors for loop runner operations.
var (
	ErrSessionStoreNotAvailable   = errors.New("session store not available")
	ErrSessionManagerNotAvailable = errors.New("session manager not available")
	ErrLoopNotEnabled             = errors.New("loop is not enabled for this session")
	ErrSessionBusy                = errors.New("session is currently processing a prompt")
	ErrPromptResolveFailed        = errors.New("loop prompt could not be resolved")
)

// LoopStartedCallback is called when a loop prompt is delivered.
// sessionID is the session that received the prompt.
// sessionName is the display name of the session.
type LoopStartedCallback func(sessionID, sessionName string)

// AutoArchiveCallback is called when the loop runner auto-archives a session.
// It should handle broadcasting the archive state change and stopping ACP.
type AutoArchiveCallback func(sessionID string)

// AutoUnarchiveFunc is called when the loop runner attempts to auto-unarchive
// a loop conversation previously archived due to broken ACP communication.
// It should perform the same steps as a manual unarchive (resume ACP, restore
// the loop, and broadcast the state changes) and return the resume error, if any.
type AutoUnarchiveFunc func(sessionID string) error

// LoopAutoStoppedCallback is called when a loop conversation is auto-stopped after reaching max iterations.
// It should broadcast the updated loop state to all WebSocket clients.
type LoopAutoStoppedCallback func(sessionID string, loop *session.LoopPrompt)

// LoopUpdatedCallback is called when a loop conversation's schedule advances after a delivery.
// It should broadcast the updated loop state (including the new next_scheduled_at) to all
// WebSocket clients so the countdown resets.
type LoopUpdatedCallback func(sessionID string, loop *session.LoopPrompt)

// LoopRunner manages scheduled loop prompt delivery and session housekeeping.
// It polls all sessions at regular intervals and:
// - Delivers loop prompts that are due
// - Auto-archives sessions inactive beyond the configured threshold
// - Cleans up archived sessions past their retention period
type LoopRunner struct {
	store          *session.Store
	sessionManager *conversation.SessionManager
	logger         *slog.Logger

	pollInterval time.Duration

	// startupDelay is how long to wait before the first poll on startup.
	// This gives interactive sessions time to resume first via WebSocket connections.
	startupDelay time.Duration

	// resumeStagger is the delay between consecutive session resumes within a single poll.
	// This prevents thundering herd when many loop sessions are due simultaneously.
	resumeStagger time.Duration

	// onLoopStarted is called when a loop prompt is delivered
	onLoopStarted LoopStartedCallback

	// onAutoArchive is called when a session is auto-archived.
	// The callback should broadcast the archive state change and ACP stop to WebSocket clients.
	onAutoArchive AutoArchiveCallback

	// onLoopAutoStopped is called when a loop conversation is disabled after reaching max iterations.
	onLoopAutoStopped LoopAutoStoppedCallback

	// onLoopUpdated is called when a loop conversation's schedule advances after a delivery,
	// so clients can reset the countdown to the new next-run time.
	onLoopUpdated LoopUpdatedCallback

	// autoArchiveAfter, when > 0, causes sessions inactive for this long to be archived.
	autoArchiveAfter time.Duration

	// onAutoUnarchive is called when the loop runner attempts to auto-unarchive a loop
	// conversation archived due to broken ACP communication. Guarded implicitly: set
	// once via SetOnAutoUnarchive before Start(), read without locking elsewhere.
	onAutoUnarchive AutoUnarchiveFunc

	// autoUnarchiveEnabled, autoUnarchiveRetryInterval, autoUnarchiveStagger and
	// lastAutoUnarchiveAttempt configure and track the auto-unarchive recovery
	// scheduler. Guarded by mu.
	autoUnarchiveEnabled       bool
	autoUnarchiveRetryInterval time.Duration
	autoUnarchiveStagger       time.Duration
	lastAutoUnarchiveAttempt   time.Time

	// archiveRetentionPeriod, when non-empty, causes archived sessions older than this
	// to be permanently deleted during each poll cycle (not just at startup).
	archiveRetentionPeriod string

	// promptResolver resolves a prompt name to its text at execution time.
	promptResolver conversation.PromptResolver

	// maxLoopIterations is the user-configured default cap on scheduled
	// loop runs. 0 means unlimited; the hardcoded backstop still applies.
	maxLoopIterations int

	// minCompletionDelaySeconds is the global floor applied to the on-completion
	// loop trigger's delay, preventing hot loops.
	minCompletionDelaySeconds int

	// consecutiveFailures tracks how many times in a row a session's loop
	// prompt delivery failed due to ACP resume errors. After MaxLoopResumeFailures
	// consecutive failures, the session is automatically archived.
	consecutiveFailures   map[string]int
	consecutiveFailuresMu sync.Mutex

	// promptResolveFailures tracks consecutive failures to resolve a loop prompt
	// name. After MaxPromptResolveFailures consecutive failures the loop config is
	// auto-paused (disabled) to stop the retry storm.
	promptResolveFailures   map[string]int
	promptResolveFailuresMu sync.Mutex

	// scheduleBackoffFailures tracks consecutive delivery failures for scheduled
	// loop prompts. It drives an exponential backoff on NextScheduledAt so a
	// flaky transport does not cause the same prompt to re-fire every poll tick
	// (mitto-qal.2). Reset to zero on the next successful delivery. Distinct from
	// consecutiveFailures, which tracks resume failures and triggers auto-archive.
	scheduleBackoffFailures   map[string]int
	scheduleBackoffFailuresMu sync.Mutex

	// completionTimers holds the armed one-shot timers for onCompletion loop
	// conversations, keyed by session ID. Arming a new timer replaces (stops) any
	// existing one, so at most one firing is pending per session.
	completionTimers   map[string]*time.Timer
	completionTimersMu sync.Mutex

	// beadsClient lists beads issues for onTasks condition evaluation. Lazily
	// defaulted to beads.NewClient() on first use; tests inject a fake via
	// SetBeadsClient.
	beadsClient   beads.Client
	beadsClientMu sync.Mutex

	// tasksEvaluator compiles and evaluates onTasks CEL conditions. Built once at
	// construction; nil if the CEL environment failed to initialize, in which case
	// OnBeadsChanged is a no-op (fail-closed).
	tasksEvaluator *config.TasksConditionEvaluator

	// minTasksCooldownSeconds is the global floor (seconds) for the onTasks
	// trigger's cooldown between fires, preventing hot loops from rapid beads
	// churn. Mirrors minCompletionDelaySeconds for onCompletion.
	minTasksCooldownSeconds int

	// tasksQuiescenceWindow is how long the onTasks loop waits, after a
	// conversation (and its whole child subtree) goes idle, before rebasing the
	// per-conversation baseline (Layer 2 loop prevention).
	tasksQuiescenceWindow time.Duration

	// tasksRebaseTimers holds armed one-shot timers that rebase the onTasks
	// baseline once a busy conversation's subtree goes idle and the quiescence
	// window elapses, keyed by session ID.
	tasksRebaseTimers   map[string]*time.Timer
	tasksRebaseTimersMu sync.Mutex

	// tasksNoProgressCount and tasksLastTouchedIDs track, per session, the
	// consecutive-no-progress circuit breaker (Layer 3): tasksLastTouchedIDs
	// holds the set of issue IDs touched by the previous fire so the next fire
	// can detect whether it touched anything genuinely new.
	tasksNoProgressCount map[string]int
	tasksLastTouchedIDs  map[string]map[string]struct{}
	tasksNoProgressMu    sync.Mutex

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewLoopRunner creates a new loop runner.
func NewLoopRunner(store *session.Store, sm *conversation.SessionManager, logger *slog.Logger) *LoopRunner {
	evaluator, err := config.NewTasksConditionEvaluator()
	if err != nil {
		evaluator = nil
		if logger != nil {
			logger.Warn("Failed to initialize onTasks CEL evaluator; onTasks trigger will be inactive", "error", err)
		}
	}
	return &LoopRunner{
		store:                      store,
		sessionManager:             sm,
		logger:                     logger,
		pollInterval:               DefaultPollInterval,
		maxLoopIterations:          config.DefaultMaxLoopIterations,
		minCompletionDelaySeconds:  config.DefaultMinLoopCompletionDelaySeconds,
		consecutiveFailures:        make(map[string]int),
		promptResolveFailures:      make(map[string]int),
		scheduleBackoffFailures:    make(map[string]int),
		completionTimers:           make(map[string]*time.Timer),
		tasksEvaluator:             evaluator,
		minTasksCooldownSeconds:    DefaultMinLoopTasksCooldownSeconds,
		tasksQuiescenceWindow:      tasksDefaultQuiescenceWindow,
		tasksRebaseTimers:          make(map[string]*time.Timer),
		tasksNoProgressCount:       make(map[string]int),
		tasksLastTouchedIDs:        make(map[string]map[string]struct{}),
		autoUnarchiveEnabled:       true,
		autoUnarchiveRetryInterval: DefaultAutoUnarchiveRetryInterval,
		autoUnarchiveStagger:       DefaultAutoUnarchiveStaggerInterval,
	}
}

// SetPollInterval sets the polling interval. Must be called before Start().
func (r *LoopRunner) SetPollInterval(interval time.Duration) {
	r.pollInterval = interval
}

// SetStartupDelay sets the delay before the first poll on startup.
// This gives interactive sessions time to resume first via WebSocket connections.
// Must be called before Start().
func (r *LoopRunner) SetStartupDelay(d time.Duration) {
	r.startupDelay = d
}

// SetResumeStagger sets the stagger delay between consecutive session resumes within a poll.
// When non-zero, the runner waits this long between each resume to prevent thundering herd.
func (r *LoopRunner) SetResumeStagger(d time.Duration) {
	r.resumeStagger = d
}

// SetOnLoopStarted sets the callback for when a loop prompt is delivered.
func (r *LoopRunner) SetOnLoopStarted(callback LoopStartedCallback) {
	r.onLoopStarted = callback
}

// SetAutoArchiveAfter configures the runner to automatically archive sessions
// that have been inactive for longer than the given duration.
// A duration of 0 disables auto-archiving.
func (r *LoopRunner) SetAutoArchiveAfter(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoArchiveAfter = d
}

// SetOnAutoArchive sets the callback for when a session is auto-archived.
func (r *LoopRunner) SetOnAutoArchive(callback AutoArchiveCallback) {
	r.onAutoArchive = callback
}

// SetOnAutoUnarchive sets the callback invoked when the loop runner attempts
// to auto-unarchive a loop conversation archived due to broken ACP communication.
func (r *LoopRunner) SetOnAutoUnarchive(callback AutoUnarchiveFunc) {
	r.onAutoUnarchive = callback
}

// SetAutoUnarchiveRecovery configures the auto-unarchive recovery scheduler.
// If retryInterval or stagger is <= 0, the current (or default) value is kept,
// allowing tests to override only what they need.
func (r *LoopRunner) SetAutoUnarchiveRecovery(enabled bool, retryInterval, stagger time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoUnarchiveEnabled = enabled
	if retryInterval > 0 {
		r.autoUnarchiveRetryInterval = retryInterval
	}
	if stagger > 0 {
		r.autoUnarchiveStagger = stagger
	}
}

// SetOnLoopAutoStopped sets the callback for when a loop conversation is auto-stopped after reaching max iterations.
func (r *LoopRunner) SetOnLoopAutoStopped(callback LoopAutoStoppedCallback) {
	r.onLoopAutoStopped = callback
}

// SetOnLoopUpdated sets the callback for when a loop conversation's schedule advances after a delivery.
func (r *LoopRunner) SetOnLoopUpdated(callback LoopUpdatedCallback) {
	r.onLoopUpdated = callback
}

// SetArchiveRetentionPeriod sets the retention period for archived session cleanup.
// When set, archived sessions older than this period are permanently deleted during each poll.
// Pass an empty string to disable loop cleanup.
func (r *LoopRunner) SetArchiveRetentionPeriod(period string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archiveRetentionPeriod = period
}

// SetMaxLoopIterations sets the user-configured default cap on scheduled
// loop runs. 0 means unlimited (still bounded by GlobalMaxLoopIterations).
func (r *LoopRunner) SetMaxLoopIterations(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxLoopIterations = n
}

// SetMinLoopCompletionDelaySeconds sets the global floor for the on-completion
// loop trigger's delay. Values < 0 are clamped to 0.
func (r *LoopRunner) SetMinLoopCompletionDelaySeconds(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minCompletionDelaySeconds = n
}

// MinLoopCompletionDelaySeconds returns the current floor for the on-completion
// loop trigger's delay in seconds.
func (r *LoopRunner) MinLoopCompletionDelaySeconds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.minCompletionDelaySeconds
}

// SetPromptResolver sets the function used to resolve prompt names to their text at execution time.
func (r *LoopRunner) SetPromptResolver(resolver conversation.PromptResolver) {
	r.promptResolver = resolver
}

// Start begins the loop polling loop in a background goroutine.
// It returns immediately. Call Stop() to stop the runner.
func (r *LoopRunner) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return
	}

	r.running = true
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})

	go r.pollLoop()

	if r.logger != nil {
		r.logger.Debug("Loop runner started", "poll_interval", r.pollInterval)
	}
}

// Stop gracefully stops the loop runner and waits for it to finish.
func (r *LoopRunner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	close(r.stopCh)
	doneCh := r.doneCh
	r.mu.Unlock()

	// Cancel any pending on-completion timers so they don't fire after shutdown.
	r.completionTimersMu.Lock()
	for id, t := range r.completionTimers {
		t.Stop()
		delete(r.completionTimers, id)
	}
	r.completionTimersMu.Unlock()

	// Cancel any pending onTasks baseline-rebase timers so they don't fire after shutdown.
	r.tasksRebaseTimersMu.Lock()
	for id, t := range r.tasksRebaseTimers {
		t.Stop()
		delete(r.tasksRebaseTimers, id)
	}
	r.tasksRebaseTimersMu.Unlock()

	// Wait for the poll loop to finish
	<-doneCh

	if r.logger != nil {
		r.logger.Debug("Loop runner stopped")
	}
}

// IsRunning returns true if the runner is currently active.
func (r *LoopRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// TriggerNow immediately delivers the loop prompt for a session,
// bypassing the normal schedule check. This is used for manual "run now" requests.
// resetTimer controls whether RecordSent() is called after the prompt completes:
//   - true  → the countdown resets from now (same as a normal scheduled run)
//   - false → the existing next-run schedule is preserved unchanged
//
// Returns an error if the delivery fails or the session is not configured for loop prompts.
func (r *LoopRunner) TriggerNow(sessionID string, resetTimer bool) error {
	if r.store == nil {
		return ErrSessionStoreNotAvailable
	}

	// Get session metadata
	meta, err := r.store.GetMetadata(sessionID)
	if err != nil {
		return err
	}

	// Get loop config for this session
	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil {
		return err
	}

	// Check if enabled
	if !loop.Enabled {
		return ErrLoopNotEnabled
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		return ErrSessionManagerNotAvailable
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the loop prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for immediate loop delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}

		bs, err = r.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			return err
		}

		if r.logger != nil {
			r.logger.Info("Session auto-resumed for immediate loop delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		return ErrSessionBusy
	}

	if r.logger != nil {
		r.logger.Info("Triggering immediate loop delivery",
			"session_id", sessionID,
			"session_name", meta.Name,
			"prompt_preview", truncatePrompt(loop.Prompt, 100))
	}

	// Deliver the prompt
	return r.deliverPrompt(bs, meta.Name, loop, loopStore, resetTimer, true)
}

// OnConversationIdle is invoked when a session's agent has stopped and the session
// is fully idle (no queued work). For conversations configured with the onCompletion
// trigger it arms a one-shot timer that delivers the next run after the configured
// delay (clamped to the global minimum floor). For any other configuration it cancels
// a possibly-stale timer and returns.
func (r *LoopRunner) OnConversationIdle(sessionID string) {
	if r.store == nil {
		return
	}

	// Never arm a timer for an archived conversation — archiving stops the loop (mitto-efnb).
	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.Archived {
		r.cancelCompletionTimer(sessionID)
		return
	}

	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnCompletion() {
		// Not an active onCompletion loop — drop any timer left over from a prior config.
		r.cancelCompletionTimer(sessionID)
		return
	}

	r.mu.Lock()
	floor := r.minCompletionDelaySeconds
	r.mu.Unlock()

	delaySeconds := loop.DelaySeconds
	if delaySeconds < floor {
		delaySeconds = floor
	}
	delay := time.Duration(delaySeconds) * time.Second

	r.armCompletionTimer(sessionID, delay)

	if r.logger != nil {
		r.logger.Debug("Armed on-completion loop timer",
			"session_id", sessionID,
			"delay_seconds", delaySeconds)
	}
}

// armCompletionTimer schedules fireOnCompletion after delay, replacing (and stopping)
// any timer already pending for the session so only one firing is queued.
func (r *LoopRunner) armCompletionTimer(sessionID string, delay time.Duration) {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	if existing, ok := r.completionTimers[sessionID]; ok {
		existing.Stop()
	}
	r.completionTimers[sessionID] = time.AfterFunc(delay, func() {
		r.fireOnCompletion(sessionID)
	})
}

// StopLoopForArchive authoritatively stops a conversation's loop as part
// of archiving it (manual or automatic). It cancels any pending on-completion timer,
// disables the loop config with the given reason when currently enabled, and
// broadcasts the updated loop state so the UI no longer shows an enabled loop.
// It is a no-op for sessions without a loop config and is idempotent (an
// already-disabled config keeps its existing StoppedReason).
func (r *LoopRunner) StopLoopForArchive(sessionID string, reason session.StoppedReason) {
	if r.store == nil {
		return
	}
	// Cancel any pending on-completion timer regardless of config state.
	r.cancelCompletionTimer(sessionID)

	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil {
		// No loop config (ErrLoopNotFound) or unreadable — nothing to disable.
		return
	}
	if !loop.Enabled {
		// Already stopped/paused — leave the existing reason intact.
		return
	}
	if err := loopStore.MarkStopped(reason); err != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to stop loop config on archive",
				"session_id", sessionID, "error", err)
		}
		return
	}
	if r.onLoopAutoStopped != nil {
		if final, gErr := loopStore.Get(); gErr == nil {
			r.onLoopAutoStopped(sessionID, final)
		}
	}
	if r.logger != nil {
		r.logger.Info("Stopped loop on archive",
			"session_id", sessionID, "reason", reason)
	}
}

// cancelCompletionTimer stops and removes any pending on-completion timer for the session.
func (r *LoopRunner) cancelCompletionTimer(sessionID string) {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	if existing, ok := r.completionTimers[sessionID]; ok {
		existing.Stop()
		delete(r.completionTimers, sessionID)
	}
}

// BootstrapOnCompletion delivers the very first run of an onCompletion loop
// conversation that has never executed (IterationCount == 0 && LastSentAt == nil).
//
// Why this is needed — the bootstrap deadlock:
//   - For onCompletion, the next run is armed only when an agent turn completes and
//     the session goes idle (onTurnIdle → OnConversationIdle → armCompletionTimer →
//     fireOnCompletion → TriggerNow).
//   - The schedule-based poll loop deliberately skips onCompletion configs because
//     computeNextScheduledTime() returns nil when IsOnCompletion(), so NextScheduledAt
//     stays nil and checkSession returns early.
//   - For a brand-new conversation: no prompt has ever been delivered → no turn
//     completes → the idle transition never fires → the loop never bootstraps.
//
// This method breaks the deadlock by delivering the first run immediately (no
// delay_seconds wait — delay is a between-runs gap, not a pre-first-run delay).
// It is idempotent and crash-safe:
//   - The IterationCount==0 && LastSentAt==nil guard prevents re-delivery after restart.
//   - The completionTimers pending-check provides a cheap extra guard against double-fire
//     within the same process lifetime.
//   - TriggerNow's internal IsPrompting() check rejects a racing call with ErrSessionBusy
//     once PromptWithMeta sets isPrompting synchronously before returning.
//
// Called from checkSession (crash-safe on poll-loop restart), handleSetLoop,
// handlePatchLoop (HTTP), and handleConversationStart/handleConversationUpdate (MCP).
// Best-effort — errors are logged but not propagated.
func (r *LoopRunner) BootstrapOnCompletion(sessionID string) {
	if r.store == nil {
		return
	}

	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnCompletion() {
		return
	}

	// Only bootstrap the very first run.
	if loop.IterationCount != 0 || loop.LastSentAt != nil {
		return
	}

	// Extra guard: if a timer is already pending for this session, skip.
	r.completionTimersMu.Lock()
	_, pending := r.completionTimers[sessionID]
	r.completionTimersMu.Unlock()
	if pending {
		return
	}

	// Deliver the first run immediately — no delay on first run.
	if err := r.TriggerNow(sessionID, true); err != nil {
		if r.logger == nil {
			return
		}
		if errors.Is(err, ErrSessionBusy) {
			r.logger.Debug("On-completion bootstrap skipped, session busy",
				"session_id", sessionID)
		} else {
			r.logger.Warn("On-completion bootstrap failed",
				"session_id", sessionID,
				"error", err)
		}
	}
}

// recoverStalledOnCompletion is the poll-loop self-healing fallback for an
// onCompletion loop that missed its end-of-turn re-arm and would
// otherwise stall forever (see mitto-5dn).
//
// The next onCompletion run is normally armed only by an in-memory timer set
// when a turn completes on the clean idle path. If a turn completes in a
// non-idle state (notably around an ACP session resume or a heavy
// children-wait turn), the re-arm is skipped and nothing reschedules the loop.
// This poll-loop check mirrors how schedule-based triggers recover: it re-arms
// the completion timer when the loop has clearly stalled.
//
// It re-arms only when ALL of the following hold:
//   - the loop has run at least once (IterationCount > 0 || LastSentAt != nil);
//     a fresh loop is handled by BootstrapOnCompletion, not here;
//   - no completion timer is currently armed for the session; a healthy loop
//     always has one pending while waiting for the next run, so an absent timer
//     is the precise stall signal;
//   - the wall-clock maxDuration cap has not been reached; a capped loop should
//     auto-stop on its next fire, not be kept alive;
//   - the session is not currently prompting; an in-flight turn will re-arm
//     itself on completion, and if it misses (the bug) the next poll recovers it.
//
// When those hold it re-arms via OnConversationIdle, which re-reads the config
// and arms the timer with the floor-clamped delay. The downstream
// fireOnCompletion auto-resumes a non-running session and enforces caps, so this
// also self-heals after a process restart (in-memory timers do not survive one).
func (r *LoopRunner) recoverStalledOnCompletion(meta session.Metadata, loop *session.LoopPrompt) {
	if loop == nil {
		return
	}

	// Fresh loops are bootstrapped elsewhere; only recover loops that have run.
	if loop.IterationCount == 0 && loop.LastSentAt == nil {
		return
	}

	// A pending timer means the loop is healthy — nothing to recover.
	r.completionTimersMu.Lock()
	_, pending := r.completionTimers[meta.SessionID]
	r.completionTimersMu.Unlock()
	if pending {
		return
	}

	// If the wall-clock cap is reached, auto-stop consistently with the schedule path
	// (sets Enabled=false, StoppedReason=maxDuration, broadcasts). Without this the
	// onCompletion loop stays Enabled=true but dormant, inconsistent with schedule loops.
	if loop.ReachedMaxDuration(time.Now()) {
		if r.store != nil {
			loopStore := r.store.Loop(meta.SessionID)
			r.autoStopIfMaxDurationReached(meta.SessionID, loop, loopStore, time.Now())
		}
		return
	}

	// A turn in flight will re-arm itself on completion; if it misses (the bug),
	// the next poll catches it with the session idle. Avoid touching it now so we
	// neither interfere with a healthy turn nor race the fire→deliver window.
	if r.sessionManager != nil {
		if bs := r.sessionManager.GetSession(meta.SessionID); bs != nil && bs.IsPrompting() {
			return
		}
	}

	if r.logger != nil {
		r.logger.Info("Re-arming stalled on-completion loop (missed end-of-turn re-arm)",
			"session_id", meta.SessionID,
			"iteration_count", loop.IterationCount)
	}

	// Re-read config and arm the timer with the floor-clamped delay.
	r.OnConversationIdle(meta.SessionID)
}

// fireOnCompletion delivers the next onCompletion loop run. It re-validates the
// session and loop configuration (the conversation may have been archived, disabled,
// or reconfigured during the delay) and then delivers via TriggerNow. A busy session is
// skipped — the next idle transition re-arms the timer.
func (r *LoopRunner) fireOnCompletion(sessionID string) {
	// Drop our timer handle; it has fired.
	r.completionTimersMu.Lock()
	delete(r.completionTimers, sessionID)
	r.completionTimersMu.Unlock()

	if r.store == nil {
		return
	}

	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.Archived {
		return
	}

	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnCompletion() {
		return
	}

	// Auto-stop if the wall-clock maxDuration cap is reached before delivering.
	if r.autoStopIfMaxDurationReached(sessionID, loop, loopStore, time.Now()) {
		return
	}

	// Deliver via the standard immediate path with resetTimer=true so the iteration
	// counter advances and the max-iteration auto-stop applies. The delivered prompt's
	// completion produces another idle transition, which re-arms the next run.
	if err := r.TriggerNow(sessionID, true); err != nil {
		if r.logger == nil {
			return
		}
		if errors.Is(err, ErrSessionBusy) {
			r.logger.Debug("On-completion loop firing skipped, session busy",
				"session_id", sessionID)
		} else {
			r.logger.Warn("On-completion loop firing failed",
				"session_id", sessionID,
				"error", err)
		}
	}
}

// pollLoop is the main polling loop that checks for due prompts.
func (r *LoopRunner) pollLoop() {
	defer close(r.doneCh)

	// Wait before first poll to let interactive sessions resume first via WebSocket.
	// Loop sessions can afford to wait since their prompts are scheduled.
	if r.startupDelay > 0 {
		if r.logger != nil {
			r.logger.Info("Deferring loop poll to let interactive sessions resume first",
				"startup_delay", r.startupDelay)
		}
		select {
		case <-r.stopCh:
			return
		case <-time.After(r.startupDelay):
		}
	}

	// Run after delay to handle any prompts that were due
	r.RunOnce()

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.RunOnce()
		}
	}
}

// RunOnce performs a single poll iteration, checking all sessions for due prompts,
// auto-archiving inactive sessions, and cleaning up old archived sessions.
// Returns counts of delivered, skipped, and errored prompts.
// This method is exported for testing purposes.
func (r *LoopRunner) RunOnce() (delivered, skipped, errored int) {
	if r.store == nil {
		return 0, 0, 0
	}

	// List all sessions
	sessions, err := r.store.List()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to list sessions for loop check", "error", err)
		}
		return 0, 0, 1
	}

	now := time.Now().UTC()

	// Sort sessions so most-overdue loop prompts are processed first.
	// Non-loop sessions are kept in original order (sorted to the end).
	sort.SliceStable(sessions, func(i, j int) bool {
		pi := r.getNextScheduledAt(sessions[i])
		pj := r.getNextScheduledAt(sessions[j])
		if pi == nil && pj == nil {
			return false
		}
		if pi == nil {
			return false // non-loop sorts after loop
		}
		if pj == nil {
			return true // loop sorts before non-loop
		}
		return pi.Before(*pj) // most overdue (earliest NextScheduledAt) first
	})

	// Collect sessions that have due loop prompts and need resuming.
	// Process them with stagger delay to prevent thundering herd.
	var lastResumeTime time.Time

	for _, meta := range sessions {
		// Apply stagger delay between resume-triggering loop checks.
		// Only stagger when we actually resumed a session in a previous iteration.
		if r.resumeStagger > 0 && !lastResumeTime.IsZero() {
			elapsed := time.Since(lastResumeTime)
			if elapsed < r.resumeStagger {
				wait := r.resumeStagger - elapsed
				if r.logger != nil {
					r.logger.Debug("Staggering loop session resume",
						"session_id", meta.SessionID,
						"wait_ms", wait.Milliseconds())
				}
				time.Sleep(wait)
			}
		}

		willResume := r.sessionNeedsResume(meta, now)
		d, s, e := r.checkSession(meta, now)
		delivered += d
		skipped += s
		errored += e

		if willResume && d > 0 {
			lastResumeTime = time.Now()
		}
	}

	// Check scheduled queue messages across all active sessions
	r.checkScheduledQueues(sessions)

	// Auto-archive inactive sessions
	r.checkAutoArchive(sessions, now)

	// Retry auto-unarchiving loop conversations archived due to broken ACP
	r.checkAutoUnarchiveRecovery(sessions, now)

	// Clean up archived sessions past retention
	r.checkArchiveCleanup()

	if r.logger != nil {
		r.logger.Debug("Loop poll completed",
			"delivered", delivered,
			"skipped", skipped,
			"errored", errored)
	}

	return delivered, skipped, errored
}

// sessionNeedsResume returns true if checkSession would trigger a ResumeSession call.
// Used to apply stagger delays between consecutive resume attempts.
func (r *LoopRunner) sessionNeedsResume(meta session.Metadata, now time.Time) bool {
	if meta.Archived {
		return false
	}

	loopStore := r.store.Loop(meta.SessionID)
	loop, err := loopStore.Get()
	if err != nil || !loop.Enabled {
		return false
	}

	if loop.NextScheduledAt == nil || loop.NextScheduledAt.After(now) {
		return false
	}

	// Will need resume if not currently running
	bs := r.sessionManager.GetSession(meta.SessionID)
	return bs == nil
}

// getNextScheduledAt returns the NextScheduledAt for a session's loop config, or nil if not loop/not enabled.
func (r *LoopRunner) getNextScheduledAt(meta session.Metadata) *time.Time {
	if meta.Archived {
		return nil
	}
	loopStore := r.store.Loop(meta.SessionID)
	loop, err := loopStore.Get()
	if err != nil || !loop.Enabled {
		return nil
	}
	return loop.NextScheduledAt
}

// checkScheduledQueues checks all active sessions for scheduled queue messages
// that are now due for delivery, and triggers processing.
func (r *LoopRunner) checkScheduledQueues(sessions []session.Metadata) {
	if r.store == nil || r.sessionManager == nil {
		return
	}

	now := time.Now()

	for _, meta := range sessions {
		// Skip archived or non-active sessions
		if meta.Archived || (meta.Status != session.SessionStatusActive && meta.Status != "") {
			continue
		}

		// Check if this session has scheduled messages that are now due
		queue := r.store.Queue(meta.SessionID)
		nextTime, err := queue.NextScheduledTime()
		if err != nil || nextTime == nil {
			continue
		}

		// If the next scheduled time has arrived, try to process
		if !nextTime.After(now) {
			bs := r.sessionManager.GetSession(meta.SessionID)
			if bs != nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}
}

// checkSession checks a single session for due loop prompts.
// Returns (1, 0, 0) if delivered, (0, 1, 0) if skipped, (0, 0, 1) if error.
func (r *LoopRunner) checkSession(meta session.Metadata, now time.Time) (delivered, skipped, errored int) {
	sessionID := meta.SessionID

	// Skip archived sessions - loop prompts are inactive for archived sessions
	if meta.Archived {
		return 0, 0, 0
	}

	// Get loop config for this session
	loopStore := r.store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil {
		if err == session.ErrLoopNotFound {
			// No loop config - this is normal, not an error
			return 0, 0, 0
		}
		if r.logger != nil {
			r.logger.Error("Failed to read loop config",
				"session_id", sessionID,
				"error", err)
		}
		return 0, 0, 1
	}

	// Skip if disabled
	if !loop.Enabled {
		return 0, 0, 0
	}

	// onCompletion configs never have a NextScheduledAt — the schedule loop cannot
	// deliver them. Bootstrap the very first run here so that a crash or restart
	// before any delivery still kicks off the loop. No-op if already run or in-flight.
	if loop.IsOnCompletion() {
		r.BootstrapOnCompletion(sessionID)
		// Self-healing safety net for an already-running loop whose end-of-turn
		// re-arm was missed (e.g. around an ACP resume or a heavy children-wait
		// turn that did not register as a clean idle transition). See mitto-5dn.
		r.recoverStalledOnCompletion(meta, loop)
		return 0, 0, 0
	}

	// onTasks configs are event-driven (fired from OnBeadsChanged) and never have
	// a NextScheduledAt either. Bootstrap the baseline here so a crash/restart
	// before the baseline was ever captured does not cause a spurious first fire
	// the next time beads change (mitto-oja.2).
	if loop.IsOnTasks() {
		r.BootstrapTasksBaseline(sessionID)
		return 0, 0, 0
	}

	// Check if due
	if loop.NextScheduledAt == nil || loop.NextScheduledAt.After(now) {
		return 0, 0, 0
	}

	// Auto-stop if the wall-clock maxDuration cap is reached before delivering.
	if r.autoStopIfMaxDurationReached(sessionID, loop, loopStore, now) {
		return 0, 0, 0
	}

	// Prompt is due - calculate how overdue it is
	scheduledAt := *loop.NextScheduledAt
	overdueBy := now.Sub(scheduledAt)

	// Calculate how many runs were missed (for logging purposes)
	missedRuns := 0
	if overdueBy > 0 && loop.Frequency.Duration() > 0 {
		// Number of full intervals that passed since scheduled time
		missedRuns = int(overdueBy / loop.Frequency.Duration())
	}

	// Log the catch-up situation
	if r.logger != nil {
		if missedRuns > 0 {
			r.logger.Debug("Loop prompt overdue - running catch-up (skipping missed runs)",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"overdue_by", overdueBy.Round(time.Second),
				"missed_runs", missedRuns,
				"prompt_preview", truncatePrompt(loop.Prompt, 50))
		} else {
			r.logger.Debug("Loop prompt is due",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"prompt_preview", truncatePrompt(loop.Prompt, 50))
		}
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		if r.logger != nil {
			r.logger.Debug("Skipping loop prompt - no session manager",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the loop prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for loop prompt",
				"session_id", sessionID,
				"session_name", meta.Name)
		}

		var err error
		bs, err = r.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			r.consecutiveFailuresMu.Lock()
			r.consecutiveFailures[sessionID]++
			failures := r.consecutiveFailures[sessionID]
			r.consecutiveFailuresMu.Unlock()

			if r.logger != nil {
				r.logger.Error("Failed to resume session for loop prompt",
					"session_id", sessionID,
					"consecutive_failures", failures,
					"max_failures", MaxLoopResumeFailures,
					"error", err)
			}

			// After too many consecutive failures, archive the session
			// to stop the retry storm. The user can unarchive it manually.
			if failures >= MaxLoopResumeFailures {
				if r.logger != nil {
					r.logger.Warn("Archiving session after repeated ACP resume failures",
						"session_id", sessionID,
						"session_name", meta.Name,
						"consecutive_failures", failures)
				}

				// Note: the session is NOT running (resume failed), so no need to close it gracefully.

				// Persist the stopped reason before archiving so it survives even though
				// the session leaves the active view. Failures are non-fatal — archiving proceeds.
				loopStore := r.store.Loop(sessionID)
				if markErr := loopStore.MarkStopped(session.StoppedReasonResumeFailures); markErr != nil {
					if r.logger != nil {
						r.logger.Warn("Failed to mark loop stopped reason before archive",
							"session_id", sessionID,
							"error", markErr)
					}
				}
				// Cancel any pending on-completion timer so it cannot fire after archiving (mitto-efnb).
				r.cancelCompletionTimer(sessionID)

				// Update metadata to mark as archived
				if updateErr := r.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
					m.Archived = true
					m.ArchivedAt = time.Now()
					m.ArchiveReason = session.ArchiveReasonACPFailures
				}); updateErr != nil {
					if r.logger != nil {
						r.logger.Error("Failed to archive session after ACP failures",
							"session_id", sessionID,
							"error", updateErr)
					}
				} else {
					// Notify via callback (broadcasts to WebSocket clients)
					if r.onAutoArchive != nil {
						r.onAutoArchive(sessionID)
					}
					// Delete child sessions (async, same as manual archive)
					go r.sessionManager.DeleteChildSessions(sessionID)

					// Broadcast the loop disable so the UI badge reflects reality (mitto-efnb).
					if r.onLoopAutoStopped != nil {
						if final, gErr := loopStore.Get(); gErr == nil {
							r.onLoopAutoStopped(sessionID, final)
						}
					}

					if r.logger != nil {
						r.logger.Info("Session archived after repeated ACP resume failures",
							"session_id", sessionID,
							"session_name", meta.Name)
					}
				}

				// Reset counter after archiving
				r.consecutiveFailuresMu.Lock()
				delete(r.consecutiveFailures, sessionID)
				r.consecutiveFailuresMu.Unlock()
			}
			return 0, 0, 1
		}

		// Reset consecutive failure counter on successful resume
		r.consecutiveFailuresMu.Lock()
		delete(r.consecutiveFailures, sessionID)
		r.consecutiveFailuresMu.Unlock()

		if r.logger != nil {
			r.logger.Info("Session auto-resumed for loop prompt",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		if r.logger != nil {
			r.logger.Debug("Skipping loop prompt - session is busy",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Deliver the prompt — normal scheduled runs always reset the timer.
	if err := r.deliverPrompt(bs, meta.Name, loop, loopStore, true, false); err != nil {
		if errors.Is(err, ErrPromptResolveFailed) {
			r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
		} else {
			if r.logger != nil {
				r.logger.Error("Failed to deliver loop prompt",
					"session_id", sessionID,
					"error", err)
			}
		}
		return 0, 0, 1
	}

	// Reset resolve-failure counter on successful delivery.
	r.promptResolveFailuresMu.Lock()
	delete(r.promptResolveFailures, sessionID)
	r.promptResolveFailuresMu.Unlock()

	return 1, 0, 0
}

// autoStopIfMaxDurationReached checks whether the loop conversation has exceeded
// its wall-clock maxDuration cap (elapsed time since FirstRunAt). When the cap is
// reached it disables the loop config (without archiving) and broadcasts the
// auto-stop via onLoopAutoStopped, mirroring the max-iterations auto-stop. It
// returns true to signal the caller to skip delivery. Returns false when the cap is
// unlimited, not yet anchored (FirstRunAt nil), or not reached — delivery may proceed.
func (r *LoopRunner) autoStopIfMaxDurationReached(sessionID string, loop *session.LoopPrompt, loopStore *session.LoopStore, now time.Time) bool {
	if loop == nil || !loop.ReachedMaxDuration(now) {
		return false
	}

	if r.logger != nil {
		var elapsed time.Duration
		if loop.FirstRunAt != nil {
			elapsed = now.Sub(*loop.FirstRunAt).Round(time.Second)
		}
		r.logger.Info("Loop conversation reached max duration, auto-stopping",
			"session_id", sessionID,
			"max_duration_seconds", loop.MaxDurationSeconds,
			"elapsed", elapsed)
	}

	if err := loopStore.MarkStopped(session.StoppedReasonMaxDuration); err != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to disable loop after reaching max duration",
				"session_id", sessionID,
				"error", err)
		}
		return true
	}
	if r.onLoopAutoStopped != nil {
		// Re-read so the broadcast reflects Enabled=false / NextScheduledAt=nil.
		if final, err := loopStore.Get(); err == nil {
			r.onLoopAutoStopped(sessionID, final)
		}
	}
	return true
}

// handlePromptResolveFailure handles a loop prompt whose name no longer resolves.
// It logs the first failure at WARN and suppresses subsequent identical failures (to
// avoid one ERROR per tick), and after MaxPromptResolveFailures consecutive failures it
// auto-pauses (disables) the loop config and broadcasts the change, mirroring the
// MaxLoopResumeFailures auto-archive safety.
func (r *LoopRunner) handlePromptResolveFailure(sessionID, sessionName string, loop *session.LoopPrompt, loopStore *session.LoopStore, err error) {
	r.promptResolveFailuresMu.Lock()
	r.promptResolveFailures[sessionID]++
	failures := r.promptResolveFailures[sessionID]
	r.promptResolveFailuresMu.Unlock()

	if r.logger != nil {
		if failures == 1 {
			r.logger.Warn("Loop prompt could not be resolved; will auto-pause after repeated failures",
				"session_id", sessionID,
				"prompt_name", loop.PromptName,
				"consecutive_failures", failures,
				"max_failures", MaxPromptResolveFailures,
				"error", err)
		} else {
			r.logger.Debug("Loop prompt still unresolved",
				"session_id", sessionID,
				"prompt_name", loop.PromptName,
				"consecutive_failures", failures)
		}
	}

	if failures < MaxPromptResolveFailures {
		return
	}

	if updErr := loopStore.MarkStopped(session.StoppedReasonPromptUnresolved); updErr != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to disable loop after repeated resolve failures",
				"session_id", sessionID, "error", updErr)
		}
		return
	}
	if r.logger != nil {
		r.logger.Warn("Auto-paused loop conversation after repeated prompt resolve failures",
			"session_id", sessionID,
			"session_name", sessionName,
			"prompt_name", loop.PromptName,
			"consecutive_failures", failures)
	}
	if r.onLoopAutoStopped != nil {
		if final, gErr := loopStore.Get(); gErr == nil {
			r.onLoopAutoStopped(sessionID, final)
		}
	}
	r.promptResolveFailuresMu.Lock()
	delete(r.promptResolveFailures, sessionID)
	r.promptResolveFailuresMu.Unlock()
}

// deliverPrompt sends the loop prompt to the session.
// resetTimer controls whether RecordSent() is called when the prompt completes:
//   - true  → schedule advances from now (normal behaviour)
//   - false → schedule is left untouched (manual "run now" without resetting the timer)
func (r *LoopRunner) deliverPrompt(bs *conversation.BackgroundSession, sessionName string, loop *session.LoopPrompt, loopStore *session.LoopStore, resetTimer bool, forced bool) error {
	sessionID := bs.GetSessionID()

	// Resolve prompt text from name if needed
	promptText := loop.Prompt
	if loop.PromptName != "" && r.promptResolver != nil {
		sessionMeta, err := r.store.GetMetadata(sessionID)
		if err != nil {
			return fmt.Errorf("failed to get session metadata for prompt resolution: %w", err)
		}
		resolved, err := r.promptResolver(loop.PromptName, sessionMeta.WorkingDir)
		if err != nil {
			return fmt.Errorf("%w: %q: %v", ErrPromptResolveFailed, loop.PromptName, err)
		}
		promptText = resolved
		if r.logger != nil {
			r.logger.Debug("Resolved loop prompt name to text",
				"session_id", sessionID,
				"prompt_name", loop.PromptName,
				"prompt_preview", truncatePrompt(promptText, 100))
		}
	}

	if r.logger != nil {
		r.logger.Debug("Delivering loop prompt",
			"session_id", sessionID,
			"session_name", sessionName,
			"reset_timer", resetTimer,
			"prompt_preview", truncatePrompt(promptText, 100))
	}

	// Use OnComplete callback to defer RecordSent until the prompt actually finishes.
	// PromptWithMeta is async — it returns nil immediately. Without OnComplete,
	// RecordSent would advance the schedule even if the prompt later fails
	// (e.g., ACP process crash).
	loopKind := conversation.LoopKindScheduled
	if forced {
		loopKind = conversation.LoopKindForced
	}
	meta := conversation.PromptMeta{
		SenderID:        "loop-runner",
		PromptID:        "",              // No client to confirm delivery to
		PromptName:      loop.PromptName, // Pass prompt name so UI can render a badge instead of full text
		Arguments:       loop.Arguments,  // User-supplied values for Go-template .Args placeholders in the resolved text
		IsLoopForced:    forced,
		LoopKind:        loopKind,
		IterationNumber: loop.IterationCount,
		MaxIterations:   loop.MaxIterations,
		FreshContext:    loop.FreshContext,
		OnComplete: func(err error) {
			if err != nil {
				// Scheduled triggers: back off NextScheduledAt so a transient transport
				// failure (e.g. -32603) does not re-fire the same prompt on every poll
				// tick (mitto-qal.2). onCompletion triggers are event-driven (their
				// NextScheduledAt is nil) and manual "keep schedule" runs (resetTimer=false)
				// or forced one-shots must not push out the regular schedule.
				if resetTimer && !forced && !loop.IsOnCompletion() {
					r.scheduleBackoffFailuresMu.Lock()
					r.scheduleBackoffFailures[sessionID]++
					failures := r.scheduleBackoffFailures[sessionID]
					r.scheduleBackoffFailuresMu.Unlock()

					delay := loopScheduleBackoff(failures)
					if deferErr := loopStore.DeferNextSchedule(delay); deferErr != nil {
						if r.logger != nil {
							r.logger.Warn("Loop prompt failed, backoff could not be applied",
								"session_id", sessionID,
								"session_name", sessionName,
								"consecutive_failures", failures,
								"error", deferErr)
						}
					} else {
						if r.logger != nil {
							r.logger.Warn("Loop prompt failed, backing off next run",
								"session_id", sessionID,
								"session_name", sessionName,
								"consecutive_failures", failures,
								"backoff", delay,
								"error", err)
						}
						// Broadcast the new next-run time so the countdown reflects the backoff.
						if r.onLoopUpdated != nil {
							if updated, gErr := loopStore.Get(); gErr == nil && updated != nil {
								r.onLoopUpdated(sessionID, updated)
							}
						}
					}
					return
				}

				if r.logger != nil {
					r.logger.Warn("Loop prompt failed, schedule not advanced",
						"session_id", sessionID,
						"session_name", sessionName,
						"error", err)
				}
				return
			}

			// Successful delivery — clear any accumulated scheduled-delivery backoff.
			r.scheduleBackoffFailuresMu.Lock()
			delete(r.scheduleBackoffFailures, sessionID)
			r.scheduleBackoffFailuresMu.Unlock()

			if !resetTimer {
				// Manual run with "keep schedule" — leave NextScheduledAt unchanged.
				if r.logger != nil {
					r.logger.Debug("Loop prompt completed, timer not reset (manual run)",
						"session_id", sessionID,
						"session_name", sessionName)
				}
				return
			}

			// Prompt completed successfully — now update the schedule
			if err := loopStore.RecordSent(); err != nil {
				if r.logger != nil {
					r.logger.Warn("Failed to update loop last_sent_at",
						"session_id", sessionID,
						"error", err)
				}
			} else {
				updated, getErr := loopStore.Get()
				if getErr == nil && updated != nil {
					r.mu.Lock()
					cfgCap := r.maxLoopIterations
					r.mu.Unlock()
					effective := config.EffectiveMaxLoopIterations(updated.MaxIterations, cfgCap)
					perPromptReached := updated.ReachedMaxIterations()
					if updated.IterationCount >= effective {
						// Cap reached — disable the loop prompt so it stops firing.
						if r.logger != nil {
							if perPromptReached {
								r.logger.Info("Loop conversation reached max iterations, auto-stopping",
									"session_id", sessionID,
									"max_iterations", updated.MaxIterations,
									"iteration_count", updated.IterationCount)
							} else {
								// Stopped by the global/config backstop rather than the per-prompt cap.
								r.logger.Warn("Loop conversation reached global iteration safeguard, auto-stopping",
									"session_id", sessionID,
									"iteration_count", updated.IterationCount,
									"effective_cap", effective,
									"config_cap", cfgCap,
									"backstop", config.GlobalMaxLoopIterations)
							}
						}
						// Distinguish per-prompt cap from global/config backstop.
						stoppedReason := session.StoppedReasonIterationSafeguard
						if perPromptReached {
							stoppedReason = session.StoppedReasonMaxIterations
						}
						if disableErr := loopStore.MarkStopped(stoppedReason); disableErr != nil {
							if r.logger != nil {
								r.logger.Warn("Failed to disable loop after reaching iteration cap",
									"session_id", sessionID,
									"error", disableErr)
							}
						} else if r.onLoopAutoStopped != nil {
							// Re-read so the broadcast reflects Enabled=false / NextScheduledAt=nil.
							if final, err := loopStore.Get(); err == nil {
								r.onLoopAutoStopped(sessionID, final)
							}
						}
					} else {
						// Schedule advanced normally — notify clients so the countdown resets
						// to the freshly computed next-run time.
						if r.onLoopUpdated != nil {
							r.onLoopUpdated(sessionID, updated)
						}
						if r.logger != nil && updated.NextScheduledAt != nil {
							r.logger.Debug("Loop schedule updated after delivery",
								"session_id", sessionID,
								"next_scheduled_at", updated.NextScheduledAt)
						}
					}
				}
			}
		},
	}

	if err := bs.PromptWithMeta(promptText, meta); err != nil {
		return err
	}

	// Notify about the loop prompt delivery (the prompt is now queued/started).
	// Skip notification for forced (manual "Run Now") triggers — the user already
	// knows they triggered it, so showing a notification is redundant.
	if r.onLoopStarted != nil && !forced {
		r.onLoopStarted(sessionID, sessionName)
	}

	return nil
}

// truncatePrompt truncates a string to maxLen characters, adding "..." if truncated.
func truncatePrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// autoArchiveWaitTimeout is the maximum time to wait for a response to complete
// before forcibly closing a session during auto-archiving.
const autoArchiveWaitTimeout = 30 * time.Second

// checkAutoArchive archives sessions that have been inactive for longer than autoArchiveAfter.
// It skips sessions that are already archived, child sessions (children are archived via parent cascade),
// or sessions with loop prompts — enabled or paused (they should remain active indefinitely).
func (r *LoopRunner) checkAutoArchive(sessions []session.Metadata, now time.Time) {
	r.mu.Lock()
	threshold := r.autoArchiveAfter
	r.mu.Unlock()

	if threshold <= 0 {
		return
	}

	if r.sessionManager == nil {
		return
	}

	for _, meta := range sessions {
		// Skip already archived sessions
		if meta.Archived {
			continue
		}

		// Skip child sessions — they are archived via parent cascade only
		if meta.ParentSessionID != "" {
			continue
		}

		// Skip sessions with loop prompts (enabled or paused) — they should remain active indefinitely.
		// A paused loop conversation is still a loop conversation and should not be auto-archived;
		// the user may re-enable it at any time.
		loopStore := r.store.Loop(meta.SessionID)
		_, err := loopStore.Get()
		if err != nil && err != session.ErrLoopNotFound {
			if r.logger != nil {
				r.logger.Error("Failed to read loop config during auto-archive check",
					"session_id", meta.SessionID,
					"error", err)
			}
			// Continue processing other sessions even if we can't read this one's config
			continue
		}
		if err == nil {
			if r.logger != nil {
				r.logger.Debug("Skipping auto-archive for loop session",
					"session_id", meta.SessionID,
					"session_name", meta.Name)
			}
			continue
		}

		// Check inactivity: use LastUserMessageAt if available, fall back to UpdatedAt
		lastActivity := meta.UpdatedAt
		if !meta.LastUserMessageAt.IsZero() && meta.LastUserMessageAt.After(lastActivity) {
			lastActivity = meta.LastUserMessageAt
		}

		if now.Sub(lastActivity) < threshold {
			continue
		}

		// Session is inactive beyond threshold — auto-archive it
		sessionID := meta.SessionID
		if r.logger != nil {
			r.logger.Info("Auto-archiving inactive session",
				"session_id", sessionID,
				"session_name", meta.Name,
				"last_activity", lastActivity,
				"inactive_for", now.Sub(lastActivity).Round(time.Minute))
		}

		// 1. Gracefully close ACP process (wait for any in-progress response)
		reason := "auto_archived"
		if !r.sessionManager.CloseSessionGracefully(sessionID, reason, autoArchiveWaitTimeout) {
			if r.logger != nil {
				r.logger.Warn("Timeout waiting for response before auto-archiving, forcing close",
					"session_id", sessionID)
			}
			reason = "auto_archived_timeout"
			r.sessionManager.CloseSession(sessionID, reason)
		}

		// 2. Update metadata to mark as archived
		err = r.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
			m.Archived = true
			m.ArchivedAt = now
			m.ArchiveReason = session.ArchiveReasonInactivity
		})
		if err != nil {
			if r.logger != nil {
				r.logger.Error("Failed to mark session as archived",
					"session_id", sessionID,
					"error", err)
			}
			continue
		}

		// 3. Notify via callback (broadcasts to WebSocket clients)
		if r.onAutoArchive != nil {
			r.onAutoArchive(sessionID)
		}

		// 4. Delete child sessions (async, same as manual archive)
		go r.sessionManager.DeleteChildSessions(sessionID)

		if r.logger != nil {
			r.logger.Info("Session auto-archived successfully",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}
}

// autoUnarchiveEligible reports whether meta qualifies for auto-unarchive recovery
// and, if so, returns the anchor timestamp its retry cadence is computed from.
// A session is eligible iff it is archived with ArchiveReasonACPFailures and has a
// loop configured (loop.json present). The anchor is AutoUnarchiveLastAttemptAt if
// non-zero, else ArchivedAt.
func (r *LoopRunner) autoUnarchiveEligible(meta session.Metadata) (time.Time, bool) {
	if !meta.Archived || meta.ArchiveReason != session.ArchiveReasonACPFailures {
		return time.Time{}, false
	}

	_, err := r.store.Loop(meta.SessionID).Get()
	if err != nil {
		if err != session.ErrLoopNotFound && r.logger != nil {
			r.logger.Error("Failed to read loop config during auto-unarchive check",
				"session_id", meta.SessionID, "error", err)
		}
		return time.Time{}, false
	}

	anchor := meta.ArchivedAt
	if !meta.AutoUnarchiveLastAttemptAt.IsZero() {
		anchor = meta.AutoUnarchiveLastAttemptAt
	}
	return anchor, true
}

// checkAutoUnarchiveRecovery retries auto-unarchiving loop conversations archived
// due to broken ACP communication (session.ArchiveReasonACPFailures), on a slow,
// staggered, restart-durable schedule. At most one session is attempted per poll.
func (r *LoopRunner) checkAutoUnarchiveRecovery(sessions []session.Metadata, now time.Time) {
	r.mu.Lock()
	enabled := r.autoUnarchiveEnabled
	retryInterval := r.autoUnarchiveRetryInterval
	stagger := r.autoUnarchiveStagger
	lastAttempt := r.lastAutoUnarchiveAttempt
	r.mu.Unlock()

	if !enabled || r.onAutoUnarchive == nil {
		return
	}

	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < stagger {
		return
	}

	var mostOverdue *session.Metadata
	var mostOverdueAnchor time.Time
	for i := range sessions {
		anchor, ok := r.autoUnarchiveEligible(sessions[i])
		if !ok || now.Sub(anchor) < retryInterval {
			continue
		}
		if mostOverdue == nil || anchor.Before(mostOverdueAnchor) {
			mostOverdue = &sessions[i]
			mostOverdueAnchor = anchor
		}
	}

	if mostOverdue != nil {
		r.attemptAutoUnarchive(*mostOverdue, now)
	}
}

// attemptAutoUnarchive attempts to auto-unarchive a single loop conversation
// archived due to broken ACP communication. It persists the attempt timestamp
// before calling the callback (so the cadence survives a crash mid-attempt),
// and clears it only on success (resetting the cadence for the next failure).
func (r *LoopRunner) attemptAutoUnarchive(meta session.Metadata, now time.Time) {
	sessionID := meta.SessionID

	if err := r.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.AutoUnarchiveLastAttemptAt = now
	}); err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to persist auto-unarchive attempt timestamp",
				"session_id", sessionID, "error", err)
		}
		return
	}

	r.mu.Lock()
	r.lastAutoUnarchiveAttempt = now
	r.mu.Unlock()

	if r.logger != nil {
		r.logger.Info("Attempting auto-unarchive of loop conversation archived due to broken ACP",
			"session_id", sessionID, "session_name", meta.Name)
	}

	err := r.onAutoUnarchive(sessionID)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("Auto-unarchive attempt failed, will retry later",
				"session_id", sessionID, "error", err)
		}
		return
	}

	if clearErr := r.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.AutoUnarchiveLastAttemptAt = time.Time{}
	}); clearErr != nil {
		if r.logger != nil {
			r.logger.Error("Failed to clear auto-unarchive attempt timestamp after success",
				"session_id", sessionID, "error", clearErr)
		}
	}

	if r.logger != nil {
		r.logger.Info("Auto-unarchived loop conversation successfully", "session_id", sessionID)
	}
}

// checkArchiveCleanup permanently deletes archived sessions older than the retention period.
func (r *LoopRunner) checkArchiveCleanup() {
	r.mu.Lock()
	retentionPeriod := r.archiveRetentionPeriod
	r.mu.Unlock()

	if retentionPeriod == "" {
		return
	}

	deleted, err := r.store.CleanupArchivedSessions(retentionPeriod)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to clean up archived sessions",
				"retention_period", retentionPeriod,
				"error", err)
		}
		return
	}

	if deleted > 0 && r.logger != nil {
		r.logger.Info("Loop archive cleanup completed",
			"deleted_count", deleted,
			"retention_period", retentionPeriod)
	}
}
