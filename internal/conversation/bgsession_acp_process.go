package conversation

// ACP process management cluster for BackgroundSession.

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/procstart"
	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

// maxACPStartRetries is the maximum number of times to retry starting the ACP process
// if the initial connection fails (e.g., "peer disconnected before response").
const maxACPStartRetries = 3

// acpStartRetryBaseDelay is the initial delay between ACP start retries.
const acpStartRetryBaseDelay = 500 * time.Millisecond

// acpStartRetryMaxDelay is the maximum delay between ACP start retries.
const acpStartRetryMaxDelay = 4 * time.Second

// acpStartRetryJitterRatio is the jitter ratio (±) applied to retry delays.
const acpStartRetryJitterRatio = 0.3

// acpInitializeAttemptTimeout is the per-attempt deadline for the ACP Initialize
// handshake in doStartACPProcess. Bounding this prevents dead sessions from hanging
// the full SDK-internal 60 s control timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT) on
// each retry. The existing conn.Done()/acpProcessDone watcher cancels initCtx on
// detected crashes; this timeout is the backstop for cases where neither signal
// arrives (e.g. a live-but-unresponsive process with open pipes).
//
// 25 s: generous for healthy cold starts (agent typically initialises in < 10 s)
// while cutting dead-session total retry tail from ~180 s (3×60 s) to ~90 s
// (3×25 s + backoffs). Do NOT increase toward 60 s. (mitto-13ck.2)
const acpInitializeAttemptTimeout = 25 * time.Second

// Note: Runtime restart constants (maxACPRestarts, acpRestartWindow,
// acpRestartBaseDelay, acpRestartMaxDelay) are now defined in
// acp_error_classification.go as shared constants (MaxACPRestarts, ACPRestartWindow,
// ACPRestartBaseDelay, ACPRestartMaxDelay) to ensure consistent behavior between
// SharedACPProcess and BackgroundSession.

// killACPProcess terminates the ACP process and cleans up resources.
// It handles both direct execution (acpCmd) and runner-based execution.
// In shared-process mode, it only unregisters this session from the MultiplexClient —
// it does NOT kill the shared OS process, which is owned by the ACPProcessManager.
func (bs *BackgroundSession) killACPProcess() {
	if bs.sharedProcess != nil {
		// Shared mode: we don't own the OS process.
		// Just unregister this session so it stops receiving events.
		if bs.acpID != "" {
			bs.sharedProcess.UnregisterSession(acp.SessionId(bs.acpID))
		}
		return
	}

	// Kill the entire process group to ensure all child processes are terminated.
	// Without this, child processes (e.g., "claude" spawned by "node claude-code-acp")
	// survive and become orphans.
	if bs.acpCmd != nil && bs.acpCmd.Process != nil {
		mittoAcp.KillProcessGroup(bs.acpCmd.Process.Pid)
	}

	// Call wait() to clean up resources (from runner.RunWithPipes or cmd.Wait)
	// This is safe to call even if the process is already dead
	if bs.acpWait != nil {
		bs.acpWait()
		bs.acpWait = nil // Prevent double cleanup
	}
}

// canRestartACP checks if we can restart the ACP process based on rate limiting.
// Returns true if restart is allowed, false if we've exceeded the limit.
// This method is thread-safe.
func (bs *BackgroundSession) canRestartACP() bool {
	return bs.procCtl.canRestart(bs.logger, bs.persistedID)
}

// recordRestart records a restart attempt for rate limiting and telemetry.
// This method is thread-safe.
func (bs *BackgroundSession) recordRestart(reason mittoAcp.RestartReason) {
	bs.procCtl.recordRestart(reason, bs.logger, bs.persistedID)
}

// getRestartInfo returns a human-readable restart attempt indicator like "(attempt 2 of 3)".
// This is shown to the user so they understand the system is in a retry loop and won't retry forever.
// This method is thread-safe.
func (bs *BackgroundSession) getRestartInfo() string {
	return bs.procCtl.getRestartInfo()
}

// GetRestartStats returns statistics about ACP process restarts for telemetry.
// This method is thread-safe.
func (bs *BackgroundSession) GetRestartStats() RestartStats {
	return bs.procCtl.stats()
}

// restartACPProcess attempts to restart the ACP process after it has died.
// It kills the old process, cleans up resources, and starts a new one.
// The new process will attempt to resume the ACP session if the agent supports it.
// The reason parameter is used for telemetry and diagnostics.
// Returns nil on success, or an error if restart fails.
// Returns an *mittoAcp.ACPClassifiedError for permanent failures.
func (bs *BackgroundSession) restartACPProcess(reason mittoAcp.RestartReason) error {
	// Apply backoff based on how many recent restarts have occurred.
	recentCount := bs.procCtl.recentRestartCount()

	if recentCount > 0 {
		delay := mittoAcp.BackoffDelay(recentCount-1, mittoAcp.ACPRestartBaseDelay, mittoAcp.ACPRestartMaxDelay, acpStartRetryJitterRatio)
		if bs.logger != nil {
			bs.logger.Info("Waiting before ACP restart",
				"delay", delay.String(),
				"recent_restarts", recentCount,
				"session_id", bs.persistedID,
				"command", bs.acpCommand,
				"cwd", bs.acpCwd)
		}
		select {
		case <-bs.ctx.Done():
			return &sessionError{"context cancelled during restart backoff"}
		case <-time.After(delay):
		}
	}

	if bs.logger != nil {
		bs.logger.Info("Restarting ACP process",
			"session_id", bs.persistedID,
			"acp_id", bs.acpID,
			"restart_count", bs.procCtl.totalRestarts()+1,
			"reason", string(reason),
			"command", bs.acpCommand,
			"cwd", bs.acpCwd)
	}

	// Unregister from global MCP server before killing the old process.
	// Without this, the re-registration fails with "session already registered".
	bs.stopSessionMcpServer()

	// Kill the old process (per-session) or unregister from MultiplexClient (shared).
	bs.killACPProcess()

	// Close the old ACP client if it exists
	if bs.acpClient != nil {
		bs.acpClient.Close()
		bs.acpClient = nil
	}

	// Clear the old connection
	bs.acpConn = nil

	// Breaking the loop continuation: an ACP reinit disrupts agent context, so the next
	// loop run must render the verbose form (mitto-5xjn).
	bs.ResetLoopContinuation()

	// Record this restart attempt with reason
	bs.recordRestart(reason)

	var err error
	if bs.sharedProcess != nil {
		// Shared mode: restart the shared OS process, then create a new session on it.
		// Note: multiple sessions may call Restart() concurrently; SharedACPProcess.canRestart()
		// is rate-limited so only one restart happens, others get the already-restarted process.

		// Save the shared process reference before attempting session creation.
		// resumeSharedACPSession nils bs.sharedProcess on failure (to clean up for
		// initial session creation), but during restart we must preserve it so future
		// prompts can trigger another restart attempt instead of getting permanently
		// stuck with "The AI agent is still starting up".
		savedSharedProcess := bs.sharedProcess

		if restartErr := bs.sharedProcess.Restart(); restartErr != nil {
			// Log but don't fail — the process may have been restarted by another session.
			if bs.logger != nil {
				bs.logger.Warn("Shared ACP process restart returned error, attempting new session anyway",
					"session_id", bs.persistedID,
					"error", restartErr)
			}
		}
		err = bs.resumeSharedACPSession(bs.sharedProcess, bs.workingDir, bs.acpID)

		// Restore the shared process reference if session creation failed.
		// This prevents the session from becoming a permanent zombie — future
		// prompts will still detect the dead connection and can retry.
		if err != nil && bs.sharedProcess == nil {
			bs.sharedProcess = savedSharedProcess
		}
	} else {
		// Per-session mode: start a new ACP process, attempting to resume the session.
		err = bs.startACPProcess(bs.acpCommand, bs.acpCwd, bs.workingDir, bs.acpID)
	}
	if err != nil {
		// If the restart failed with a permanent (non-retryable) error, trip the circuit
		// breaker so canRestartACP() returns false immediately on all future calls.
		// This prevents the sliding-window timer from resetting and allowing further
		// futile retry cycles (e.g. "write |1: file already closed" pipe errors).
		if classified, ok := err.(*mittoAcp.ACPClassifiedError); ok && !classified.IsRetryable() {
			bs.procCtl.markPermanentlyFailed()
			if bs.logger != nil {
				bs.logger.Warn("ACP restart returned permanent error, circuit breaker opened",
					"session_id", bs.persistedID,
					"error_class", classified.Class.String(),
					"user_message", classified.UserMessage)
			}
		}
		if bs.logger != nil {
			logAttrs := []any{
				"session_id", bs.persistedID,
				"error", err,
			}
			if classified, ok := err.(*mittoAcp.ACPClassifiedError); ok {
				logAttrs = append(logAttrs,
					"error_class", classified.Class.String(),
					"user_message", classified.UserMessage,
					"user_guidance", classified.UserGuidance)
			}
			bs.logger.Error("Failed to restart ACP process", logAttrs...)
		}
		return err
	}

	// Update the ACP session ID in metadata if it changed
	if bs.store != nil && bs.acpID != "" {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to update ACP session ID after restart", "error", err)
		}
	}

	if bs.logger != nil {
		bs.logger.Info("ACP process restarted successfully",
			"session_id", bs.persistedID,
			"acp_id", bs.acpID,
			"command", bs.acpCommand)
	}

	return nil
}

// startACPProcess starts the ACP server process and initializes the connection.
// If acpSessionID is provided and the agent supports session loading, it attempts
// to resume that session. Otherwise, it creates a new session.
// The acpCwd parameter sets the working directory for the ACP process itself.
// This method includes retry logic with exponential backoff for transient failures.
// Permanent errors (missing module, command not found, etc.) skip retries.
// Returns an *mittoAcp.ACPClassifiedError when the error has been classified.
func (bs *BackgroundSession) startACPProcess(acpCommand, acpCwd, workingDir, acpSessionID string) error {
	var lastErr error
	var lastClassified *mittoAcp.ACPClassifiedError

	for attempt := 0; attempt < maxACPStartRetries; attempt++ {
		if attempt > 0 {
			delay := mittoAcp.BackoffDelay(attempt-1, acpStartRetryBaseDelay, acpStartRetryMaxDelay, acpStartRetryJitterRatio)
			if bs.logger != nil {
				bs.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxACPStartRetries,
					"delay", delay.String(),
					"last_error", lastErr,
					"error_class", lastClassified.Class.String(),
					"command", acpCommand,
					"cwd", acpCwd)
			}
			// Wait before retry with exponential backoff.
			select {
			case <-bs.ctx.Done():
				return &sessionError{"context cancelled during retry: " + bs.ctx.Err().Error()}
			case <-time.After(delay):
			}
		}

		stderr, processErr := bs.doStartACPProcess(acpCommand, acpCwd, workingDir, acpSessionID)
		if processErr == nil {
			return nil
		}
		lastErr = processErr

		// Classify the error to determine if retrying is worthwhile.
		lastClassified = mittoAcp.ClassifyACPError(processErr, stderr)

		if bs.logger != nil {
			bs.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"max_attempts", maxACPStartRetries,
				"error", processErr,
				"error_class", lastClassified.Class.String(),
				"command", acpCommand,
				"cwd", acpCwd)
		}

		// Don't retry permanent errors — they won't resolve by retrying.
		if !lastClassified.IsRetryable() {
			if bs.logger != nil {
				bs.logger.Error("ACP process start failed with permanent error, skipping retries",
					"error", processErr,
					"user_message", lastClassified.UserMessage,
					"user_guidance", lastClassified.UserGuidance,
					"command", acpCommand,
					"cwd", acpCwd)
			}
			return lastClassified
		}
	}

	// All retries exhausted — return the classified error if available.
	if lastClassified != nil {
		return lastClassified
	}
	return lastErr
}

// The stderr collector, per-agent pattern types/compiler, stderr monitor, and
// startup watchdog live in the leaf subpackage internal/acpproc/procstart so
// both internal/conversation and internal/acpproc can consume them without an
// import cycle (mitto-iuw2).

// promptInactivityWatchdogWarnDelay is the idle duration (no streamed agent activity)
// after which the prompt inactivity watchdog emits a WARN log. Non-destructive.
// Exposed as a var so tests can override it.
var promptInactivityWatchdogWarnDelay = 2 * time.Minute

// promptInactivityWatchdogTimeoutNanos holds the configured prompt inactivity
// watchdog cancellation timeout, in nanoseconds. It is an atomic.Int64 (rather than
// a plain time.Duration var) because it can be updated at runtime from a live config
// change (see SetPromptInactivityTimeout) while watchdog goroutines started by prior
// prompts concurrently read it. Zero means automatic cancellation is DISABLED — the
// watchdog is WARN-only. This avoids ever cancelling a legitimate long-running tool
// call that produces no intermediate streamed output (the residual false-positive of
// an automatic cancel). The zero value (disabled) is the safe default before startup
// config wiring calls SetPromptInactivityTimeout; production wires a 10m default via
// SessionConfig.ParseAgentInactivityTimeout.
var promptInactivityWatchdogTimeoutNanos atomic.Int64

// SetPromptInactivityTimeout sets the process-wide duration of no streamed agent
// activity after which the prompt inactivity watchdog cancels an in-flight prompt,
// clearing is_prompting and surfacing a recoverable error. Zero disables automatic
// cancellation (WARN-only). Safe to call concurrently with running watchdog
// goroutines, e.g. from a live settings update.
func SetPromptInactivityTimeout(d time.Duration) {
	promptInactivityWatchdogTimeoutNanos.Store(int64(d))
}

// promptInactivityWatchdogTimeout returns the currently configured watchdog
// cancellation timeout. See SetPromptInactivityTimeout.
func promptInactivityWatchdogTimeout() time.Duration {
	return time.Duration(promptInactivityWatchdogTimeoutNanos.Load())
}

// signalAgentActivity records the current time as the most recent streamed agent
// activity. It is called on every ACP SessionUpdate so the prompt inactivity watchdog
// can distinguish a working agent from a wedged one.
func (bs *BackgroundSession) signalAgentActivity() {
	now := time.Now().UnixNano()
	bs.lastAgentActivityAt.Store(now)
	bs.lastStreamActivityAt.Store(now)
	// Cold-start diagnostics (mitto-3mv): mark the first token of the first
	// prompt after activation. One-shot; nil-safe when no trace is active.
	bs.coldPhaseFirstToken()
}

// trackToolCallStatus records a tool call's status transition so the prompt
// inactivity watchdog can tell when the agent is legitimately blocked on an
// in-flight tool. A tool call is considered in flight from its first non-terminal
// status (pending/in_progress) until a terminal status (completed/failed) is seen.
// Unknown/empty statuses are treated as non-terminal (in flight) — failing toward
// suppressing the warning, which is the desired behavior for a WARN-only signal.
// title, when non-empty, is recorded alongside the in-flight entry so the agent
// working heartbeat can surface which tool the agent is blocked on.
func (bs *BackgroundSession) trackToolCallStatus(id, title, status string) {
	if id == "" {
		return
	}
	bs.inFlightToolCallsMu.Lock()
	defer bs.inFlightToolCallsMu.Unlock()
	switch status {
	case string(acp.ToolCallStatusCompleted), string(acp.ToolCallStatusFailed):
		delete(bs.inFlightToolCalls, id)
		delete(bs.inFlightToolTitles, id)
	default:
		if bs.inFlightToolCalls == nil {
			bs.inFlightToolCalls = make(map[string]struct{})
		}
		bs.inFlightToolCalls[id] = struct{}{}
		if title != "" {
			if bs.inFlightToolTitles == nil {
				bs.inFlightToolTitles = make(map[string]string)
			}
			bs.inFlightToolTitles[id] = title
		}
	}
}

// currentInFlightToolTitle returns the title of any in-flight tool call, or "".
func (bs *BackgroundSession) currentInFlightToolTitle() string {
	bs.inFlightToolCallsMu.Lock()
	defer bs.inFlightToolCallsMu.Unlock()
	for _, t := range bs.inFlightToolTitles {
		if t != "" {
			return t
		}
	}
	return ""
}

// hasInFlightToolCall reports whether at least one tool call is currently in flight.
func (bs *BackgroundSession) hasInFlightToolCall() bool {
	bs.inFlightToolCallsMu.Lock()
	defer bs.inFlightToolCallsMu.Unlock()
	return len(bs.inFlightToolCalls) > 0
}

// resetInFlightToolCalls clears the in-flight tool-call set. Called at prompt start
// so stale entries (e.g. a tool call whose terminal update was lost) never carry
// over and permanently suppress the watchdog warning across prompts.
func (bs *BackgroundSession) resetInFlightToolCalls() {
	bs.inFlightToolCallsMu.Lock()
	defer bs.inFlightToolCallsMu.Unlock()
	bs.inFlightToolCalls = nil
	bs.inFlightToolTitles = nil
}

// startPromptInactivityWatchdog launches a background goroutine that watches for a
// live-but-unresponsive agent during a prompt. Unlike the process-death and
// connection-EOF monitors, this catches the case where the agent stays alive with an
// open connection but stops streaming any updates (the "stuck, still responding"
// state the user sees in the UI).
//
// The watchdog resets its idle baseline to now, then on each tick:
//   - returns when ctx is done (the prompt completed or was cancelled elsewhere);
//   - pauses (resets the baseline) while a UI prompt is active, since permission
//     dialogs and MCP tool questions legitimately block the agent on user input;
//   - pauses (resets the baseline) while a tool call is in flight, since a long-running
//     tool that streams no intermediate updates is the agent working, not a wedged agent;
//   - emits a WARN log once the idle time crosses promptInactivityWatchdogWarnDelay;
//   - sets fired and calls cancel() once the idle time crosses
//     promptInactivityWatchdogTimeout, unblocking the prompt RPC so is_prompting clears.
//
// The goroutine is torn down via ctx.Done(); callers cancel the prompt context after
// Prompt() returns. It is a no-op when both delays are non-positive.
func (bs *BackgroundSession) startPromptInactivityWatchdog(ctx context.Context, cancel context.CancelFunc, fired *atomic.Bool) {
	warnDelay := promptInactivityWatchdogWarnDelay
	timeout := promptInactivityWatchdogTimeout()
	if warnDelay <= 0 && timeout <= 0 {
		return
	}

	// Establish the idle baseline at prompt start, and clear any stale in-flight
	// tool-call tracking carried over from a prior prompt.
	bs.lastAgentActivityAt.Store(time.Now().UnixNano())
	bs.resetInFlightToolCalls()

	// Tick frequently enough to detect the threshold with reasonable granularity
	// (a quarter of the smaller delay), with a small floor to bound overhead. In
	// production the delays are tens of seconds, so the floor never applies; it only
	// guards against pathologically small configured values.
	interval := timeout
	if interval <= 0 || (warnDelay > 0 && warnDelay < interval) {
		interval = warnDelay
	}
	interval /= 4
	if interval < 25*time.Millisecond {
		interval = 25 * time.Millisecond
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		warned := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Pause while the agent is legitimately blocked on a UI prompt
				// (permission dialog or MCP tool question) or waiting on an in-flight
				// tool call (a long-running tool may stream no intermediate updates).
				// Reset the baseline so the idle clock starts fresh once it resumes.
				if bs.GetActiveUIPrompt() != nil || bs.hasInFlightToolCall() {
					bs.lastAgentActivityAt.Store(time.Now().UnixNano())
					warned = false
					continue
				}

				idle := time.Since(time.Unix(0, bs.lastAgentActivityAt.Load()))

				if timeout > 0 && idle >= timeout {
					if bs.logger != nil {
						bs.logger.Error("Agent unresponsive during prompt — no streamed activity within inactivity window, cancelling prompt",
							"session_id", bs.persistedID,
							"idle", idle.Round(time.Second).String(),
							"timeout", timeout.String())
					}
					fired.Store(true)
					cancel()
					return
				}

				if warnDelay > 0 && !warned && idle >= warnDelay {
					warned = true
					if bs.logger != nil {
						// Cold-start diagnostics (mitto-3mv): attach a host-contention
						// snapshot so slowness can be correlated with concurrent load
						// (num_goroutine, load1, concurrent_prompting, live_acp_processes).
						attrs := []any{
							"session_id", bs.persistedID,
							"idle", idle.Round(time.Second).String(),
							"warn_delay", warnDelay.String(),
						}
						attrs = append(attrs, coldstart.Contention().LogAttrs()...)
						bs.logger.Warn("Agent slow during prompt — no streamed activity observed", attrs...)
					}
				}
			}
		}
	}()
}

// agentWorkingHeartbeatInterval is how often, during a prompt, the heartbeat is
// re-evaluated. agentWorkingHeartbeatQuietThreshold is the minimum idle time (no
// streamed activity) before a heartbeat is emitted, so it fires only during genuine
// silence and never during active streaming. Vars so tests can override them.
var agentWorkingHeartbeatInterval = 15 * time.Second
var agentWorkingHeartbeatQuietThreshold = 10 * time.Second

// startAgentWorkingHeartbeat launches a per-prompt goroutine that emits a transient
// "agent is still working" heartbeat to observers while the agent is alive and working
// but streaming no updates (e.g. blocked on a long silent tool call). Tied to ctx, so
// it stops when the prompt completes/cancels or the ACP process/connection dies.
func (bs *BackgroundSession) startAgentWorkingHeartbeat(ctx context.Context) {
	interval := agentWorkingHeartbeatInterval
	if interval <= 0 {
		return
	}
	quiet := agentWorkingHeartbeatQuietThreshold
	// Establish the heartbeat's own idle baseline. Unlike the watchdog, this baseline
	// is advanced only by real streamed activity (signalAgentActivity), never reset by
	// a tool-call/UI-prompt pause, so the reported idle grows monotonically through a
	// long silent tool call.
	bs.lastStreamActivityAt.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if bs.GetActiveUIPrompt() != nil {
					continue
				}
				idle := time.Since(time.Unix(0, bs.lastStreamActivityAt.Load()))
				if idle < quiet {
					continue
				}
				data := AgentWorkingData{IdleMs: idle.Milliseconds(), ToolTitle: bs.currentInFlightToolTitle()}
				bs.notifyObservers(func(o SessionObserver) {
					if w, ok := o.(AgentWorkingObserver); ok {
						w.OnAgentWorking(data)
					}
				})
			}
		}
	}()
}

// doStartACPProcess performs a single attempt to start the ACP process.
// Returns the error and any captured stderr output for error classification.
func (bs *BackgroundSession) doStartACPProcess(acpCommand, acpCwd, workingDir, acpSessionID string) (string, error) {
	if bs.logger != nil {
		bs.logger.Info("Starting ACP process",
			"command", acpCommand,
			"cwd", acpCwd,
			"working_dir", workingDir,
			"acp_session_id", acpSessionID)
	}

	// Parse command using shell-aware tokenization FIRST,
	// then expand $MITTO_* references in each arg individually.
	// This preserves paths with spaces as single arguments.
	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return "", &sessionError{err.Error()}
	}
	mittoEnv := mittoAcp.BuildMittoEnv(bs.persistedID, workingDir, "", "")
	agentHintEnv := mittoAcp.BuildAgentHintEnv("")
	expandedArgs := mittoAcp.ExpandArgs(args, mittoEnv)
	if bs.logger != nil {
		changedIndices := make([]int, 0)
		for i, orig := range args {
			if orig != expandedArgs[i] {
				changedIndices = append(changedIndices, i)
			}
		}
		if len(changedIndices) > 0 {
			bs.logger.Debug("expanded MITTO_* vars in ACP command args",
				"changed_indices", changedIndices,
				"changed_count", len(changedIndices),
				"session_id", bs.persistedID)
		}
	}
	args = expandedArgs
	// Expand cwd (single string, not shlex-parsed)
	originalCwd := acpCwd
	acpCwd = mittoAcp.ExpandCommand(acpCwd, mittoEnv)
	if acpCwd != originalCwd && bs.logger != nil {
		bs.logger.Debug("expanded MITTO_* vars in ACP cwd",
			"session_id", bs.persistedID)
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	// Create stderr collector to capture output for error reporting
	// Keep last 8KB of stderr output
	stderrCollector := procstart.NewStderrCollector(8192, bs.logger)
	// Install per-agent ignore patterns (mitto-k6h) so matching writes are
	// suppressed from the debug-level stderr log. Nil is a safe no-op.
	if bs.stderrPatterns != nil {
		stderrCollector.SetIgnorePatterns(bs.stderrPatterns.Ignore)
	}

	// Pre-create the process death detection channel so the stderr monitor
	// (started below) can signal crash detection immediately.
	// The channel will be wired into the wait function wrapper after the process starts.
	bs.acpProcessDone = make(chan struct{})
	bs.acpProcessDoneOnce = sync.Once{}

	// Create the crash detection callback for the stderr monitor (Fix C).
	// When the stderr monitor detects crash patterns from the SDK (e.g., "EOF received
	// from CLI stdout"), this callback closes acpProcessDone immediately — bypassing
	// the SDK's 60-second control request timeout.
	onCrashDetected := func() {
		if bs.logger != nil {
			bs.logger.Warn("ACP subprocess crash detected via stderr patterns",
				"session_id", bs.persistedID)
		}
		bs.acpProcessDoneOnce.Do(func() {
			close(bs.acpProcessDone)
		})
	}

	// Startup watchdog: warn/error if no stderr activity and no Initialize completion
	// within the configured windows. Cancelled when doStartACPProcess returns.
	watchdogCtx, watchdogCancel := context.WithCancel(bs.ctx)
	defer watchdogCancel()
	var signalStartupActivity func()

	// Use runner if configured, otherwise direct execution
	if bs.runner != nil {
		// Use restricted runner with RunWithPipes
		// Note: acpCwd is not supported with restricted runners
		if acpCwd != "" && bs.logger != nil {
			bs.logger.Warn("cwd is not supported with restricted runners, ignoring",
				"cwd", acpCwd,
				"runner_type", bs.runner.Type())
		}
		if bs.logger != nil {
			bs.logger.Info("starting ACP process through restricted runner",
				"runner_type", bs.runner.Type(),
				"command", acpCommand)
		}
		// Pass the same env layering used by the direct-exec branch so server-specific
		// vars reach the runner-spawned process. agentHintEnv (AGENT_MODE=1) sits
		// below serverEnv so per-server settings can override it.
		runnerEnv := procstart.BuildACPProcessEnv(bs.serverEnv, mittoEnv, agentHintEnv)
		stdin, stdout, stderr, wait, err = bs.runner.RunWithPipes(bs.ctx, args[0], args[1:], runnerEnv)
		if err != nil {
			return "", &sessionError{"failed to start with runner: " + err.Error()}
		}

		signalStartupActivity = procstart.StartACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", -1)

		// Monitor stderr in background (with crash detection for Fix C and watchdog wake-up).
		// BackgroundSession's own ACP process (non-shared path) does not multiplex sessions,
		// so the MCP-init callbacks are unused here — the extended-budget policy lives on
		// SharedACPProcess where MCP servers are actually attached (mitto-8ul.1).
		// Per-session (non-shared) path: no saturation counter on this side,
		// so onDegraded is nil — degraded stderr chunks are captured in the buffer
		// but do not feed a rolling-window signal (there is no shared process to
		// promote/recycle here).
		procstart.StartStderrMonitor(stderr, stderrCollector, onCrashDetected, signalStartupActivity, nil, nil, nil, bs.stderrPatterns)

		// Store wait function for cleanup
		// We'll call it in Close() method
		bs.acpCmd = nil // No cmd when using runner
	} else {
		// Direct execution (no restrictions)
		cmd = exec.CommandContext(bs.ctx, args[0], args[1:]...)
		// Create a new process group so we can kill all child processes on Close().
		// Without this, child processes (e.g., "claude" spawned by "node claude-code-acp")
		// become orphans when we kill only the direct child.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		// Set working directory for the ACP process if specified
		if acpCwd != "" {
			cmd.Dir = acpCwd
			if bs.logger != nil {
				bs.logger.Info("setting ACP process working directory",
					"cwd", acpCwd,
					"command", acpCommand)
			}
		}

		stdin, err = cmd.StdinPipe()
		if err != nil {
			return "", &sessionError{"failed to create stdin pipe: " + err.Error()}
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return "", &sessionError{"failed to create stdout pipe: " + err.Error()}
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return "", &sessionError{"failed to create stderr pipe: " + err.Error()}
		}

		// Set environment variables for the ACP subprocess: agent hints + server-specific
		// env from settings.json + MITTO_* vars (same layering as the runner branch).
		cmd.Env = procstart.BuildACPProcessEnv(bs.serverEnv, mittoEnv, agentHintEnv)

		if err := cmd.Start(); err != nil {
			return "", &sessionError{"failed to start ACP server: " + err.Error()}
		}

		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		signalStartupActivity = procstart.StartACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", pid)

		// Monitor stderr in background (same as runner case, with crash detection for Fix C
		// and watchdog wake-up on first stderr activity). MCP-init callbacks are unused on
		// the non-shared BackgroundSession path (mitto-8ul.1).
		// See rationale at the sibling StartStderrMonitor callsite in the shared
		// process fallback above: onDegraded is nil on the non-shared path.
		procstart.StartStderrMonitor(stderrPipe, stderrCollector, onCrashDetected, signalStartupActivity, nil, nil, nil, bs.stderrPatterns)

		bs.acpCmd = cmd

		// Create wait function for direct execution
		wait = func() error {
			return cmd.Wait()
		}
	}

	// Store wait function for cleanup and wire process death detection.
	//
	// Fix A: The acpProcessDone channel was pre-created above (before stderr monitors)
	// so that the stderr crash detector (Fix C) can signal it immediately.
	// Here we wrap the wait function to ALSO close acpProcessDone when the OS process
	// exits (either via killACPProcess or natural termination).
	//
	// Fix A+C combined detection strategy:
	// 1. Stderr crash patterns (Fix C) — instant detection when inner CLI dies
	//    (the SDK logs "EOF received from CLI stdout" to stderr immediately)
	// 2. OS process liveness polling (Fix A) — 2-second detection when ACP process exits
	// 3. Wait function wrapper (Fix A) — detection when killACPProcess() is called
	// 4. acpConn.Done() (existing) — fallback via JSON-RPC pipe EOF detection
	origWait := wait
	bs.acpWait = func() error {
		err := origWait()

		// Log exit code and signal for crash telemetry
		if err != nil && bs.logger != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				logAttrs := []any{
					"exit_code", exitErr.ExitCode(),
					"session_id", bs.persistedID,
				}
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						logAttrs = append(logAttrs, "signal", status.Signal().String())
					}
				}

				// Log at DEBUG if we intentionally killed it, WARN if it crashed on its own
				if bs.ctx.Err() != nil {
					bs.logger.Debug("ACP process exited (intentional shutdown)", logAttrs...)
				} else {
					bs.logger.Warn("ACP process exited abnormally", logAttrs...)
				}
			} else {
				// Non-ExitError wait failures (shouldn't happen in practice)
				if bs.ctx.Err() != nil {
					bs.logger.Debug("ACP process wait error (intentional shutdown)",
						"error", err,
						"session_id", bs.persistedID)
				} else {
					bs.logger.Warn("ACP process wait error",
						"error", err,
						"session_id", bs.persistedID)
				}
			}
		}

		bs.acpProcessDoneOnce.Do(func() {
			close(bs.acpProcessDone)
		})
		return err
	}

	// Start process liveness monitor for direct-exec processes.
	// This polls the process every 2 seconds using kill(pid, 0) which checks if the
	// process exists without actually sending a signal. When the process is gone,
	// we close acpProcessDone immediately — providing much faster detection than
	// waiting for the pipe EOF to propagate through the JSON-RPC layer.
	if cmd != nil && cmd.Process != nil {
		processDoneCh := bs.acpProcessDone
		processDoneOnce := &bs.acpProcessDoneOnce
		pid := cmd.Process.Pid
		sessionCtx := bs.ctx
		logger := bs.logger
		sessionID := bs.persistedID
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-processDoneCh:
					// Already signaled (e.g., by killACPProcess calling acpWait)
					return
				case <-sessionCtx.Done():
					return
				case <-ticker.C:
					// Check if process is still alive using kill(pid, 0).
					// This returns an error if the process doesn't exist.
					err := syscall.Kill(pid, 0)
					if err != nil {
						if logger != nil {
							logger.Warn("ACP process no longer alive (detected by liveness check)",
								"pid", pid,
								"error", err,
								"session_id", sessionID)
						}
						processDoneOnce.Do(func() {
							close(processDoneCh)
						})
						return
					}
				}
			}
		}()
	}

	// Create web client with callbacks that route to attached client or persist.
	// BackgroundSession implements SeqProvider, so seq is assigned at ACP receive time.
	bs.acpClient = NewWebClient(bs.buildWebClientConfig())

	// Wrap stdout with a JSON line filter to discard non-JSON output
	// (e.g., ANSI escape sequences, terminal UI from crashed agents)
	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, bs.logger)

	// Create ACP connection with filtered stdout
	bs.acpConn = acp.NewClientSideConnection(bs.acpClient, stdin, filteredStdout)
	if bs.logger != nil {
		// Use a downgraded logger for the SDK to convert INFO to DEBUG and
		// downgrade specific ERROR messages (malformed JSONRPC during crashes) to WARN.
		// This prevents verbose SDK logs (e.g., "peer connection closed") from
		// appearing in stdout when log level is INFO, and prevents misleading ERROR
		// logs for expected crash recovery scenarios.
		bs.acpConn.SetLogger(logging.DowngradeACPSDKErrors(bs.logger))
	}

	// Create an init context with a per-attempt timeout (mitto-13ck.2).
	// This bounds each Initialize attempt so a dead-session doesn't hang the full
	// SDK-internal 60 s control timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT) on every
	// retry, cutting the total retry tail from ~180 s to ~90 s (3×25 s + backoffs).
	// The conn.Done()/acpProcessDone watcher below cancels initCtx immediately on
	// detected crashes; the timeout is the backstop for cases where neither signal
	// arrives (live-but-hung process with open pipes). 25 s is generous for healthy
	// cold starts.
	initCtx, initCancel := context.WithTimeout(bs.ctx, acpInitializeAttemptTimeout)
	defer initCancel()

	// Monitor ACP process health: if the connection's Done() channel closes
	// or the OS process exits (acpProcessDone), cancel the init context immediately.
	go func() {
		select {
		case <-bs.acpConn.Done():
			if bs.logger != nil {
				bs.logger.Warn("ACP connection closed during initialization, cancelling",
					"session_id", bs.persistedID)
			}
			initCancel()
		case <-bs.acpProcessDone:
			if bs.logger != nil {
				bs.logger.Warn("ACP process exited during initialization, cancelling",
					"session_id", bs.persistedID)
			}
			initCancel()
		case <-initCtx.Done():
			// Initialization completed normally or was cancelled for another reason
		}
	}()

	// Initialize and get agent capabilities
	initResp, err := bs.acpConn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		// Give stderr goroutine a moment to capture any error output
		time.Sleep(100 * time.Millisecond)

		// Log the failure with command and stderr output
		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if bs.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"cwd", acpCwd,
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		bs.killACPProcess()
		return stderrOutput, &sessionError{"failed to initialize: " + err.Error()}
	}

	// Log agent information at DEBUG level
	bs.logAgentInfo(initResp)

	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}

	// Build MCP servers list based on session settings and agent capabilities
	mcpServers := bs.startSessionMcpServer(bs.store, initResp.AgentCapabilities)

	// Try to resume/load existing session if we have an ACP session ID
	if acpSessionID != "" {
		caps := initResp.AgentCapabilities
		supportsResume := caps.SessionCapabilities.Resume != nil
		supportsLoad := caps.LoadSession

		// Try Resume first (fast path)
		if supportsResume {
			resumeCtx, resumeCancel := context.WithTimeout(initCtx, 10*time.Second)
			resumeResp, err := bs.acpConn.ResumeSession(resumeCtx, acp.ResumeSessionRequest{
				SessionId:  acp.SessionId(acpSessionID),
				Cwd:        cwd,
				McpServers: mcpServers,
			})
			resumeCancel()
			if err == nil {
				bs.acpID = acpSessionID
				bs.resumeMethod = "resume"
				bs.setSessionModes(resumeResp.Modes)
				models, cfgId := ModelStateFromConfigOptions(resumeResp.ConfigOptions)
				bs.setAgentModels(models)
				if cfgId != "" {
					bs.modelConfigId = cfgId
				}
				if bs.logger != nil {
					bs.logger.Info("Resumed ACP session",
						"acp_session_id", acpSessionID,
						"resume_method", "resume")
					bs.logSessionModes(resumeResp.Modes)
					bs.logAgentModels(models)
				}
				return "", nil
			}
			// Log resume failure and fall through to Load
			logFields := []any{
				"acp_session_id", acpSessionID,
				"error", err,
				"method", "resume",
			}
			if resumeCtx.Err() == context.DeadlineExceeded {
				logFields = append(logFields, "timeout", true)
			}
			if bs.logger != nil {
				bs.logger.Info("Resume failed, will try Load or New", logFields...)
			}
		}

		// Fallback to Load (slow path with history replay)
		if supportsLoad {
			// Suppress event processing during Load to prevent notification queue overflow.
			// The agent replays the entire conversation history as notifications; with large
			// sessions this can exceed the SDK's 1024-entry queue before the consumer
			// (markdown conversion + persistence) can drain it. The events are historical
			// and already persisted, so discarding them is safe.
			bs.acpClient.SetLoadingSession(true)
			loadCtx, loadCancel := context.WithTimeout(initCtx, 30*time.Second)
			loadResp, err := bs.acpConn.LoadSession(loadCtx, acp.LoadSessionRequest{
				SessionId:  acp.SessionId(acpSessionID),
				Cwd:        cwd,
				McpServers: mcpServers,
			})
			loadCancel()
			bs.acpClient.SetLoadingSession(false)
			if err == nil {
				bs.acpID = acpSessionID
				bs.resumeMethod = "load"
				// Store available modes from session load
				bs.setSessionModes(loadResp.Modes)
				models, cfgId := ModelStateFromConfigOptions(loadResp.ConfigOptions)
				bs.setAgentModels(models)
				if cfgId != "" {
					bs.modelConfigId = cfgId
				}
				if bs.logger != nil {
					bs.logger.Info("Resumed ACP session using load (with history replay)",
						"acp_session_id", acpSessionID,
						"resume_method", "load")
					bs.logSessionModes(loadResp.Modes)
					bs.logAgentModels(bs.agentModels)
				}
				return "", nil
			}
			// Log load failure and fall through to New
			logFields := []any{
				"acp_session_id", acpSessionID,
				"error", err,
				"method", "load",
			}
			if loadCtx.Err() == context.DeadlineExceeded {
				logFields = append(logFields, "timeout", true)
			}
			if bs.logger != nil {
				bs.logger.Warn("Load failed, creating new session", logFields...)
			}
		}
	}

	// Create new session (final fallback)
	bs.resumeMethod = "new"

	// Create new session
	sessResp, err := bs.acpConn.NewSession(initCtx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	if err != nil {
		// Give stderr goroutine a moment to capture any error output
		time.Sleep(100 * time.Millisecond)

		// Log the failure with command and stderr output
		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if bs.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"cwd", acpCwd,
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP session creation failed", logAttrs...)
		}

		bs.killACPProcess()
		return stderrOutput, &sessionError{"failed to create session: " + err.Error()}
	}

	bs.acpID = string(sessResp.SessionId)

	// Store available modes from session setup
	bs.setSessionModes(sessResp.Modes)
	models, cfgId := ModelStateFromConfigOptions(sessResp.ConfigOptions)
	bs.setAgentModels(models)
	if cfgId != "" {
		bs.modelConfigId = cfgId
	}

	if bs.logger != nil {
		bs.logger.Info("Created new ACP session",
			"acp_session_id", bs.acpID,
			"command", acpCommand,
			"resume_method", bs.resumeMethod)
		bs.logSessionModes(sessResp.Modes)
		bs.logAgentModels(bs.agentModels)
	}

	// Notify observers that ACP is now ready to accept prompts.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStarted()
	})

	return "", nil
}
