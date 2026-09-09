package conversation

// Prompt dispatch cluster for BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// maxArgValueLen is the maximum number of runes recorded for a single argument value.
// Values longer than this are truncated and suffixed with "…".
const maxArgValueLen = 80

// sensitiveArgNamePatterns contains lowercase substrings that flag an argument name as sensitive.
var sensitiveArgNamePatterns = []string{
	"secret", "password", "passwd", "token", "api_key", "apikey",
	"private_key", "credentials", "access_key", "auth_key",
}

// isSensitiveArgName returns true when the argument name suggests it holds a secret.
func isSensitiveArgName(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range sensitiveArgNamePatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// redactArgValue returns the safe-to-record form of an argument value:
// sensitive names are replaced with "***"; non-sensitive values are
// truncated to maxArgValueLen runes (with "…" suffix when truncated).
func redactArgValue(name, value string) string {
	if isSensitiveArgName(name) {
		return "***"
	}
	runes := []rune(value)
	if len(runes) > maxArgValueLen {
		return string(runes[:maxArgValueLen]) + "…"
	}
	return value
}

// persistableArguments returns a copy of args with any sensitive-named keys
// (per isSensitiveArgName) omitted entirely, for exact-replay persistence in
// the user_prompt event and broadcast. Unlike redactArgValue/buildArgumentMetadata,
// surviving values are stored raw and untruncated — the persisted map must be
// exactly replayable or the key must be absent (never a "***" placeholder or a
// truncated value). Returns nil when args is empty or nothing survives
// filtering, so callers can rely on it for both the recorder (omitempty) and
// the observer notification.
func persistableArguments(args map[string]string) map[string]string {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]string, len(args))
	for name, value := range args {
		if isSensitiveArgName(name) {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildArgumentMetadata derives the sorted argument_names list and the ordered
// arguments bag ([]map[string]any with "name"/"value" keys) from the raw args map.
// Values are processed through redactArgValue before inclusion.
// The two slices share the same sort order so index N in names == index N in arguments.
func buildArgumentMetadata(args map[string]string) (names []string, arguments []map[string]any) {
	names = make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)

	arguments = make([]map[string]any, len(names))
	for i, name := range names {
		arguments[i] = map[string]any{
			"name":  name,
			"value": redactArgValue(name, args[name]),
		}
	}
	return names, arguments
}

// buildPromptWithHistory prepends stored conversation history to the prompt for resumed sessions.
func (bs *BackgroundSession) buildPromptWithHistory(message string) string {
	if bs.store == nil {
		return message
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read events for history injection", "error", err)
		}
		return message
	}

	// Build conversation history (limit to last 5 turns to avoid token limits)
	history := session.BuildConversationHistory(events, 5)
	if history == "" {
		return message
	}

	if bs.logger != nil {
		bs.logger.Debug("Injecting conversation history into resumed session",
			"history_length", len(history))
	}

	return history + message
}

// SetPromptResolver sets the function used to resolve named workspace prompts to their full text.
// This is called by the server setup code (same resolver used by LoopRunner).
func (bs *BackgroundSession) SetPromptResolver(resolver PromptResolver) {
	bs.promptResolver = resolver
}

// SetPromptFragmentsResolver sets the workspace-scoped fragment resolver used
// by prompt rendering.
func (bs *BackgroundSession) SetPromptFragmentsResolver(resolver PromptFragmentsResolver) {
	bs.promptFragmentsResolver = resolver
}

// LoopKind classifies how a loop prompt was triggered so the dispatch path can
// distinguish a normal scheduled/onCompletion delivery from a manual "run now" without
// matching the magic SenderID string. LoopKindNone means the prompt is not a
// loop run (user/other sender).
type LoopKind int

const (
	LoopKindNone      LoopKind = iota // not a loop run
	LoopKindScheduled                 // normal scheduled / onCompletion delivery
	LoopKindForced                    // manual "run now"
)

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID     string          // Unique identifier of the sending client (for broadcast deduplication)
	PromptID     string          // Client-generated prompt ID (for delivery confirmation)
	PromptName   string          // Name of workspace prompt (resolved to full text before ACP; empty for ad-hoc prompts)
	ImageIDs     []string        // IDs of images attached to the prompt
	FileIDs      []string        // IDs of files attached to the prompt
	OnComplete   func(err error) // Called when the async prompt goroutine finishes (nil = success)
	IsLoopForced bool            // True when this loop prompt was triggered manually via "run now"
	// IsLoopRunOnStart is true when this loop prompt was fired by the boot-pulse
	// (mitto-ystk). Mirrors ProcessorInput.IsLoopRunOnStart, the CEL
	// Session.IsLoopRunOnStart variable, and the @mitto:loop_run_on_start
	// placeholder. Set only on the initial startup pulse; false on all
	// subsequent scheduled/onCompletion/onTasks/forced deliveries.
	IsLoopRunOnStart bool
	// QueueOrigin carries session.QueueOrigin* for queue dispatches: "agent" for
	// cross-session/MCP sends (fail-closed on template errors), "user"/empty for
	// human-typed messages that were queued (fail-open, delivered verbatim);
	// empty for non-queue dispatches.
	QueueOrigin string
	// LoopKind classifies a loop run (none/scheduled/forced). Set by the
	// LoopRunner. Drives the Iteration.IsUninterrupted continuation signal.
	LoopKind LoopKind
	// LoopTrigger names the trigger that won this dispatch for a multi-trigger
	// loop (mitto-r6j.2): schedule, onCompletion, onTasks, onChild, or onSlack. Set by the
	// LoopRunner; empty for non-loop prompts.
	LoopTrigger session.LoopTrigger
	// IterationNumber is the 0-based index of the current loop run (loop.IterationCount
	// at dispatch). Zero for non-loop prompts. Feeds the {{ .Iteration.* }} template namespace.
	IterationNumber int
	// MaxIterations is the configured maximum number of loop runs (0 = unlimited).
	MaxIterations int
	// IterationUninterrupted feeds {{ .Iteration.IsUninterrupted }}: true only on a
	// scheduled, non-forced, non-FreshContext loop run that directly follows another
	// such run with no interruption. Computed in PromptWithMeta from the session-scoped
	// continuation marker (peeked before body render, advanced at the dispatch commit).
	IterationUninterrupted bool
	FreshContext           bool // True to suppress history injection and use a new ACP session for this prompt
	// Arguments, when non-empty, provides values for Go-template .Args placeholders
	// in the resolved prompt text before persistence and broadcast.
	// Only set for named/scenario prompts; ad-hoc messages leave this nil so that
	// pasted shell/code containing template-like text is never corrupted.
	Arguments map[string]string
	// PreferredModels is an ordered list of references to global model profiles
	// (Settings → Models), by profile name or capability tag. The first entry that
	// resolves to an available model wins; absent/empty uses the session's baseline
	// model. When empty and PromptName is set, the list is resolved from the prompt
	// definition via preferredModelsResolver inside PromptWithMeta.
	PreferredModels []config.PromptPreferredModel
	// Meta is an optional generic metadata bag attached to the persisted user-prompt
	// event. Same sensitivity rules as session.RecordOption apply: no full prompt text
	// or raw secrets. Bounded (≤80 chars), name-redacted argument values ARE recorded
	// (see buildArgumentMetadata). When non-empty, the bag is forwarded to
	// EventMetaObserver.OnEventMeta so it can flow through to the WebSocket payload
	// without per-field wiring.
	Meta map[string]any
	// Trigger carries structured trigger-source data for this dispatch. Non-nil
	// only when the loop runner fires an onTasks trigger and wants the change
	// delta exposed to the prompt body via {{ .Trigger.OnTasks.* }}. Nil for
	// scheduled, onCompletion, manual "Run Now", and non-loop dispatches — no
	// allocations in the hot path. See prompt_dispatcher.go for the copy into
	// ProcessorInput.TriggerOnTasksChanges (mitto-xkn).
	Trigger *PromptTriggerContext
	// modelPreferenceResolved is set after synchronous pre-render selection so
	// ModelName/ModelTags describe the actual result and the async prompt path
	// does not repeat the switch. FreshContext selects after session replacement.
	modelPreferenceResolved bool
	// modelContext covers the owning turn from preparation through the ACP
	// prompt. Model switches must finish within this context, never outlive it.
	modelContext context.Context
}

// promptTurn owns the reservation from preparation through completion. The
// cleanup lock serializes cancellation with completion/retry RPCs; promptMu is
// only used for short state checks and is never held across those RPCs.
type promptTurn struct {
	ctx             context.Context
	cancel          context.CancelFunc
	preparationDone chan struct{}
	preparationOnce sync.Once
	cleanupMu       sync.Mutex
	stopRequested   bool // guarded by BackgroundSession.promptMu
}

func (t *promptTurn) finishPreparation() {
	t.preparationOnce.Do(func() { close(t.preparationDone) })
}

// promptTurnDeps gives stateless dispatch helpers the owning context rather
// than allowing an old goroutine to operate on the next turn's mutable state.
type promptTurnDeps struct {
	*BackgroundSession
	turn *promptTurn
}

func (d promptTurnDeps) ownsTurn() bool {
	d.promptMu.Lock()
	defer d.promptMu.Unlock()
	return d.promptTurn == d.turn
}

// release requires turn.cleanupMu. Restore and flush while the reservation is
// still held, including when the queue is absent/disabled or preparation failed.
func (d promptTurnDeps) release() {
	if !d.ownsTurn() {
		return
	}
	// A deferred-handshake watchdog may have returned before its worker
	// launched startup model work. cleanupMu joins that worker; this joins
	// the bounded model RPC it launched before restoring/releasing the slot.
	d.waitForStartupConfigConstraints()
	d.restoreBaselineIfOverride()
	d.flushPendingConfig()
	d.BackgroundSession.pdFlushMarkdown()
	d.DismissActiveUIPrompt()
	d.BackgroundSession.pdNotifyStreamingStateChanged(false)
	// Pair the last drain with config admission. A manual selection may arrive
	// during an earlier RPC or cleanup callback; apply it before opening the slot.
	for {
		d.promptMu.Lock()
		if d.IsClosed() || !d.hasPendingConfigLocked() {
			break
		}
		d.promptMu.Unlock()
		d.flushPendingConfig()
	}
	d.promptTurn = nil
	d.isPrompting = false
	d.clearActiveDispatchLocked()
	d.promptStartTime = time.Time{}
	d.lastResponseComplete = time.Now()
	if d.promptCond != nil {
		d.promptCond.Broadcast()
	}
	d.promptMu.Unlock()
}

func (d promptTurnDeps) releaseAborted() {
	// Cancel/ForceReset must send their cancel notification/flush before releasing
	// the slot. Do not let the prompt's defer race ahead of that cleanup.
	d.turn.cancel()
	d.turn.finishPreparation()
	d.turn.cleanupMu.Lock()
	defer d.turn.cleanupMu.Unlock()
	d.promptMu.Lock()
	stopping := d.turn.stopRequested
	d.promptMu.Unlock()
	if !stopping {
		d.release()
	}
}

func (d promptTurnDeps) pdSessionCtx() context.Context { return d.turn.ctx }
func (d promptTurnDeps) pdMarkPromptComplete()         { d.release() }

func (d promptTurnDeps) waitForStartupModel() error {
	// Startup RPCs already have a bounded budget. Join them rather than racing
	// a per-turn switch against the callback's asynchronous baseline selection.
	d.waitForStartupConfigConstraints()
	if err := d.turn.ctx.Err(); err != nil {
		return err
	}
	if !d.startupConfigConstraintsReady() {
		return &sessionError{"the conversation model is still initializing; please retry"}
	}
	return nil
}

// Handshake failure cleanup belongs to the owning preparation defer, not the
// watchdog (which may return while its worker is still unwinding).
func (d promptTurnDeps) pdResetPromptingStateForAbort() {}

func (d promptTurnDeps) pdRestoreBaselineIfOverride() {
	if d.ownsTurn() {
		d.restoreBaselineIfOverride()
	}
}

func (d promptTurnDeps) pdFlushPendingConfig() {
	if d.ownsTurn() {
		d.flushPendingConfig()
	}
}

func (d promptTurnDeps) pdFlushMarkdown() {
	if d.ownsTurn() {
		d.BackgroundSession.pdFlushMarkdown()
	}
}

func (d promptTurnDeps) pdDismissActiveUIPrompt() {
	if d.ownsTurn() {
		d.DismissActiveUIPrompt()
	}
}

func (d promptTurnDeps) pdNotifyStreamingStateChanged(active bool) {
	// release publishes false before opening the slot to a newer turn.
	if active && d.ownsTurn() && d.turn.ctx.Err() == nil {
		d.BackgroundSession.pdNotifyStreamingStateChanged(true)
	}
}

func (d promptTurnDeps) pdReacquirePromptingState() {
	// Crash retry keeps this turn's reservation; there is nothing to reacquire.
}

// The completion path already restored the baseline before becoming idle.
// Queue-drain fallbacks must not restore a newer concurrently accepted turn.
type completedPromptQueueDeps struct{ *BackgroundSession }

func (completedPromptQueueDeps) restoreBaselineIfOverride() {}

func (d promptTurnDeps) pdProcessNextQueuedMessage() bool {
	d.release()
	if d.IsPrompting() || !d.startupConfigConstraintsReady() {
		return false
	}
	return d.queueDisp.processNext(completedPromptQueueDeps{d.BackgroundSession})
}

type promptHandshakeDeps struct {
	*BackgroundSession
	ctx context.Context
}

func (d promptHandshakeDeps) hsSessionCtx() context.Context { return d.ctx }

func (d promptTurnDeps) pdCompleteDeferredHandshake() error {
	// Also cover the outer watchdog's worker: it must not adopt a late session
	// after the preparation defer/cancellation has handed the slot to a new turn.
	d.turn.cleanupMu.Lock()
	defer d.turn.cleanupMu.Unlock()
	if err := d.turn.ctx.Err(); err != nil {
		return err
	}
	if !d.ownsTurn() {
		return context.Canceled
	}
	budget := d.pdRecommendedHandshakeDeadline()
	if budget <= 0 {
		budget = handshakeWatchdogFallback
	}
	ctx, cancel := context.WithTimeout(d.turn.ctx, budget)
	defer cancel()
	return d.handshaker.completeDeferredHandshake(promptHandshakeDeps{d.BackgroundSession, ctx})
}

// PromptTriggerContext holds trigger-source data threaded from the LoopRunner
// through the prompt dispatch pipeline into the template evaluation context.
// Only non-nil sub-fields represent triggers that actually fired for this
// dispatch (currently: OnTasks, OnSlack).
type PromptTriggerContext struct {
	// OnTasks is populated only when a beads change fired an onTasks loop.
	OnTasks *PromptOnTasksContext
	// OnChild is populated only when a child-conversation lifecycle event
	// fired an onChild loop (mitto-qvlh).
	OnChild *PromptOnChildContext
	// OnSlack is populated with a bounded batch when onSlack fired.
	OnSlack *PromptOnSlackContext
	// Slack is the first event compatibility alias for the environment PoC.
	// Deprecated: use OnSlack.Events.
	Slack *PromptSlackContext
}

// PromptOnChildContext carries the child-lifecycle detail that fired an
// onChild loop dispatch (mitto-qvlh). ChildID intentionally does NOT include
// the child's name/title — by the time an anyDeleted fire reaches here, the
// deleted child's metadata (including its name) may already be gone.
type PromptOnChildContext struct {
	ChildID       string
	Event         session.ChildEvent
	StoppedReason session.StoppedReason
}

// PromptOnTasksContext carries the beads change delta already computed by
// the onTasks loop runner (see internal/web/loop_runner_tasks.go
// processTasksChange). The pointer is copied by reference into
// ProcessorInput.TriggerOnTasksChanges by the prompt dispatcher; from there
// BuildCELContext lifts the slices onto the template PromptEnabledContext.
type PromptOnTasksContext struct {
	Changes *config.TasksDelta
}

// PromptOnSlackContext carries the bounded normalized Slack event batch that
// caused one onSlack dispatch. One batch consumes one loop iteration.
type PromptOnSlackContext struct {
	Events []PromptSlackEvent
}

// PromptSlackEvent is the credential-free, SDK-free template representation of
// one Slack event. Every string is bounded before PromptMeta is constructed.
//
// SENSITIVITY: Text is UNTRUSTED external content. Before PromptMeta is built,
// it is JSON-escaped inside a fixed data-only delimiter that Slack input cannot
// forge. Never persist it to loop.json or generic event metadata, or log it.
type PromptSlackEvent struct {
	InstallationID  string
	EventID         string
	ChannelID       string
	Kind            string
	AuthorID        string
	Timestamp       string
	ThreadTimestamp string
	// Untrusted is always true and makes provenance explicit to templates.
	Untrusted bool
	// Text is a JSON-escaped, explicitly untrusted data-only prompt block.
	Text string
}

// PromptSlackContext is the legacy single-event type name.
// Deprecated: use PromptSlackEvent via PromptOnSlackContext.Events.
type PromptSlackContext = PromptSlackEvent

// deriveUserPromptProvenance builds a credential-free session.PromptProvenance
// from the dispatch PromptMeta (mitto-rg79). Returns nil for ordinary
// human-typed/ad-hoc prompts (LoopTrigger empty, not forced, not the startup
// pulse) so old-shape events/payloads are unaffected. Both IsLoopForced and
// IsLoopRunOnStart are recorded verbatim (raw fidelity); presentation layers
// that need a single label prefer startup over forced when both are true —
// see web/static/utils/promptProvenance.js.
//
// Never copies Slack event Text/bodies — only InstallationID/ChannelID (both
// non-secret identifiers) and the batch length.
func deriveUserPromptProvenance(meta PromptMeta) *session.PromptProvenance {
	if meta.LoopTrigger == "" && !meta.IsLoopForced && !meta.IsLoopRunOnStart {
		return nil
	}
	p := &session.PromptProvenance{
		LoopTrigger:      meta.LoopTrigger,
		IsLoopForced:     meta.IsLoopForced,
		IsLoopRunOnStart: meta.IsLoopRunOnStart,
	}
	if meta.LoopTrigger == session.TriggerOnSlack && meta.Trigger != nil && meta.Trigger.OnSlack != nil {
		events := meta.Trigger.OnSlack.Events
		if len(events) > 0 {
			first := events[0]
			p.Slack = &session.PromptSlackProvenance{
				InstallationID: first.InstallationID,
				ChannelID:      first.ChannelID,
				EventCount:     len(events),
			}
		}
	}
	if meta.LoopTrigger == session.TriggerOnChild && meta.Trigger != nil && meta.Trigger.OnChild != nil {
		oc := meta.Trigger.OnChild
		p.OnChild = &session.PromptOnChildProvenance{
			ChildID:       oc.ChildID,
			Event:         string(oc.Event),
			StoppedReason: string(oc.StoppedReason),
		}
	}
	return p
}

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	return bs.PromptWithMeta(message, PromptMeta{})
}

// PromptWithImages sends a message with optional images to the agent. This runs asynchronously.
// The imageIDs should be IDs of images previously uploaded to this session.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithImages(message string, imageIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs})
}

// PromptWithAttachments sends a message with optional images and files to the agent.
// This runs asynchronously. The IDs should be of previously uploaded images/files.
func (bs *BackgroundSession) PromptWithAttachments(message string, imageIDs, fileIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs, FileIDs: fileIDs})
}

// flushContextInPlace sends the configured contextFlushCommand to the existing ACP
// session synchronously, suppressing all streaming callbacks for the duration so the
// flush turn never reaches the recorder, observers, or the transcript.
//
// Behavioral contract:
//   - Sends contextFlushCommand as a single-block Prompt() RPC on the existing session.
//   - All streaming callbacks are suppressed (setStreamingSuppressed) for the duration.
//   - Best-effort: the caller MUST continue with the main loop prompt regardless of
//     any returned error.
//   - Works for both direct-conn (acpConn) and shared-process (sharedProcess) sessions.
func (bs *BackgroundSession) flushContextInPlace(ctx context.Context) error {
	cmd := strings.TrimSpace(bs.ContextFlushCommand())
	if cmd == "" {
		return &sessionError{"context flush command not configured for this server"}
	}
	if bs.acpID == "" {
		return &sessionError{"no ACP session ID available for in-place flush"}
	}
	blocks := []acp.ContentBlock{acp.TextBlock(cmd)}
	bs.setStreamingSuppressed(true)
	defer bs.setStreamingSuppressed(false)
	if bs.sharedProcess != nil {
		_, err := bs.sharedProcess.Prompt(ctx, acp.SessionId(bs.acpID), blocks)
		return err
	}
	if bs.acpConn != nil {
		_, err := bs.acpConn.Prompt(ctx, acp.PromptRequest{
			SessionId: acp.SessionId(bs.acpID),
			Prompt:    blocks,
		})
		return err
	}
	return &sessionError{"no ACP transport available for in-place flush"}
}

// FlushContext clears the agent's conversation context by sending the configured
// agent-native context-flush command (e.g. "/clear") directly to the existing ACP
// session via flushContextInPlace — the same in-place primitive used by the loop
// FreshContext path (see createFreshContextSession) — instead of routing through
// the full PromptWithMeta pipeline. Routing through PromptWithMeta previously
// wrapped/processor-polluted the command (breaking the agent's prefix-recognition
// contract for slash commands) and persisted+broadcast a fake user turn for what
// is a UI-only control action (mitto-ip1).
//
// It runs asynchronously (dispatched in a goroutine), mirroring the async
// contract of the normal prompt path: callers (e.g. the REST handler) get an
// immediate nil return and the flush RPC completes in the background. Streaming
// is suppressed for the duration so the flush turn never reaches the recorder,
// observers, or the transcript; on success a "context_cleared" timeline pill is
// recorded instead. Returns an error synchronously when no flush command is
// configured for this session's ACP server, when the session is closed, or when
// there is no live ACP session ID to flush. Callers should gate on IsPrompting()
// to avoid issuing a flush while a turn is in flight.
func (bs *BackgroundSession) FlushContext() error {
	cmd := strings.TrimSpace(bs.ContextFlushCommand())
	if cmd == "" {
		return &sessionError{"context flush command not configured for this server"}
	}
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpID == "" {
		return &sessionError{"no ACP session ID available for in-place flush"}
	}

	go func() {
		flushCtx, flushCancel := context.WithTimeout(bs.ctx, 30*time.Second)
		defer flushCancel()
		if err := bs.flushContextInPlace(flushCtx); err != nil {
			if bs.logger != nil {
				bs.logger.Warn("In-place context flush failed",
					"error", err, "session_id", bs.persistedID)
			}
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("Failed to clear context: " + err.Error())
			})
			return
		}
		// The manual flush succeeded: the session is now provably empty (mitto-s9g2).
		// This only resets the counter — the manual action does not skip itself.
		bs.markACPContextFresh()
		// Surface the context clear in the conversation timeline (mirrors the
		// loop FreshContext pill recorded by createFreshContextSession).
		bs.cmRecordSessionChange("context_cleared", "flush", "")
	}()

	return nil
}

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	imageIDs := meta.ImageIDs
	fileIDs := meta.FileIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil && bs.sharedProcess == nil {
		return &sessionError{"The AI agent is still starting up. Please wait a moment and try again."}
	}

retryAfterRestart:
	bs.promptMu.Lock()
	if bs.isPrompting {
		if bs.promptTurn != nil {
			// The owner handles connection death and releases only after all
			// preparation/retry work has stopped. Never restart underneath it.
			bs.promptMu.Unlock()
			return &sessionError{"prompt already in progress"}
		}
		// Check if the ACP connection is dead (process crashed)
		// We use non-blocking checks on both Done() and acpProcessDone channels.
		// acpProcessDone fires faster than Done() because it uses OS-level process
		// liveness checks rather than waiting for pipe EOF propagation.
		acpDead := false
		if bs.acpConn != nil {
			select {
			case <-bs.acpConn.Done():
				acpDead = true
			default:
				// Connection still alive
			}
		} else if bs.sharedProcess != nil {
			select {
			case <-bs.sharedProcess.Done():
				acpDead = true
			default:
				// Shared connection still alive
			}
		} else {
			acpDead = true // No connection at all
		}
		// Also check OS-level process death (faster detection)
		if !acpDead && bs.acpProcessDone != nil {
			select {
			case <-bs.acpProcessDone:
				acpDead = true
			default:
			}
		}

		if acpDead {
			elapsed := time.Since(bs.promptStartTime)
			if bs.logger != nil {
				bs.logger.Warn("Detected dead ACP connection",
					"prompt_start_time", bs.promptStartTime,
					"elapsed", elapsed)
			}
			bs.isPrompting = false
			bs.clearActiveDispatchLocked()
			bs.lastResponseComplete = time.Now()
			bs.promptMu.Unlock()

			// Check if we can restart automatically
			if bs.canRestartACP() {
				// Notify observers that we're restarting (include attempt count so
				// the user understands this is a retry loop, not a one-off)
				restartInfo := bs.getRestartInfo()
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
				})

				// Attempt to restart the ACP process
				if err := bs.restartACPProcess(mittoAcp.RestartReasonCrashDuringPrompt); err != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + err.Error() + ". Please switch to another conversation and back to retry."
					if classified, ok := err.(*mittoAcp.ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = mittoAcp.FormatClassifiedError(classified)
					}
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError(errMsg)
					})
					return &sessionError{"ACP process died and restart failed: " + err.Error()}
				}

				// Restart succeeded — automatically retry the prompt.
				// Note: we say "restarted" (not "restarted successfully") because the
				// process may crash again on the next prompt — we don't want to give
				// false confidence.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Retrying your message automatically...")
				})
				if bs.logger != nil {
					bs.logger.Info("Auto-retrying prompt after ACP restart",
						"session_id", bs.persistedID,
						"reason", "crash_during_prompt")
				}
				// isPrompting was cleared above; re-acquire promptMu and proceed
				// through the normal prompt path below.
				goto retryAfterRestart
			}

			// Restart limit exceeded - notify user to manually restart
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
			})
			return &sessionError{"ACP process died repeatedly - switch conversations to restart"}
		} else {
			bs.promptMu.Unlock()
			return &sessionError{"prompt already in progress"}
		}
	}
	turnCtx, turnCancel := context.WithCancel(bs.ctx)
	turn := &promptTurn{ctx: turnCtx, cancel: turnCancel, preparationDone: make(chan struct{})}
	bs.promptTurn = turn
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	// Record the in-flight dispatch identity so a duplicate identical
	// dispatch via target.reuseCoalesce can be a no-op (mitto-djs1).
	// A shallow copy of Arguments keeps callers isolated from later
	// mutation (map is passed by reference through the queue path).
	bs.activePromptName = meta.PromptName
	if len(meta.Arguments) > 0 {
		bs.activePromptArgs = make(map[string]string, len(meta.Arguments))
		for k, v := range meta.Arguments {
			bs.activePromptArgs[k] = v
		}
	} else {
		bs.activePromptArgs = nil
	}
	bs.TouchActivity()
	bs.promptMu.Unlock()

	// Reserve before any rendering/model preflight: a rejected concurrent send
	// must not be allowed to change the running turn's model.
	d := promptTurnDeps{BackgroundSession: bs, turn: turn}
	meta.modelContext = turn.ctx
	asyncStarted := false
	defer func() {
		if !asyncStarted {
			d.releaseAborted()
		}
	}()
	if err := d.waitForStartupModel(); err != nil {
		return err
	}

	isScheduledLoop := meta.LoopKind == LoopKindScheduled && !meta.FreshContext
	meta.IterationUninterrupted = bs.peekLoopContinuation(isScheduledLoop)
	var argCount int
	var err error
	message, argCount, meta, err = bs.promptDisp.resolveAndSubstitute(d, message, meta)
	if err != nil {
		bs.notifyObservers(func(o SessionObserver) { o.OnError(err.Error()) })
		return err
	}
	if err := turn.ctx.Err(); err != nil {
		return err
	}
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}

	// Only accepted preparation consumes the first-prompt/history counters.
	bs.promptMu.Lock()
	bs.promptCount++

	// Check if we need to inject conversation history (first prompt of resumed session).
	// FreshContext suppresses history injection so each loop run starts clean.
	shouldInjectHistory := bs.isResumed && !bs.historyInjected && !meta.FreshContext
	if shouldInjectHistory {
		bs.historyInjected = true
	}

	// Capture first prompt state for message processors
	isFirst := bs.isFirstPrompt
	if isFirst {
		bs.isFirstPrompt = false
	}
	bs.promptMu.Unlock()

	// Point of no return: this dispatch is committed. Advance the loop continuation
	// marker so the NEXT dispatch can detect an uninterrupted continuation. A non-scheduled
	// dispatch (user/forced/FreshContext) sets it false, breaking the chain (mitto-5xjn).
	bs.advanceLoopContinuation(isScheduledLoop)

	// Notify about streaming state change (prompt started)
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, true)
	}

	// Load images and files, build content blocks + session refs.
	// See promptDispatcher.buildAttachmentBlocks for the full logic.
	contentBlocks, imageRefs, fileRefs := bs.promptDisp.buildAttachmentBlocks(d, imageIDs, fileIDs)

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Clear cached plan state when new prompt starts
	// The existing plan becomes stale; a new plan will be generated for this prompt
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, nil)
	}

	// FreshContext seq reservation (mitto-c36): when the loop turn will flush the
	// context (either via in-place flush command or a new ACP session), reserve the
	// "context_cleared" pill seq BEFORE the user-prompt seq so the persisted transcript
	// orders as pill(N) → user_prompt(N+1) → agent_stream(N+2..). The reserved seq is
	// consumed inside createFreshContextSession on the async goroutine below. If the
	// flush/new-session ultimately does not fire (e.g. flush RPC error), the seq
	// becomes a persistence-tolerated gap.
	var freshContextPillSeq int64
	if meta.FreshContext && bs.recorder != nil {
		freshContextPillSeq = bs.getNextSeq()
	}

	// Persist user prompt with image/file references and prompt ID.
	// Seq is pre-assigned from the shared getNextSeq() counter so that the user-prompt
	// event is ordered atomically with respect to any concurrent streaming events.
	// This avoids the duplicate/out-of-order seq bug caused by AppendEvent assigning
	// seq independently from the in-memory counter.
	// persistArgs holds the raw (exactly replayable) argument values to persist
	// and broadcast, with sensitive-named keys omitted (see persistableArguments).
	// Computed unconditionally so both the recorder call below and the observer
	// notification further down use the same filtered map.
	persistArgs := persistableArguments(meta.Arguments)

	// Derive credential-free trigger provenance (mitto-rg79) BEFORE persisting so
	// both the recorded event and the live observer notification carry the same
	// value. Nil for ordinary human-typed/ad-hoc prompts.
	provenance := deriveUserPromptProvenance(meta)

	var userPromptSeq int64
	if bs.recorder != nil {
		userPromptSeq = bs.getNextSeq()
		var recordOpts []session.RecordOption
		if len(meta.Meta) > 0 {
			recordOpts = append(recordOpts, session.WithMetaMap(meta.Meta))
		}
		data := session.UserPromptData{
			Message:       message,
			Images:        imageRefs,
			Files:         fileRefs,
			PromptID:      meta.PromptID,
			PromptName:    meta.PromptName,
			ArgumentCount: argCount,
			Arguments:     persistArgs,
			Provenance:    provenance,
		}
		if err := bs.recorder.RecordUserPromptDataWithSeq(userPromptSeq, data, recordOpts...); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	fileIDStrings := make([]string, len(fileRefs))
	for i, f := range fileRefs {
		fileIDStrings[i] = f.ID
	}

	// Propagate generic event metadata to observers that implement EventMetaObserver.
	// This must happen BEFORE OnUserPrompt so observers can store the meta keyed by seq
	// and attach it to the outgoing payload inside OnUserPrompt.
	if userPromptSeq > 0 && len(meta.Meta) > 0 {
		eventMeta := meta.Meta
		bs.notifyObservers(func(o SessionObserver) {
			if m, ok := o.(EventMetaObserver); ok {
				m.OnEventMeta(userPromptSeq, eventMeta)
			}
		})
	}

	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings, meta.PromptName, argCount, persistArgs, provenance)
	})

	// Build processor input and assemble final content blocks.
	// See promptDispatcher.buildProcessorInput + applyProcessorsAndBuildBlocks.
	processorInput := bs.promptDisp.buildProcessorInput(d, message, isFirst, meta)
	finalBlocks := bs.promptDisp.applyProcessorsAndBuildBlocks(d, processorInput, message, contentBlocks, shouldInjectHistory)
	if err := turn.ctx.Err(); err != nil {
		return err
	}

	// Run prompt in background
	asyncStarted = true
	go func() {
		defer turn.cancel()
		// PromptWithMeta has already returned success to its caller, so every exit
		// from this goroutine must invoke OnComplete exactly once. Normal completion
		// goes through finalizeTurn below; abort paths (including deferred-handshake
		// failure and session closure) fall back to this defer. Keeping release with
		// the owning turn avoids a stale lifecycle cleanup releasing a newer claim.
		abortedErr := &sessionError{"prompt ended before completion finalization"}
		var completionErr error = abortedErr
		if meta.OnComplete != nil {
			originalOnComplete := meta.OnComplete
			var completionOnce sync.Once
			meta.OnComplete = func(err error) {
				completionOnce.Do(func() { originalOnComplete(err) })
			}
			defer func() { meta.OnComplete(completionErr) }()
		}
		defer d.releaseAborted()

		// autoRetried guards a single automatic retry after an ACP crash during
		// streaming. On the first crash we restart the process and jump back to
		// retryPrompt; if the retry also crashes we fall through to the normal
		// "please resend" message instead of looping forever.
		autoRetried := false

		// Complete the deferred handshake, create a fresh-context session if requested,
		// and apply any per-prompt model preference.
		// See promptDispatcher.completeHandshakeOrAbort, createFreshContextSession, applyModelPreference.
		if !bs.promptDisp.completeHandshakeOrAbort(d) {
			return
		}
		if err := d.waitForStartupModel(); err != nil {
			completionErr = err
			bs.notifyObservers(func(o SessionObserver) { o.OnError(err.Error()) })
			return
		}
		_, freshErr := bs.promptDisp.createFreshContextSession(d, meta, freshContextPillSeq)
		if freshErr != nil {
			completionErr = freshErr
			bs.notifyObservers(func(o SessionObserver) { o.OnError(freshErr.Error()) })
			return
		}
		// Rendering already applied non-FreshContext model preferences. Fresh
		// sessions apply their preference only after adopting the new catalog/ID.
		if !meta.modelPreferenceResolved {
			bs.promptDisp.applyModelPreference(d, meta)
		}
		turn.finishPreparation()
		if err := turn.ctx.Err(); err != nil {
			completionErr = err
			return
		}

		// Declare all variables that are live across the retryPrompt goto target
		// here, before the label, so that Go's "no jumping over declarations" rule
		// is satisfied. They are assigned (not declared) inside the loop body.
		var (
			promptCtx       context.Context
			promptCancel    context.CancelFunc
			promptResp      acp.PromptResponse
			err             error
			promptStartedAt time.Time
			promptEndedAt   time.Time
			processDoneCh   <-chan struct{}
			connDoneCh      <-chan struct{}
			// inactivityWatchdogFired is set by the prompt inactivity watchdog when it
			// cancels the prompt because the agent stopped streaming (live-but-unresponsive).
			// The error-handling path below reads it to surface a recoverable message and
			// skip the crash-restart logic (the process is alive, not dead).
			inactivityWatchdogFired atomic.Bool
		)

	retryPrompt:
		turn.cleanupMu.Lock()
		if !d.ownsTurn() || turn.ctx.Err() != nil {
			completionErr = context.Canceled
			turn.cleanupMu.Unlock()
			return
		}
		// Reset the inactivity flag for this attempt (a goto retryPrompt reuses it).
		inactivityWatchdogFired.Store(false)
		// Create a prompt context that gets cancelled when the ACP process dies.
		// This ensures we fail fast instead of waiting for the ACP server's internal
		// 60-second control request timeout when the CLI subprocess has crashed.
		// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
		promptCtx, promptCancel = context.WithCancel(turn.ctx)
		// NOTE: no defer — we call promptCancel() explicitly after the prompt
		// returns so that (a) we clean up the health-monitor goroutine eagerly,
		// and (b) a goto back to retryPrompt doesn't accumulate extra defers.

		// Monitor ACP process health: if the connection's Done() channel closes
		// or the OS process exits (acpProcessDone), cancel the prompt context immediately.
		// The acpProcessDone channel provides faster detection than Done() because it
		// uses OS-level process liveness checks (signal 0) rather than waiting for
		// pipe EOF to propagate through the JSON-RPC transport layer.
		processDoneCh = bs.acpProcessDone // refresh on each retry (new process after restart)
		connDoneCh = nil                  // reset before assigning below
		if bs.acpConn != nil {
			connDoneCh = bs.acpConn.Done()
		} else if bs.sharedProcess != nil {
			connDoneCh = bs.sharedProcess.Done()
		}
		if connDoneCh != nil {
			go func(promptCtx context.Context, promptCancel context.CancelFunc, connDoneCh, processDoneCh <-chan struct{}) {
				select {
				case <-connDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP connection closed during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-processDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP process exited during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-promptCtx.Done():
					// Prompt completed normally or was cancelled for another reason
				}
			}(promptCtx, promptCancel, connDoneCh, processDoneCh)
		}

		// Monitor for a live-but-unresponsive agent: if the agent stops streaming any
		// updates for the configured window (and is not blocked on a UI prompt), cancel
		// the prompt so is_prompting clears and the user can resend. This catches the
		// "stuck, still responding" state that the process-death/connection monitors miss.
		bs.startPromptInactivityWatchdog(promptCtx, promptCancel, &inactivityWatchdogFired)
		bs.startAgentWorkingHeartbeat(promptCtx)

		// Fresh-context replacement adopts its ID before model selection, just
		// like crash restart. The prompt and config RPCs always use the same ID.
		acpSessionIDForPrompt := bs.acpID
		promptConn, sharedProcess := bs.acpConn, bs.sharedProcess

		// Track that a turn is being dispatched on the current ACP session, so a
		// later FreshContext iteration can tell it is no longer virgin (mitto-s9g2).
		bs.noteACPTurnDispatched()

		promptStartedAt = time.Now() // captured for after-phase processors
		turn.cleanupMu.Unlock()
		// A concurrent Cancel now cancels promptCtx; captured transport/ID keep
		// this attempt from reading state adopted by a newer turn.
		if sharedProcess != nil {
			promptResp, err = sharedProcess.Prompt(promptCtx, acp.SessionId(acpSessionIDForPrompt), finalBlocks)
		} else {
			promptResp, err = promptConn.Prompt(promptCtx, acp.PromptRequest{
				SessionId: acp.SessionId(acpSessionIDForPrompt),
				Prompt:    finalBlocks,
			})
		}
		promptCancel()             // cancel context to unblock the health-monitor goroutine
		promptEndedAt = time.Now() // captured for after-phase processors
		completionErr = err
		if completionErr == nil {
			completionErr = abortedErr
		}

		// Serialize completion/retry RPCs with Cancel/ForceReset, not promptMu.
		// Keep the reservation until the error handler has decided whether to retry.
		retry := func() bool {
			turn.cleanupMu.Lock()
			defer turn.cleanupMu.Unlock()
			if !d.ownsTurn() || turn.ctx.Err() != nil {
				completionErr = turn.ctx.Err()
				return false
			}
			bs.promptDisp.accumulateTokenUsage(d, promptResp, message)
			eventCount, observerCount := bs.GetEventCount(), bs.ObserverCount()
			if err != nil && bs.promptDisp.handlePromptError(d, err, &autoRetried, observerCount, inactivityWatchdogFired.Load()) {
				// Restart replaced the ACP session. Reapply this turn's preference
				// while cleanup is serialized, before retrying on that session.
				if startupErr := d.waitForStartupModel(); startupErr != nil {
					completionErr = startupErr
					bs.notifyObservers(func(o SessionObserver) { o.OnError(startupErr.Error()) })
					return false
				}
				bs.promptDisp.applyModelPreference(d, meta)
				return turn.ctx.Err() == nil
			}
			if bs.promptDisp.markPromptCompleteAndFlush(d) {
				return false
			}
			sessionIdle := false
			if err == nil {
				sessionIdle = bs.promptDisp.handlePromptSuccess(d, eventCount, observerCount, promptResp, message, meta, promptStartedAt, promptEndedAt)
			}
			completionErr = err
			bs.promptDisp.finalizeTurn(d, err, meta, sessionIdle)
			return false
		}()
		if retry {
			goto retryPrompt
		}
	}()

	return nil
}

// cancelPromptTurn cancels without releasing the reservation. The caller must
// wait for preparation to leave model/session RPCs before restoring the baseline.
func (bs *BackgroundSession) cancelPromptTurn() *promptTurn {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	if !bs.isPrompting {
		return nil
	}
	turn := bs.promptTurn
	if turn == nil {
		// Legacy/minimal sessions used by callers/tests can have a busy flag
		// without a turn. Give their cleanup the same ownership protection.
		ctx, cancel := context.WithCancel(context.Background())
		turn = &promptTurn{ctx: ctx, cancel: cancel, preparationDone: make(chan struct{})}
		turn.finishPreparation()
		bs.promptTurn = turn
	}
	turn.stopRequested = true
	turn.cancel()
	return turn
}

func (bs *BackgroundSession) finishCancelledPromptTurn(turn *promptTurn, sendCancel bool) error {
	if turn == nil {
		return nil
	}
	d := promptTurnDeps{BackgroundSession: bs, turn: turn}
	turn.cleanupMu.Lock()
	if d.ownsTurn() {
		bs.DismissActiveUIPrompt()
	}
	turn.cleanupMu.Unlock()
	<-turn.preparationDone
	turn.cleanupMu.Lock()
	defer turn.cleanupMu.Unlock()
	if !d.ownsTurn() {
		return nil
	}

	// Never cancel after releasing the slot: it could cancel a newer turn on
	// the same ACP session. The protocol notification is best-effort/bounded.
	var cancelErr error
	if sendCancel {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if bs.sharedProcess != nil {
			cancelErr = bs.sharedProcess.Cancel(ctx, acp.SessionId(bs.acpID))
		} else if bs.acpConn != nil {
			cancelErr = bs.acpConn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(bs.acpID)})
		}
	}
	// The canceled context suppresses the old response's completion path, so
	// both Stop and ForceReset must finalize the session stream here, exactly once.
	bs.pdFlushMarkdown()
	eventCount := bs.GetEventCount()
	bs.notifyObservers(func(o SessionObserver) { o.OnPromptComplete(eventCount) })
	d.release()
	return cancelErr
}

// Cancel cancels the owning context, waits for preparation/model RPCs to finish,
// then restores and releases the turn, even without an enabled queue.
func (bs *BackgroundSession) Cancel() error {
	// UI-only questions can exist even when no agent turn is in progress.
	bs.DismissActiveUIPrompt()
	turn := bs.cancelPromptTurn()
	err := bs.finishCancelledPromptTurn(turn, true)
	if turn != nil {
		go bs.TryProcessQueuedMessage()
	}
	return err
}

// ForceReset has the same ownership and model-cleanup barrier as Cancel but
// does not send an ACP cancel notification.
func (bs *BackgroundSession) ForceReset() {
	turn := bs.cancelPromptTurn()
	if turn == nil {
		if bs.logger != nil {
			bs.logger.Debug("ForceReset called but session was not prompting")
		}
		return
	}
	_ = bs.finishCancelledPromptTurn(turn, false)
	go bs.TryProcessQueuedMessage()

	if bs.logger != nil {
		bs.logger.Warn("Session forcefully reset due to unresponsive agent")
	}
}

// =============================================================================
// promptDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) pdPromptResolver() PromptResolver { return bs.promptResolver }
func (bs *BackgroundSession) pdPromptFragmentsResolver() PromptFragmentsResolver {
	return bs.promptFragmentsResolver
}
func (bs *BackgroundSession) pdWorkingDir() string { return bs.workingDir }

func (bs *BackgroundSession) pdBeadsDatabaseMode() config.BeadsDatabaseMode {
	if bs.beadsDatabaseModeResolver == nil {
		return config.BeadsDatabaseModeLocal
	}
	mode, err := bs.beadsDatabaseModeResolver(bs.ctx, bs.workingDir)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("failed to resolve Beads database mode for prompt context; using local",
				"error", err,
				"working_dir", bs.workingDir)
		}
		return config.BeadsDatabaseModeLocal
	}
	return mode
}

// pdPromptsSnapshot returns a lazy fn that snapshots the workspace prompt
// registry for the render-time {{ .Prompts.Exists }} / {{ .Prompts.Enabled }}
// predicates (mitto-s1w). Returns nil when no PromptsCache is wired — the
// dispatcher hands the nil fn to ProcessorInput, and BuildCELContext leaves
// ctx.Prompts zero-valued (predicates fail-closed).
func (bs *BackgroundSession) pdPromptsSnapshot() func() *config.PromptsSnapshot {
	if bs.promptsCache == nil {
		return nil
	}
	cache := bs.promptsCache
	return func() *config.PromptsSnapshot {
		snap := cache.NamesSnapshot()
		return &snap
	}
}

func (bs *BackgroundSession) pdAgentSupportsImages() bool { return bs.agentSupportsImages }

func (bs *BackgroundSession) pdHasStore() bool { return bs.store != nil }

func (bs *BackgroundSession) pdGetImagePath(imageID string) (string, error) {
	return bs.store.GetImagePath(bs.persistedID, imageID)
}

func (bs *BackgroundSession) pdGetFilePath(fileID string) (string, error) {
	return bs.store.GetFilePath(bs.persistedID, fileID)
}

func (bs *BackgroundSession) pdLogger() *slog.Logger { return bs.logger }
func (bs *BackgroundSession) pdSessionID() string    { return bs.persistedID }

func (bs *BackgroundSession) pdNotifyObservers(fn func(SessionObserver)) {
	bs.notifyObservers(fn)
}

// === New in 2.5-b ===

func (bs *BackgroundSession) pdWorkspaceUUID() string { return bs.workspaceUUID }

func (bs *BackgroundSession) pdAvailableACPServers() []processors.AvailableACPServer {
	return bs.availableACPServers
}

func (bs *BackgroundSession) pdGetSessionMetadata() (session.Metadata, error) {
	if bs.store == nil || bs.persistedID == "" {
		return session.Metadata{}, fmt.Errorf("store not available")
	}
	return bs.store.GetMetadata(bs.persistedID)
}

func (bs *BackgroundSession) pdGetMetadataForID(id string) (session.Metadata, error) {
	if bs.store == nil {
		return session.Metadata{}, fmt.Errorf("store not available")
	}
	return bs.store.GetMetadata(id)
}

func (bs *BackgroundSession) pdListChildSessions() ([]session.Metadata, error) {
	if bs.store == nil || bs.persistedID == "" {
		return nil, fmt.Errorf("store not available")
	}
	return bs.store.ListChildSessions(bs.persistedID)
}

// pdListWorkspacePeers returns non-archived sessions sharing this session's
// workspace (identified by the (WorkingDir, ACPServer) composite key, which
// maps 1:1 to a WorkspaceUUID in the registry), excluding self. Returns an
// empty slice — never nil — when no store is available so callers can range
// unconditionally. Errors from store.List are propagated so the caller can
// swallow them the same way sibling helpers do.
func (bs *BackgroundSession) pdListWorkspacePeers() ([]session.Metadata, error) {
	if bs.store == nil || bs.persistedID == "" {
		return nil, fmt.Errorf("store not available")
	}
	if bs.workingDir == "" && bs.acpServer == "" {
		return []session.Metadata{}, nil
	}
	all, err := bs.store.List()
	if err != nil {
		return nil, err
	}
	peers := make([]session.Metadata, 0, len(all))
	for _, m := range all {
		if m.SessionID == bs.persistedID {
			continue
		}
		if m.Archived {
			continue
		}
		if m.WorkingDir != bs.workingDir || m.ACPServer != bs.acpServer {
			continue
		}
		peers = append(peers, m)
	}
	return peers, nil
}

func (bs *BackgroundSession) pdIsChildPrompting(childSessionID string) bool {
	if bs.isChildPrompting == nil {
		return false
	}
	return bs.isChildPrompting(childSessionID)
}

// pdChildQueueLength returns the number of pending queued prompts on the
// given child session, or 0 when the store is unavailable or the length
// cannot be read. Errors are swallowed to match the best-effort semantics
// of the surrounding child/peer enumeration.
func (bs *BackgroundSession) pdChildQueueLength(childSessionID string) int {
	if bs.store == nil || childSessionID == "" {
		return 0
	}
	q := bs.store.Queue(childSessionID)
	if q == nil {
		return 0
	}
	n, err := q.Len()
	if err != nil {
		return 0
	}
	return n
}

func (bs *BackgroundSession) pdCachedMCPToolNames() []string {
	if bs.auxiliaryManager == nil || bs.workspaceUUID == "" {
		return nil
	}
	tools, ok := bs.auxiliaryManager.GetCachedMCPTools(bs.workspaceUUID)
	if !ok {
		return nil
	}
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func (bs *BackgroundSession) pdGetUserData() (*session.UserData, error) {
	if bs.store == nil || bs.persistedID == "" {
		return nil, fmt.Errorf("store not available")
	}
	return bs.store.GetUserData(bs.persistedID)
}

func (bs *BackgroundSession) pdSessionCtx() context.Context { return bs.ctx }

func (bs *BackgroundSession) pdHasProcessorManager() bool { return bs.processorManager != nil }

func (bs *BackgroundSession) pdApplyProcessors(ctx context.Context, input *processors.ProcessorInput) (*processors.ProcessorResult, error) {
	return bs.processorManager.Apply(ctx, input)
}

func (bs *BackgroundSession) pdWorkspaceProcessorArgOverrides() map[string]map[string]string {
	return bs.workspaceProcessorArgOverrides
}

func (bs *BackgroundSession) pdPersistProcessorActivation() {
	if bs.store == nil || bs.persistedID == "" {
		return
	}
	_, procActivations, procLastAt, _ := bs.GetProcessorStats()
	_ = bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		m.ProcessorActivations = procActivations
		m.ProcessorLastActivation = procLastAt
	})
}

func (bs *BackgroundSession) pdBuildPromptWithHistory(message string) string {
	return bs.buildPromptWithHistory(message)
}

// === New in 2.5-c ===

func (bs *BackgroundSession) pdHasSharedProcess() bool { return bs.sharedProcess != nil }

// pdSharedProcessHistory reports whether this session's shared process has
// previously completed at least one successful session RPC, corroborating
// -32603 "query closed" handshake-failure diagnosis (mitto-azk). Backed by
// MCPInitDone(), which latches on the first successful session/new or
// session/load and is NOT reset by Restart() — a process recycle keeps the
// prior latch, so a first-contact failure on a freshly-restarted process is
// still reported as "warm". This errs safely: it only ever removes the
// (possibly misleading) auth hint, never adds one, and the underlying agent
// did previously authenticate successfully in this Mitto run. Returns
// ProcessHistoryUnknown when no shared process is configured (legacy
// per-session process ownership) and ProcessHistoryCold/Warm otherwise.
func (bs *BackgroundSession) pdSharedProcessHistory() mittoAcp.ProcessHistory {
	if bs.sharedProcess == nil {
		return mittoAcp.ProcessHistoryUnknown
	}
	if bs.sharedProcess.MCPInitDone() {
		return mittoAcp.ProcessHistoryWarm
	}
	return mittoAcp.ProcessHistoryCold
}

func (bs *BackgroundSession) pdCompleteDeferredHandshake() error {
	return bs.completeDeferredHandshake()
}

// pdRecommendedHandshakeDeadline reads the extended-budget hint from the shared
// process so completeHandshakeOrAbort can bound a hung deferred session/new
// against it (mitto-f51). Returns 0 when no shared process is configured or the
// process signals no widening is needed.
func (bs *BackgroundSession) pdRecommendedHandshakeDeadline() time.Duration {
	if bs.sharedProcess == nil {
		return 0
	}
	bs.pendingSharedMu.Lock()
	hasMCP := len(bs.pendingSharedMcpServers) > 0
	bs.pendingSharedMu.Unlock()
	return bs.sharedProcess.RecommendedLoadTimeout(hasMCP)
}

func (bs *BackgroundSession) pdHasRecorder() bool { return bs.recorder != nil }

func (bs *BackgroundSession) pdGetNextSeq() int64 { return bs.getNextSeq() }

func (bs *BackgroundSession) pdRefreshNextSeq() { bs.refreshNextSeq() }

func (bs *BackgroundSession) pdRecordErrorEvent(seq int64, msg string) error {
	return bs.recorder.RecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeError,
		Timestamp: time.Now(),
		Data:      session.ErrorData{Message: msg},
	})
}

// pdRestoreBaselineIfOverride restores the session's persisted baseline model
// if a per-prompt override is currently active. No-op otherwise. Exposed to
// promptDeps callers (e.g. handlePromptError's error-class-gated branch) that
// need to restore without also advancing/draining the queue.
func (bs *BackgroundSession) pdRestoreBaselineIfOverride() { bs.restoreBaselineIfOverride() }

// pdAuthGuidanceAlreadySurfaced reports whether the durable auth-expiry
// guidance has already been recorded for the current outage streak (mitto-6vs).
func (bs *BackgroundSession) pdAuthGuidanceAlreadySurfaced() bool {
	bs.authGuidanceMu.Lock()
	defer bs.authGuidanceMu.Unlock()
	return bs.authGuidanceSurfaced
}

// pdMarkAuthGuidanceSurfaced marks the durable auth-expiry guidance as
// recorded, so subsequent consecutive auth failures do not write duplicate
// transcript entries until the flag is cleared by a successful prompt.
func (bs *BackgroundSession) pdMarkAuthGuidanceSurfaced() {
	bs.authGuidanceMu.Lock()
	bs.authGuidanceSurfaced = true
	bs.authGuidanceMu.Unlock()
}

// pdClearAuthGuidanceSurfaced re-arms the auth-expiry guidance dedupe guard.
// Called on successful prompt completion so a future re-expiry surfaces a
// fresh durable record instead of staying silently suppressed forever.
func (bs *BackgroundSession) pdClearAuthGuidanceSurfaced() {
	bs.authGuidanceMu.Lock()
	bs.authGuidanceSurfaced = false
	bs.authGuidanceMu.Unlock()
}

// pdNotifyAgentAuthState invokes the onAgentAuthStateChanged hook, when set,
// reporting whether the agent's CLI currently requires re-authentication
// (mitto-3du). Called from handlePromptError's auth branch (required=true)
// and handlePromptSuccess (required=false, guarded by the prior-surfaced
// flag) to drive a workspace-scoped sidebar health pill via a global
// broadcast, so unattended/loop sessions with no attached client still
// surface the state.
func (bs *BackgroundSession) pdNotifyAgentAuthState(required bool) {
	if bs.onAgentAuthStateChanged != nil {
		bs.onAgentAuthStateChanged(bs.persistedID, bs.workspaceUUID, bs.workingDir, required)
	}
}

func (bs *BackgroundSession) pdResetPromptingStateForAbort() {
	bs.promptMu.Lock()
	bs.isPrompting = false
	bs.clearActiveDispatchLocked()
	bs.promptStartTime = time.Time{}
	bs.promptCond.Broadcast()
	bs.promptMu.Unlock()
}

func (bs *BackgroundSession) pdNotifyStreamingStateChanged(active bool) {
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, active)
	}
}

func (bs *BackgroundSession) pdHasACPConn() bool { return bs.acpConn != nil }

// Fresh-session setup already owns a prompt turn and applies its baseline
// synchronously. Do not spawn a competing startup model-selection worker.
type freshSessionModelDeps struct{ *BackgroundSession }

func (freshSessionModelDeps) cbApplyConfigConstraintsAsync(string) {}

func (bs *BackgroundSession) pdACPConnNewSession(ctx context.Context, cwd string) (string, error) {
	mcpServers := bs.sessionMCPServers
	if mcpServers == nil {
		mcpServers = []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	}
	freshSess, err := bs.acpConn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if freshSess.SessionId == "" {
		return "", &sessionError{"fresh ACP session returned an empty session ID"}
	}

	// Adopt before any model RPC: config and prompt dispatch must address the
	// same new session. WebClient itself has no session ID/binding to update;
	// authenticated MCP ownership is bound to persistedID, which is unchanged.
	bs.acpID = string(freshSess.SessionId)
	bs.markACPContextUnknown() // Only mark fresh after initialization succeeds.
	bs.hsPersistACPSessionID()
	bs.setSessionModes(freshSess.Modes)
	models, configID, _ := DeriveAgentModels(freshSess.ConfigOptions, freshSess.Models, bs.mittoConfig.EffectiveModelProfiles())
	bs.modelConfigId = configID // Clear an old ID when the new catalog uses the fallback.
	bs.callbackSink.setAgentModels(freshSessionModelDeps{bs}, models)
	baseline := bs.pdReadBaselineModel()
	if baseline != "" && models != nil && !models.Synthesized && models.CurrentModelId != baseline {
		bs.pdWriteOverrideActive(true) // Failed initialization must retain restoration intent.
		if err := bs.setActiveModelOnly(ctx, baseline); err != nil {
			return "", fmt.Errorf("initialize fresh ACP session baseline: %w", err)
		}
	}
	bs.pdWriteOverrideActive(false)
	bs.markACPContextFresh()
	return bs.acpID, nil
}

func (bs *BackgroundSession) pdGetAgentModels() *SessionModelState {
	return bs.AgentModels()
}

func (bs *BackgroundSession) pdResolveModelTags(modelName string) []string {
	if bs.mittoConfig == nil || modelName == "" {
		return nil
	}
	return bs.mittoConfig.ResolveModelTags(modelName)
}

func (bs *BackgroundSession) pdResolvePreferredModels(promptName string) []config.PromptPreferredModel {
	if bs.preferredModelsResolver == nil || promptName == "" {
		return nil
	}
	return bs.preferredModelsResolver(promptName, bs.workingDir)
}

// pdModelProfiles exposes the model profiles used to resolve PromptPreferredModel
// entries by name/tag. It returns the user-configured profiles (Settings → Models)
// unioned with the canonical DefaultModelProfiles as a fallback, so well-known tags
// (e.g. "Coding", "Cheap") always resolve even when settings.json omits `models:`.
func (bs *BackgroundSession) pdModelProfiles() []config.ModelProfile {
	return bs.mittoConfig.EffectiveModelProfiles()
}

func (bs *BackgroundSession) pdResolvePromptParameters(promptName string) []config.PromptParameter {
	if bs.promptParametersResolver == nil || promptName == "" {
		return nil
	}
	return bs.promptParametersResolver(promptName, bs.workingDir)
}

func (bs *BackgroundSession) pdCacheGetArg(promptName, paramName string) (string, bool) {
	return bs.promptArgCache.Get(promptName, paramName)
}

func (bs *BackgroundSession) pdCacheSetArg(promptName, paramName, value string, ttl time.Duration) {
	bs.promptArgCache.Set(promptName, paramName, value, ttl)
}

func (bs *BackgroundSession) pdReadBaselineModel() string {
	bs.modelMu.Lock()
	defer bs.modelMu.Unlock()
	return bs.baselineModel
}

func (bs *BackgroundSession) pdWriteOverrideActive(active bool) {
	bs.modelMu.Lock()
	bs.overrideActive = active
	bs.modelMu.Unlock()
}

func (bs *BackgroundSession) pdSetActiveModelOnly(ctx context.Context, modelID string) error {
	return bs.setActiveModelOnly(ctx, modelID)
}

func (bs *BackgroundSession) pdRecordSessionChange(kind, value, previousValue string) {
	bs.cmRecordSessionChange(kind, value, previousValue)
}

// pdRecordSessionChangeWithSeq (mitto-c36) is the seq-aware sibling of
// pdRecordSessionChange used by createFreshContextSession to emit the
// "context_cleared" pill with a caller-reserved seq allocated in PromptWithMeta
// before the user-prompt seq. Passing a zero seq is a caller bug — the
// dispatcher falls back to the plain seq-allocating variant in that case.
func (bs *BackgroundSession) pdRecordSessionChangeWithSeq(seq int64, kind, value, previousValue string) {
	bs.cmRecordSessionChangeWithSeq(seq, kind, value, previousValue)
}

// === New in 2.5-d ===

func (bs *BackgroundSession) pdSetLastUsage(usage *acp.Usage) {
	bs.lastUsageMu.Lock()
	bs.lastUsage = usage
	bs.lastUsageMu.Unlock()
}

func (bs *BackgroundSession) pdAccumulateTokenUsage(tokens int) {
	bs.processorManager.AccumulateTokenUsage(tokens)
}

func (bs *BackgroundSession) pdAccumulateCumulativeUsage(usage *acp.Usage) {
	if usage == nil {
		return
	}
	bs.cumInputTokens.Add(int64(usage.InputTokens))
	bs.cumOutputTokens.Add(int64(usage.OutputTokens))
	bs.cumTotalTokens.Add(int64(usage.TotalTokens))
}

func (bs *BackgroundSession) pdEstimateTokensFromMessage(msg string) int {
	return processors.EstimateTokens(msg)
}

func (bs *BackgroundSession) pdReadLastAgentMessage() string {
	if bs.store == nil {
		return ""
	}
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		return ""
	}
	return session.GetLastAgentMessage(events)
}

// pdDismissActiveUIPrompt dismisses any active blocking UI prompt. See
// promptDeps.pdDismissActiveUIPrompt (mitto-nisb).
func (bs *BackgroundSession) pdDismissActiveUIPrompt() {
	bs.DismissActiveUIPrompt()
}

func (bs *BackgroundSession) pdMarkPromptComplete() {
	bs.promptMu.Lock()
	bs.isPrompting = false
	bs.clearActiveDispatchLocked()
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()
}

func (bs *BackgroundSession) pdIsClosed() bool {
	return bs.IsClosed()
}

func (bs *BackgroundSession) pdFlushMarkdown() {
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}
}

func (bs *BackgroundSession) pdObserverCount() int {
	return bs.ObserverCount()
}

func (bs *BackgroundSession) pdGetEventCount() int {
	return bs.GetEventCount()
}

func (bs *BackgroundSession) pdFlushPendingConfig() {
	bs.flushPendingConfig()
}

func (bs *BackgroundSession) pdProcessNextQueuedMessage() bool {
	return bs.processNextQueuedMessage()
}

func (bs *BackgroundSession) pdRetryTitleGenerationIfNeeded(message string) {
	bs.retryTitleGenerationIfNeeded(message)
}

func (bs *BackgroundSession) pdActionButtonsEnabled() bool {
	return bs.actionButtonsConfig.IsEnabled()
}

func (bs *BackgroundSession) pdReadLastAgentMessageFromStore() string {
	return bs.pdReadLastAgentMessage()
}

func (bs *BackgroundSession) pdHasImmediateQueuedMessages() bool {
	return bs.hasImmediateQueuedMessages()
}

func (bs *BackgroundSession) pdStartFollowUpAnalysis(userMessage, agentMessage string) {
	go bs.analyzeFollowUpQuestions(userMessage, agentMessage)
}

func (bs *BackgroundSession) pdApplyAfterProcessors(ctx context.Context, message, senderID, stopReason string,
	startedAt, endedAt time.Time, resp acp.PromptResponse, agentIdle bool,
) {
	if bs.processorManager != nil {
		bs.applyAfterProcessors(ctx, message, senderID, stopReason, startedAt, endedAt, resp, agentIdle)
	}
}

func (bs *BackgroundSession) pdOnTurnIdle() {
	if bs.onTurnIdle != nil {
		bs.onTurnIdle(bs.persistedID)
	}
}

func (bs *BackgroundSession) pdIsSelfDestructRequested() bool {
	return bs.IsSelfDestructRequested()
}

func (bs *BackgroundSession) pdTriggerSelfDestruct() {
	if bs.onSelfDestruct != nil {
		go bs.onSelfDestruct(bs.persistedID)
	}
}

// === New in 2.5-e ===

func (bs *BackgroundSession) pdIsACPDead() bool {
	acpDead := false
	if bs.acpConn != nil {
		select {
		case <-bs.acpConn.Done():
			acpDead = true
		default:
		}
	} else if bs.sharedProcess != nil {
		select {
		case <-bs.sharedProcess.Done():
			acpDead = true
		default:
		}
	}
	if !acpDead && bs.acpProcessDone != nil {
		select {
		case <-bs.acpProcessDone:
			acpDead = true
		default:
		}
	}
	return acpDead
}

func (bs *BackgroundSession) pdCanRestartACP() bool {
	return bs.canRestartACP()
}

func (bs *BackgroundSession) pdGetRestartInfo() string {
	return bs.getRestartInfo()
}

func (bs *BackgroundSession) pdRestartACPProcess() error {
	return bs.restartACPProcess(mittoAcp.RestartReasonCrashDuringStream)
}

func (bs *BackgroundSession) pdReacquirePromptingState() {
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptMu.Unlock()
}

// === New in mitto-2tm ===

func (bs *BackgroundSession) pdContextFlushCommand() string { return bs.ContextFlushCommand() }

func (bs *BackgroundSession) pdFlushContextInPlace(ctx context.Context) error {
	err := bs.flushContextInPlace(ctx)
	if err == nil {
		// The in-place flush succeeded: the session is now provably empty (mitto-s9g2).
		bs.markACPContextFresh()
	}
	return err
}

// === New in mitto-s9g2: skip redundant FreshContext clear on a virgin session ===

func (bs *BackgroundSession) pdContextIsEmpty() bool { return bs.acpContextIsEmpty() }

// Cold-start diagnostics (mitto-3mv WI-2). Delegates to the nil-safe helper.
func (bs *BackgroundSession) pdColdPhase(name string, kv ...any) { bs.coldPhase(name, kv...) }

// peekLoopContinuation reports whether the current dispatch is an uninterrupted
// continuation (a scheduled loop run directly following another one) WITHOUT mutating
// the marker. The marker is advanced separately at the dispatch point of no return so that
// early-return/rejected dispatches do not corrupt the continuation chain.
func (bs *BackgroundSession) peekLoopContinuation(isScheduledLoop bool) bool {
	bs.loopContinuationMu.Lock()
	defer bs.loopContinuationMu.Unlock()
	return isScheduledLoop && bs.lastTurnScheduledLoop
}

// advanceLoopContinuation records whether the just-committed dispatch was a scheduled
// loop run, so the next dispatch can detect an uninterrupted continuation. Setting it
// false (any non-scheduled dispatch: user prompt, forced run, FreshContext) breaks the chain.
func (bs *BackgroundSession) advanceLoopContinuation(isScheduledLoop bool) {
	bs.loopContinuationMu.Lock()
	bs.lastTurnScheduledLoop = isScheduledLoop
	bs.loopContinuationMu.Unlock()
}

// ResetLoopContinuation clears the continuation marker so the next loop run renders
// the verbose form. Called on lifecycle boundaries that break the "agent just finished that
// exact task and still holds the context" assumption while keeping the same BackgroundSession:
// ACP process reinit/restart and loop config changes (create/update/pause/re-enable).
// Boundaries that recreate the BackgroundSession reset it for free.
func (bs *BackgroundSession) ResetLoopContinuation() {
	bs.loopContinuationMu.Lock()
	bs.lastTurnScheduledLoop = false
	bs.loopContinuationMu.Unlock()
}
