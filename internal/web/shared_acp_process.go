package web

import (
	"context"
	"fmt"
	"log/slog"
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

	// Note: Runtime restart constants (maxProcessRestarts, processRestartWindow,
	// processRestartBaseDelay, processRestartMaxDelay) are now defined in
	// acp_error_classification.go as shared constants (MaxACPRestarts, ACPRestartWindow,
	// ACPRestartBaseDelay, ACPRestartMaxDelay) to ensure consistent behavior between
	// SharedACPProcess and BackgroundSession.
)

// SharedACPProcessConfig holds configuration for creating a SharedACPProcess.
type SharedACPProcessConfig struct {
	// ACPCommand is the shell command to start the ACP server process.
	ACPCommand string
	// ACPCwd is the working directory for the ACP server process itself.
	ACPCwd string
	// ACPServer is the name of the ACP server (for logging).
	ACPServer string
	// WorkingDir is the workspace's project directory (e.g., /Users/.../myproject).
	// Used as the cwd for auxiliary sessions so the agent discovers MCP servers.
	WorkingDir string
	// Runner is an optional restricted runner for sandboxed execution.
	Runner *runner.Runner
	// Logger for process-level logging.
	Logger *slog.Logger
}

// SessionHandle is returned when creating a new session on a SharedACPProcess.
// It provides the session-scoped interface for the BackgroundSession.
type SessionHandle struct {
	// SessionID is the ACP-assigned session ID.
	SessionID string
	// Capabilities are the agent's capabilities (from Initialize).
	Capabilities acp.AgentCapabilities
	// Modes are the session mode state (from NewSession/LoadSession).
	Modes *acp.SessionModeState
	// ConfigOptions are the session config options (from NewSession/LoadSession).
	ConfigOptions []SessionConfigOption
	// Process is a reference to the parent SharedACPProcess.
	Process *SharedACPProcess
}

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

	// Restart tracking
	restartMu    sync.Mutex
	restartCount int
	restartTimes []time.Time

	// Logger
	logger *slog.Logger
}

// NewSharedACPProcess creates and starts a new shared ACP process.
// The process is initialized (ACP handshake) but no sessions are created yet.
func NewSharedACPProcess(ctx context.Context, config SharedACPProcessConfig) (*SharedACPProcess, error) {
	processCtx, processCancel := context.WithCancel(ctx)

	p := &SharedACPProcess{
		config:    config,
		client:    NewMultiplexClient(),
		ctx:       processCtx,
		ctxCancel: processCancel,
		logger:    config.Logger,
	}

	if err := p.startProcess(); err != nil {
		processCancel()
		return nil, err
	}

	return p, nil
}

// startProcess starts the ACP process and performs the Initialize handshake.
// Must be called with appropriate synchronization (only from constructor or restart).
// Returns an *ACPClassifiedError when the error has been classified, allowing
// callers to distinguish permanent from transient failures.
func (p *SharedACPProcess) startProcess() error {
	var lastErr error
	var lastClassified *ACPClassifiedError

	for attempt := 0; attempt < maxProcessStartRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt-1, processStartRetryBaseDelay, processStartRetryMaxDelay, processStartRetryJitterRatio)
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
		lastClassified = classifyACPError(processErr, stderr)

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

	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return "", fmt.Errorf("parse command: %w", err)
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	stderrCollector := newStderrCollector(8192, p.logger)

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

		stdin, stdout, stderr, wait, err = p.config.Runner.RunWithPipes(runCtx, args[0], args[1:], nil)
		if err != nil {
			runCancel()
			return "", fmt.Errorf("failed to start with runner: %w", err)
		}
		p.cancel = runCancel

		startStderrMonitor(stderr, stderrCollector, onCrashDetected)
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

		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start ACP server: %w", err)
		}

		startStderrMonitor(stderrPipe, stderrCollector, onCrashDetected)

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

	p.conn = acp.NewClientSideConnection(p.client, stdin, filteredStdout)
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
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
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
		p.logger.Info("Shared ACP process started",
			"acp_server", p.config.ACPServer,
			"command", acpCommand,
			"cwd", acpCwd,
			"protocol_version", initResp.ProtocolVersion,
			"load_session", initResp.AgentCapabilities.LoadSession,
			"process_start_ms", time.Since(processStart).Milliseconds(),
			"initialize_rpc_ms", initDuration.Milliseconds())
	}

	return "", nil
}

// NewSession creates a new ACP session on this shared process.
func (p *SharedACPProcess) NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	if cwd == "" {
		cwd = "."
	}

	rpcStart := time.Now()
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	rpcDuration := time.Since(rpcStart)

	if err != nil {
		if p.logger != nil {
			p.logger.Warn("SharedACPProcess.NewSession failed",
				"rpc_ms", rpcDuration.Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	handle := &SessionHandle{
		SessionID: string(sessResp.SessionId),
		Process:   p,
		Modes:     sessResp.Modes,
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
			"total_ms", time.Since(totalStart).Milliseconds(),
			"rpc_new_session_ms", rpcDuration.Milliseconds())
	}

	return handle, nil
}

// LoadSession attempts to load/resume an existing ACP session.
func (p *SharedACPProcess) LoadSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error) {
	p.activeRPCs.Add(1)
	defer p.activeRPCs.Add(-1)

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}
	if caps == nil || !caps.LoadSession {
		return nil, fmt.Errorf("agent does not support session loading")
	}

	if cwd == "" {
		cwd = "."
	}

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
				"error", err)
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	handle := &SessionHandle{
		SessionID:    acpSessionID,
		Capabilities: *caps,
		Modes:        loadResp.Modes,
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

// RegisterSession registers per-session callbacks with the MultiplexClient.
func (p *SharedACPProcess) RegisterSession(sessionID acp.SessionId, callbacks *SessionCallbacks) {
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
		p.cmd.Process.Kill()
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
	cutoff := now.Add(-ACPRestartWindow)

	// Remove old restart timestamps
	valid := p.restartTimes[:0]
	for _, t := range p.restartTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	p.restartTimes = valid

	return len(p.restartTimes) < MaxACPRestarts
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
// Returns nil on success. Returns an *ACPClassifiedError for permanent failures.
func (p *SharedACPProcess) Restart() error {
	if !p.canRestart() {
		return fmt.Errorf("restart limit exceeded (%d restarts in %v)", MaxACPRestarts, ACPRestartWindow)
	}

	// Apply backoff based on how many recent restarts have occurred.
	p.restartMu.Lock()
	recentCount := len(p.restartTimes)
	p.restartMu.Unlock()

	if recentCount > 0 {
		delay := backoffDelay(recentCount-1, ACPRestartBaseDelay, ACPRestartMaxDelay, processStartRetryJitterRatio)
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

	if err := p.startProcess(); err != nil {
		if p.logger != nil {
			logAttrs := []any{"error", err}
			if classified, ok := err.(*ACPClassifiedError); ok {
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

	return nil
}
