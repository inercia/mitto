package conversation

// ACP process management cluster for BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
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
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	// Circuit breaker: a permanent error (or lifetime cap) has already tripped this flag.
	// Once set, no further restart attempts are made — the sliding window is irrelevant.
	if bs.permanentlyFailed {
		if bs.logger != nil {
			bs.logger.Debug("canRestartACP: permanently failed, circuit breaker open",
				"session_id", bs.persistedID,
				"total_restarts", bs.restartCount)
		}
		return false
	}

	// Lifetime cap: even for transient errors, don't restart more than MaxACPTotalRestarts
	// times in total. This prevents infinite retry cycles where the sliding window keeps
	// resetting every ACPRestartWindow (e.g. dead pipe, repeatedly failing cold-start).
	if bs.restartCount >= MaxACPTotalRestarts {
		bs.permanentlyFailed = true
		if bs.logger != nil {
			bs.logger.Warn("canRestartACP: lifetime restart cap reached, circuit breaker opened",
				"session_id", bs.persistedID,
				"total_restarts", bs.restartCount,
				"max_total_restarts", MaxACPTotalRestarts)
		}
		return false
	}

	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)

	// Filter out old restart times and corresponding reasons (keep indices in sync)
	var recentRestarts []time.Time
	var recentReasons []RestartReason
	for i, t := range bs.restartTimes {
		if t.After(cutoff) {
			recentRestarts = append(recentRestarts, t)
			// Keep reasons in sync with times
			if i < len(bs.restartReasons) {
				recentReasons = append(recentReasons, bs.restartReasons[i])
			}
		}
	}
	bs.restartTimes = recentRestarts
	bs.restartReasons = recentReasons

	return len(recentRestarts) < MaxACPRestarts
}

// recordRestart records a restart attempt for rate limiting and telemetry.
// This method is thread-safe.
func (bs *BackgroundSession) recordRestart(reason RestartReason) {
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	bs.restartCount++
	now := time.Now()
	bs.restartTimes = append(bs.restartTimes, now)
	bs.restartReasons = append(bs.restartReasons, reason)

	// Log restart reason for telemetry
	if bs.logger != nil {
		bs.logger.Info("Recording ACP restart",
			"session_id", bs.persistedID,
			"restart_count", bs.restartCount,
			"reason", string(reason),
			"timestamp", now.Format(time.RFC3339))
	}
}

// getRestartInfo returns a human-readable restart attempt indicator like "(attempt 2 of 3)".
// This is shown to the user so they understand the system is in a retry loop and won't retry forever.
// This method is thread-safe.
func (bs *BackgroundSession) getRestartInfo() string {
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)
	count := 0
	for _, t := range bs.restartTimes {
		if t.After(cutoff) {
			count++
		}
	}
	// count is the number of recent restarts already done; the next one will be count+1
	return fmt.Sprintf("(attempt %d of %d)", count+1, MaxACPRestarts)
}

// RestartStats contains statistics about ACP process restarts.
type RestartStats struct {
	TotalRestarts   int                   // Total number of restarts in session lifetime
	RecentRestarts  int                   // Number of restarts in the current window
	ReasonCounts    map[RestartReason]int // Count of restarts by reason
	LastRestartTime time.Time             // Timestamp of most recent restart
	LastReason      RestartReason         // Reason for most recent restart
}

// GetRestartStats returns statistics about ACP process restarts for telemetry.
// This method is thread-safe.
func (bs *BackgroundSession) GetRestartStats() RestartStats {
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	stats := RestartStats{
		TotalRestarts: bs.restartCount,
		ReasonCounts:  make(map[RestartReason]int),
	}

	// Count recent restarts and reasons
	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)
	for i, t := range bs.restartTimes {
		if t.After(cutoff) {
			stats.RecentRestarts++
		}
		// Count all reasons (not just recent)
		if i < len(bs.restartReasons) {
			stats.ReasonCounts[bs.restartReasons[i]]++
		}
	}

	// Get last restart info
	if len(bs.restartTimes) > 0 {
		stats.LastRestartTime = bs.restartTimes[len(bs.restartTimes)-1]
		if len(bs.restartReasons) > 0 {
			stats.LastReason = bs.restartReasons[len(bs.restartReasons)-1]
		}
	}

	return stats
}

// restartACPProcess attempts to restart the ACP process after it has died.
// It kills the old process, cleans up resources, and starts a new one.
// The new process will attempt to resume the ACP session if the agent supports it.
// The reason parameter is used for telemetry and diagnostics.
// Returns nil on success, or an error if restart fails.
// Returns an *ACPClassifiedError for permanent failures.
func (bs *BackgroundSession) restartACPProcess(reason RestartReason) error {
	// Apply backoff based on how many recent restarts have occurred.
	bs.restartMu.Lock()
	recentCount := len(bs.restartTimes)
	bs.restartMu.Unlock()

	if recentCount > 0 {
		delay := BackoffDelay(recentCount-1, ACPRestartBaseDelay, ACPRestartMaxDelay, acpStartRetryJitterRatio)
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
			"restart_count", bs.restartCount+1,
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
		if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
			bs.restartMu.Lock()
			bs.permanentlyFailed = true
			bs.restartMu.Unlock()
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
			if classified, ok := err.(*ACPClassifiedError); ok {
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
// Returns an *ACPClassifiedError when the error has been classified.
func (bs *BackgroundSession) startACPProcess(acpCommand, acpCwd, workingDir, acpSessionID string) error {
	var lastErr error
	var lastClassified *ACPClassifiedError

	for attempt := 0; attempt < maxACPStartRetries; attempt++ {
		if attempt > 0 {
			delay := BackoffDelay(attempt-1, acpStartRetryBaseDelay, acpStartRetryMaxDelay, acpStartRetryJitterRatio)
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
		lastClassified = ClassifyACPError(processErr, stderr)

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

// doStartACPProcess performs a single attempt to start the ACP process.
// StderrCollector collects stderr output from the ACP process for error reporting.
// It stores the last N bytes of stderr output that can be retrieved when errors occur.
type StderrCollector struct {
	mu       sync.Mutex
	buffer   []byte
	maxSize  int
	logger   *slog.Logger
	isClosed bool
}

// NewStderrCollector creates a new stderr collector with the given max buffer size.
func NewStderrCollector(maxSize int, logger *slog.Logger) *StderrCollector {
	return &StderrCollector{
		buffer:  make([]byte, 0, maxSize),
		maxSize: maxSize,
		logger:  logger,
	}
}

// Write implements io.Writer to collect stderr output.
func (c *StderrCollector) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return len(p), nil
	}

	// Log at debug level as it comes in, suppressing harmless protocol noise.
	// The acp-go-sdk sends $/cancel_request (JSON-RPC LSP-style) which ACP agents
	// don't support; their "Method not found" rejection written to stderr is expected
	// and can be safely ignored. The SDK-level error log for this is already suppressed
	// in logging.go; this suppresses the agent-side stderr counterpart.
	if c.logger != nil && len(p) > 0 {
		output := string(p)
		if !strings.Contains(output, "$/cancel_request") {
			c.logger.Debug("agent stderr", "output", output)
		}
	}

	// Append to buffer, keeping only the last maxSize bytes
	c.buffer = append(c.buffer, p...)
	if len(c.buffer) > c.maxSize {
		c.buffer = c.buffer[len(c.buffer)-c.maxSize:]
	}

	return len(p), nil
}

// GetOutput returns the collected stderr output.
func (c *StderrCollector) GetOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buffer)
}

// Close marks the collector as closed and logs any remaining output at warn level if non-empty.
func (c *StderrCollector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isClosed = true
}

// stderrCrashPatterns are substrings in ACP process stderr output that indicate
// the inner CLI subprocess has crashed. When detected, we proactively signal
// process death via onCrashDetected callback rather than waiting for the SDK's
// 60-second control request timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT).
//
// Fix C: These patterns come from the claude-code-agent-sdk Rust layer which logs
// to stderr when the CLI subprocess dies unexpectedly.
var stderrCrashPatterns = []string{
	"stream ended unexpectedly",
	"EOF received from CLI stdout",
	"background reader: stream ended",
	"connection reset by peer",
	"broken pipe",
	// From acp-go-sdk's JSONRPC parser when receiving malformed messages from a dying process
	"received message with neither id nor method",
	// From acp-go-sdk's notification queue overflow handler (triggers when process is overwhelmed)
	"failed to queue notification; closing connection",
}

// StartStderrMonitor starts a goroutine that reads from stderr and writes to the collector.
// If onCrashDetected is non-nil, it is called (at most once) when crash patterns are
// detected in the stderr output, enabling early process death signaling.
// If onFirstActivity is non-nil, it is called (at most once) the first time any bytes
// are observed on stderr — used by the startup watchdog to detect "live" processes.
func StartStderrMonitor(stderr runner.ReadCloser, collector *StderrCollector, onCrashDetected func(), onFirstActivity func()) {
	go func() {
		crashSignaled := false
		activitySignaled := false
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				collector.Write(buf[:n])

				if !activitySignaled && onFirstActivity != nil {
					activitySignaled = true
					onFirstActivity()
				}

				// Fix C: Check for crash patterns in stderr output.
				// This detects inner CLI subprocess death immediately from SDK
				// stderr messages, bypassing the 60s control request timeout.
				if !crashSignaled && onCrashDetected != nil {
					chunk := string(buf[:n])
					for _, pattern := range stderrCrashPatterns {
						if strings.Contains(chunk, pattern) {
							crashSignaled = true
							onCrashDetected()
							break
						}
					}
				}
			}
			if readErr != nil {
				break
			}
		}
		collector.Close()
	}()
}

// acpStartupWatchdogWarnDelay is the delay before the startup watchdog emits a WARN log
// when no stderr activity has been observed and the ACP Initialize handshake has not completed.
// Exposed as a var so tests can override it.
var acpStartupWatchdogWarnDelay = 10 * time.Second

// acpStartupWatchdogErrorDelay is the delay before the startup watchdog emits an ERROR log
// when the process is still unresponsive.
var acpStartupWatchdogErrorDelay = 30 * time.Second

// StartACPStartupWatchdog runs a background goroutine that emits a WARN log if no stderr
// activity is observed within acpStartupWatchdogWarnDelay, and an ERROR log if the process
// is still unresponsive after acpStartupWatchdogErrorDelay. The returned signalActivity
// callback should be wired to stderr first-activity AND called when the Initialize
// handshake completes (success or failure); callers should also defer-cancel ctx so the
// watchdog is torn down when startup finishes. Returns a no-op if logger is nil.
func StartACPStartupWatchdog(ctx context.Context, logger *slog.Logger, command, acpServer string, pid int) func() {
	if logger == nil {
		return func() {}
	}
	activityCh := make(chan struct{})
	var once sync.Once
	signalActivity := func() { once.Do(func() { close(activityCh) }) }

	go func() {
		warnTimer := time.NewTimer(acpStartupWatchdogWarnDelay)
		errTimer := time.NewTimer(acpStartupWatchdogErrorDelay)
		defer warnTimer.Stop()
		defer errTimer.Stop()

		baseAttrs := []any{"command", command, "acp_server", acpServer}
		if pid > 0 {
			baseAttrs = append(baseAttrs, "pid", pid)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-activityCh:
				return
			case <-warnTimer.C:
				logger.Warn("ACP process appears unresponsive — no stderr output and no handshake observed in startup window",
					append(baseAttrs, "elapsed", acpStartupWatchdogWarnDelay.String())...)
			case <-errTimer.C:
				logger.Error("ACP process still unresponsive after extended startup window — handshake has not completed",
					append(baseAttrs, "elapsed", acpStartupWatchdogErrorDelay.String())...)
			}
		}
	}()

	return signalActivity
}

// promptInactivityWatchdogWarnDelay is the idle duration (no streamed agent activity)
// after which the prompt inactivity watchdog emits a WARN log. Non-destructive.
// Exposed as a var so tests can override it.
var promptInactivityWatchdogWarnDelay = 2 * time.Minute

// promptInactivityWatchdogTimeout is the idle duration (no streamed agent activity)
// after which the prompt inactivity watchdog cancels the in-flight prompt so the
// session can recover from a live-but-unresponsive agent (one that stops streaming
// without crashing — e.g. wedged during MCP init or GC-thrashing).
//
// Default 0: automatic cancellation is DISABLED — the watchdog is WARN-only out of
// the box. This avoids ever cancelling a legitimate long-running tool call that
// produces no intermediate streamed output (the residual false-positive of an
// automatic cancel). Set to a positive duration to opt in to automatic cancellation.
// Exposed as a var so tests can override it.
var promptInactivityWatchdogTimeout time.Duration = 0

// signalAgentActivity records the current time as the most recent streamed agent
// activity. It is called on every ACP SessionUpdate so the prompt inactivity watchdog
// can distinguish a working agent from a wedged one.
func (bs *BackgroundSession) signalAgentActivity() {
	bs.lastAgentActivityAt.Store(time.Now().UnixNano())
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
//   - emits a WARN log once the idle time crosses promptInactivityWatchdogWarnDelay;
//   - sets fired and calls cancel() once the idle time crosses
//     promptInactivityWatchdogTimeout, unblocking the prompt RPC so is_prompting clears.
//
// The goroutine is torn down via ctx.Done(); callers cancel the prompt context after
// Prompt() returns. It is a no-op when both delays are non-positive.
func (bs *BackgroundSession) startPromptInactivityWatchdog(ctx context.Context, cancel context.CancelFunc, fired *atomic.Bool) {
	warnDelay := promptInactivityWatchdogWarnDelay
	timeout := promptInactivityWatchdogTimeout
	if warnDelay <= 0 && timeout <= 0 {
		return
	}

	// Establish the idle baseline at prompt start.
	bs.lastAgentActivityAt.Store(time.Now().UnixNano())

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
				// (permission dialog or MCP tool question). Reset the baseline so the
				// idle clock starts fresh once the user responds.
				if bs.GetActiveUIPrompt() != nil {
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
						bs.logger.Warn("Agent slow during prompt — no streamed activity observed",
							"session_id", bs.persistedID,
							"idle", idle.Round(time.Second).String(),
							"warn_delay", warnDelay.String())
					}
				}
			}
		}
	}()
}

// BuildACPProcessEnv constructs the environment slice for an ACP subprocess.
// Keys are replaced in-place via mittoAcp.MergeEnv; precedence is:
//
//  1. os.Environ() — inherited from the Mitto process (lowest).
//  2. serverEnv — server-specific env from settings.json (acp_servers[].env).
//  3. mittoEnv — MITTO_* vars set by Mitto (highest precedence).
//
// This is shared between the direct-exec and restricted-runner branches so that
// the runner branch sees the same env as the non-runner branch.
func BuildACPProcessEnv(serverEnv map[string]string, mittoEnv map[string]string) []string {
	combined := make(map[string]string, len(serverEnv)+len(mittoEnv))
	for k, v := range serverEnv {
		combined[k] = v
	}
	for k, v := range mittoEnv {
		combined[k] = v // MITTO_* vars keep highest precedence
	}
	return mittoAcp.MergeEnv(os.Environ(), combined)
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
	StderrCollector := NewStderrCollector(8192, bs.logger)

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
		// vars reach the runner-spawned process.
		runnerEnv := BuildACPProcessEnv(bs.serverEnv, mittoEnv)
		stdin, stdout, stderr, wait, err = bs.runner.RunWithPipes(bs.ctx, args[0], args[1:], runnerEnv)
		if err != nil {
			return "", &sessionError{"failed to start with runner: " + err.Error()}
		}

		signalStartupActivity = StartACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", -1)

		// Monitor stderr in background (with crash detection for Fix C and watchdog wake-up)
		StartStderrMonitor(stderr, StderrCollector, onCrashDetected, signalStartupActivity)

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

		// Set environment variables for the ACP subprocess: server-specific env from
		// settings.json layered with MITTO_* vars (same layering as the runner branch).
		cmd.Env = BuildACPProcessEnv(bs.serverEnv, mittoEnv)

		if err := cmd.Start(); err != nil {
			return "", &sessionError{"failed to start ACP server: " + err.Error()}
		}

		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		signalStartupActivity = StartACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", pid)

		// Monitor stderr in background (same as runner case, with crash detection for Fix C
		// and watchdog wake-up on first stderr activity)
		StartStderrMonitor(stderrPipe, StderrCollector, onCrashDetected, signalStartupActivity)

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

	// Create an init context that gets cancelled when the ACP process dies.
	// This ensures we fail fast instead of waiting for the ACP server's internal
	// 60-second control request timeout when the CLI subprocess has crashed.
	// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
	initCtx, initCancel := context.WithCancel(bs.ctx)
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
		stderrOutput := strings.TrimSpace(StderrCollector.GetOutput())
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
			resumeResp, err := bs.acpConn.UnstableResumeSession(resumeCtx, acp.UnstableResumeSessionRequest{
				SessionId:  acp.SessionId(acpSessionID),
				Cwd:        cwd,
				McpServers: mcpServers,
			})
			resumeCancel()
			if err == nil {
				bs.acpID = acpSessionID
				bs.resumeMethod = "resume"
				bs.setSessionModes(resumeResp.Modes)
				bs.setAgentModels(resumeResp.Models)
				if bs.logger != nil {
					bs.logger.Info("Resumed ACP session using UNSTABLE resume API",
						"acp_session_id", acpSessionID,
						"resume_method", "resume")
					bs.logSessionModes(resumeResp.Modes)
					bs.logAgentModels(resumeResp.Models)
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
				bs.setAgentModels(StableToUnstableModelState(loadResp.Models))
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
		stderrOutput := strings.TrimSpace(StderrCollector.GetOutput())
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
	bs.setAgentModels(StableToUnstableModelState(sessResp.Models))

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
