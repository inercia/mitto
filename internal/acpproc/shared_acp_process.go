package acpproc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/runner"
)

const (
	// maxProcessStartRetries is the maximum number of times to retry starting the ACP process.
	maxProcessStartRetries = 3
	// processStartRetryBaseDelay is the initial delay between process start retries.
	processStartRetryBaseDelay = 500 * time.Millisecond
	// processStartRetryMaxDelay is the maximum delay between process start retries.
	processStartRetryMaxDelay = 4 * time.Second
	// processStartRetryJitterRatio is the jitter ratio (±) applied to retry delays.
	processStartRetryJitterRatio = 0.3

	// setSessionModelMaxAttempts is the maximum number of set_model RPC attempts per call.
	// Per-attempt deadline (8s) × 3 + jittered backoffs (≤900ms total) ≈ 25s per caller.
	// Do NOT increase — widening per-attempt deadlines is explicitly discouraged (mitto-f7q).
	setSessionModelMaxAttempts = 3
	// setSessionModelAttemptTimeout is the per-attempt timeout for set_model RPCs.
	// Each attempt gets a fresh 8s budget so a queued caller is not penalised by the wait.
	// Do NOT increase (mitto-f7q: Option 1 is explicitly discouraged).
	setSessionModelAttemptTimeout = 8 * time.Second
	// setSessionModelRetryBaseDelay is the base backoff between set_model retry attempts.
	setSessionModelRetryBaseDelay = 300 * time.Millisecond
	// setSessionModelRetryJitterRatio is the maximum jitter as a fraction of the base delay
	// added to each retry backoff. Jitter in [0, base×ratio) de-correlates concurrent callers
	// that would otherwise retry in lock-step (mitto-f7q, Option 3).
	// With ratio=0.5: attempt-2 delay ∈ [300ms, 450ms), attempt-3 ∈ [600ms, 750ms).
	// Total per-caller worst-case: 3×8s + 750ms ≈ 25s.
	setSessionModelRetryJitterRatio = 0.5

	// sessionCreateMaxAttempts is the maximum number of session/new RPC attempts per call.
	// Mirrors set_model's bounded-retry policy (mitto-4no7, parity with mitto-f7q).
	sessionCreateMaxAttempts = 3
	// sessionCreateAttemptTimeout is the per-attempt deadline for session/new RPCs.
	// Keeps the documented widened create deadline (was sessionCreationRPCTimeout=25s,
	// mitto-63o8) as a FRESH per-attempt budget so a single slow create is not regressed.
	sessionCreateAttemptTimeout = 25 * time.Second
	// sessionCreateRetryBaseDelay is the base backoff between session/new retry attempts.
	sessionCreateRetryBaseDelay = 300 * time.Millisecond
	// sessionCreateRetryJitterRatio is the max jitter as a fraction of the base delay added
	// to each retry backoff, de-correlating concurrent callers (mitto-4no7, mirrors set_model).
	// With ratio=0.5: attempt-2 delay ∈ [300ms,450ms), attempt-3 ∈ [600ms,750ms).
	sessionCreateRetryJitterRatio = 0.5

	// setModelAsyncCallerBudget is the context timeout given to the background goroutine
	// that performs the aux-session model switch asynchronously (mitto-f7q, Option 4).
	// Budget reasoning: the capacity-1 setModelSem may be held by up to ~3 concurrent callers,
	// each taking at most ~25s (3×8s + jitter). Semaphore wait ≤ 3×25s = 75s; adding slack
	// for our own retries gives ~100s worst-case. 90s covers the expected contention at server
	// wakeup (≤4 concurrent aux sessions) while avoiding an indefinite hang if the process
	// is unhealthy. m.ctx cancels on manager shutdown as a hard backstop.
	setModelAsyncCallerBudget = 90 * time.Second

	// auxModelSwitchStartupJitter is the maximum random startup delay applied to each
	// async aux-session set_model goroutine before it enters the budget context window
	// (mitto-xicp). When prewarmAuxiliarySessions fires all 4 purposes in parallel, each
	// spawns an async model-set goroutine at nearly the same instant; without this jitter
	// they all race onto the capacity-1 setModelSem simultaneously. With a 5 s jitter
	// window the goroutines are de-staggered so later arrivals are still well within the
	// 90 s setModelAsyncCallerBudget, eliminating the "context deadline exceeded" failures
	// observed during cold-process wakeup.
	//
	// This mirrors the child-session de-stagger pattern (constraintModelSwitchChildStartupJitter
	// in internal/conversation/bgsession_config.go, introduced for mitto-x4e). The jitter
	// waits on m.ctx — not the budget context — so it does NOT consume the 90 s budget.
	// Do NOT change the per-attempt 8 s deadline (mitto-f7q explicitly discourages that).
	auxModelSwitchStartupJitter = 5 * time.Second

	// Note: Runtime restart constants (maxProcessRestarts, processRestartWindow,
	// processRestartBaseDelay, processRestartMaxDelay) are now defined in
	// acp_error_classification.go as shared constants (conversation.MaxACPRestarts, conversation.ACPRestartWindow,
	// conversation.ACPRestartBaseDelay, conversation.ACPRestartMaxDelay) to ensure consistent behavior between
	// SharedACPProcess and conversation.BackgroundSession.
)

// auxStartupJitter returns a random duration in [0, max) to de-stagger concurrent
// async aux-session model-set goroutines that would otherwise all hit the capacity-1
// setModelSem at the same instant (mitto-xicp). Returns 0 if max ≤ 0.
// Mirrors childStartupJitter in internal/conversation/bgsession_config.go (mitto-x4e).
func auxStartupJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// SharedACPProcessConfig holds configuration for creating a SharedACPProcess.
type SharedACPProcessConfig struct {
	// WorkspaceUUID is the unique identifier for the workspace this process belongs to.
	// Used for PID file tracking to detect orphaned processes on startup.
	WorkspaceUUID string
	// ACPCommand is the shell command to start the ACP server process.
	ACPCommand string
	// ACPCwd is the working directory for the ACP server process itself.
	ACPCwd string
	// ACPServer is the name of the ACP server (for logging).
	ACPServer string
	// WorkingDir is the workspace's project directory (e.g., /Users/.../myproject).
	// Used as the cwd for auxiliary sessions so the agent discovers MCP servers.
	WorkingDir string
	// Env is a map of environment variables to set when starting the ACP server.
	// These are merged with the current environment (server-specific vars take precedence).
	// Comes from the ACP server definition in settings.json.
	Env map[string]string
	// Runner is an optional restricted runner for sandboxed execution.
	Runner *runner.Runner
	// Logger for process-level logging.
	Logger *slog.Logger
	// CanRestartGlobal is an optional callback that checks the global (cross-workspace)
	// restart rate limiter. When set, Restart() checks this before proceeding.
	// Returns true if restart is globally allowed, false to block.
	CanRestartGlobal func() bool
	// RecordRestart is an optional callback to record a restart in the global tracker.
	RecordRestart func()
}

// Compile-time assertion: *SharedACPProcess must satisfy the conversation.SharedProcess interface.
var _ conversation.SharedProcess = (*SharedACPProcess)(nil)

// SharedACPProcess manages a single ACP server process that can host multiple sessions.
// Multiple BackgroundSessions share this process via the MultiplexClient.
type SharedACPProcess struct {
	mu sync.RWMutex

	// Process state
	cmd    *exec.Cmd
	conn   *acp.ClientSideConnection
	client *MultiplexClient
	wait   func() error
	cancel context.CancelFunc // for restricted runner processes

	// Process death detection (Fix A: faster crash detection)
	// processDone is closed when the ACP OS process exits, providing sub-second
	// detection via OS-level liveness checks (signal 0 polling).
	processDone     chan struct{} // Closed when ACP OS process exits
	processDoneOnce sync.Once     // Ensures processDone is closed exactly once

	// Configuration (immutable after creation)
	config SharedACPProcessConfig

	// Agent capabilities (set after Initialize)
	capabilities *acp.AgentCapabilities

	// Context for process lifetime
	ctx       context.Context
	ctxCancel context.CancelFunc

	// activeRPCs tracks the number of in-flight RPCs on this process (session/prompt,
	// session/load, and session/new). The GC checks this counter before stopping an
	// idle process to avoid killing the pipe while an RPC is in-flight (LoadSession
	// can take 70+ seconds).
	activeRPCs atomic.Int32

	// setModelSem serialises set_model RPCs per process so concurrent callers queue
	// instead of racing the serially-served agent subprocess (mitto-3q9).
	// Capacity 1 means at most one set_model RPC is in flight at a time; additional
	// callers block (respecting their ctx) until the slot is released.
	// This semaphore guards ONLY set_model — it must never be held during prompts.
	setModelSem chan struct{}

	// Restart tracking
	restartMu    sync.Mutex
	restartCount int
	restartTimes []time.Time

	// onRestart is called after a successful Restart(), allowing the process manager
	// to invalidate caches (e.g., auxiliary sessions) that reference old session IDs.
	onRestart func()

	// Logger
	logger *slog.Logger
}

// NewSharedACPProcess creates and starts a new shared ACP process.
// The process is initialized (ACP handshake) but no sessions are created yet.
func NewSharedACPProcess(ctx context.Context, config SharedACPProcessConfig) (*SharedACPProcess, error) {
	processCtx, processCancel := context.WithCancel(ctx)

	p := &SharedACPProcess{
		config:      config,
		client:      NewMultiplexClient(),
		ctx:         processCtx,
		ctxCancel:   processCancel,
		logger:      config.Logger,
		setModelSem: make(chan struct{}, 1),
	}

	if err := p.startProcess(); err != nil {
		processCancel()
		return nil, err
	}

	return p, nil
}

// startProcess starts the ACP process and performs the Initialize handshake.
// Must be called with appropriate synchronization (only from constructor or restart).
// Returns an *conversation.ACPClassifiedError when the error has been classified, allowing
// callers to distinguish permanent from transient failures.
func (p *SharedACPProcess) startProcess() error {
	var lastErr error
	var lastClassified *conversation.ACPClassifiedError

	for attempt := 0; attempt < maxProcessStartRetries; attempt++ {
		if attempt > 0 {
			delay := conversation.BackoffDelay(attempt-1, processStartRetryBaseDelay, processStartRetryMaxDelay, processStartRetryJitterRatio)
			if p.logger != nil {
				p.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxProcessStartRetries,
					"delay", delay.String(),
					"last_error", lastErr,
					"error_class", lastClassified.Class.String(),
					"command", p.config.ACPCommand,
					"cwd", p.config.ACPCwd)
			}
			select {
			case <-p.ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", p.ctx.Err())
			case <-time.After(delay):
			}
		}

		stderr, processErr := p.doStartProcess()
		if processErr == nil {
			return nil
		}
		lastErr = processErr

		// Classify the error to determine if retrying is worthwhile.
		lastClassified = conversation.ClassifyACPError(processErr, stderr)

		if p.logger != nil {
			p.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"max_attempts", maxProcessStartRetries,
				"error", processErr,
				"error_class", lastClassified.Class.String(),
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}

		// Don't retry permanent errors — they won't resolve by retrying.
		if !lastClassified.IsRetryable() {
			if p.logger != nil {
				p.logger.Error("ACP process start failed with permanent error, skipping retries",
					"error", processErr,
					"user_message", lastClassified.UserMessage,
					"user_guidance", lastClassified.UserGuidance,
					"command", p.config.ACPCommand,
					"cwd", p.config.ACPCwd)
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

// doStartProcess performs a single attempt to start the ACP process and run the Initialize handshake.
// Returns the error and any captured stderr output for error classification.
func (p *SharedACPProcess) doStartProcess() (string, error) {
	processStart := time.Now()
	acpCommand := p.config.ACPCommand
	acpCwd := p.config.ACPCwd

	if p.logger != nil {
		p.logger.Info("Starting ACP process",
			"command", acpCommand,
			"cwd", acpCwd,
			"acp_server", p.config.ACPServer)
	}

	// Parse command using shell-aware tokenization FIRST,
	// then expand $MITTO_* references in each arg individually.
	// This preserves paths with spaces as single arguments.
	// Session ID is empty for shared processes (they serve multiple sessions).
	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return "", fmt.Errorf("parse command: %w", err)
	}
	mittoEnv := mittoAcp.BuildMittoEnv("", p.config.WorkingDir, p.config.ACPServer, "")
	expandedArgs := mittoAcp.ExpandArgs(args, mittoEnv)
	if p.logger != nil {
		changedIndices := make([]int, 0)
		for i, orig := range args {
			if orig != expandedArgs[i] {
				changedIndices = append(changedIndices, i)
			}
		}
		if len(changedIndices) > 0 {
			p.logger.Debug("expanded MITTO_* vars in shared ACP command args",
				"changed_indices", changedIndices,
				"changed_count", len(changedIndices),
				"acp_server", p.config.ACPServer)
		}
	}
	args = expandedArgs
	// Expand cwd (single string, not shlex-parsed)
	originalCwd := acpCwd
	acpCwd = mittoAcp.ExpandCommand(acpCwd, mittoEnv)
	if acpCwd != originalCwd && p.logger != nil {
		p.logger.Debug("expanded MITTO_* vars in shared ACP cwd",
			"acp_server", p.config.ACPServer)
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	stderrCollector := conversation.NewStderrCollector(8192, p.logger)

	// Pre-create process death detection channel so the stderr crash detector
	// (Fix C) can signal it immediately when crash patterns are detected.
	p.processDone = make(chan struct{})
	p.processDoneOnce = sync.Once{}

	onCrashDetected := func() {
		if p.logger != nil {
			p.logger.Warn("ACP subprocess crash detected via stderr patterns",
				"acp_server", p.config.ACPServer)
		}
		p.processDoneOnce.Do(func() {
			close(p.processDone)
		})
	}

	// Startup watchdog: warn/error if no stderr activity and no Initialize completion
	// within the configured windows. Cancelled when doStartProcess returns.
	watchdogCtx, watchdogCancel := context.WithCancel(p.ctx)
	defer watchdogCancel()
	var signalStartupActivity func()

	if p.config.Runner != nil {
		if acpCwd != "" && p.logger != nil {
			p.logger.Warn("cwd is not supported with restricted runners, ignoring",
				"cwd", acpCwd,
				"runner_type", p.config.Runner.Type())
		}
		if p.logger != nil {
			p.logger.Info("starting shared ACP process through restricted runner",
				"runner_type", p.config.Runner.Type(),
				"command", acpCommand)
		}

		var runCancel context.CancelFunc
		var runCtx context.Context
		runCtx, runCancel = context.WithCancel(p.ctx)

		// Build env using the same layering as the direct-exec branch below so that
		// server-specific vars (from settings.json acp_servers[].env) AND MITTO_* vars
		// are propagated to the restricted-runner-spawned process.
		runnerEnv := conversation.BuildACPProcessEnv(p.config.Env, mittoEnv)
		stdin, stdout, stderr, wait, err = p.config.Runner.RunWithPipes(runCtx, args[0], args[1:], runnerEnv)
		if err != nil {
			runCancel()
			return "", fmt.Errorf("failed to start with runner: %w", err)
		}
		p.cancel = runCancel

		if p.logger != nil && len(p.config.Env) > 0 {
			envKeys := make([]string, 0, len(p.config.Env))
			for k := range p.config.Env {
				envKeys = append(envKeys, k)
			}
			p.logger.Info("Applied server-specific environment variables to runner-spawned process",
				"env_keys", envKeys,
				"acp_server", p.config.ACPServer)
		}

		signalStartupActivity = conversation.StartACPStartupWatchdog(watchdogCtx, p.logger, acpCommand, p.config.ACPServer, -1)

		conversation.StartStderrMonitor(stderr, stderrCollector, onCrashDetected, signalStartupActivity)
	} else {
		cmd = exec.CommandContext(p.ctx, args[0], args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if acpCwd != "" {
			cmd.Dir = acpCwd
			if p.logger != nil {
				p.logger.Info("setting ACP process working directory",
					"cwd", acpCwd,
					"command", acpCommand)
			}
		}

		stdin, err = cmd.StdinPipe()
		if err != nil {
			return "", fmt.Errorf("stdin pipe: %w", err)
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("stdout pipe: %w", err)
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return "", fmt.Errorf("stderr pipe: %w", err)
		}

		// Set environment variables for the ACP subprocess. Same layering as the
		// runner branch (os.Environ + server-specific Env + MITTO_*).
		cmd.Env = conversation.BuildACPProcessEnv(p.config.Env, mittoEnv)

		if p.logger != nil && len(p.config.Env) > 0 {
			envKeys := make([]string, 0, len(p.config.Env))
			for k := range p.config.Env {
				envKeys = append(envKeys, k)
			}
			p.logger.Info("Applied server-specific environment variables",
				"env_keys", envKeys,
				"acp_server", p.config.ACPServer)
		}

		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start ACP server: %w", err)
		}

		// Track process PID for orphan detection on restart
		if p.config.WorkspaceUUID != "" {
			if pidErr := writeACPPIDFile(p.config.WorkspaceUUID, cmd.Process.Pid, false); pidErr != nil {
				if p.logger != nil {
					p.logger.Warn("Failed to write ACP PID file", "error", pidErr,
						"workspace_uuid", p.config.WorkspaceUUID)
				}
			}
		}

		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		signalStartupActivity = conversation.StartACPStartupWatchdog(watchdogCtx, p.logger, acpCommand, p.config.ACPServer, pid)

		conversation.StartStderrMonitor(stderrPipe, stderrCollector, onCrashDetected, signalStartupActivity)

		wait = func() error {
			return cmd.Wait()
		}
	}

	p.cmd = cmd

	// Wrap wait function to also close processDone channel on process exit.
	// The channel was pre-created above (before stderr monitors started).
	origWait := wait
	p.wait = func() error {
		err := origWait()

		// Log exit code and signal for crash telemetry
		if err != nil && p.logger != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				logAttrs := []any{
					"exit_code", exitErr.ExitCode(),
					"acp_server", p.config.ACPServer,
				}
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						logAttrs = append(logAttrs, "signal", status.Signal().String())
					}
				}

				// Log at DEBUG if we intentionally killed it, WARN if it crashed on its own
				if p.ctx.Err() != nil {
					p.logger.Debug("ACP process exited (intentional shutdown)", logAttrs...)
				} else {
					p.logger.Warn("ACP process exited abnormally", logAttrs...)
				}
			} else {
				// Non-ExitError wait failures (shouldn't happen in practice)
				if p.ctx.Err() != nil {
					p.logger.Debug("ACP process wait error (intentional shutdown)",
						"error", err,
						"acp_server", p.config.ACPServer)
				} else {
					p.logger.Warn("ACP process wait error",
						"error", err,
						"acp_server", p.config.ACPServer)
				}
			}
		}

		p.processDoneOnce.Do(func() {
			close(p.processDone)
		})
		return err
	}

	// Start process liveness monitor for direct-exec processes.
	// Polls process every 2 seconds using kill(pid, 0) for fast death detection.
	if cmd != nil && cmd.Process != nil {
		processDoneCh := p.processDone
		processDoneOnce := &p.processDoneOnce
		pid := cmd.Process.Pid
		processCtx := p.ctx
		logger := p.logger
		acpServer := p.config.ACPServer
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-processDoneCh:
					return
				case <-processCtx.Done():
					return
				case <-ticker.C:
					err := syscall.Kill(pid, 0)
					if err != nil {
						if logger != nil {
							logger.Warn("ACP process no longer alive (detected by liveness check)",
								"pid", pid,
								"error", err,
								"acp_server", acpServer)
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

	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, p.logger)

	// Use a larger notification queue for shared processes since all sessions
	// multiplex over the same connection. The default 1024 can overflow when
	// many sessions stream concurrently, killing the connection.
	p.conn = acp.NewClientSideConnection(p.client, stdin, filteredStdout,
		acp.WithMaxQueuedNotifications(8192))
	if p.logger != nil {
		p.conn.SetLogger(logging.DowngradeACPSDKErrors(p.logger))
	}

	// Create an init context that gets cancelled when the ACP process dies.
	// This ensures we fail fast instead of waiting for the ACP server's internal
	// 60-second control request timeout when the CLI subprocess has crashed.
	// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
	initCtx, initCancel := context.WithCancel(p.ctx)
	defer initCancel()

	// Monitor ACP process health: if the connection's Done() channel closes
	// or the OS process exits (processDone), cancel the init context immediately.
	go func() {
		select {
		case <-p.conn.Done():
			if p.logger != nil {
				p.logger.Warn("ACP connection closed during initialization, cancelling",
					"acp_server", p.config.ACPServer)
			}
			initCancel()
		case <-p.processDone:
			if p.logger != nil {
				p.logger.Warn("ACP process exited during initialization, cancelling",
					"acp_server", p.config.ACPServer)
			}
			initCancel()
		case <-initCtx.Done():
			// Initialization completed normally or was cancelled for another reason
		}
	}()

	initStart := time.Now()
	initResp, err := p.conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
		ClientInfo: &acp.Implementation{
			Name:    "mitto",
			Title:   strPtr("Mitto"),
			Version: "dev", // Use a constant for now, we'll improve this later
		},
	})
	initDuration := time.Since(initStart)

	if err != nil {
		time.Sleep(100 * time.Millisecond)

		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if p.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"cwd", acpCwd,
				"error", err,
				"initialize_ms", initDuration.Milliseconds(),
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			p.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		p.killProcess()
		return stderrOutput, fmt.Errorf("failed to initialize: %w", err)
	}

	p.capabilities = &initResp.AgentCapabilities

	if p.logger != nil {
		logAttrs := []any{
			"acp_server", p.config.ACPServer,
			"command", acpCommand,
			"cwd", acpCwd,
			"protocol_version", initResp.ProtocolVersion,
			"load_session", initResp.AgentCapabilities.LoadSession,
			"process_start_ms", time.Since(processStart).Milliseconds(),
			"initialize_rpc_ms", initDuration.Milliseconds(),
		}

		// Add agent info if available
		if initResp.AgentInfo != nil {
			logAttrs = append(logAttrs,
				"agent_name", initResp.AgentInfo.Name,
				"agent_version", initResp.AgentInfo.Version)
		}

		// Add detailed capabilities
		logAttrs = append(logAttrs,
			"prompt_image", initResp.AgentCapabilities.PromptCapabilities.Image,
			"prompt_audio", initResp.AgentCapabilities.PromptCapabilities.Audio,
			"prompt_embedded_context", initResp.AgentCapabilities.PromptCapabilities.EmbeddedContext)

		p.logger.Info("Shared ACP process started", logAttrs...)

		// Log SessionCapabilities at DEBUG level
		p.logger.Debug("Agent session capabilities",
			"acp_server", p.config.ACPServer,
			"resume_supported", initResp.AgentCapabilities.SessionCapabilities.Resume != nil,
			"fork_supported", initResp.AgentCapabilities.SessionCapabilities.Fork != nil,
			"list_supported", initResp.AgentCapabilities.SessionCapabilities.List != nil)

		// Log Meta fields separately at DEBUG level if present
		if len(initResp.Meta) > 0 {
			p.logger.Debug("ACP initialize response meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.Meta)
		}
		if len(initResp.AgentCapabilities.Meta) > 0 {
			p.logger.Debug("ACP agent capabilities meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.AgentCapabilities.Meta)
		}
		if initResp.AgentInfo != nil && len(initResp.AgentInfo.Meta) > 0 {
			p.logger.Debug("ACP agent info meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.AgentInfo.Meta)
		}
	}

	return "", nil
}

// NewSession creates a new ACP session on this shared process.
func (p *SharedACPProcess) NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	// This catches the race window between OS termination and detection (up to 2s).
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	if cwd == "" {
		cwd = "."
	}

	// Bounded retry-with-jitter loop (mitto-4no7): mirrors SetSessionModel's policy so
	// transient deadline failures on session/new are retried up to sessionCreateMaxAttempts.
	// Each attempt gets a fresh sessionCreateAttemptTimeout budget, preserving the
	// documented 25s per-attempt create deadline (mitto-63o8) without regression.
	var lastErr error
	for attempt := 1; attempt <= sessionCreateMaxAttempts; attempt++ {
		// Honour caller cancellation before each attempt.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("session/new: context cancelled before attempt %d: %w", attempt, ctx.Err())
		}

		// Jittered backoff between retries (skip before first attempt). Mirrors set_model
		// (mitto-4no7): de-correlates concurrent callers that would retry in lock-step.
		if attempt > 1 {
			jitter := time.Duration(rand.Int63n(int64(float64(sessionCreateRetryBaseDelay) * sessionCreateRetryJitterRatio)))
			delay := time.Duration(attempt-1)*sessionCreateRetryBaseDelay + jitter
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, fmt.Errorf("session/new: context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		// Fresh per-attempt sub-context so each attempt gets a full create budget.
		attemptCtx, attemptCancel := context.WithTimeout(ctx, sessionCreateAttemptTimeout)

		ctxRemainingMs := int64(-1)
		if dl, ok := ctx.Deadline(); ok {
			ctxRemainingMs = time.Until(dl).Milliseconds()
		}

		rpcStart := time.Now()
		sessResp, err := conn.NewSession(attemptCtx, acp.NewSessionRequest{
			Cwd:        cwd,
			McpServers: mcpServers,
		})
		rpcDuration := time.Since(rpcStart)
		attemptCancel()

		if err == nil {
			handle := &conversation.SessionHandle{
				SessionID: string(sessResp.SessionId),
				Process:   p,
				Modes:     sessResp.Modes,
				Models:    conversation.StableToUnstableModelState(sessResp.Models),
			}
			if caps != nil {
				handle.Capabilities = *caps
			}
			// TODO: ConfigOptions support when SDK is updated
			// if sessResp.ConfigOptions != nil {
			// 	handle.ConfigOptions = sessResp.ConfigOptions
			// }
			if p.logger != nil {
				p.logger.Info("Created new ACP session on shared process",
					"acp_session_id", handle.SessionID,
					"attempt", attempt,
					"total_ms", time.Since(totalStart).Milliseconds(),
					"rpc_new_session_ms", rpcDuration.Milliseconds())
			}
			return handle, nil
		}

		lastErr = err
		if p.logger != nil {
			p.logger.Warn("SharedACPProcess.NewSession failed",
				"attempt", attempt,
				"max_attempts", sessionCreateMaxAttempts,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"error", err)
		}

		// Non-transient errors are not retried.
		if !isRetryableCreateError(err) {
			return nil, fmt.Errorf("failed to create session: %w", err)
		}
	}

	return nil, fmt.Errorf("session/new failed after %d attempts: %w", sessionCreateMaxAttempts, lastErr)
}

// LoadSession attempts to load/resume an existing ACP session.
func (p *SharedACPProcess) LoadSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	if caps == nil || !caps.LoadSession {
		return nil, fmt.Errorf("agent does not support session loading")
	}

	if cwd == "" {
		cwd = "."
	}

	ctxRemainingMs := int64(-1)
	if dl, ok := ctx.Deadline(); ok {
		ctxRemainingMs = time.Until(dl).Milliseconds()
	}
	ctxAlreadyExpired := ctx.Err() != nil

	rpcStart := time.Now()
	loadResp, err := conn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  acp.SessionId(acpSessionID),
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	rpcDuration := time.Since(rpcStart)

	if err != nil {
		if p.logger != nil {
			p.logger.Info("SharedACPProcess.LoadSession failed",
				"acp_session_id", acpSessionID,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"ctx_already_expired", ctxAlreadyExpired,
				"error", err)
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	handle := &conversation.SessionHandle{
		SessionID:    acpSessionID,
		Capabilities: *caps,
		Modes:        loadResp.Modes,
		Models:       conversation.StableToUnstableModelState(loadResp.Models),
		Process:      p,
	}

	if p.logger != nil {
		p.logger.Info("Loaded ACP session on shared process",
			"acp_session_id", acpSessionID,
			"total_ms", time.Since(totalStart).Milliseconds(),
			"rpc_load_session_ms", rpcDuration.Milliseconds())
	}

	return handle, nil
}

// ResumeSession attempts to resume an existing ACP session without replaying history.
// This is faster than LoadSession but requires the agent to support session/resume
// and still have the session in memory.
func (p *SharedACPProcess) ResumeSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	// Check capability
	if caps == nil || caps.SessionCapabilities.Resume == nil {
		return nil, fmt.Errorf("agent does not support session resume (UNSTABLE API)")
	}

	if cwd == "" {
		cwd = "."
	}

	rpcStart := time.Now()
	resumeResp, err := conn.UnstableResumeSession(ctx, acp.UnstableResumeSessionRequest{
		SessionId:  acp.SessionId(acpSessionID),
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	rpcDuration := time.Since(rpcStart)

	if err != nil {
		if p.logger != nil {
			p.logger.Info("SharedACPProcess.ResumeSession failed (UNSTABLE API)",
				"acp_session_id", acpSessionID,
				"rpc_ms", rpcDuration.Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	handle := &conversation.SessionHandle{
		SessionID:    acpSessionID,
		Capabilities: *caps,
		Modes:        resumeResp.Modes,
		Models:       resumeResp.Models,
		Process:      p,
	}

	if p.logger != nil {
		p.logger.Info("Resumed ACP session on shared process (UNSTABLE API)",
			"acp_session_id", acpSessionID,
			"total_ms", time.Since(totalStart).Milliseconds(),
			"rpc_resume_session_ms", rpcDuration.Milliseconds())
	}

	return handle, nil
}

// RegisterSession registers per-session callbacks with the MultiplexClient.
func (p *SharedACPProcess) RegisterSession(sessionID acp.SessionId, callbacks *conversation.SessionCallbacks) {
	p.client.RegisterSession(sessionID, callbacks)
}

// UnregisterSession removes per-session callbacks.
func (p *SharedACPProcess) UnregisterSession(sessionID acp.SessionId) {
	p.client.UnregisterSession(sessionID)
}

// ProcessDone returns a channel that is closed when the ACP OS process exits.
// This provides faster death detection than conn.Done() which relies on pipe EOF.
// Returns nil if the process has not been started yet.
func (p *SharedACPProcess) ProcessDone() <-chan struct{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.processDone
}

// Prompt sends a prompt to a specific session on this shared process.
func (p *SharedACPProcess) Prompt(ctx context.Context, sessionID acp.SessionId, content []acp.ContentBlock) (acp.PromptResponse, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return acp.PromptResponse{}, fmt.Errorf("shared ACP process is not running")
	}

	return conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    content,
	})
}

// ActiveRPCs returns the number of in-flight RPCs on this process (session/prompt,
// session/load, and session/new). Used by the GC to avoid killing the process
// while it is actively serving requests.
func (p *SharedACPProcess) ActiveRPCs() int32 {
	return p.activeRPCs.Load()
}

// RSSBytes returns the resident set size in bytes summed over this process's
// tree (the ACP agent process plus all of its descendants). Used by the GC's
// memory-recycle tier to decide whether an idle process has grown bloated
// enough to be reclaimed.
func (p *SharedACPProcess) RSSBytes() (uint64, error) {
	p.mu.RLock()
	if p.cmd == nil || p.cmd.Process == nil {
		p.mu.RUnlock()
		return 0, fmt.Errorf("shared ACP process is not running")
	}
	pid := p.cmd.Process.Pid
	p.mu.RUnlock()

	return processTreeRSS(pid)
}

// Cancel cancels the current operation for a specific session.
func (p *SharedACPProcess) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return nil
	}

	return conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
}

// SetSessionMode sets the mode for a specific session.
func (p *SharedACPProcess) SetSessionMode(ctx context.Context, sessionID acp.SessionId, modeID string) error {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("shared ACP process is not running")
	}

	_, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{
		SessionId: sessionID,
		ModeId:    acp.SessionModeId(modeID),
	})
	return err
}

// SetSessionModel sets the model for a specific session.
// It serialises concurrent callers via setModelSem (one in-flight RPC at a time per
// process) and retries on transient timeouts so burst startups don't race the
// serially-served agent subprocess (mitto-3q9).
func (p *SharedACPProcess) SetSessionModel(ctx context.Context, sessionID acp.SessionId, modelID string) error {
	// Read conn under RLock; keep existing nil-check semantics.
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("shared ACP process is not running")
	}

	// Acquire the per-process serialisation semaphore, respecting caller ctx.
	// This ensures only one set_model RPC is in-flight at a time — concurrent
	// callers queue here instead of racing the serially-served agent subprocess.
	select {
	case p.setModelSem <- struct{}{}:
		defer func() { <-p.setModelSem }()
	case <-ctx.Done():
		return fmt.Errorf("set_model: cancelled while waiting for serialization slot: %w", ctx.Err())
	}

	// Track as an active RPC for GC visibility (mirrors other methods).
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	var lastErr error
	for attempt := 1; attempt <= setSessionModelMaxAttempts; attempt++ {
		// Honour caller cancellation before each attempt.
		if ctx.Err() != nil {
			return fmt.Errorf("set_model: context cancelled before attempt %d: %w", attempt, ctx.Err())
		}

		// Backoff between retries (skip before first attempt).
		// Jitter (mitto-f7q, Option 3): add a random fraction up to 50% of the base
		// delay so concurrent callers de-correlate instead of retrying in lock-step.
		// attempt 2: delay ∈ [300ms, 450ms); attempt 3: ∈ [600ms, 750ms).
		if attempt > 1 {
			jitter := time.Duration(rand.Int63n(int64(float64(setSessionModelRetryBaseDelay) * setSessionModelRetryJitterRatio)))
			delay := time.Duration(attempt-1)*setSessionModelRetryBaseDelay + jitter
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("set_model: context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		// Fresh per-attempt sub-context so each attempt (especially a caller that
		// waited on the semaphore) gets a full budget regardless of wait time.
		attemptCtx, attemptCancel := context.WithTimeout(ctx, setSessionModelAttemptTimeout)

		ctxRemainingMs := int64(-1)
		if dl, ok := ctx.Deadline(); ok {
			ctxRemainingMs = time.Until(dl).Milliseconds()
		}

		rpcStart := time.Now()
		_, err := conn.UnstableSetSessionModel(attemptCtx, acp.UnstableSetSessionModelRequest{
			SessionId: sessionID,
			ModelId:   acp.UnstableModelId(modelID),
		})
		rpcDuration := time.Since(rpcStart)
		attemptCancel()

		if err == nil {
			if attempt > 1 && p.logger != nil {
				p.logger.Info("SharedACPProcess.SetSessionModel succeeded after retry",
					"session_id", sessionID,
					"model_id", modelID,
					"attempt", attempt,
					"rpc_ms", rpcDuration.Milliseconds())
			}
			return nil
		}

		lastErr = err
		if p.logger != nil {
			p.logger.Warn("SharedACPProcess.SetSessionModel failed",
				"session_id", sessionID,
				"model_id", modelID,
				"attempt", attempt,
				"max_attempts", setSessionModelMaxAttempts,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"error", err)
		}

		// Non-transient errors are not retried (e.g. invalid model ID).
		if !isRetryableSetModelError(err) {
			return err
		}
	}

	return fmt.Errorf("set_model failed after %d attempts: %w", setSessionModelMaxAttempts, lastErr)
}

// isRetryableSetModelError reports whether a set_model error is worth retrying.
// set_model is idempotent so retrying on timeout is safe.
func isRetryableSetModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out")
}

// isRetryableCreateError reports whether a session/new error is worth retrying.
// NOTE: unlike set_model, session/new is NOT idempotent — a create that times out
// MAY have succeeded server-side, so a retry can orphan a session on the shared
// process. We accept this trade-off (mitto-4no7): on a deadline we never received a
// session ID, so the only recovery is to create again; the orphan is bounded by the
// shared process lifetime. Only deadline/timeout failures are retried.
func isRetryableCreateError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out")
}

// SetSessionConfigOption sets a config option for a specific session.
// TODO: Implement when SDK supports SetSessionConfigOption
func (p *SharedACPProcess) SetSessionConfigOption(ctx context.Context, sessionID acp.SessionId, configID, value string) error {
	// p.mu.RLock()
	// conn := p.conn
	// p.mu.RUnlock()

	// if conn == nil {
	// 	return fmt.Errorf("shared ACP process is not running")
	// }

	// _, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
	// 	SessionId: sessionID,
	// 	ConfigId:  acp.SessionConfigId(configID),
	// 	Value:     acp.SessionConfigValueId(value),
	// })
	// return err
	return fmt.Errorf("SetSessionConfigOption not yet implemented in SDK")
}

// Done returns a channel that is closed when the ACP process exits.
func (p *SharedACPProcess) Done() <-chan struct{} {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return conn.Done()
}

// Capabilities returns the agent's capabilities.
func (p *SharedACPProcess) Capabilities() *acp.AgentCapabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities
}

// WorkingDir returns the workspace's project directory.
// Falls back to ACPCwd if WorkingDir is not set.
func (p *SharedACPProcess) WorkingDir() string {
	if p.config.WorkingDir != "" {
		return p.config.WorkingDir
	}
	return p.config.ACPCwd
}

// Close terminates the ACP process and cleans up resources.
func (p *SharedACPProcess) Close() {
	p.ctxCancel()
	p.killProcess()
}

// killProcess terminates the ACP process.
func (p *SharedACPProcess) killProcess() {
	if p.cancel != nil {
		p.cancel()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		// Kill the entire process group to ensure all child processes are terminated.
		// Without this, child processes (e.g., "claude" spawned by "node claude-code-acp")
		// survive and become orphans.
		mittoAcp.KillProcessGroup(p.cmd.Process.Pid)
	}

	// Remove PID tracking file
	if p.config.WorkspaceUUID != "" {
		_ = removeACPPIDFile(p.config.WorkspaceUUID, false)
	}

	if p.wait != nil {
		p.wait()
		p.wait = nil
	}
}

// canRestart checks if we can restart the process based on rate limiting.
func (p *SharedACPProcess) canRestart() bool {
	p.restartMu.Lock()
	defer p.restartMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-conversation.ACPRestartWindow)

	// Remove old restart timestamps
	valid := p.restartTimes[:0]
	for _, t := range p.restartTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	p.restartTimes = valid

	return len(p.restartTimes) < conversation.MaxACPRestarts
}

// recordRestart records a restart attempt.
func (p *SharedACPProcess) recordRestart() {
	p.restartMu.Lock()
	defer p.restartMu.Unlock()
	p.restartCount++
	p.restartTimes = append(p.restartTimes, time.Now())
}

// Restart kills the old process and starts a new one.
// All sessions must re-register their callbacks and LoadSession after restart.
// Returns nil on success. Returns an *conversation.ACPClassifiedError for permanent failures.
func (p *SharedACPProcess) Restart() error {
	if !p.canRestart() {
		return fmt.Errorf("restart limit exceeded (%d restarts in %v)", conversation.MaxACPRestarts, conversation.ACPRestartWindow)
	}

	// Check global (cross-workspace) restart rate limiter before proceeding.
	if p.config.CanRestartGlobal != nil && !p.config.CanRestartGlobal() {
		return fmt.Errorf("global restart limit exceeded (cross-workspace cooldown active)")
	}

	// Apply backoff based on how many recent restarts have occurred.
	p.restartMu.Lock()
	recentCount := len(p.restartTimes)
	p.restartMu.Unlock()

	if recentCount > 0 {
		delay := conversation.BackoffDelay(recentCount-1, conversation.ACPRestartBaseDelay, conversation.ACPRestartMaxDelay, processStartRetryJitterRatio)
		if p.logger != nil {
			p.logger.Info("Waiting before restart",
				"delay", delay.String(),
				"recent_restarts", recentCount,
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}
		select {
		case <-p.ctx.Done():
			return fmt.Errorf("context cancelled during restart backoff: %w", p.ctx.Err())
		case <-time.After(delay):
		}
	}

	if p.logger != nil {
		p.logger.Info("Restarting shared ACP process",
			"restart_count", p.restartCount+1,
			"command", p.config.ACPCommand,
			"cwd", p.config.ACPCwd)
	}

	p.mu.Lock()
	p.killProcess()
	p.conn = nil
	p.capabilities = nil
	p.mu.Unlock()

	p.recordRestart()

	// Record in the global restart tracker (cross-workspace rate limiter).
	if p.config.RecordRestart != nil {
		p.config.RecordRestart()
	}

	if err := p.startProcess(); err != nil {
		if p.logger != nil {
			logAttrs := []any{"error", err}
			if classified, ok := err.(*conversation.ACPClassifiedError); ok {
				logAttrs = append(logAttrs,
					"error_class", classified.Class.String(),
					"user_message", classified.UserMessage,
					"user_guidance", classified.UserGuidance)
			}
			p.logger.Error("Failed to restart shared ACP process", logAttrs...)
		}
		return err
	}

	if p.logger != nil {
		p.logger.Info("Shared ACP process restarted successfully",
			"command", p.config.ACPCommand)
	}

	// Notify the process manager so it can invalidate stale auxiliary sessions.
	if p.onRestart != nil {
		p.onRestart()
	}

	return nil
}

// SetOnRestart registers a callback that is called after a successful Restart().
// This allows the process manager to invalidate caches (e.g., auxiliary sessions)
// that reference old session IDs from the previous process instance.
func (p *SharedACPProcess) SetOnRestart(fn func()) {
	p.onRestart = fn
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}
