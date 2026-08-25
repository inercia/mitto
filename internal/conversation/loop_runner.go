package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPollInterval is the default interval between loop prompt checks.
	DefaultPollInterval = 1 * time.Minute
	// Slack batches are bounded before entering PromptMeta/template processing.
	MaxSlackEventsPerDispatch = 20
	MaxSlackEventBatchBytes   = 32 * 1024
	slackUntrustedTextOpen    = "<!-- SLACK_UNTRUSTED_START -->\n"
	slackUntrustedTextClose   = "\n<!-- SLACK_UNTRUSTED_END -->"

	// MaxLoopResumeFailures is the number of consecutive ACP resume failures
	// after which a loop session is automatically archived.
	MaxLoopResumeFailures = 3

	// MaxPromptResolveFailures is the number of consecutive prompt-name resolution
	// failures after which the loop config is auto-paused (disabled).
	MaxPromptResolveFailures = 3

	// MaxTransientCompileRaceFailures bounds how many consecutive
	// ErrPromptTransientCompileRace classifications a session may accumulate
	// within transientCompileRaceWindow before the carve-out (mitto-8bg) is
	// treated as exhausted and the failure falls through to the normal
	// MaxPromptResolveFailures strike path (mitto-uvsn). A genuine sub-second
	// fragment-registry reload race clears well under this bound; a
	// persistent prompt-cache hole (e.g. a boot-time compile failure that
	// never resolves) would otherwise retry forever without ever tripping
	// the auto-pause tripwire.
	MaxTransientCompileRaceFailures = 3

	// transientCompileRaceWindow bounds the wall-clock span over which
	// MaxTransientCompileRaceFailures consecutive transient classifications
	// are counted (mitto-uvsn). If the window elapses before the count is
	// reached, the next transient classification still escalates — this
	// caps how long a stalled loop can silently retry even when fires are
	// spaced further apart than the default poll interval (e.g. a
	// longer-frequency schedule or onTasks trigger).
	transientCompileRaceWindow = 60 * time.Second

	// MaxLoopContextWindowFailures is the number of consecutive `augmentTooLarge`
	// (HTTP 413 / context window exceeded) failures after which the loop is
	// auto-paused with StoppedReasonContextWindowExceeded. The loop is retried
	// with exponential backoff for hits 1..N-1, then stopped so it no longer
	// re-fires against a wedged context (mitto-7jn).
	MaxLoopContextWindowFailures = 3

	// MaxLoopDeliveryFailures is the number of consecutive generic (non
	// context-window) scheduled-delivery failures after which the loop is
	// auto-paused with StoppedReasonDeliveryFailures. Set higher than the
	// other Max*Failures constants (which are all 3) because this branch also
	// catches genuinely transient transport errors (mitto-qal.2) that the
	// exponential backoff alone is meant to absorb — but a failure that
	// persists this many consecutive times is, in practice, permanent and
	// must not be retried forever (mitto-aeb).
	MaxLoopDeliveryFailures = 8

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

// logLoopRecordSentFailure logs a failure to persist the loop's last_sent_at
// timestamp after a successful prompt delivery. It exists so the classification
// of RecordSent errors (in particular the teardown-order race that surfaces
// as session.ErrLoopNotFound when the loop file is removed between the loop
// fire and the OnComplete callback — see mitto-rz9j) can be unit-tested
// without driving PromptWithMeta end-to-end. Logger may be nil.
func logLoopRecordSentFailure(logger *slog.Logger, sessionID string, err error) {
	if logger == nil {
		return
	}
	// Teardown-order race (mitto-rz9j): loop.json can be removed between the
	// loop fire and this OnComplete callback (parent MCP delete cascade,
	// LoopStore.Detach, or LoopStore.Delete). RecordSent then returns
	// session.ErrLoopNotFound — expected during teardown, not a real failure,
	// so log at Debug to avoid noisy WARNs.
	if errors.Is(err, session.ErrLoopNotFound) {
		logger.Debug("Skipped loop last_sent_at update: loop already removed",
			"session_id", sessionID,
			"error", err)
		return
	}
	// Resurrection detector (mitto-uun): RecordSent was invoked on a loop that
	// was already MarkStopped'd — the write still succeeded, but the fact that
	// a delivery fired at all points to a resurrection bug upstream (a Set()
	// clobber or a lost auto-stop write). Log loudly so regressions surface in
	// production without altering delivery behavior.
	if errors.Is(err, session.ErrRecordSentOnStoppedLoop) {
		logger.Warn("Loop RecordSent fired on an already-stopped loop (auto-stop resurrection)",
			"session_id", sessionID,
			"error", err)
		return
	}
	logger.Warn("Failed to update loop last_sent_at",
		"session_id", sessionID,
		"error", err)
}

// Errors for loop runner operations.
var (
	ErrSessionStoreNotAvailable   = errors.New("session store not available")
	ErrSessionManagerNotAvailable = errors.New("session manager not available")
	ErrLoopNotEnabled             = errors.New("loop is not enabled for this session")
	ErrSessionBusy                = errors.New("session is currently processing a prompt")
	ErrPromptResolveFailed        = errors.New("loop prompt could not be resolved")
	// ErrPromptTransientCompileRace signals that a prompt-name resolution failure
	// was caused by a transient template fragment compile-race (e.g. a consumer
	// prompt referencing a `_shared/foo` fragment while the fragment registry has
	// not yet been refreshed by the fs-watcher). The resolver wraps a "not found"
	// with this sentinel when PromptsCache.LoadErrors() indicates the miss stems
	// from a `template "..." not defined` compile error rather than a genuinely
	// missing/renamed prompt. The loop-runner strike counter (mitto-8bg) skips
	// these to avoid auto-pausing loops on transient reload races.
	ErrPromptTransientCompileRace = errors.New("loop prompt transiently unresolved due to template compile race")
	// ErrWorkspaceBusy signals that the workspace/ACP-server pair already has the
	// configured maximum number of loop prompts in flight. The scheduled loop is
	// skipped for this poll cycle and retried on the next tick (no schedule
	// advance, no failure backoff). Manual "Run Now" (forced) bypasses this cap.
	// See mitto-61z.
	ErrWorkspaceBusy = errors.New("workspace has reached the loop concurrency cap")
	// ErrLoopDispatchCoalesced signals that another trigger of the same
	// multi-trigger loop already owns an in-flight dispatch for this session, so
	// this fire was dropped rather than queued (mitto-r6j.2). It is an expected,
	// non-error outcome: callers log it at Debug and do not count it as a
	// delivery failure.
	ErrLoopDispatchCoalesced = errors.New("loop dispatch coalesced: another trigger is already in flight")
)

// transientRaceState tracks a session's consecutive ErrPromptTransientCompileRace
// classifications so handlePromptResolveFailure can bound the mitto-8bg
// carve-out (see MaxTransientCompileRaceFailures / transientCompileRaceWindow,
// mitto-uvsn).
type transientRaceState struct {
	count     int
	firstSeen time.Time
}

// isTransientPromptCompileRace reports whether err represents a transient
// template-fragment compile-race — i.e. a prompt-name resolution failure caused
// by the fragment registry lagging behind a fs-watcher reload (see the wrap
// logic in internal/web/server.go around the PromptsCache.LoadErrors iteration).
//
// The primary match is errors.Is against ErrPromptTransientCompileRace, which
// covers every wrapped occurrence from the resolver. The defensive heuristic
// (both "template " and "not defined" in the error string) catches an unwrapped
// raw Go template error if any inner code path returns it verbatim; it mirrors
// the substring set the wrapper itself uses to detect the same condition.
//
// Used by the loop-runner strike counter (mitto-8bg) and by the queue
// dispatcher's bounded retry (mitto-omu) to avoid treating a transient race as
// a durable error.
func isTransientPromptCompileRace(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPromptTransientCompileRace) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "template ") && strings.Contains(msg, "not defined")
}

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
	sessionManager *SessionManager
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
	promptResolver PromptResolver

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

	// transientCompileRaceFailures tracks, per session, how many consecutive
	// ErrPromptTransientCompileRace classifications have been seen and when
	// the streak started. Once MaxTransientCompileRaceFailures is reached
	// within transientCompileRaceWindow, the carve-out is exhausted and the
	// failure escalates into the normal promptResolveFailures strike path
	// (mitto-uvsn) instead of retrying silently forever.
	transientCompileRaceFailures   map[string]*transientRaceState
	transientCompileRaceFailuresMu sync.Mutex

	// scheduleBackoffFailures tracks consecutive delivery failures for scheduled
	// loop prompts. It drives an exponential backoff on NextScheduledAt so a
	// flaky transport does not cause the same prompt to re-fire every poll tick
	// (mitto-qal.2). Reset to zero on the next successful delivery. Distinct from
	// consecutiveFailures, which tracks resume failures and triggers auto-archive.
	// Scoped strictly to schedule-initiated (resetTimer=true, forced=false,
	// firedBy=TriggerSchedule) runs so a failed onCompletion/onTasks leg on a
	// multi-trigger loop never consumes the schedule leg's own breaker
	// (mitto-r6j.2). See deliveryFailures for the trigger-agnostic counterpart.
	scheduleBackoffFailures   map[string]int
	scheduleBackoffFailuresMu sync.Mutex

	// deliveryFailures tracks consecutive generic (non context-window) delivery
	// failures REGARDLESS of trigger type or forced-ness — unlike
	// scheduleBackoffFailures above, which is deliberately schedule-only. Once
	// it reaches MaxLoopDeliveryFailures the loop is auto-paused with
	// StoppedReasonDeliveryFailures, giving forced dispatches (manual "Run Now",
	// the runOnStart boot pulse) and event-driven fires (onCompletion/onTasks/
	// onChild) the same bounded safety net a scheduled run gets — a durably
	// broken forced/event-driven delivery previously had no ceiling at all and
	// could go silently deaf forever (mitto-bmct). Reset to zero on the next
	// successful delivery, regardless of trigger.
	deliveryFailures   map[string]int
	deliveryFailuresMu sync.Mutex

	// contextWindowFailures tracks consecutive `augmentTooLarge` (HTTP 413 /
	// context window exceeded) delivery failures. After MaxLoopContextWindowFailures
	// consecutive hits the loop is auto-paused with
	// StoppedReasonContextWindowExceeded (mitto-7jn). Reset on the next successful
	// delivery.
	contextWindowFailures   map[string]int
	contextWindowFailuresMu sync.Mutex

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

	// beadsWatcher is the (optional) BeadsWatcher instance the runner is
	// subscribed to as a BeadsSubscriber. Stashed by SetBeadsWatcher so
	// Stop() can Unsubscribe(r) and drop out of the watcher's fan-out list
	// before shutdown continues (mitto-cbx). Nil in tests that don't wire
	// a watcher — Stop() handles the nil case.
	beadsWatcher *watcher.BeadsWatcher

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

	// tasksRefirePending is a sticky per-session boolean, set when a fs-watcher
	// delta arrives during a busy window (tasksActionDeferBusy branch) and
	// consumed by fireTasksRebase at quiescence to trigger exactly one re-fire
	// regardless of loop.ShouldCoalesceDuringBusy() (mitto-cwg.1). The flag is
	// "mark, don't stack": any number of fs events during a busy window collapses
	// to a single re-fire after quiescence, subject to Layer 0 guards. Guarded by
	// tasksRebaseTimersMu (same lock as the timer map, since every access sits on
	// the same code paths).
	tasksRefirePending map[string]bool

	// tasksRefireDeliveryFailures counts consecutive tasksRefireDeliveryFailed
	// outcomes from maybeFireAccumulatedDelta for a session (mitto-rrq work item
	// 3). fireTasksRebase self-heals a failed re-fire by re-arming the
	// quiescence timer instead of rebasing the baseline (which would destroy the
	// pending delta), but only up to maxTasksRefireDeliveryFailures — beyond
	// that a durably-broken loop prompt would re-arm forever. Cleared on any
	// successful re-fire dispatch and by clearTasksRefirePending. Guarded by
	// tasksRebaseTimersMu (same lock as tasksRefirePending).
	tasksRefireDeliveryFailures map[string]int

	// tasksSettleWindow is the runner-level default pre-fire settle/debounce
	// window applied on the idle→first-fire path of the onTasks trigger
	// (mitto-1uv). When > 0 (or the per-loop LoopPrompt.SettleWindow() is > 0),
	// processTasksChange arms a settle timer on tasksActionFire instead of firing
	// immediately; the timer is reset by subsequent material fs-watcher deltas
	// and dispatches a single coalesced fire when it expires. Default 0 =
	// disabled (current fire-on-first-delta behaviour). Intended primarily as a
	// test seam via SetTasksSettleWindow; production loops opt in per-prompt via
	// LoopPrompt.SettleWindowSeconds.
	tasksSettleWindow time.Duration

	// tasksSettleTimers holds armed one-shot settle timers that dispatch a
	// coalesced onTasks fire once the pre-fire settle window elapses without a
	// further material delta, keyed by session ID. Guarded by
	// tasksSettleTimersMu.
	tasksSettleTimers   map[string]*time.Timer
	tasksSettleTimersMu sync.Mutex

	// loopWorkspaceConcurrency caps how many loop prompts may be in flight for a
	// single WorkingDir + ACPServer pair. 0 disables the cap. Default is set by
	// config (see DefaultLoopWorkspaceConcurrency). Manual "Run Now" (forced)
	// deliveries bypass the cap unconditionally. See mitto-61z.
	loopWorkspaceConcurrency int

	// runOnStartAntiFlapSeconds is the anti-flap window (seconds) applied to the
	// boot pulse (mitto-ystk). When a loop with RunOnStart=true was last
	// delivered within this window (loop.LastSentAt), the boot pulse is
	// suppressed. 0 disables the guard (always fire on start).
	runOnStartAntiFlapSeconds int

	// runOnStartFired tracks session IDs that already received their once-per-
	// process boot pulse, so a subsequent Start() (only used in tests) or a
	// spurious re-invocation of fireOnStartPulses does not re-fire. Guarded by
	// runOnStartFiredMu.
	runOnStartFired   map[string]bool
	runOnStartFiredMu sync.Mutex

	// workspaceInFlight counts in-flight loop prompts per workspace key
	// (WorkingDir + "\x00" + ACPServer). Guarded by workspaceInFlightMu.
	workspaceInFlight   map[string]int
	workspaceInFlightMu sync.Mutex

	// dispatchInFlight records, per session, which trigger currently owns an
	// in-flight loop dispatch (mitto-r6j.2). A multi-trigger loop arms every
	// listed trigger independently, so two of them can fire in the same window;
	// the claim makes the coalescing rule explicit and testable: the first
	// trigger to claim wins, and a losing trigger is DROPPED (never queued).
	// Claimed just before deliverPrompt, released in OnComplete and on every
	// synchronous failure path. Guarded by dispatchInFlightMu.
	dispatchInFlight   map[string]session.LoopTrigger
	dispatchInFlightMu sync.Mutex

	mu      sync.Mutex
	running bool
	// stopped flips to true exactly once, inside Stop(), and stays true
	// forever after. Used by OnBeadsChanged as a fan-out shutdown guard
	// (mitto-cbx). Distinct from !running, which is also true for a
	// never-started runner (tests routinely call OnBeadsChanged without
	// Start()).
	stopped bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewLoopRunner creates a new loop runner.
func NewLoopRunner(store *session.Store, sm *SessionManager, logger *slog.Logger) *LoopRunner {
	evaluator, err := config.NewTasksConditionEvaluator()
	if err != nil {
		evaluator = nil
		if logger != nil {
			logger.Warn("Failed to initialize onTasks CEL evaluator; onTasks trigger will be inactive", "error", err)
		}
	}
	return &LoopRunner{
		store:                        store,
		sessionManager:               sm,
		logger:                       logger,
		pollInterval:                 DefaultPollInterval,
		maxLoopIterations:            config.DefaultMaxLoopIterations,
		minCompletionDelaySeconds:    config.DefaultMinLoopCompletionDelaySeconds,
		consecutiveFailures:          make(map[string]int),
		promptResolveFailures:        make(map[string]int),
		transientCompileRaceFailures: make(map[string]*transientRaceState),
		scheduleBackoffFailures:      make(map[string]int),
		deliveryFailures:             make(map[string]int),
		contextWindowFailures:        make(map[string]int),
		completionTimers:             make(map[string]*time.Timer),
		tasksEvaluator:               evaluator,
		minTasksCooldownSeconds:      DefaultMinLoopTasksCooldownSeconds,
		tasksQuiescenceWindow:        tasksDefaultQuiescenceWindow,
		tasksRebaseTimers:            make(map[string]*time.Timer),
		tasksRefirePending:           make(map[string]bool),
		tasksRefireDeliveryFailures:  make(map[string]int),
		tasksSettleTimers:            make(map[string]*time.Timer),
		autoUnarchiveEnabled:         true,
		autoUnarchiveRetryInterval:   DefaultAutoUnarchiveRetryInterval,
		autoUnarchiveStagger:         DefaultAutoUnarchiveStaggerInterval,
		loopWorkspaceConcurrency:     config.DefaultLoopWorkspaceConcurrency,
		workspaceInFlight:            make(map[string]int),
		dispatchInFlight:             make(map[string]session.LoopTrigger),
		runOnStartAntiFlapSeconds:    config.DefaultRunOnStartAntiFlapSeconds,
		runOnStartFired:              make(map[string]bool),
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

// SetLoopWorkspaceConcurrency sets the maximum number of scheduled loop
// prompts that may be in flight simultaneously per WorkingDir + ACPServer
// pair. 0 disables the cap. Negative values are clamped to 0. Manual "Run
// Now" (forced) deliveries always bypass this cap. See mitto-61z.
func (r *LoopRunner) SetLoopWorkspaceConcurrency(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loopWorkspaceConcurrency = n
}

// workspaceKey returns the workspace concurrency key for a session's metadata.
// The key uniquely identifies the shared ACP process boundary (WorkingDir +
// ACPServer). NUL is used as a separator because it cannot appear in path
// or agent-id strings.
func workspaceKey(workingDir, acpServer string) string {
	return workingDir + "\x00" + acpServer
}

// tryReserveWorkspaceSlot atomically reserves a loop-dispatch slot for the
// given workspace key. Returns true if the slot was reserved (caller must
// eventually call releaseWorkspaceSlot). Returns false if the workspace is
// already at its concurrency cap. Returns true unconditionally when the cap
// is 0 (disabled) — no counter increment is performed in that case.
func (r *LoopRunner) tryReserveWorkspaceSlot(key string) bool {
	r.mu.Lock()
	cap := r.loopWorkspaceConcurrency
	r.mu.Unlock()
	if cap <= 0 {
		return true
	}
	r.workspaceInFlightMu.Lock()
	defer r.workspaceInFlightMu.Unlock()
	if r.workspaceInFlight[key] >= cap {
		return false
	}
	r.workspaceInFlight[key]++
	return true
}

// releaseWorkspaceSlot releases a previously reserved slot for the given
// workspace key. Safe to call when the cap is disabled (0): it is a no-op
// if the counter is already zero. Never goes negative.
func (r *LoopRunner) releaseWorkspaceSlot(key string) {
	r.workspaceInFlightMu.Lock()
	defer r.workspaceInFlightMu.Unlock()
	if n, ok := r.workspaceInFlight[key]; ok && n > 0 {
		if n == 1 {
			delete(r.workspaceInFlight, key)
		} else {
			r.workspaceInFlight[key] = n - 1
		}
	}
}

// claimDispatch attempts to claim the single in-flight dispatch slot for a
// session on behalf of firedBy (mitto-r6j.2). It returns ok=true when the claim
// succeeded; otherwise ok=false and winner names the trigger that already owns
// the dispatch. This implements the documented coalescing rule for
// multi-trigger loops: a trigger that fires while another one's run is already
// dispatching or in flight is DROPPED, not queued.
func (r *LoopRunner) claimDispatch(sessionID string, firedBy session.LoopTrigger) (winner session.LoopTrigger, ok bool) {
	r.dispatchInFlightMu.Lock()
	defer r.dispatchInFlightMu.Unlock()
	if existing, held := r.dispatchInFlight[sessionID]; held {
		return existing, false
	}
	r.dispatchInFlight[sessionID] = firedBy
	return firedBy, true
}

// releaseDispatch drops the in-flight dispatch claim for a session. Idempotent.
func (r *LoopRunner) releaseDispatch(sessionID string) {
	r.dispatchInFlightMu.Lock()
	defer r.dispatchInFlightMu.Unlock()
	delete(r.dispatchInFlight, sessionID)
}

// workspaceInFlightCount returns the current in-flight count for a workspace
// key. Exported to the package for tests.
func (r *LoopRunner) workspaceInFlightCount(key string) int {
	r.workspaceInFlightMu.Lock()
	defer r.workspaceInFlightMu.Unlock()
	return r.workspaceInFlight[key]
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

// SetRunOnStartAntiFlapSeconds sets the anti-flap window (seconds) applied to
// the loop boot pulse (mitto-ystk). A loop configured with RunOnStart=true is
// skipped by fireOnStartPulses when its LastSentAt falls within this window.
// A value < 0 is treated as 0 (guard disabled). Test-only knob; production
// runners get the default from config.
func (r *LoopRunner) SetRunOnStartAntiFlapSeconds(n int) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runOnStartAntiFlapSeconds = n
}

// SetPromptResolver sets the function used to resolve prompt names to their text at execution time.
func (r *LoopRunner) SetPromptResolver(resolver PromptResolver) {
	r.promptResolver = resolver
}

// FireOnStartPulses is a test-only wrapper around fireOnStartPulses that lets
// integration tests invoke the boot-pulse pass on demand instead of waiting
// out the (production-default 15 s) startup delay in pollLoop. It mirrors the
// exact code path executed once at boot, including the once-per-process guard
// (runOnStartFired) and the anti-flap window.
func (r *LoopRunner) FireOnStartPulses() {
	r.fireOnStartPulses()
}

// HasFiredRunOnStart reports whether the runner has already dispatched a
// boot-pulse (mitto-ystk) for the given session ID in this process lifetime.
// Test-only inspection helper used by integration tests to assert once-per-
// process idempotence of fireOnStartPulses.
func (r *LoopRunner) HasFiredRunOnStart(sessionID string) bool {
	r.runOnStartFiredMu.Lock()
	defer r.runOnStartFiredMu.Unlock()
	return r.runOnStartFired[sessionID]
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
		// Even if the runner was never started (or has already been
		// stopped), flag `stopped` so any late fan-out from a subscribed
		// BeadsWatcher is dropped by OnBeadsChanged's guard (mitto-cbx).
		r.stopped = true
		r.mu.Unlock()
		return
	}
	r.running = false
	r.stopped = true
	close(r.stopCh)
	doneCh := r.doneCh
	r.mu.Unlock()

	// Unsubscribe from the beads watcher BEFORE cancelling timers so any
	// in-flight debounced fan-out no longer sees this runner in its
	// subscriber snapshot (mitto-cbx). The belt-and-suspenders guard in
	// OnBeadsChanged handles events already snapshotted before this call.
	if r.beadsWatcher != nil {
		r.beadsWatcher.Unsubscribe(r)
	}

	// Cancel any pending on-completion timers so they don't fire after shutdown.
	r.completionTimersMu.Lock()
	for id, t := range r.completionTimers {
		t.Stop()
		delete(r.completionTimers, id)
	}
	r.completionTimersMu.Unlock()

	// Cancel any pending onTasks baseline-rebase timers so they don't fire after shutdown.
	// Also drop any sticky re-fire flags — a re-fire scheduled by an armed timer
	// cannot land after Stop(), so leaving the flag set would only leak the entry
	// across a subsequent Start() in tests (mitto-cwg.1).
	r.tasksRebaseTimersMu.Lock()
	for id, t := range r.tasksRebaseTimers {
		t.Stop()
		delete(r.tasksRebaseTimers, id)
	}
	for id := range r.tasksRefirePending {
		delete(r.tasksRefirePending, id)
	}
	r.tasksRebaseTimersMu.Unlock()

	// Cancel any pending onTasks pre-fire settle timers so they don't fire
	// after shutdown (mitto-1uv). Separate from the rebase timer map — the
	// settle timer arms on the idle→first-fire path before any run has
	// started, so a Stop() during the settle window must drop the pending
	// dispatch cleanly.
	r.tasksSettleTimersMu.Lock()
	for id, t := range r.tasksSettleTimers {
		t.Stop()
		delete(r.tasksSettleTimers, id)
	}
	r.tasksSettleTimersMu.Unlock()

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

// LoopDispatchOptions bundles per-fire optional dispatch context so
// triggerNowFull / deliverPrompt do not grow more positional parameters
// (mitto-qvlh). A nil *LoopDispatchOptions is the common case (schedule,
// onCompletion, manual "Run Now", boot pulse). Empty sub-fields are treated
// identically to a nil *LoopDispatchOptions for their respective trigger.
type LoopDispatchOptions struct {
	// TasksDelta carries the beads change delta for onTasks fires.
	TasksDelta *config.TasksDelta
	// SlackEvents is the already-bounded canonical batch for onSlack fires.
	SlackEvents []PromptSlackEvent
	// OnChild carries child-lifecycle detail for onChild fires.
	OnChild *LoopDispatchOnChild
}

// LoopDispatchOnChild carries the child-lifecycle detail that fired an
// onChild loop dispatch (mitto-qvlh).
type LoopDispatchOnChild struct {
	ChildID string
	Event   session.ChildEvent
	// StoppedReason is non-empty only for anyLoopStopped.
	StoppedReason session.StoppedReason
}

// optsTasksDelta returns opts.TasksDelta, nil-safe for a nil opts.
func optsTasksDelta(opts *LoopDispatchOptions) *config.TasksDelta {
	if opts == nil {
		return nil
	}
	return opts.TasksDelta
}

// optsSlackEvents returns opts.SlackEvents, nil-safe for a nil opts.
func optsSlackEvents(opts *LoopDispatchOptions) []PromptSlackEvent {
	if opts == nil {
		return nil
	}
	return opts.SlackEvents
}

// optsOnChild returns opts.OnChild, nil-safe for a nil opts.
func optsOnChild(opts *LoopDispatchOptions) *LoopDispatchOnChild {
	if opts == nil {
		return nil
	}
	return opts.OnChild
}

// buildPromptTriggerContext assembles the PromptTriggerContext for a
// delivered loop prompt from a computed onTasks delta, a bounded onSlack
// batch, and/or onChild fire detail (mitto-qvlh). Returns nil when none of
// the three carry data. Extracted as a pure function (mirroring the
// mitto-rz9j rationale for logLoopRecordSentFailure above) so the onChild
// threading can be unit-tested directly, without driving PromptWithMeta
// end-to-end.
func buildPromptTriggerContext(tasksDelta *config.TasksDelta, slackEvents []PromptSlackEvent, onChild *LoopDispatchOnChild) *PromptTriggerContext {
	if tasksDelta == nil && len(slackEvents) == 0 && onChild == nil {
		return nil
	}
	ctx := &PromptTriggerContext{}
	if tasksDelta != nil {
		ctx.OnTasks = &PromptOnTasksContext{Changes: tasksDelta}
	}
	if len(slackEvents) > 0 {
		ctx.OnSlack = &PromptOnSlackContext{Events: slackEvents}
		ctx.Slack = &ctx.OnSlack.Events[0]
	}
	if onChild != nil {
		ctx.OnChild = &PromptOnChildContext{
			ChildID:       onChild.ChildID,
			Event:         onChild.Event,
			StoppedReason: onChild.StoppedReason,
		}
	}
	return ctx
}

// TriggerNow immediately delivers the loop prompt for a session,
// bypassing the normal schedule check. This is used for manual "run now" requests.
// resetTimer controls whether RecordSent() is called after the prompt completes:
//   - true  → the countdown resets from now (same as a normal scheduled run)
//   - false → the existing next-run schedule is preserved unchanged
//
// Returns an error if the delivery fails or the session is not configured for loop prompts.
func (r *LoopRunner) TriggerNow(sessionID string, resetTimer bool) error {
	return r.triggerNowFull(sessionID, resetTimer, false, "", nil)
}

// TriggerNowFrom is TriggerNow with an explicit winning trigger, recorded on the
// delivered PromptMeta and used for the coalescing claim (mitto-r6j.2). An empty
// firedBy defers to the loop's primary EffectiveTrigger.
func (r *LoopRunner) TriggerNowFrom(sessionID string, resetTimer bool, firedBy session.LoopTrigger) error {
	return r.triggerNowFull(sessionID, resetTimer, false, firedBy, nil)
}

// TriggerNowWithSlackEvent is the legacy single-event wrapper retained for the
// environment PoC. New sources should call TriggerNowWithSlackEvents and render
// {{ .Trigger.OnSlack.Events }}.
func (r *LoopRunner) TriggerNowWithSlackEvent(sessionID string, resetTimer bool, firedBy session.LoopTrigger, evt *PromptSlackContext) error {
	if evt == nil {
		return r.triggerNowFull(sessionID, resetTimer, false, firedBy, nil)
	}
	return r.TriggerNowWithSlackEvents(sessionID, resetTimer, firedBy, []PromptSlackEvent{*evt})
}

// TriggerNowWithSlackEvents dispatches one bounded onSlack event batch. The
// batch shares the normal dispatch claim and counts as one loop iteration.
func (r *LoopRunner) TriggerNowWithSlackEvents(sessionID string, resetTimer bool, firedBy session.LoopTrigger, events []PromptSlackEvent) error {
	if firedBy == "" {
		firedBy = session.TriggerOnSlack
	}
	return r.triggerNowFull(sessionID, resetTimer, false, firedBy, &LoopDispatchOptions{SlackEvents: boundSlackEvents(events)})
}

// triggerNowFromChild dispatches an onChild fire, bundling the child-lifecycle
// detail into a LoopDispatchOptions envelope (mitto-qvlh) so fireOnChild
// itself stays readable. stoppedReason is empty for anyEndResponse/anyDeleted
// and carries the child's own stop reason for anyLoopStopped.
func (r *LoopRunner) triggerNowFromChild(parentID string, resetTimer bool, event session.ChildEvent, childID string, stoppedReason session.StoppedReason) error {
	return r.triggerNowFull(parentID, resetTimer, false, session.TriggerOnChild, &LoopDispatchOptions{
		OnChild: &LoopDispatchOnChild{ChildID: childID, Event: event, StoppedReason: stoppedReason},
	})
}

func boundSlackEvents(events []PromptSlackEvent) []PromptSlackEvent {
	if len(events) > MaxSlackEventsPerDispatch {
		events = events[:MaxSlackEventsPerDispatch]
	}
	bounded := make([]PromptSlackEvent, 0, len(events))
	used := 0
	for _, event := range events {
		event.InstallationID = truncateUTF8Bytes(event.InstallationID, 512)
		event.EventID = truncateUTF8Bytes(event.EventID, 512)
		event.ChannelID = truncateUTF8Bytes(event.ChannelID, 512)
		event.Kind = truncateUTF8Bytes(event.Kind, 64)
		event.AuthorID = truncateUTF8Bytes(event.AuthorID, 512)
		event.Timestamp = truncateUTF8Bytes(event.Timestamp, 128)
		event.ThreadTimestamp = truncateUTF8Bytes(event.ThreadTimestamp, 128)
		event.Untrusted = true
		metadataBytes := len(event.InstallationID) + len(event.EventID) + len(event.ChannelID) +
			len(event.Kind) + len(event.AuthorID) + len(event.Timestamp) + len(event.ThreadTimestamp)
		if used+metadataBytes >= MaxSlackEventBatchBytes {
			break
		}
		var ok bool
		event.Text, ok = wrapSlackUntrustedText(event.Text, MaxSlackEventBatchBytes-used-metadataBytes)
		if !ok {
			break
		}
		used += metadataBytes + len(event.Text)
		bounded = append(bounded, event)
	}
	return bounded
}

func wrapSlackUntrustedText(text string, maxBytes int) (string, bool) {
	minimum := len(slackUntrustedTextOpen) + len(`{"text":""}`) + len(slackUntrustedTextClose)
	if maxBytes < minimum {
		return "", false
	}
	for {
		payload, _ := json.Marshal(struct {
			Text string `json:"text"`
		}{Text: text})
		framed := slackUntrustedTextOpen + string(payload) + slackUntrustedTextClose
		if len(framed) <= maxBytes {
			return framed, true
		}
		overflow := len(framed) - maxBytes
		text = truncateUTF8Bytes(text, len(text)-overflow)
	}
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

// triggerNowWithTasksDelta is the internal variant of TriggerNow that
// additionally threads a beads change delta into the delivered PromptMeta so
// the loop prompt body can render {{ .Trigger.OnTasks.Changes.* }} (mitto-xkn).
// Currently used only by processTasksChange in loop_runner_tasks.go for
// onTasks fires; all other paths (manual "Run Now", onCompletion, delayed
// retries) pass a nil delta via the public TriggerNow.
func (r *LoopRunner) triggerNowWithTasksDelta(sessionID string, resetTimer bool, tasksDelta *config.TasksDelta) error {
	return r.triggerNowFull(sessionID, resetTimer, false, session.TriggerOnTasks, &LoopDispatchOptions{TasksDelta: tasksDelta})
}

// triggerNowFull is the unified internal entry point behind TriggerNow and its
// specialized variants. It additionally threads the boot-pulse flag
// (mitto-ystk): when isRunOnStart is true the delivered PromptMeta carries
// IsLoopRunOnStart=true so the prompt body can gate on
// {{ .Session.IsLoopRunOnStart }}. All existing behaviour (auto-resume of a
// stopped session, IsPrompting guard, workspace concurrency cap bypass because
// this dispatch is a forced/manual-equivalent) is preserved.
//
// firedBy names the trigger that won this dispatch (mitto-r6j.2); empty defers
// to the loop's primary EffectiveTrigger. opts bundles the optional per-fire
// dispatch context (onTasks delta, onSlack batch, onChild detail — mitto-qvlh);
// nil for every caller that carries none of those.
func (r *LoopRunner) triggerNowFull(sessionID string, resetTimer bool, isRunOnStart bool, firedBy session.LoopTrigger, opts *LoopDispatchOptions) error {
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

	// Classify manual vs. automatic BEFORE firedBy is defaulted to the loop's
	// primary EffectiveTrigger (mitto-brdo). Every triggerNowFull caller passes
	// forced=true to deliverPrompt below for cap-bypass/LoopKind purposes (an
	// "act now" dispatch, not a throttled scheduled one) — that behavior is
	// unchanged. But only a genuine manual "Run Now" (the public TriggerNow,
	// invoked from the REST handleRunLoopNow and MCP
	// mitto_conversation_run_loop_now handlers) reaches here with neither
	// isRunOnStart set nor an explicit firedBy: every automatic path
	// (onCompletion/onTasks/onSlack fires via TriggerNowFrom/
	// triggerNowWithTasksDelta/TriggerNowWithSlackEvents, onChild fires via
	// triggerNowFromChild, and the boot pulse) always supplies a non-empty
	// firedBy or isRunOnStart=true. isManual is
	// used ONLY to set PromptMeta.IsLoopForced (surfaced to the UI/provenance
	// as "Manual run") so an onTasks/onCompletion/onSlack/startup dispatch is
	// no longer mislabeled as a manual run.
	const forced = true
	isManual := forced && !isRunOnStart && firedBy == ""

	if firedBy == "" {
		firedBy = loop.EffectiveTrigger()
	}

	if r.logger != nil {
		r.logger.Info("Triggering immediate loop delivery",
			"session_id", sessionID,
			"session_name", meta.Name,
			"fired_by", string(firedBy),
			"is_manual", isManual,
			"slack_event_count", len(optsSlackEvents(opts)),
			"prompt_preview", truncatePrompt(loop.Prompt, 100))
	}

	// Deliver the prompt
	return r.deliverPrompt(bs, meta, loop, loopStore, resetTimer, forced, isRunOnStart, firedBy, isManual, opts)
}

// OnConversationIdle is invoked when a session's agent has stopped and the session
// is fully idle (no queued work). It runs two independent legs:
//
//  1. onCompletion (this session's own loop): see onConversationIdleCompletion.
//  2. onChild (mitto-987y.5): if this session is itself a child (has a
//     ParentSessionID), notify the parent's onChild loop leg via
//     OnChildEndResponse so a parent armed for onChild can fire. This runs
//     regardless of whether this session is archived — archiving stops THIS
//     session's own loop, but the parent's onChild arming is independent, and
//     fireOnChild separately guards on the PARENT's archived state.
//
// Order matters: onCompletion only arms a timer, while the onChild leg
// dispatches synchronously — running onCompletion first preserves the
// documented onChild > onCompletion precedence (see fireOnChild).
func (r *LoopRunner) OnConversationIdle(sessionID string) {
	if r.store == nil {
		return
	}

	meta, err := r.store.GetMetadata(sessionID)
	if err != nil {
		r.cancelCompletionTimer(sessionID)
		return
	}

	r.onConversationIdleCompletion(sessionID, meta)

	if meta.ParentSessionID != "" {
		r.OnChildEndResponse(sessionID)
	}
}

// onConversationIdleCompletion is the onCompletion leg of OnConversationIdle.
// For conversations configured with the onCompletion trigger it arms a
// one-shot timer that delivers the next run after the configured delay
// (clamped to the global minimum floor). For any other configuration
// (including an archived conversation — archiving stops the loop, mitto-efnb)
// it cancels a possibly-stale timer and returns.
func (r *LoopRunner) onConversationIdleCompletion(sessionID string, meta session.Metadata) {
	if meta.Archived {
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

	// Cancel any pending onTasks baseline-rebase timer and drop the sticky
	// re-fire flag — an archived session must not re-fire on quiescence
	// (mitto-cwg.1).
	r.tasksRebaseTimersMu.Lock()
	if existing, ok := r.tasksRebaseTimers[sessionID]; ok {
		existing.Stop()
		delete(r.tasksRebaseTimers, sessionID)
	}
	delete(r.tasksRefirePending, sessionID)
	r.tasksRebaseTimersMu.Unlock()

	// Cancel any pending pre-fire settle timer — an archived session must not
	// dispatch a settled fire (mitto-1uv).
	r.tasksSettleTimersMu.Lock()
	if existing, ok := r.tasksSettleTimers[sessionID]; ok {
		existing.Stop()
		delete(r.tasksSettleTimers, sessionID)
	}
	r.tasksSettleTimersMu.Unlock()

	// Drop any dispatch claim so a stopped loop cannot leave a session
	// permanently coalesced if it is later re-enabled (mitto-r6j.2).
	r.releaseDispatch(sessionID)

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
//   - triggerNowFull's internal IsPrompting() check (shared by TriggerNow and
//     TriggerNowFrom) rejects a racing call with ErrSessionBusy once
//     PromptWithMeta sets isPrompting synchronously before returning.
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

	// Deliver the first run immediately — no delay on first run. This is an
	// automatic bootstrap, not a user-initiated "Run Now", so it is fired via
	// TriggerNowFrom with the onCompletion trigger (not the bare TriggerNow)
	// so the delivered PromptMeta/Provenance correctly records
	// LoopTrigger=onCompletion and IsLoopForced=false instead of being
	// mislabeled "Manual run" in the UI (mitto-brdo).
	if err := r.TriggerNowFrom(sessionID, true, session.TriggerOnCompletion); err != nil {
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

// fireOnStartPulses walks every session with a loop configured for
// RunOnStart=true and fires each one exactly once shortly after Mitto boots
// (mitto-ystk). Called by pollLoop after the interactive-resume startup delay,
// so live WebSocket sessions have already reclaimed their ACP connections.
//
// Skipped when:
//   - the session store is not available;
//   - the session is archived;
//   - the loop is disabled or RunOnStart is not *true;
//   - a previous delivery falls within the anti-flap window
//     (runOnStartAntiFlapSeconds); this catches a fast restart of a healthy
//     loop and prevents a redundant re-fire immediately after the last run;
//   - the runner already fired the pulse in this process lifetime (guarded by
//     runOnStartFired).
//
// For onTasks loops the baseline is bootstrapped before firing so the delivered
// prompt has a sane {{ .Trigger.OnTasks.* }} rendering context on future
// beads-driven runs; the boot pulse itself does not carry a tasks delta.
//
// All deliveries go through triggerNowFull with isRunOnStart=true so the
// PromptMeta carries IsLoopRunOnStart=true (surfaced as the CEL
// Session.IsLoopRunOnStart variable, the Go-template
// {{ .Session.IsLoopRunOnStart }} accessor, and the @mitto:loop_run_on_start
// placeholder). Errors are logged but not propagated — the boot pulse is
// best-effort.
//
// runOnStartFired is set BEFORE the dispatch attempt (to close a race between
// concurrent ticks), but a transient contention error — ErrSessionBusy,
// ErrWorkspaceBusy, or ErrLoopDispatchCoalesced — rolls it back so the once-
// per-process guard does not permanently consume the pulse (mitto-bmct defect
// A): fireOnStartPulses runs on every RunOnce poll tick, not just once at
// boot (mitto-wyob), specifically so these three sentinels can self-heal on a
// later tick. Other synchronous and asynchronous delivery errors are routed
// through their bounded failure classifier, then re-arm the pulse while the
// loop remains enabled. A durably failing loop therefore retries on later
// ticks until its classifier auto-pauses it instead of going silently deaf.
func (r *LoopRunner) fireOnStartPulses() {
	if r.store == nil {
		return
	}

	sessions, err := r.store.List()
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("fireOnStartPulses: failed to list sessions", "error", err)
		}
		return
	}

	r.mu.Lock()
	antiFlap := r.runOnStartAntiFlapSeconds
	r.mu.Unlock()

	now := time.Now().UTC()

	for _, meta := range sessions {
		if meta.Archived {
			continue
		}

		loopStore := r.store.Loop(meta.SessionID)
		loop, err := loopStore.Get()
		if err != nil || loop == nil {
			continue
		}
		if !loop.Enabled || !loop.ShouldRunOnStart() {
			continue
		}

		// Anti-flap: a loop that ran seconds ago (across a fast restart) should
		// not immediately re-fire on boot. Since fireOnStartPulses now runs on
		// every RunOnce tick (mitto-wyob), not just once at boot, the session
		// must also be marked as fired here — otherwise suppression would only
		// defer the pulse to the tick after the anti-flap window elapses,
		// turning a one-time-suppression guard into a delayed re-fire.
		if antiFlap > 0 && loop.LastSentAt != nil {
			since := now.Sub(loop.LastSentAt.UTC())
			if since < time.Duration(antiFlap)*time.Second {
				r.runOnStartFiredMu.Lock()
				r.runOnStartFired[meta.SessionID] = true
				r.runOnStartFiredMu.Unlock()
				if r.logger != nil {
					r.logger.Debug("Boot pulse suppressed by anti-flap window",
						"session_id", meta.SessionID,
						"since_last_run", since,
						"anti_flap_seconds", antiFlap)
				}
				continue
			}
		}

		// Once-per-process guard.
		r.runOnStartFiredMu.Lock()
		alreadyFired := r.runOnStartFired[meta.SessionID]
		if !alreadyFired {
			r.runOnStartFired[meta.SessionID] = true
		}
		r.runOnStartFiredMu.Unlock()
		if alreadyFired {
			continue
		}

		// For onTasks loops, ensure the baseline exists before the boot pulse
		// so subsequent beads-driven runs have a well-defined delta anchor.
		if loop.IsOnTasks() {
			r.BootstrapTasksBaseline(meta.SessionID)
		}

		if r.logger != nil {
			r.logger.Info("Firing loop boot pulse",
				"session_id", meta.SessionID,
				"session_name", meta.Name,
				"trigger", string(loop.EffectiveTrigger()))
		}

		if err := r.triggerNowFull(meta.SessionID, true, true, "", nil); err != nil {
			isContention := errors.Is(err, ErrSessionBusy) || errors.Is(err, ErrWorkspaceBusy) || errors.Is(err, ErrLoopDispatchCoalesced)
			if isContention {
				// Transient contention, not a real delivery failure: roll back
				// the once-per-process guard so a later tick of the per-tick
				// fireOnStartPulses (mitto-wyob) can retry instead of the
				// pulse being permanently consumed (mitto-bmct defect A).
				r.runOnStartFiredMu.Lock()
				delete(r.runOnStartFired, meta.SessionID)
				r.runOnStartFiredMu.Unlock()
			} else if errors.Is(err, ErrPromptResolveFailed) {
				r.handlePromptResolveFailure(meta.SessionID, meta.Name, loop, loopStore, err)
				r.rearmRunOnStartAfterFailure(meta.SessionID, meta.Name, loopStore, err)
			} else {
				r.handleRunOnStartDeliveryFailure(meta.SessionID, meta.Name, loop, loopStore, err, true, true, loop.EffectiveTrigger())
			}
			if r.logger == nil {
				continue
			}
			if errors.Is(err, ErrSessionBusy) {
				r.logger.Debug("Boot pulse skipped, session busy",
					"session_id", meta.SessionID)
			} else if errors.Is(err, ErrWorkspaceBusy) {
				r.logger.Debug("Boot pulse skipped, workspace concurrency cap reached",
					"session_id", meta.SessionID)
			} else if errors.Is(err, ErrLoopDispatchCoalesced) {
				r.logger.Debug("Boot pulse coalesced, another trigger already in flight",
					"session_id", meta.SessionID)
			} else {
				r.logger.Warn("Boot pulse delivery failed",
					"session_id", meta.SessionID,
					"error", err)
			}
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
// When those hold it re-arms via onConversationIdleCompletion, which re-reads
// the config and arms the timer with the floor-clamped delay. The downstream
// fireOnCompletion auto-resumes a non-running session and enforces caps, so this
// also self-heals after a process restart (in-memory timers do not survive one).
//
// It deliberately calls the onCompletion leg directly rather than the full
// OnConversationIdle: this is a poll-driven repair, not a real end-of-turn, so
// it must NOT run the onChild leg (mitto-987y.5). Otherwise the first poll after
// a restart — when no in-memory timer exists for any session — would fire a
// spurious anyEndResponse at the parent of every onCompletion child.
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

	// Re-read config and arm the timer with the floor-clamped delay. Only the
	// onCompletion leg — see the doc comment on why the onChild leg is skipped.
	r.onConversationIdleCompletion(meta.SessionID, meta)
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
	if err := r.TriggerNowFrom(sessionID, true, session.TriggerOnCompletion); err != nil {
		// Route resolve failures (including an empty/draft prompt) through the
		// shared strike counter so onCompletion loops auto-pause with
		// StoppedReasonPromptUnresolved like the scheduled and onTasks paths.
		if errors.Is(err, ErrPromptResolveFailed) {
			r.handlePromptResolveFailure(sessionID, meta.Name, loop, loopStore, err)
			return
		}
		if r.logger == nil {
			return
		}
		if errors.Is(err, ErrSessionBusy) {
			r.logger.Debug("On-completion loop firing skipped, session busy",
				"session_id", sessionID)
		} else if errors.Is(err, ErrLoopDispatchCoalesced) {
			r.logger.Debug("On-completion loop firing coalesced, another trigger already in flight",
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

	// Run after delay to handle any prompts that were due. RunOnce fires any
	// pending once-per-boot pulses (mitto-ystk) for RunOnStart loops as its
	// first step, so the ordering guarantee (boot pulse before a due scheduled
	// fire can win first) is preserved without a separate call here.
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

	// Fire any pending once-per-boot pulses for RunOnStart loops (mitto-ystk).
	// This used to run only once, from pollLoop's startup sequence, before the
	// ticker began calling RunOnce on every tick. A loop created after that
	// single boot-time call (e.g. via the UI or an MCP call, post-boot) would
	// then never receive its startup pulse. fireOnStartPulses is idempotent
	// per session (guarded by runOnStartFired, including on the anti-flap
	// path), so calling it on every tick is safe and cheap (mitto-wyob).
	r.fireOnStartPulses()

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

	// Self-healing for a promptUnresolved auto-pause (mitto-a4yg): this is the
	// one stop reason that is provably self-verifiable — the pause exists
	// solely because the named prompt would not resolve, so re-resolving it
	// on every poll tick is a cheap, safe check. Attempt it before the
	// disabled-guard below so a transient PromptsCache reload gap does not
	// leave the loop paused forever once the registry recovers, matching the
	// existing ErrPromptTransientCompileRace carve-out for the same class of
	// transient failure. Update (not Set) is used because it explicitly
	// clears StoppedReason/StoppedAt — Set's sticky-stop guard (mitto-uun)
	// would otherwise re-preserve the stopped state.
	if !loop.Enabled && loop.StoppedReason == session.StoppedReasonPromptUnresolved &&
		loop.PromptName != "" && r.promptResolver != nil {
		if _, resolveErr := r.promptResolver(loop.PromptName, meta.WorkingDir); resolveErr == nil {
			enabled := true
			if updErr := loopStore.Update(session.LoopUpdate{Enabled: &enabled}); updErr != nil {
				if r.logger != nil {
					r.logger.Warn("Failed to self-heal promptUnresolved loop after prompt resolved again",
						"session_id", sessionID, "prompt_name", loop.PromptName, "error", updErr)
				}
			} else {
				if r.logger != nil {
					r.logger.Info("Self-healed promptUnresolved loop after prompt name resolved again",
						"session_id", sessionID, "prompt_name", loop.PromptName)
				}
				r.promptResolveFailuresMu.Lock()
				delete(r.promptResolveFailures, sessionID)
				r.promptResolveFailuresMu.Unlock()
				r.transientCompileRaceFailuresMu.Lock()
				delete(r.transientCompileRaceFailures, sessionID)
				r.transientCompileRaceFailuresMu.Unlock()
				if refreshed, gErr := loopStore.Get(); gErr == nil {
					loop = refreshed
				}
			}
		}
	}

	// Skip if disabled
	if !loop.Enabled {
		return 0, 0, 0
	}

	// Arm every listed trigger independently (mitto-r6j.2). A multi-trigger loop
	// keeps all of its sources live at once, so these legs are sequential and
	// non-exclusive: the event-driven ones are (re-)armed first — establishing
	// the onTasks > onCompletion > schedule precedence within a single tick —
	// and only then does the schedule leg run its due-check.

	// onCompletion is delivered by an in-memory timer, never by NextScheduledAt.
	// Bootstrap the very first run here so that a crash or restart before any
	// delivery still kicks off the loop. No-op if already run or in-flight.
	if loop.IsOnCompletion() {
		r.BootstrapOnCompletion(sessionID)
		// Self-healing safety net for an already-running loop whose end-of-turn
		// re-arm was missed (e.g. around an ACP resume or a heavy children-wait
		// turn that did not register as a clean idle transition). See mitto-5dn.
		r.recoverStalledOnCompletion(meta, loop)
	}

	// onTasks is fired from OnBeadsChanged. Bootstrap the baseline here so a
	// crash/restart before the baseline was ever captured does not cause a
	// spurious first fire the next time beads change (mitto-oja.2).
	if loop.IsOnTasks() {
		r.BootstrapTasksBaseline(sessionID)
	}

	// Without a schedule leg there is nothing left for the poll loop to deliver.
	if !loop.IsSchedule() {
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
			// mitto-hjx facet D: a ResumeSession failure caused by transient
			// shared-ACP-process saturation (cold-start MCP-init timeout, a
			// saturation bail, or a concurrent recycle race) must NOT count
			// toward the archive threshold below — a later resume attempt
			// succeeds once the shared process's saturation clears. This
			// mirrors the identical carve-out in session_manager.go's
			// resumeSessionWithConstraint for the ACPStartFailureCount
			// counter. Without it, a heavy-but-transient saturation burst
			// archives (and disables) an otherwise perfectly healthy loop.
			//
			// mitto-hjx (recurrence): the agent's OWN internal -32603 wedges
			// (IsAgentInternalDeadlineErr's "context deadline exceeded" and
			// IsAgentQueryClosedErr's "query closed before response received")
			// are equally transient and equally self-clearing, but were not
			// classified here, so a saturation storm producing these specific
			// shapes still archived a healthy loop.
			if mittoAcp.IsMCPInitTimeout(err) || errors.Is(err, acperrors.ErrSharedProcessSaturated) || errors.Is(err, acperrors.ErrProcessClosedConcurrently) || acperrors.IsAgentInternalDeadlineErr(err) || acperrors.IsAgentQueryClosedErr(err) {
				if r.logger != nil {
					r.logger.Warn("Resume hit transient shared-process saturation for loop prompt; not counting toward archive threshold (will retry)",
						"session_id", sessionID,
						"error", err)
				}
				return 0, 0, 1
			}

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
				// Persist the stopped reason so it survives even when the session
				// leaves the active view. Done unconditionally (even for a
				// non-archivable conversation, mitto-yvel.3) so the loop stops
				// retrying regardless of whether the archive itself proceeds.
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

				if !meta.IsArchivable() {
					// Skip archiving a non-archivable conversation (mitto-yvel.3):
					// a supervisor loop that goes quiet must not be reaped. The
					// loop is already stopped above, so no retry storm results.
					if r.logger != nil {
						r.logger.Warn("Skipping auto-archive after repeated ACP resume failures: conversation is non-archivable",
							"session_id", sessionID,
							"session_name", meta.Name,
							"consecutive_failures", failures)
					}
					if r.onLoopAutoStopped != nil {
						if final, gErr := loopStore.Get(); gErr == nil {
							r.onLoopAutoStopped(sessionID, final)
						}
					}
				} else {
					if r.logger != nil {
						r.logger.Warn("Archiving session after repeated ACP resume failures",
							"session_id", sessionID,
							"session_name", meta.Name,
							"consecutive_failures", failures)
					}

					// Note: the session is NOT running (resume failed), so no need to close it gracefully.

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
				}

				// Reset counter after handling (archived or skipped) so we don't
				// re-trigger this branch on every subsequent tick.
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

	// Deliver the prompt — normal scheduled runs always reset the timer. No
	// onTasks delta on the scheduled path (that path only fires on time; onTasks
	// fires go through triggerNowWithTasksDelta — mitto-xkn).
	if err := r.deliverPrompt(bs, meta, loop, loopStore, true, false, false, session.TriggerSchedule, false, nil); err != nil {
		if errors.Is(err, ErrWorkspaceBusy) {
			// A sibling loop in the same workspace is in flight. Skip this
			// session for this poll cycle — do not advance NextScheduledAt
			// and do not apply the failure backoff. The next poll (1 min)
			// will retry (mitto-61z).
			if r.logger != nil {
				r.logger.Debug("Skipping loop prompt - workspace concurrency cap reached",
					"session_id", sessionID,
					"session_name", meta.Name)
			}
			return 0, 1, 0
		}
		if errors.Is(err, ErrLoopDispatchCoalesced) {
			// Another trigger of this same multi-trigger loop already owns the
			// in-flight dispatch. The schedule fire is dropped, not queued: no
			// schedule advance, no failure backoff (mitto-r6j.2).
			if r.logger != nil {
				r.logger.Debug("Skipping loop prompt - dispatch coalesced with another trigger",
					"session_id", sessionID,
					"session_name", meta.Name)
			}
			return 0, 1, 0
		}
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
	r.transientCompileRaceFailuresMu.Lock()
	delete(r.transientCompileRaceFailures, sessionID)
	r.transientCompileRaceFailuresMu.Unlock()

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
	// mitto-8bg: a transient template fragment compile-race must not count as a
	// strike toward the auto-pause tripwire. The resolver wraps such misses with
	// ErrPromptTransientCompileRace; the failure will clear itself on the next
	// reload. We do NOT reset the promptResolveFailures counter here — a
	// subsequent genuine "not found" still trips the tripwire as before.
	//
	// mitto-uvsn: the carve-out above was unbounded — every classification
	// returned unconditionally, with no counter, no time bound, and no
	// escalation. A persistent prompt-cache hole (e.g. the resolver in
	// internal/web/server.go wraps ANY prompt-not-found as transient
	// whenever PromptsCache holds ANY unrelated compile error) made this
	// carve-out fire forever, silently stalling loops for hours. Bound it:
	// once a session accumulates MaxTransientCompileRaceFailures consecutive
	// classifications within transientCompileRaceWindow, treat the carve-out
	// as exhausted and fall through to the normal strike path below so the
	// loop eventually auto-pauses and surfaces the amber warning, same as
	// any other unresolved prompt.
	if errors.Is(err, ErrPromptTransientCompileRace) {
		r.transientCompileRaceFailuresMu.Lock()
		state := r.transientCompileRaceFailures[sessionID]
		now := time.Now()
		if state == nil || now.Sub(state.firstSeen) >= transientCompileRaceWindow {
			state = &transientRaceState{firstSeen: now}
			r.transientCompileRaceFailures[sessionID] = state
		}
		state.count++
		exhausted := state.count >= MaxTransientCompileRaceFailures
		if exhausted {
			delete(r.transientCompileRaceFailures, sessionID)
		}
		r.transientCompileRaceFailuresMu.Unlock()

		if !exhausted {
			if r.logger != nil {
				r.logger.Debug("Loop prompt transiently unresolved (template compile race); not counting as strike",
					"session_id", sessionID,
					"prompt_name", loop.PromptName,
					"consecutive_transient_failures", state.count,
					"error", err)
			}
			return
		}

		if r.logger != nil {
			r.logger.Warn("Loop prompt transient compile-race carve-out exhausted; escalating to strike path",
				"session_id", sessionID,
				"prompt_name", loop.PromptName,
				"consecutive_transient_failures", state.count,
				"window", transientCompileRaceWindow,
				"error", err)
		}
		// Fall through to the normal strike-counting path below.
	}

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

// releaseBeadClaim best-effort releases an automated-driver claim on the bead
// linked to sessionID (via session.Metadata.BeadsIssue) when a loop auto-stops
// for a non-benign reason (mitto-2efc). Without this, an auto-paused driver
// leaves the "in-flight" label plus claimed_by/claimed_at/claim_heartbeat_at
// metadata set forever, hiding the bead from "bd ready --exclude-label
// in-flight" indefinitely even though no driver is actually working it
// anymore. Best-effort and non-blocking: any failure (missing store, no
// linked bead, bd CLI error) is logged at WARN and swallowed — releasing the
// claim must never fail or delay the auto-stop it is called from. Callers
// must NOT call this for benign stops (pausedByUser, maxIterations,
// maxDuration) where the driver's work is simply complete/paused by choice,
// not because it got stuck.
func (r *LoopRunner) releaseBeadClaim(sessionID, sessionName string) {
	if r.store == nil {
		return
	}
	meta, err := r.store.GetMetadata(sessionID)
	if err != nil || meta.BeadsIssue == "" {
		return
	}
	client := r.beadsClientOrDefault()
	if client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tasksListTimeout)
	defer cancel()

	if labelErr := client.Label(ctx, meta.WorkingDir, beads.LabelParams{
		ID:     meta.BeadsIssue,
		Label:  "in-flight",
		Action: "remove",
	}); labelErr != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to release in-flight label on loop auto-stop",
				"session_id", sessionID,
				"session_name", sessionName,
				"beads_issue", meta.BeadsIssue,
				"error", labelErr)
		}
	}

	if updateErr := client.Update(ctx, meta.WorkingDir, beads.UpdateParams{
		ID:            meta.BeadsIssue,
		UnsetMetadata: []string{"claimed_by", "claimed_at", "claim_heartbeat_at"},
	}); updateErr != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to unset bead claim metadata on loop auto-stop",
				"session_id", sessionID,
				"session_name", sessionName,
				"beads_issue", meta.BeadsIssue,
				"error", updateErr)
		}
	}
}

// handleContextWindowFailure processes a context-window (HTTP 413 / augmentTooLarge)
// delivery failure for a scheduled loop. It bumps the per-session counter; when the
// counter reaches MaxLoopContextWindowFailures the loop is auto-paused with
// StoppedReasonContextWindowExceeded and the counter is cleared. Returns true when
// the loop was auto-paused (caller must skip normal backoff); returns false when the
// failure was recorded but the counter is still below threshold (caller should
// proceed with the regular schedule backoff and log the specific cause).
// The onLoopAutoStopped callback is invoked when the auto-pause succeeds so clients
// receive a broadcast update, mirroring the max-iterations auto-stop.
func (r *LoopRunner) handleContextWindowFailure(sessionID, sessionName string, loopStore *session.LoopStore) bool {
	r.contextWindowFailuresMu.Lock()
	r.contextWindowFailures[sessionID]++
	failures := r.contextWindowFailures[sessionID]
	r.contextWindowFailuresMu.Unlock()

	if failures < MaxLoopContextWindowFailures {
		if r.logger != nil {
			r.logger.Warn("Loop prompt failed with context-window exceeded; will auto-pause after repeated failures",
				"session_id", sessionID,
				"session_name", sessionName,
				"consecutive_failures", failures,
				"max_failures", MaxLoopContextWindowFailures)
		}
		return false
	}

	if stopErr := loopStore.MarkStopped(session.StoppedReasonContextWindowExceeded); stopErr != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to auto-pause loop after repeated context-window failures",
				"session_id", sessionID,
				"session_name", sessionName,
				"error", stopErr)
		}
		// Do not clear the counter so a subsequent retry can try again.
		return false
	}
	r.releaseBeadClaim(sessionID, sessionName)
	if r.logger != nil {
		r.logger.Warn("Auto-paused loop conversation after repeated context-window failures",
			"session_id", sessionID,
			"session_name", sessionName,
			"consecutive_failures", failures,
			"max_failures", MaxLoopContextWindowFailures)
	}
	if r.onLoopAutoStopped != nil {
		if final, gErr := loopStore.Get(); gErr == nil {
			r.onLoopAutoStopped(sessionID, final)
		}
	}
	r.contextWindowFailuresMu.Lock()
	delete(r.contextWindowFailures, sessionID)
	r.contextWindowFailuresMu.Unlock()
	return true
}

// handleDeliveryFailure processes a failed loop-prompt delivery. Called from the
// OnComplete callback set by deliverPrompt when PromptWithMeta returned an error.
// The logic is:
//   - An HTTP 413 / augmentTooLarge failure bumps the per-session context-window
//     counter and auto-pauses the loop after MaxLoopContextWindowFailures
//     consecutive hits (mitto-7jn). This runs regardless of trigger type —
//     onCompletion loops need the same safety net (mitto-4he), and the counter
//     is trigger-agnostic.
//   - Every other failure bumps the trigger-agnostic deliveryFailures counter
//     and is compared against a MaxLoopDeliveryFailures ceiling REGARDLESS of
//     trigger type or forced-ness (mitto-bmct): a forced dispatch (manual
//     "Run Now", or the runOnStart boot pulse) or an event-driven fire
//     (onCompletion/onTasks/onChild) that fails deterministically must still
//     auto-pause with StoppedReasonDeliveryFailures after MaxLoopDeliveryFailures
//     consecutive hits, exactly like a scheduled run — otherwise a durable
//     failure on one of those paths is a silent, unbounded drop with the loop
//     still reporting Enabled and no StoppedReason (observed: a boot-pulse
//     onTasks loop went deaf for 5 days). deliveryFailures is cleared on any
//     successful delivery regardless of trigger (see deliverPrompt's
//     OnComplete), so incrementing it unconditionally on failure is safe and
//     symmetric.
//   - The schedule *backoff* (deferring NextScheduledAt via
//     loopScheduleBackoff/DeferNextSchedule) uses a SEPARATE, schedule-scoped
//     counter (scheduleBackoffFailures) and remains gated to schedule-initiated
//     runs with resetTimer=true and forced=false, so a transient transport
//     failure (e.g. -32603) does not re-fire the same prompt on every poll tick
//     (mitto-qal.2) — event-driven fires (onCompletion/onTasks) and manual
//     "keep schedule" runs (resetTimer=false) or forced one-shots must not push
//     out the regular schedule. The gate is on firedBy — the trigger that
//     actually won this dispatch — so that on a multi-trigger loop a failed
//     onCompletion run does not consume the schedule leg's own breaker
//     (mitto-r6j.2); using a separate counter from the trigger-agnostic ceiling
//     keeps that invariant intact even though both ceilings are compared
//     against the same MaxLoopDeliveryFailures constant. Below the ceiling, a
//     forced/event-driven failure is still logged (with the trigger-agnostic
//     failure count) but the schedule itself is left untouched.
func (r *LoopRunner) handleDeliveryFailure(sessionID, sessionName string, loop *session.LoopPrompt, loopStore *session.LoopStore, err error, resetTimer, forced bool, firedBy session.LoopTrigger) {
	if mittoAcp.IsContextTooLargeError(err) {
		if r.handleContextWindowFailure(sessionID, sessionName, loopStore) {
			if r.onLoopUpdated != nil {
				if updated, gErr := loopStore.Get(); gErr == nil && updated != nil {
					r.onLoopUpdated(sessionID, updated)
				}
			}
			return
		}
		// Under threshold — fall through to the normal schedule backoff so the
		// loop keeps ticking (with backoff) until the auto-pause threshold is hit.
	}

	// Classify the failure so the operator can distinguish a transient upstream
	// provider outage (Augment/Auggie backend 5xx or connect-timeout brownout,
	// data.apiStatus:"unavailable") from a generic delivery failure or an
	// auth/config error. This is purely a logging/surfacing signal — the counter
	// and backoff behavior below is identical either way, so the already-correct
	// exponential backoff is unchanged (mitto-bfu).
	failureClass := "generic"
	if mittoAcp.IsUpstreamUnavailableError(err) {
		failureClass = "upstream_provider_unavailable"
	}

	// Trigger-agnostic ceiling (mitto-bmct): count this failure and compare
	// against MaxLoopDeliveryFailures regardless of trigger/forced-ness.
	r.deliveryFailuresMu.Lock()
	r.deliveryFailures[sessionID]++
	failures := r.deliveryFailures[sessionID]
	r.deliveryFailuresMu.Unlock()

	if failures >= MaxLoopDeliveryFailures {
		if stopErr := loopStore.MarkStopped(session.StoppedReasonDeliveryFailures); stopErr != nil {
			if r.logger != nil {
				r.logger.Warn("Failed to auto-pause loop after repeated delivery failures",
					"session_id", sessionID,
					"session_name", sessionName,
					"consecutive_failures", failures,
					"error", stopErr)
			}
			// Do not clear the counter so a subsequent retry can try again.
			return
		}
		r.releaseBeadClaim(sessionID, sessionName)
		if r.logger != nil {
			r.logger.Warn("Auto-paused loop conversation after repeated delivery failures",
				"session_id", sessionID,
				"session_name", sessionName,
				"consecutive_failures", failures,
				"max_failures", MaxLoopDeliveryFailures,
				"forced", forced,
				"fired_by", firedBy,
				"failure_class", failureClass,
				"error", err)
		}
		if r.onLoopAutoStopped != nil {
			if final, gErr := loopStore.Get(); gErr == nil {
				r.onLoopAutoStopped(sessionID, final)
			}
		}
		r.deliveryFailuresMu.Lock()
		delete(r.deliveryFailures, sessionID)
		r.deliveryFailuresMu.Unlock()
		r.scheduleBackoffFailuresMu.Lock()
		delete(r.scheduleBackoffFailures, sessionID)
		r.scheduleBackoffFailuresMu.Unlock()
		return
	}

	// Below the ceiling: only a schedule-initiated, resetTimer=true, non-forced
	// run defers NextScheduledAt, using its own dedicated schedule-scoped
	// counter (mitto-r6j.2) — distinct from the trigger-agnostic ceiling above.
	if resetTimer && !forced && firedBy == session.TriggerSchedule {
		r.scheduleBackoffFailuresMu.Lock()
		r.scheduleBackoffFailures[sessionID]++
		scheduleFailures := r.scheduleBackoffFailures[sessionID]
		r.scheduleBackoffFailuresMu.Unlock()

		delay := loopScheduleBackoff(scheduleFailures)
		if deferErr := loopStore.DeferNextSchedule(delay); deferErr != nil {
			if r.logger != nil {
				r.logger.Warn("Loop prompt failed, backoff could not be applied",
					"session_id", sessionID,
					"session_name", sessionName,
					"consecutive_failures", scheduleFailures,
					"failure_class", failureClass,
					"error", deferErr)
			}
		} else {
			if r.logger != nil {
				backoffMsg := "Loop prompt failed, backing off next run"
				if failureClass == "upstream_provider_unavailable" {
					backoffMsg = "Loop delivery hit a transient upstream provider outage; backing off next run (auto-retrying, not a broken loop)"
				}
				r.logger.Warn(backoffMsg,
					"session_id", sessionID,
					"session_name", sessionName,
					"consecutive_failures", scheduleFailures,
					"max_failures", MaxLoopDeliveryFailures,
					"backoff", delay,
					"failure_class", failureClass,
					"error", err)
			}
			if r.onLoopUpdated != nil {
				if updated, gErr := loopStore.Get(); gErr == nil && updated != nil {
					r.onLoopUpdated(sessionID, updated)
				}
			}
		}
		return
	}

	// Forced/event-driven fires must not push out the regular schedule, but
	// still get the same trigger-agnostic failure accounting logged.
	if r.logger != nil {
		notAdvancedMsg := "Loop prompt failed, schedule not advanced"
		if failureClass == "upstream_provider_unavailable" {
			notAdvancedMsg = "Loop delivery hit a transient upstream provider outage; schedule not advanced (auto-retrying, not a broken loop)"
		}
		r.logger.Warn(notAdvancedMsg,
			"session_id", sessionID,
			"session_name", sessionName,
			"consecutive_failures", failures,
			"max_failures", MaxLoopDeliveryFailures,
			"forced", forced,
			"fired_by", firedBy,
			"failure_class", failureClass,
			"error", err)
	}
}

// handleRunOnStartDeliveryFailure applies the trigger-agnostic delivery-failure
// ceiling, then re-arms a consumed boot-pulse guard while the loop remains
// enabled. It is shared by synchronous dispatch failures and asynchronous ACP
// turn failures arriving through PromptMeta.OnComplete.
func (r *LoopRunner) handleRunOnStartDeliveryFailure(sessionID, sessionName string, loop *session.LoopPrompt, loopStore *session.LoopStore, err error, resetTimer, forced bool, firedBy session.LoopTrigger) {
	r.handleDeliveryFailure(sessionID, sessionName, loop, loopStore, err, resetTimer, forced, firedBy)
	r.rearmRunOnStartAfterFailure(sessionID, sessionName, loopStore, err)
}

func (r *LoopRunner) rearmRunOnStartAfterFailure(sessionID, sessionName string, loopStore *session.LoopStore, err error) {
	updated, getErr := loopStore.Get()
	if getErr != nil || updated == nil || !updated.Enabled {
		return
	}

	r.runOnStartFiredMu.Lock()
	wasFired := r.runOnStartFired[sessionID]
	delete(r.runOnStartFired, sessionID)
	r.runOnStartFiredMu.Unlock()

	if wasFired && r.logger != nil {
		r.logger.Warn("Re-armed runOnStart boot pulse after delivery failure",
			"session_id", sessionID,
			"session_name", sessionName,
			"error", err)
	}
}

// deliverPrompt sends the loop prompt to the session.
// resetTimer controls whether RecordSent() is called when the prompt completes:
//   - true  → schedule advances from now (normal behaviour)
//   - false → schedule is left untouched (manual "run now" without resetting the timer)
//
// sessionMeta carries the session's workspace/ACP-server pair used to enforce
// the per-workspace loop-dispatch concurrency cap (mitto-61z). When forced is
// true (any triggerNowFull-backed "act now" dispatch — manual "Run Now" as
// well as onCompletion/onTasks/onChild/onSlack fires and the boot pulse) the
// cap is bypassed and no slot is reserved; this also selects LoopKindForced
// below. Note that forced no longer implies "genuine manual run" — see
// isManual.
//
// isRunOnStart is true only for the boot pulse (mitto-ystk) fired once by
// fireOnStartPulses shortly after Mitto boots. It flags the delivered PromptMeta
// so the prompt body can gate on {{ .Session.IsLoopRunOnStart }} (and the
// @mitto:loop_run_on_start placeholder).
//
// firedBy names the trigger that won this dispatch (mitto-r6j.2). It is recorded
// on the delivered PromptMeta and claims the per-session dispatch slot: while a
// run is in flight, a fire from any other trigger of the same loop is dropped
// with ErrLoopDispatchCoalesced rather than queued. An empty firedBy defers to
// the loop's primary EffectiveTrigger.
//
// isManual is true only for a genuine manual "Run Now" (mitto-brdo): the
// caller (triggerNowFull) computes it as forced && !isRunOnStart && the
// caller-supplied firedBy was empty, evaluated before firedBy is defaulted to
// the loop's EffectiveTrigger. It is used exclusively to set
// PromptMeta.IsLoopForced, which is surfaced to the UI/provenance as "Manual
// run" — unlike forced, it must NOT be true for onCompletion/onTasks/onChild/
// onSlack fires or the boot pulse, all of which reach deliverPrompt with
// forced=true for cap-bypass purposes but are not manual.
//
// opts bundles the optional per-fire dispatch context (mitto-qvlh): a nil
// *LoopDispatchOptions is the common case (schedule, onCompletion, manual
// "Run Now", boot pulse). opts.TasksDelta is non-nil only for onTasks fires
// (via triggerNowWithTasksDelta); it is threaded into PromptMeta.Trigger so
// the loop prompt body can render {{ .Trigger.OnTasks.Changes.* }} (mitto-xkn).
// opts.SlackEvents is non-empty only for onSlack; it is threaded into
// PromptMeta.Trigger so the prompt can render {{ .Trigger.OnSlack.Events }},
// with the first event also exposed at the deprecated {{ .Trigger.Slack }}
// alias. opts.OnChild is non-nil only for onChild fires; it is threaded into
// PromptMeta.Trigger so the prompt can render {{ .Trigger.OnChild.* }}.
func (r *LoopRunner) deliverPrompt(bs *BackgroundSession, sessionMeta session.Metadata, loop *session.LoopPrompt, loopStore *session.LoopStore, resetTimer bool, forced bool, isRunOnStart bool, firedBy session.LoopTrigger, isManual bool, opts *LoopDispatchOptions) error {
	sessionID := bs.GetSessionID()
	sessionName := sessionMeta.Name
	tasksDelta := optsTasksDelta(opts)
	slackEvents := optsSlackEvents(opts)
	onChildOpt := optsOnChild(opts)
	if firedBy == "" {
		firedBy = loop.EffectiveTrigger()
	}

	// Resolve prompt text from name if needed. EffectivePromptBody normalises
	// the legacy "(pending)" draft placeholder to "" so it is never delivered
	// as a literal prompt body.
	promptText := loop.EffectivePromptBody()
	if loop.PromptName != "" && r.promptResolver != nil {
		resolved, err := r.promptResolver(loop.PromptName, sessionMeta.WorkingDir)
		if err != nil {
			// Wrap both ErrPromptResolveFailed and the underlying resolver error
			// via %w so errors.Is preserves resolver-emitted sentinels (notably
			// ErrPromptTransientCompileRace, mitto-8bg) through this delivery layer.
			return fmt.Errorf("%w: %q: %w", ErrPromptResolveFailed, loop.PromptName, err)
		}
		promptText = resolved
		if r.logger != nil {
			r.logger.Debug("Resolved loop prompt name to text",
				"session_id", sessionID,
				"prompt_name", loop.PromptName,
				"prompt_preview", truncatePrompt(promptText, 100))
		}
	}

	// Never dispatch an empty prompt. A loop left in the draft state (no body,
	// no prompt name) or one whose named prompt resolves to nothing has nothing
	// to say; delivering it burns a turn and, if the agent errors, re-fires
	// forever. Route it through the shared resolve-failure path so the loop
	// auto-pauses with StoppedReasonPromptUnresolved and the sidebar shows the
	// amber warning.
	if strings.TrimSpace(promptText) == "" {
		return fmt.Errorf("%w: %q: resolved prompt is empty", ErrPromptResolveFailed, loop.PromptName)
	}

	// Coalescing claim (mitto-r6j.2). A multi-trigger loop arms every listed
	// trigger independently, so two of them can reach this point in the same
	// window. The first to claim wins; the loser is dropped, never queued. The
	// claim is taken after prompt resolution so a resolve failure does not hold
	// the slot, and before the workspace reservation so a coalesced fire does
	// not consume workspace capacity. It applies to forced runs too: unlike the
	// workspace cap, this guards a single conversation against a double turn.
	if winner, ok := r.claimDispatch(sessionID, firedBy); !ok {
		if r.logger != nil {
			r.logger.Debug("Loop dispatch coalesced - another trigger is already in flight",
				"session_id", sessionID,
				"session_name", sessionName,
				"winner", string(winner),
				"dropped", string(firedBy))
		}
		return ErrLoopDispatchCoalesced
	}

	if r.logger != nil {
		r.logger.Debug("Delivering loop prompt",
			"session_id", sessionID,
			"session_name", sessionName,
			"reset_timer", resetTimer,
			"fired_by", string(firedBy),
			"is_manual", isManual,
			"slack_event_count", len(slackEvents),
			"prompt_preview", truncatePrompt(promptText, 100))
	}

	// Broadcast the current loop state before dispatch (mitto-uun). This
	// invalidates any stale "Stopped" pill left over from a previous auto-stop
	// that a client may still be caching from a prior session — the fresh
	// Enabled/StoppedReason on the loop_updated event overrides the pill.
	// Re-read from disk so the broadcast reflects any concurrent writes rather
	// than the caller's in-memory copy.
	if r.onLoopUpdated != nil {
		if fresh, err := loopStore.Get(); err == nil && fresh != nil {
			r.onLoopUpdated(sessionID, fresh)
		}
	}

	// Per-workspace loop-dispatch concurrency guard (mitto-61z). Forced ("Run
	// Now") deliveries always bypass the cap. For scheduled deliveries we
	// reserve a slot before PromptWithMeta and release it once the prompt
	// finishes (OnComplete) or fails to dispatch synchronously.
	wsKey := workspaceKey(sessionMeta.WorkingDir, sessionMeta.ACPServer)
	slotReserved := false
	if !forced {
		if !r.tryReserveWorkspaceSlot(wsKey) {
			if r.logger != nil {
				r.logger.Debug("Skipping loop prompt - workspace concurrency cap reached",
					"session_id", sessionID,
					"session_name", sessionName,
					"working_dir", sessionMeta.WorkingDir,
					"acp_server", sessionMeta.ACPServer)
			}
			r.releaseDispatch(sessionID)
			return ErrWorkspaceBusy
		}
		slotReserved = true
	}

	// releaseOnce ensures the workspace slot and the dispatch claim are released
	// at most once, whether via OnComplete or the synchronous PromptWithMeta
	// error path below.
	var releaseOnce sync.Once
	releaseSlot := func() {
		releaseOnce.Do(func() {
			if slotReserved {
				r.releaseWorkspaceSlot(wsKey)
			}
			r.releaseDispatch(sessionID)
		})
	}

	// Use OnComplete callback to defer RecordSent until the prompt actually finishes.
	// PromptWithMeta is async — it returns nil immediately. Without OnComplete,
	// RecordSent would advance the schedule even if the prompt later fails
	// (e.g., ACP process crash).
	loopKind := LoopKindScheduled
	if forced {
		loopKind = LoopKindForced
	}
	// Trigger context is non-nil only for a computed onTasks delta, a bounded
	// onSlack batch, or an onChild fire (mitto-qvlh). All other paths pass all
	// three as nil/empty.
	triggerCtx := buildPromptTriggerContext(tasksDelta, slackEvents, onChildOpt)
	meta := PromptMeta{
		SenderID:         "loop-runner",
		PromptID:         "",              // No client to confirm delivery to
		PromptName:       loop.PromptName, // Pass prompt name so UI can render a badge instead of full text
		Arguments:        loop.Arguments,  // User-supplied values for Go-template .Args placeholders in the resolved text
		IsLoopForced:     isManual,
		IsLoopRunOnStart: isRunOnStart,
		LoopKind:         loopKind,
		IterationNumber:  loop.IterationCount,
		MaxIterations:    loop.MaxIterations,
		FreshContext:     loop.FreshContext,
		Trigger:          triggerCtx,
		LoopTrigger:      firedBy,
		OnComplete: func(err error) {
			// Always release the workspace slot and the dispatch claim when the
			// prompt terminates, regardless of success or failure (mitto-61z,
			// mitto-r6j.2).
			defer releaseSlot()
			if err != nil {
				if isRunOnStart {
					r.handleRunOnStartDeliveryFailure(sessionID, sessionName, loop, loopStore, err, resetTimer, forced, firedBy)
				} else {
					r.handleDeliveryFailure(sessionID, sessionName, loop, loopStore, err, resetTimer, forced, firedBy)
				}
				return
			}

			// Successful delivery — clear any accumulated scheduled-delivery backoff.
			r.scheduleBackoffFailuresMu.Lock()
			delete(r.scheduleBackoffFailures, sessionID)
			r.scheduleBackoffFailuresMu.Unlock()

			// Also clear the trigger-agnostic delivery-failure ceiling counter on
			// any successful delivery, regardless of trigger (mitto-bmct).
			r.deliveryFailuresMu.Lock()
			delete(r.deliveryFailures, sessionID)
			r.deliveryFailuresMu.Unlock()

			// Also clear context-window failure counter on any successful delivery (mitto-7jn).
			r.contextWindowFailuresMu.Lock()
			delete(r.contextWindowFailures, sessionID)
			r.contextWindowFailuresMu.Unlock()

			if !resetTimer {
				// Manual run with "keep schedule" — leave NextScheduledAt unchanged.
				if r.logger != nil {
					r.logger.Debug("Loop prompt completed, timer not reset (manual run)",
						"session_id", sessionID,
						"session_name", sessionName)
				}
				return
			}

			// Prompt completed successfully — now update the schedule.
			// ErrRecordSentOnStoppedLoop (mitto-uun) is a soft sentinel: the write
			// still succeeded, we just want a WARN emitted, so classify it and
			// fall through to the post-success schedule/cap logic.
			recordErr := loopStore.RecordSent()
			if recordErr != nil && !errors.Is(recordErr, session.ErrRecordSentOnStoppedLoop) {
				logLoopRecordSentFailure(r.logger, sessionID, recordErr)
			} else {
				if recordErr != nil {
					logLoopRecordSentFailure(r.logger, sessionID, recordErr)
				}
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
							} else if effective == config.GlobalMaxLoopIterations {
								// Hit the hardcoded absolute backstop — worth a WARN.
								r.logger.Warn("Loop conversation reached hardcoded iteration safeguard, auto-stopping",
									"session_id", sessionID,
									"iteration_count", updated.IterationCount,
									"effective_cap", effective,
									"config_cap", cfgCap,
									"backstop", config.GlobalMaxLoopIterations)
							} else {
								// Hit the config-level default cap — normal cap-hit, log at INFO.
								r.logger.Info("Loop conversation reached configured iteration cap, auto-stopping",
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
		// Dispatch failed synchronously — OnComplete will not fire, so we
		// must release the workspace slot here (mitto-61z).
		releaseSlot()
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

		// Skip conversations marked non-archivable (mitto-yvel.3) — a supervisor
		// loop that goes quiet must not be reaped.
		if !meta.IsArchivable() {
			if r.logger != nil {
				r.logger.Debug("Skipping auto-archive: conversation is non-archivable",
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
