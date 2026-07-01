package conversation

// Prompt dispatch cluster for BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

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
// This is called by the server setup code (same resolver used by PeriodicRunner).
func (bs *BackgroundSession) SetPromptResolver(resolver PromptResolver) {
	bs.promptResolver = resolver
}

// PeriodicKind classifies how a periodic prompt was triggered so the dispatch path can
// distinguish a normal scheduled/onCompletion delivery from a manual "run now" without
// matching the magic SenderID string. PeriodicKindNone means the prompt is not a
// periodic run (user/other sender).
type PeriodicKind int

const (
	PeriodicKindNone      PeriodicKind = iota // not a periodic run
	PeriodicKindScheduled                     // normal scheduled / onCompletion delivery
	PeriodicKindForced                        // manual "run now"
)

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID         string          // Unique identifier of the sending client (for broadcast deduplication)
	PromptID         string          // Client-generated prompt ID (for delivery confirmation)
	PromptName       string          // Name of workspace prompt (resolved to full text before ACP; empty for ad-hoc prompts)
	ImageIDs         []string        // IDs of images attached to the prompt
	FileIDs          []string        // IDs of files attached to the prompt
	OnComplete       func(err error) // Called when the async prompt goroutine finishes (nil = success)
	IsPeriodicForced bool            // True when this periodic prompt was triggered manually via "run now"
	// PeriodicKind classifies a periodic run (none/scheduled/forced). Set by the
	// PeriodicRunner. Drives the Iteration.IsUninterrupted continuation signal.
	PeriodicKind PeriodicKind
	// IterationNumber is the 0-based index of the current periodic run (periodic.IterationCount
	// at dispatch). Zero for non-periodic prompts. Feeds the {{ .Iteration.* }} template namespace.
	IterationNumber int
	// MaxIterations is the configured maximum number of periodic runs (0 = unlimited).
	MaxIterations int
	// IterationUninterrupted feeds {{ .Iteration.IsUninterrupted }}: true only on a
	// scheduled, non-forced, non-FreshContext periodic run that directly follows another
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
//   - Best-effort: the caller MUST continue with the main periodic prompt regardless of
//     any returned error.
//   - Works for both direct-conn (acpConn) and shared-process (sharedProcess) sessions.
func (bs *BackgroundSession) flushContextInPlace(ctx context.Context) error {
	cmd := strings.TrimSpace(bs.contextFlushCommand)
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
// agent-native context-flush command (e.g. "/clear") through the normal prompt
// path. It runs asynchronously like any other prompt. Returns an error when no
// flush command is configured for this session's ACP server, or when the session
// is closed. Callers (e.g. the REST handler) should gate on IsPrompting() to
// avoid issuing a flush while a turn is in flight.
func (bs *BackgroundSession) FlushContext() error {
	cmd := strings.TrimSpace(bs.contextFlushCommand)
	if cmd == "" {
		return &sessionError{"context flush command not configured for this server"}
	}
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	return bs.PromptWithMeta(cmd, PromptMeta{SenderID: "context-flush"})
}

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	// Periodic continuation signal (mitto-5xjn): peek BEFORE resolveAndSubstitute so the
	// prompt-body template ({{ if .Iteration.IsUninterrupted }}) renders against it. We
	// only PEEK here (no mutation); the marker is advanced at the dispatch point of no
	// return below, so rejected/early-return dispatches never corrupt the chain.
	isScheduledPeriodic := meta.PeriodicKind == PeriodicKindScheduled && !meta.FreshContext
	meta.IterationUninterrupted = bs.peekPeriodicContinuation(isScheduledPeriodic)

	// Resolve prompt name, apply argument substitution, annotate meta.
	// See promptDispatcher.resolveAndSubstitute for the full logic.
	var (
		argCount int
		err      error
	)
	message, argCount, meta, err = bs.promptDisp.resolveAndSubstitute(bs, message, meta)
	if err != nil {
		bs.notifyObservers(func(o SessionObserver) { o.OnError(err.Error()) })
		return err
	}

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
				if err := bs.restartACPProcess(RestartReasonCrashDuringPrompt); err != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + err.Error() + ". Please switch to another conversation and back to retry."
					if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = formatClassifiedError(classified)
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
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptCount++
	bs.TouchActivity()

	// Check if we need to inject conversation history (first prompt of resumed session).
	// FreshContext suppresses history injection so each periodic run starts clean.
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

	// Point of no return: this dispatch is committed. Advance the periodic continuation
	// marker so the NEXT dispatch can detect an uninterrupted continuation. A non-scheduled
	// dispatch (user/forced/FreshContext) sets it false, breaking the chain (mitto-5xjn).
	bs.advancePeriodicContinuation(isScheduledPeriodic)

	// Notify about streaming state change (prompt started)
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, true)
	}

	// Load images and files, build content blocks + session refs.
	// See promptDispatcher.buildAttachmentBlocks for the full logic.
	contentBlocks, imageRefs, fileRefs := bs.promptDisp.buildAttachmentBlocks(bs, imageIDs, fileIDs)

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Clear cached plan state when new prompt starts
	// The existing plan becomes stale; a new plan will be generated for this prompt
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, nil)
	}

	// Persist user prompt with image/file references and prompt ID.
	// Seq is pre-assigned from the shared getNextSeq() counter so that the user-prompt
	// event is ordered atomically with respect to any concurrent streaming events.
	// This avoids the duplicate/out-of-order seq bug caused by AppendEvent assigning
	// seq independently from the in-memory counter.
	var userPromptSeq int64
	if bs.recorder != nil {
		userPromptSeq = bs.getNextSeq()
		var recordOpts []session.RecordOption
		if len(meta.Meta) > 0 {
			recordOpts = append(recordOpts, session.WithMetaMap(meta.Meta))
		}
		if err := bs.recorder.RecordUserPromptCompleteWithSeq(userPromptSeq, message, imageRefs, fileRefs, meta.PromptID, meta.PromptName, argCount, recordOpts...); err != nil && bs.logger != nil {
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
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings, meta.PromptName, argCount)
	})

	// Build processor input and assemble final content blocks.
	// See promptDispatcher.buildProcessorInput + applyProcessorsAndBuildBlocks.
	processorInput := bs.promptDisp.buildProcessorInput(bs, message, isFirst, meta)
	finalBlocks := bs.promptDisp.applyProcessorsAndBuildBlocks(bs, processorInput, message, contentBlocks, shouldInjectHistory)

	// Run prompt in background
	go func() {
		// autoRetried guards a single automatic retry after an ACP crash during
		// streaming. On the first crash we restart the process and jump back to
		// retryPrompt; if the retry also crashes we fall through to the normal
		// "please resend" message instead of looping forever.
		autoRetried := false

		// Complete the deferred handshake, create a fresh-context session if requested,
		// and apply any per-prompt model preference.
		// See promptDispatcher.completeHandshakeOrAbort, createFreshContextSession, applyModelPreference.
		if !bs.promptDisp.completeHandshakeOrAbort(bs) {
			return
		}
		freshContextSessionID := bs.promptDisp.createFreshContextSession(bs, meta)
		bs.promptDisp.applyModelPreference(bs, meta)

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
		// Reset the inactivity flag for this attempt (a goto retryPrompt reuses it).
		inactivityWatchdogFired.Store(false)
		// Create a prompt context that gets cancelled when the ACP process dies.
		// This ensures we fail fast instead of waiting for the ACP server's internal
		// 60-second control request timeout when the CLI subprocess has crashed.
		// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
		promptCtx, promptCancel = context.WithCancel(bs.ctx)
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
			go func() {
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
			}()
		}

		// Monitor for a live-but-unresponsive agent: if the agent stops streaming any
		// updates for the configured window (and is not blocked on a UI prompt), cancel
		// the prompt so is_prompting clears and the user can resend. This catches the
		// "stuck, still responding" state that the process-death/connection monitors miss.
		bs.startPromptInactivityWatchdog(promptCtx, promptCancel, &inactivityWatchdogFired)
		bs.startAgentWorkingHeartbeat(promptCtx)

		// On retry after ACP crash, freshContextSessionID is from the old (dead)
		// connection; fall back to bs.acpID which holds the new session.
		acpSessionIDForPrompt := bs.acpID
		if freshContextSessionID != "" && !autoRetried {
			acpSessionIDForPrompt = freshContextSessionID
		}

		promptStartedAt = time.Now() // captured for after-phase processors
		if bs.sharedProcess != nil {
			promptResp, err = bs.sharedProcess.Prompt(promptCtx, acp.SessionId(acpSessionIDForPrompt), finalBlocks)
		} else {
			promptResp, err = bs.acpConn.Prompt(promptCtx, acp.PromptRequest{
				SessionId: acp.SessionId(acpSessionIDForPrompt),
				Prompt:    finalBlocks,
			})
		}
		promptCancel()             // cancel context to unblock the health-monitor goroutine
		promptEndedAt = time.Now() // captured for after-phase processors

		bs.promptDisp.accumulateTokenUsage(bs, promptResp, message)

		if bs.promptDisp.markPromptCompleteAndFlush(bs) {
			return
		}

		// Notify all observers
		eventCount := bs.GetEventCount()
		observerCount := bs.ObserverCount()
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_notify_start",
				"session_id", bs.persistedID,
				"event_count", eventCount,
				"observer_count", observerCount)
		}

		// sessionIdle becomes true only on the success path when the turn ended and
		// no further queued message was dispatched. It gates the on-completion periodic
		// idle hook invoked after OnComplete below.
		sessionIdle := false

		if err != nil {
			if bs.promptDisp.handlePromptError(bs, err, &autoRetried, observerCount, inactivityWatchdogFired.Load()) {
				goto retryPrompt
			}
		} else {
			sessionIdle = bs.promptDisp.handlePromptSuccess(bs, eventCount, observerCount, promptResp, message, meta, promptStartedAt, promptEndedAt)
		}

		bs.promptDisp.finalizeTurn(bs, err, meta, sessionIdle)
	}()

	return nil
}

// Cancel cancels the current prompt and resets the prompting state.
// This sends a cancel notification to the ACP agent and resets the isPrompting flag
// so the session can accept new prompts even if the agent doesn't respond to the cancel.
func (bs *BackgroundSession) Cancel() error {
	// Dismiss any active UI prompt first (MCP tool questions, permissions, etc.)
	// This ensures the UI is cleaned up when the user presses Stop.
	bs.DismissActiveUIPrompt()

	// Reset prompting state regardless of whether cancel succeeds
	// This ensures the session can accept new prompts even if the agent is unresponsive
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	// Send cancel notification to ACP agent (best effort)
	var cancelErr error
	if bs.sharedProcess != nil {
		cancelErr = bs.sharedProcess.Cancel(bs.ctx, acp.SessionId(bs.acpID))
	} else if bs.acpConn != nil {
		cancelErr = bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
			SessionId: acp.SessionId(bs.acpID),
		})
	}

	// Apply any config changes deferred during the cancelled turn now that the
	// session is idle.
	if wasPrompting {
		bs.flushPendingConfig()
	}

	return cancelErr
}

// ForceReset forcefully resets the session's prompting state.
// This is used when the agent is completely unresponsive and Cancel doesn't work.
// It resets the isPrompting flag, flushes any buffered content, and notifies observers.
// Unlike Cancel, this does NOT send a cancel notification to the agent.
func (bs *BackgroundSession) ForceReset() {
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	if !wasPrompting {
		if bs.logger != nil {
			bs.logger.Debug("ForceReset called but session was not prompting")
		}
		return
	}

	// Flush any buffered content
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}

	// Notify observers that the prompt was forcefully reset
	eventCount := bs.GetEventCount()
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPromptComplete(eventCount)
	})

	// Apply any config changes deferred during the reset turn now that the session
	// is idle (best effort; the RPC fails fast if the agent connection is dead).
	bs.flushPendingConfig()

	if bs.logger != nil {
		bs.logger.Warn("Session forcefully reset due to unresponsive agent")
	}
}

// =============================================================================
// promptDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) pdPromptResolver() PromptResolver { return bs.promptResolver }
func (bs *BackgroundSession) pdWorkingDir() string             { return bs.workingDir }

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

func (bs *BackgroundSession) pdIsChildPrompting(childSessionID string) bool {
	if bs.isChildPrompting == nil {
		return false
	}
	return bs.isChildPrompting(childSessionID)
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

func (bs *BackgroundSession) pdCompleteDeferredHandshake() error {
	return bs.completeDeferredHandshake()
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

func (bs *BackgroundSession) pdResetPromptingStateForAbort() {
	bs.promptMu.Lock()
	bs.isPrompting = false
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

func (bs *BackgroundSession) pdACPConnNewSession(ctx context.Context, cwd string) (string, error) {
	freshSess, err := bs.acpConn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{}, // Must be empty array, not nil — ACP validates this
	})
	if err != nil {
		return "", err
	}
	return string(freshSess.SessionId), nil
}

func (bs *BackgroundSession) pdGetAgentModels() *acp.UnstableSessionModelState {
	return bs.agentModels
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

// pdModelProfiles exposes the global model profiles (Settings → Models) so
// SelectPreferredModel can resolve PromptPreferredModel entries by name/tag.
func (bs *BackgroundSession) pdModelProfiles() []config.ModelProfile {
	if bs.mittoConfig == nil {
		return nil
	}
	return bs.mittoConfig.Models
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

// === New in 2.5-d ===

func (bs *BackgroundSession) pdSetLastUsage(usage *acp.Usage) {
	bs.lastUsageMu.Lock()
	bs.lastUsage = usage
	bs.lastUsageMu.Unlock()
}

func (bs *BackgroundSession) pdAccumulateTokenUsage(tokens int) {
	bs.processorManager.AccumulateTokenUsage(tokens)
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

func (bs *BackgroundSession) pdMarkPromptComplete() {
	bs.promptMu.Lock()
	bs.isPrompting = false
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
	return bs.restartACPProcess(RestartReasonCrashDuringStream)
}

func (bs *BackgroundSession) pdReacquirePromptingState() {
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptMu.Unlock()
}

// === New in mitto-2tm ===

func (bs *BackgroundSession) pdContextFlushCommand() string { return bs.contextFlushCommand }

func (bs *BackgroundSession) pdFlushContextInPlace(ctx context.Context) error {
	return bs.flushContextInPlace(ctx)
}

// peekPeriodicContinuation reports whether the current dispatch is an uninterrupted
// continuation (a scheduled periodic run directly following another one) WITHOUT mutating
// the marker. The marker is advanced separately at the dispatch point of no return so that
// early-return/rejected dispatches do not corrupt the continuation chain.
func (bs *BackgroundSession) peekPeriodicContinuation(isScheduledPeriodic bool) bool {
	bs.periodicContinuationMu.Lock()
	defer bs.periodicContinuationMu.Unlock()
	return isScheduledPeriodic && bs.lastTurnScheduledPeriodic
}

// advancePeriodicContinuation records whether the just-committed dispatch was a scheduled
// periodic run, so the next dispatch can detect an uninterrupted continuation. Setting it
// false (any non-scheduled dispatch: user prompt, forced run, FreshContext) breaks the chain.
func (bs *BackgroundSession) advancePeriodicContinuation(isScheduledPeriodic bool) {
	bs.periodicContinuationMu.Lock()
	bs.lastTurnScheduledPeriodic = isScheduledPeriodic
	bs.periodicContinuationMu.Unlock()
}

// ResetPeriodicContinuation clears the continuation marker so the next periodic run renders
// the verbose form. Called on lifecycle boundaries that break the "agent just finished that
// exact task and still holds the context" assumption while keeping the same BackgroundSession:
// ACP process reinit/restart and periodic loop config changes (create/update/pause/re-enable).
// Boundaries that recreate the BackgroundSession reset it for free.
func (bs *BackgroundSession) ResetPeriodicContinuation() {
	bs.periodicContinuationMu.Lock()
	bs.lastTurnScheduledPeriodic = false
	bs.periodicContinuationMu.Unlock()
}
