package web

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPollInterval is the default interval between periodic prompt checks.
	DefaultPollInterval = 1 * time.Minute

	// MaxPeriodicResumeFailures is the number of consecutive ACP resume failures
	// after which a periodic session is automatically archived.
	MaxPeriodicResumeFailures = 3

	// MaxPromptResolveFailures is the number of consecutive prompt-name resolution
	// failures after which the periodic config is auto-paused (disabled).
	MaxPromptResolveFailures = 3
)

// Errors for periodic runner operations.
var (
	ErrSessionStoreNotAvailable   = errors.New("session store not available")
	ErrSessionManagerNotAvailable = errors.New("session manager not available")
	ErrPeriodicNotEnabled         = errors.New("periodic is not enabled for this session")
	ErrSessionBusy                = errors.New("session is currently processing a prompt")
	ErrPromptResolveFailed        = errors.New("periodic prompt could not be resolved")
)

// PeriodicStartedCallback is called when a periodic prompt is delivered.
// sessionID is the session that received the prompt.
// sessionName is the display name of the session.
type PeriodicStartedCallback func(sessionID, sessionName string)

// AutoArchiveCallback is called when the periodic runner auto-archives a session.
// It should handle broadcasting the archive state change and stopping ACP.
type AutoArchiveCallback func(sessionID string)

// PeriodicAutoStoppedCallback is called when a periodic conversation is auto-stopped after reaching max iterations.
// It should broadcast the updated periodic state to all WebSocket clients.
type PeriodicAutoStoppedCallback func(sessionID string, periodic *session.PeriodicPrompt)

// PeriodicUpdatedCallback is called when a periodic conversation's schedule advances after a delivery.
// It should broadcast the updated periodic state (including the new next_scheduled_at) to all
// WebSocket clients so the countdown resets.
type PeriodicUpdatedCallback func(sessionID string, periodic *session.PeriodicPrompt)

// PeriodicRunner manages scheduled periodic prompt delivery and session housekeeping.
// It polls all sessions at regular intervals and:
// - Delivers periodic prompts that are due
// - Auto-archives sessions inactive beyond the configured threshold
// - Cleans up archived sessions past their retention period
type PeriodicRunner struct {
	store          *session.Store
	sessionManager *conversation.SessionManager
	logger         *slog.Logger

	pollInterval time.Duration

	// startupDelay is how long to wait before the first poll on startup.
	// This gives interactive sessions time to resume first via WebSocket connections.
	startupDelay time.Duration

	// resumeStagger is the delay between consecutive session resumes within a single poll.
	// This prevents thundering herd when many periodic sessions are due simultaneously.
	resumeStagger time.Duration

	// onPeriodicStarted is called when a periodic prompt is delivered
	onPeriodicStarted PeriodicStartedCallback

	// onAutoArchive is called when a session is auto-archived.
	// The callback should broadcast the archive state change and ACP stop to WebSocket clients.
	onAutoArchive AutoArchiveCallback

	// onPeriodicAutoStopped is called when a periodic conversation is disabled after reaching max iterations.
	onPeriodicAutoStopped PeriodicAutoStoppedCallback

	// onPeriodicUpdated is called when a periodic conversation's schedule advances after a delivery,
	// so clients can reset the countdown to the new next-run time.
	onPeriodicUpdated PeriodicUpdatedCallback

	// autoArchiveAfter, when > 0, causes sessions inactive for this long to be archived.
	autoArchiveAfter time.Duration

	// archiveRetentionPeriod, when non-empty, causes archived sessions older than this
	// to be permanently deleted during each poll cycle (not just at startup).
	archiveRetentionPeriod string

	// promptResolver resolves a prompt name to its text at execution time.
	promptResolver conversation.PromptResolver

	// maxPeriodicIterations is the user-configured default cap on scheduled
	// periodic runs. 0 means unlimited; the hardcoded backstop still applies.
	maxPeriodicIterations int

	// minCompletionDelaySeconds is the global floor applied to the on-completion
	// periodic trigger's delay, preventing hot loops.
	minCompletionDelaySeconds int

	// consecutiveFailures tracks how many times in a row a session's periodic
	// prompt delivery failed due to ACP resume errors. After MaxPeriodicResumeFailures
	// consecutive failures, the session is automatically archived.
	consecutiveFailures   map[string]int
	consecutiveFailuresMu sync.Mutex

	// promptResolveFailures tracks consecutive failures to resolve a periodic prompt
	// name. After MaxPromptResolveFailures consecutive failures the periodic config is
	// auto-paused (disabled) to stop the retry storm.
	promptResolveFailures   map[string]int
	promptResolveFailuresMu sync.Mutex

	// completionTimers holds the armed one-shot timers for onCompletion periodic
	// conversations, keyed by session ID. Arming a new timer replaces (stops) any
	// existing one, so at most one firing is pending per session.
	completionTimers   map[string]*time.Timer
	completionTimersMu sync.Mutex

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewPeriodicRunner creates a new periodic runner.
func NewPeriodicRunner(store *session.Store, sm *conversation.SessionManager, logger *slog.Logger) *PeriodicRunner {
	return &PeriodicRunner{
		store:                     store,
		sessionManager:            sm,
		logger:                    logger,
		pollInterval:              DefaultPollInterval,
		maxPeriodicIterations:     config.DefaultMaxPeriodicIterations,
		minCompletionDelaySeconds: config.DefaultMinPeriodicCompletionDelaySeconds,
		consecutiveFailures:       make(map[string]int),
		promptResolveFailures:     make(map[string]int),
		completionTimers:          make(map[string]*time.Timer),
	}
}

// SetPollInterval sets the polling interval. Must be called before Start().
func (r *PeriodicRunner) SetPollInterval(interval time.Duration) {
	r.pollInterval = interval
}

// SetStartupDelay sets the delay before the first poll on startup.
// This gives interactive sessions time to resume first via WebSocket connections.
// Must be called before Start().
func (r *PeriodicRunner) SetStartupDelay(d time.Duration) {
	r.startupDelay = d
}

// SetResumeStagger sets the stagger delay between consecutive session resumes within a poll.
// When non-zero, the runner waits this long between each resume to prevent thundering herd.
func (r *PeriodicRunner) SetResumeStagger(d time.Duration) {
	r.resumeStagger = d
}

// SetOnPeriodicStarted sets the callback for when a periodic prompt is delivered.
func (r *PeriodicRunner) SetOnPeriodicStarted(callback PeriodicStartedCallback) {
	r.onPeriodicStarted = callback
}

// SetAutoArchiveAfter configures the runner to automatically archive sessions
// that have been inactive for longer than the given duration.
// A duration of 0 disables auto-archiving.
func (r *PeriodicRunner) SetAutoArchiveAfter(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoArchiveAfter = d
}

// SetOnAutoArchive sets the callback for when a session is auto-archived.
func (r *PeriodicRunner) SetOnAutoArchive(callback AutoArchiveCallback) {
	r.onAutoArchive = callback
}

// SetOnPeriodicAutoStopped sets the callback for when a periodic conversation is auto-stopped after reaching max iterations.
func (r *PeriodicRunner) SetOnPeriodicAutoStopped(callback PeriodicAutoStoppedCallback) {
	r.onPeriodicAutoStopped = callback
}

// SetOnPeriodicUpdated sets the callback for when a periodic conversation's schedule advances after a delivery.
func (r *PeriodicRunner) SetOnPeriodicUpdated(callback PeriodicUpdatedCallback) {
	r.onPeriodicUpdated = callback
}

// SetArchiveRetentionPeriod sets the retention period for archived session cleanup.
// When set, archived sessions older than this period are permanently deleted during each poll.
// Pass an empty string to disable periodic cleanup.
func (r *PeriodicRunner) SetArchiveRetentionPeriod(period string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archiveRetentionPeriod = period
}

// SetMaxPeriodicIterations sets the user-configured default cap on scheduled
// periodic runs. 0 means unlimited (still bounded by GlobalMaxPeriodicIterations).
func (r *PeriodicRunner) SetMaxPeriodicIterations(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxPeriodicIterations = n
}

// SetMinPeriodicCompletionDelaySeconds sets the global floor for the on-completion
// periodic trigger's delay. Values < 0 are clamped to 0.
func (r *PeriodicRunner) SetMinPeriodicCompletionDelaySeconds(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minCompletionDelaySeconds = n
}

// MinPeriodicCompletionDelaySeconds returns the current floor for the on-completion
// periodic trigger's delay in seconds.
func (r *PeriodicRunner) MinPeriodicCompletionDelaySeconds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.minCompletionDelaySeconds
}

// SetPromptResolver sets the function used to resolve prompt names to their text at execution time.
func (r *PeriodicRunner) SetPromptResolver(resolver conversation.PromptResolver) {
	r.promptResolver = resolver
}

// Start begins the periodic polling loop in a background goroutine.
// It returns immediately. Call Stop() to stop the runner.
func (r *PeriodicRunner) Start() {
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
		r.logger.Debug("Periodic runner started", "poll_interval", r.pollInterval)
	}
}

// Stop gracefully stops the periodic runner and waits for it to finish.
func (r *PeriodicRunner) Stop() {
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

	// Wait for the poll loop to finish
	<-doneCh

	if r.logger != nil {
		r.logger.Debug("Periodic runner stopped")
	}
}

// IsRunning returns true if the runner is currently active.
func (r *PeriodicRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// TriggerNow immediately delivers the periodic prompt for a session,
// bypassing the normal schedule check. This is used for manual "run now" requests.
// resetTimer controls whether RecordSent() is called after the prompt completes:
//   - true  → the countdown resets from now (same as a normal scheduled run)
//   - false → the existing next-run schedule is preserved unchanged
//
// Returns an error if the delivery fails or the session is not configured for periodic prompts.
func (r *PeriodicRunner) TriggerNow(sessionID string, resetTimer bool) error {
	if r.store == nil {
		return ErrSessionStoreNotAvailable
	}

	// Get session metadata
	meta, err := r.store.GetMetadata(sessionID)
	if err != nil {
		return err
	}

	// Get periodic config for this session
	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil {
		return err
	}

	// Check if enabled
	if !periodic.Enabled {
		return ErrPeriodicNotEnabled
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		return ErrSessionManagerNotAvailable
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the periodic prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for immediate periodic delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}

		bs, err = r.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			return err
		}

		if r.logger != nil {
			r.logger.Info("Session auto-resumed for immediate periodic delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		return ErrSessionBusy
	}

	if r.logger != nil {
		r.logger.Info("Triggering immediate periodic delivery",
			"session_id", sessionID,
			"session_name", meta.Name,
			"prompt_preview", truncatePrompt(periodic.Prompt, 100))
	}

	// Deliver the prompt
	return r.deliverPrompt(bs, meta.Name, periodic, periodicStore, resetTimer, true)
}

// OnConversationIdle is invoked when a session's agent has stopped and the session
// is fully idle (no queued work). For conversations configured with the onCompletion
// trigger it arms a one-shot timer that delivers the next run after the configured
// delay (clamped to the global minimum floor). For any other configuration it cancels
// a possibly-stale timer and returns.
func (r *PeriodicRunner) OnConversationIdle(sessionID string) {
	if r.store == nil {
		return
	}

	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnCompletion() {
		// Not an active onCompletion loop — drop any timer left over from a prior config.
		r.cancelCompletionTimer(sessionID)
		return
	}

	r.mu.Lock()
	floor := r.minCompletionDelaySeconds
	r.mu.Unlock()

	delaySeconds := periodic.DelaySeconds
	if delaySeconds < floor {
		delaySeconds = floor
	}
	delay := time.Duration(delaySeconds) * time.Second

	r.armCompletionTimer(sessionID, delay)

	if r.logger != nil {
		r.logger.Debug("Armed on-completion periodic timer",
			"session_id", sessionID,
			"delay_seconds", delaySeconds)
	}
}

// armCompletionTimer schedules fireOnCompletion after delay, replacing (and stopping)
// any timer already pending for the session so only one firing is queued.
func (r *PeriodicRunner) armCompletionTimer(sessionID string, delay time.Duration) {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	if existing, ok := r.completionTimers[sessionID]; ok {
		existing.Stop()
	}
	r.completionTimers[sessionID] = time.AfterFunc(delay, func() {
		r.fireOnCompletion(sessionID)
	})
}

// cancelCompletionTimer stops and removes any pending on-completion timer for the session.
func (r *PeriodicRunner) cancelCompletionTimer(sessionID string) {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	if existing, ok := r.completionTimers[sessionID]; ok {
		existing.Stop()
		delete(r.completionTimers, sessionID)
	}
}

// BootstrapOnCompletion delivers the very first run of an onCompletion periodic
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
// Called from checkSession (crash-safe on poll-loop restart), handleSetPeriodic,
// handlePatchPeriodic (HTTP), and handleConversationStart/handleConversationUpdate (MCP).
// Best-effort — errors are logged but not propagated.
func (r *PeriodicRunner) BootstrapOnCompletion(sessionID string) {
	if r.store == nil {
		return
	}

	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnCompletion() {
		return
	}

	// Only bootstrap the very first run.
	if periodic.IterationCount != 0 || periodic.LastSentAt != nil {
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
// onCompletion periodic loop that missed its end-of-turn re-arm and would
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
func (r *PeriodicRunner) recoverStalledOnCompletion(meta session.Metadata, periodic *session.PeriodicPrompt) {
	if periodic == nil {
		return
	}

	// Fresh loops are bootstrapped elsewhere; only recover loops that have run.
	if periodic.IterationCount == 0 && periodic.LastSentAt == nil {
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
	if periodic.ReachedMaxDuration(time.Now()) {
		if r.store != nil {
			periodicStore := r.store.Periodic(meta.SessionID)
			r.autoStopIfMaxDurationReached(meta.SessionID, periodic, periodicStore, time.Now())
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
		r.logger.Info("Re-arming stalled on-completion periodic loop (missed end-of-turn re-arm)",
			"session_id", meta.SessionID,
			"iteration_count", periodic.IterationCount)
	}

	// Re-read config and arm the timer with the floor-clamped delay.
	r.OnConversationIdle(meta.SessionID)
}

// fireOnCompletion delivers the next onCompletion periodic run. It re-validates the
// session and periodic configuration (the conversation may have been archived, disabled,
// or reconfigured during the delay) and then delivers via TriggerNow. A busy session is
// skipped — the next idle transition re-arms the timer.
func (r *PeriodicRunner) fireOnCompletion(sessionID string) {
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

	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil || periodic == nil || !periodic.Enabled || !periodic.IsOnCompletion() {
		return
	}

	// Auto-stop if the wall-clock maxDuration cap is reached before delivering.
	if r.autoStopIfMaxDurationReached(sessionID, periodic, periodicStore, time.Now()) {
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
			r.logger.Debug("On-completion periodic firing skipped, session busy",
				"session_id", sessionID)
		} else {
			r.logger.Warn("On-completion periodic firing failed",
				"session_id", sessionID,
				"error", err)
		}
	}
}

// pollLoop is the main polling loop that checks for due prompts.
func (r *PeriodicRunner) pollLoop() {
	defer close(r.doneCh)

	// Wait before first poll to let interactive sessions resume first via WebSocket.
	// Periodic sessions can afford to wait since their prompts are scheduled.
	if r.startupDelay > 0 {
		if r.logger != nil {
			r.logger.Info("Deferring periodic poll to let interactive sessions resume first",
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
func (r *PeriodicRunner) RunOnce() (delivered, skipped, errored int) {
	if r.store == nil {
		return 0, 0, 0
	}

	// List all sessions
	sessions, err := r.store.List()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to list sessions for periodic check", "error", err)
		}
		return 0, 0, 1
	}

	now := time.Now().UTC()

	// Sort sessions so most-overdue periodic prompts are processed first.
	// Non-periodic sessions are kept in original order (sorted to the end).
	sort.SliceStable(sessions, func(i, j int) bool {
		pi := r.getNextScheduledAt(sessions[i])
		pj := r.getNextScheduledAt(sessions[j])
		if pi == nil && pj == nil {
			return false
		}
		if pi == nil {
			return false // non-periodic sorts after periodic
		}
		if pj == nil {
			return true // periodic sorts before non-periodic
		}
		return pi.Before(*pj) // most overdue (earliest NextScheduledAt) first
	})

	// Collect sessions that have due periodic prompts and need resuming.
	// Process them with stagger delay to prevent thundering herd.
	var lastResumeTime time.Time

	for _, meta := range sessions {
		// Apply stagger delay between resume-triggering periodic checks.
		// Only stagger when we actually resumed a session in a previous iteration.
		if r.resumeStagger > 0 && !lastResumeTime.IsZero() {
			elapsed := time.Since(lastResumeTime)
			if elapsed < r.resumeStagger {
				wait := r.resumeStagger - elapsed
				if r.logger != nil {
					r.logger.Debug("Staggering periodic session resume",
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

	// Clean up archived sessions past retention
	r.checkArchiveCleanup()

	if r.logger != nil {
		r.logger.Debug("Periodic poll completed",
			"delivered", delivered,
			"skipped", skipped,
			"errored", errored)
	}

	return delivered, skipped, errored
}

// sessionNeedsResume returns true if checkSession would trigger a ResumeSession call.
// Used to apply stagger delays between consecutive resume attempts.
func (r *PeriodicRunner) sessionNeedsResume(meta session.Metadata, now time.Time) bool {
	if meta.Archived {
		return false
	}

	periodicStore := r.store.Periodic(meta.SessionID)
	periodic, err := periodicStore.Get()
	if err != nil || !periodic.Enabled {
		return false
	}

	if periodic.NextScheduledAt == nil || periodic.NextScheduledAt.After(now) {
		return false
	}

	// Will need resume if not currently running
	bs := r.sessionManager.GetSession(meta.SessionID)
	return bs == nil
}

// getNextScheduledAt returns the NextScheduledAt for a session's periodic config, or nil if not periodic/not enabled.
func (r *PeriodicRunner) getNextScheduledAt(meta session.Metadata) *time.Time {
	if meta.Archived {
		return nil
	}
	periodicStore := r.store.Periodic(meta.SessionID)
	periodic, err := periodicStore.Get()
	if err != nil || !periodic.Enabled {
		return nil
	}
	return periodic.NextScheduledAt
}

// checkScheduledQueues checks all active sessions for scheduled queue messages
// that are now due for delivery, and triggers processing.
func (r *PeriodicRunner) checkScheduledQueues(sessions []session.Metadata) {
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

// checkSession checks a single session for due periodic prompts.
// Returns (1, 0, 0) if delivered, (0, 1, 0) if skipped, (0, 0, 1) if error.
func (r *PeriodicRunner) checkSession(meta session.Metadata, now time.Time) (delivered, skipped, errored int) {
	sessionID := meta.SessionID

	// Skip archived sessions - periodic prompts are inactive for archived sessions
	if meta.Archived {
		return 0, 0, 0
	}

	// Get periodic config for this session
	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil {
		if err == session.ErrPeriodicNotFound {
			// No periodic config - this is normal, not an error
			return 0, 0, 0
		}
		if r.logger != nil {
			r.logger.Error("Failed to read periodic config",
				"session_id", sessionID,
				"error", err)
		}
		return 0, 0, 1
	}

	// Skip if disabled
	if !periodic.Enabled {
		return 0, 0, 0
	}

	// onCompletion configs never have a NextScheduledAt — the schedule loop cannot
	// deliver them. Bootstrap the very first run here so that a crash or restart
	// before any delivery still kicks off the loop. No-op if already run or in-flight.
	if periodic.IsOnCompletion() {
		r.BootstrapOnCompletion(sessionID)
		// Self-healing safety net for an already-running loop whose end-of-turn
		// re-arm was missed (e.g. around an ACP resume or a heavy children-wait
		// turn that did not register as a clean idle transition). See mitto-5dn.
		r.recoverStalledOnCompletion(meta, periodic)
		return 0, 0, 0
	}

	// Check if due
	if periodic.NextScheduledAt == nil || periodic.NextScheduledAt.After(now) {
		return 0, 0, 0
	}

	// Auto-stop if the wall-clock maxDuration cap is reached before delivering.
	if r.autoStopIfMaxDurationReached(sessionID, periodic, periodicStore, now) {
		return 0, 0, 0
	}

	// Prompt is due - calculate how overdue it is
	scheduledAt := *periodic.NextScheduledAt
	overdueBy := now.Sub(scheduledAt)

	// Calculate how many runs were missed (for logging purposes)
	missedRuns := 0
	if overdueBy > 0 && periodic.Frequency.Duration() > 0 {
		// Number of full intervals that passed since scheduled time
		missedRuns = int(overdueBy / periodic.Frequency.Duration())
	}

	// Log the catch-up situation
	if r.logger != nil {
		if missedRuns > 0 {
			r.logger.Debug("Periodic prompt overdue - running catch-up (skipping missed runs)",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"overdue_by", overdueBy.Round(time.Second),
				"missed_runs", missedRuns,
				"prompt_preview", truncatePrompt(periodic.Prompt, 50))
		} else {
			r.logger.Debug("Periodic prompt is due",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"prompt_preview", truncatePrompt(periodic.Prompt, 50))
		}
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		if r.logger != nil {
			r.logger.Debug("Skipping periodic prompt - no session manager",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the periodic prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for periodic prompt",
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
				r.logger.Error("Failed to resume session for periodic prompt",
					"session_id", sessionID,
					"consecutive_failures", failures,
					"max_failures", MaxPeriodicResumeFailures,
					"error", err)
			}

			// After too many consecutive failures, archive the session
			// to stop the retry storm. The user can unarchive it manually.
			if failures >= MaxPeriodicResumeFailures {
				if r.logger != nil {
					r.logger.Warn("Archiving session after repeated ACP resume failures",
						"session_id", sessionID,
						"session_name", meta.Name,
						"consecutive_failures", failures)
				}

				// Note: the session is NOT running (resume failed), so no need to close it gracefully.

				// Persist the stopped reason before archiving so it survives even though
				// the session leaves the active view. Failures are non-fatal — archiving proceeds.
				periodicStore := r.store.Periodic(sessionID)
				if markErr := periodicStore.MarkStopped(session.StoppedReasonResumeFailures); markErr != nil {
					if r.logger != nil {
						r.logger.Warn("Failed to mark periodic stopped reason before archive",
							"session_id", sessionID,
							"error", markErr)
					}
				}

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
			r.logger.Info("Session auto-resumed for periodic prompt",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		if r.logger != nil {
			r.logger.Debug("Skipping periodic prompt - session is busy",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Deliver the prompt — normal scheduled runs always reset the timer.
	if err := r.deliverPrompt(bs, meta.Name, periodic, periodicStore, true, false); err != nil {
		if errors.Is(err, ErrPromptResolveFailed) {
			r.handlePromptResolveFailure(sessionID, meta.Name, periodic, periodicStore, err)
		} else {
			if r.logger != nil {
				r.logger.Error("Failed to deliver periodic prompt",
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

// autoStopIfMaxDurationReached checks whether the periodic conversation has exceeded
// its wall-clock maxDuration cap (elapsed time since FirstRunAt). When the cap is
// reached it disables the periodic config (without archiving) and broadcasts the
// auto-stop via onPeriodicAutoStopped, mirroring the max-iterations auto-stop. It
// returns true to signal the caller to skip delivery. Returns false when the cap is
// unlimited, not yet anchored (FirstRunAt nil), or not reached — delivery may proceed.
func (r *PeriodicRunner) autoStopIfMaxDurationReached(sessionID string, periodic *session.PeriodicPrompt, periodicStore *session.PeriodicStore, now time.Time) bool {
	if periodic == nil || !periodic.ReachedMaxDuration(now) {
		return false
	}

	if r.logger != nil {
		var elapsed time.Duration
		if periodic.FirstRunAt != nil {
			elapsed = now.Sub(*periodic.FirstRunAt).Round(time.Second)
		}
		r.logger.Info("Periodic conversation reached max duration, auto-stopping",
			"session_id", sessionID,
			"max_duration_seconds", periodic.MaxDurationSeconds,
			"elapsed", elapsed)
	}

	if err := periodicStore.MarkStopped(session.StoppedReasonMaxDuration); err != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to disable periodic after reaching max duration",
				"session_id", sessionID,
				"error", err)
		}
		return true
	}
	if r.onPeriodicAutoStopped != nil {
		// Re-read so the broadcast reflects Enabled=false / NextScheduledAt=nil.
		if final, err := periodicStore.Get(); err == nil {
			r.onPeriodicAutoStopped(sessionID, final)
		}
	}
	return true
}

// handlePromptResolveFailure handles a periodic prompt whose name no longer resolves.
// It logs the first failure at WARN and suppresses subsequent identical failures (to
// avoid one ERROR per tick), and after MaxPromptResolveFailures consecutive failures it
// auto-pauses (disables) the periodic config and broadcasts the change, mirroring the
// MaxPeriodicResumeFailures auto-archive safety.
func (r *PeriodicRunner) handlePromptResolveFailure(sessionID, sessionName string, periodic *session.PeriodicPrompt, periodicStore *session.PeriodicStore, err error) {
	r.promptResolveFailuresMu.Lock()
	r.promptResolveFailures[sessionID]++
	failures := r.promptResolveFailures[sessionID]
	r.promptResolveFailuresMu.Unlock()

	if r.logger != nil {
		if failures == 1 {
			r.logger.Warn("Periodic prompt could not be resolved; will auto-pause after repeated failures",
				"session_id", sessionID,
				"prompt_name", periodic.PromptName,
				"consecutive_failures", failures,
				"max_failures", MaxPromptResolveFailures,
				"error", err)
		} else {
			r.logger.Debug("Periodic prompt still unresolved",
				"session_id", sessionID,
				"prompt_name", periodic.PromptName,
				"consecutive_failures", failures)
		}
	}

	if failures < MaxPromptResolveFailures {
		return
	}

	if updErr := periodicStore.MarkStopped(session.StoppedReasonPromptUnresolved); updErr != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to disable periodic after repeated resolve failures",
				"session_id", sessionID, "error", updErr)
		}
		return
	}
	if r.logger != nil {
		r.logger.Warn("Auto-paused periodic conversation after repeated prompt resolve failures",
			"session_id", sessionID,
			"session_name", sessionName,
			"prompt_name", periodic.PromptName,
			"consecutive_failures", failures)
	}
	if r.onPeriodicAutoStopped != nil {
		if final, gErr := periodicStore.Get(); gErr == nil {
			r.onPeriodicAutoStopped(sessionID, final)
		}
	}
	r.promptResolveFailuresMu.Lock()
	delete(r.promptResolveFailures, sessionID)
	r.promptResolveFailuresMu.Unlock()
}

// deliverPrompt sends the periodic prompt to the session.
// resetTimer controls whether RecordSent() is called when the prompt completes:
//   - true  → schedule advances from now (normal behaviour)
//   - false → schedule is left untouched (manual "run now" without resetting the timer)
func (r *PeriodicRunner) deliverPrompt(bs *conversation.BackgroundSession, sessionName string, periodic *session.PeriodicPrompt, periodicStore *session.PeriodicStore, resetTimer bool, forced bool) error {
	sessionID := bs.GetSessionID()

	// Resolve prompt text from name if needed
	promptText := periodic.Prompt
	if periodic.PromptName != "" && r.promptResolver != nil {
		sessionMeta, err := r.store.GetMetadata(sessionID)
		if err != nil {
			return fmt.Errorf("failed to get session metadata for prompt resolution: %w", err)
		}
		resolved, err := r.promptResolver(periodic.PromptName, sessionMeta.WorkingDir)
		if err != nil {
			return fmt.Errorf("%w: %q: %v", ErrPromptResolveFailed, periodic.PromptName, err)
		}
		promptText = resolved
		if r.logger != nil {
			r.logger.Debug("Resolved periodic prompt name to text",
				"session_id", sessionID,
				"prompt_name", periodic.PromptName,
				"prompt_preview", truncatePrompt(promptText, 100))
		}
	}

	if r.logger != nil {
		r.logger.Debug("Delivering periodic prompt",
			"session_id", sessionID,
			"session_name", sessionName,
			"reset_timer", resetTimer,
			"prompt_preview", truncatePrompt(promptText, 100))
	}

	// Use OnComplete callback to defer RecordSent until the prompt actually finishes.
	// PromptWithMeta is async — it returns nil immediately. Without OnComplete,
	// RecordSent would advance the schedule even if the prompt later fails
	// (e.g., ACP process crash).
	meta := conversation.PromptMeta{
		SenderID:         "periodic-runner",
		PromptID:         "",                  // No client to confirm delivery to
		PromptName:       periodic.PromptName, // Pass prompt name so UI can render a badge instead of full text
		Arguments:        periodic.Arguments,  // User-supplied values for ${VAR} substitution in the resolved text
		IsPeriodicForced: forced,
		FreshContext:     periodic.FreshContext,
		OnComplete: func(err error) {
			if err != nil {
				if r.logger != nil {
					r.logger.Warn("Periodic prompt failed, schedule not advanced",
						"session_id", sessionID,
						"session_name", sessionName,
						"error", err)
				}
				return
			}

			if !resetTimer {
				// Manual run with "keep schedule" — leave NextScheduledAt unchanged.
				if r.logger != nil {
					r.logger.Debug("Periodic prompt completed, timer not reset (manual run)",
						"session_id", sessionID,
						"session_name", sessionName)
				}
				return
			}

			// Prompt completed successfully — now update the schedule
			if err := periodicStore.RecordSent(); err != nil {
				if r.logger != nil {
					r.logger.Warn("Failed to update periodic last_sent_at",
						"session_id", sessionID,
						"error", err)
				}
			} else {
				updated, getErr := periodicStore.Get()
				if getErr == nil && updated != nil {
					r.mu.Lock()
					cfgCap := r.maxPeriodicIterations
					r.mu.Unlock()
					effective := config.EffectiveMaxPeriodicIterations(updated.MaxIterations, cfgCap)
					perPromptReached := updated.ReachedMaxIterations()
					if updated.IterationCount >= effective {
						// Cap reached — disable the periodic prompt so it stops firing.
						if r.logger != nil {
							if perPromptReached {
								r.logger.Info("Periodic conversation reached max iterations, auto-stopping",
									"session_id", sessionID,
									"max_iterations", updated.MaxIterations,
									"iteration_count", updated.IterationCount)
							} else {
								// Stopped by the global/config backstop rather than the per-prompt cap.
								r.logger.Warn("Periodic conversation reached global iteration safeguard, auto-stopping",
									"session_id", sessionID,
									"iteration_count", updated.IterationCount,
									"effective_cap", effective,
									"config_cap", cfgCap,
									"backstop", config.GlobalMaxPeriodicIterations)
							}
						}
						// Distinguish per-prompt cap from global/config backstop.
						stoppedReason := session.StoppedReasonIterationSafeguard
						if perPromptReached {
							stoppedReason = session.StoppedReasonMaxIterations
						}
						if disableErr := periodicStore.MarkStopped(stoppedReason); disableErr != nil {
							if r.logger != nil {
								r.logger.Warn("Failed to disable periodic after reaching iteration cap",
									"session_id", sessionID,
									"error", disableErr)
							}
						} else if r.onPeriodicAutoStopped != nil {
							// Re-read so the broadcast reflects Enabled=false / NextScheduledAt=nil.
							if final, err := periodicStore.Get(); err == nil {
								r.onPeriodicAutoStopped(sessionID, final)
							}
						}
					} else {
						// Schedule advanced normally — notify clients so the countdown resets
						// to the freshly computed next-run time.
						if r.onPeriodicUpdated != nil {
							r.onPeriodicUpdated(sessionID, updated)
						}
						if r.logger != nil && updated.NextScheduledAt != nil {
							r.logger.Debug("Periodic schedule updated after delivery",
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

	// Notify about the periodic prompt delivery (the prompt is now queued/started).
	// Skip notification for forced (manual "Run Now") triggers — the user already
	// knows they triggered it, so showing a notification is redundant.
	if r.onPeriodicStarted != nil && !forced {
		r.onPeriodicStarted(sessionID, sessionName)
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
// or sessions with periodic prompts — enabled or paused (they should remain active indefinitely).
func (r *PeriodicRunner) checkAutoArchive(sessions []session.Metadata, now time.Time) {
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

		// Skip sessions with periodic prompts (enabled or paused) — they should remain active indefinitely.
		// A paused periodic conversation is still a periodic conversation and should not be auto-archived;
		// the user may re-enable it at any time.
		periodicStore := r.store.Periodic(meta.SessionID)
		_, err := periodicStore.Get()
		if err != nil && err != session.ErrPeriodicNotFound {
			if r.logger != nil {
				r.logger.Error("Failed to read periodic config during auto-archive check",
					"session_id", meta.SessionID,
					"error", err)
			}
			// Continue processing other sessions even if we can't read this one's config
			continue
		}
		if err == nil {
			if r.logger != nil {
				r.logger.Debug("Skipping auto-archive for periodic session",
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

// checkArchiveCleanup permanently deletes archived sessions older than the retention period.
func (r *PeriodicRunner) checkArchiveCleanup() {
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
		r.logger.Info("Periodic archive cleanup completed",
			"deleted_count", deleted,
			"retention_period", retentionPeriod)
	}
}
