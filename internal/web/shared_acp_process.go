package web

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/runner"
)

const (
	// maxProcessStartRetries is the maximum number of times to retry starting the ACP process.
	maxProcessStartRetries = 2
	// processStartRetryDelay is the delay between process start retries.
	processStartRetryDelay = 500 * time.Millisecond
	// maxProcessRestarts is the maximum number of automatic restarts within processRestartWindow.
	maxProcessRestarts = 3
	// processRestartWindow is the time window for counting restart attempts.
	processRestartWindow = 5 * time.Minute
)

// SharedACPProcessConfig holds configuration for creating a SharedACPProcess.
type SharedACPProcessConfig struct {
	// ACPCommand is the shell command to start the ACP server process.
	ACPCommand string
	// ACPCwd is the working directory for the ACP server process itself.
	ACPCwd string
	// ACPServer is the name of the ACP server (for logging).
	ACPServer string
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

	// Configuration (immutable after creation)
	config SharedACPProcessConfig

	// Agent capabilities (set after Initialize)
	capabilities *acp.AgentCapabilities

	// Context for process lifetime
	ctx       context.Context
	ctxCancel context.CancelFunc

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
func (p *SharedACPProcess) startProcess() error {
	var lastErr error
	for attempt := 0; attempt < maxProcessStartRetries; attempt++ {
		if attempt > 0 {
			if p.logger != nil {
				p.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxProcessStartRetries,
					"last_error", lastErr)
			}
			select {
			case <-p.ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", p.ctx.Err())
			case <-time.After(processStartRetryDelay):
			}
		}

		err := p.doStartProcess()
		if err == nil {
			return nil
		}
		lastErr = err

		if strings.Contains(err.Error(), "empty ACP command") {
			return err // Don't retry validation errors
		}

		if p.logger != nil {
			p.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"error", err)
		}
	}

	return lastErr
}

func (p *SharedACPProcess) doStartProcess() error {
	processStart := time.Now()
	acpCommand := p.config.ACPCommand
	acpCwd := p.config.ACPCwd

	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return fmt.Errorf("parse command: %w", err)
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	stderrCollector := newStderrCollector(8192, p.logger)

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
			return fmt.Errorf("failed to start with runner: %w", err)
		}
		p.cancel = runCancel

		startStderrMonitor(stderr, stderrCollector)
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
			return fmt.Errorf("stdin pipe: %w", err)
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("stdout pipe: %w", err)
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("stderr pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start ACP server: %w", err)
		}

		startStderrMonitor(stderrPipe, stderrCollector)

		wait = func() error {
			return cmd.Wait()
		}
	}

	p.cmd = cmd
	p.wait = wait

	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, p.logger)

	p.conn = acp.NewClientSideConnection(p.client, stdin, filteredStdout)
	if p.logger != nil {
		p.conn.SetLogger(logging.DowngradeInfoToDebug(p.logger))
	}

	// Create an init context that gets cancelled when the ACP process dies.
	// This ensures we fail fast instead of waiting for the ACP server's internal
	// 60-second control request timeout when the CLI subprocess has crashed.
	// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
	initCtx, initCancel := context.WithCancel(p.ctx)
	defer initCancel()

	// Monitor ACP process health: if the connection's Done() channel closes
	// (meaning the ACP subprocess died), cancel the init context immediately.
	go func() {
		select {
		case <-p.conn.Done():
			if p.logger != nil {
				p.logger.Warn("ACP connection closed during initialization, cancelling",
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
				"error", err,
				"initialize_ms", initDuration.Milliseconds(),
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			p.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		p.killProcess()
		return fmt.Errorf("failed to initialize: %w", err)
	}

	p.capabilities = &initResp.AgentCapabilities

	if p.logger != nil {
		p.logger.Info("Shared ACP process started",
			"acp_server", p.config.ACPServer,
			"protocol_version", initResp.ProtocolVersion,
			"load_session", initResp.AgentCapabilities.LoadSession,
			"process_start_ms", time.Since(processStart).Milliseconds(),
			"initialize_rpc_ms", initDuration.Milliseconds())
	}

	return nil
}

// NewSession creates a new ACP session on this shared process.
func (p *SharedACPProcess) NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error) {
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
			p.logger.Warn("SharedACPProcess.LoadSession failed",
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

// Prompt sends a prompt to a specific session on this shared process.
func (p *SharedACPProcess) Prompt(ctx context.Context, sessionID acp.SessionId, content []acp.ContentBlock) (acp.PromptResponse, error) {
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
	cutoff := now.Add(-processRestartWindow)

	// Remove old restart timestamps
	valid := p.restartTimes[:0]
	for _, t := range p.restartTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	p.restartTimes = valid

	return len(p.restartTimes) < maxProcessRestarts
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
// Returns nil on success.
func (p *SharedACPProcess) Restart() error {
	if !p.canRestart() {
		return fmt.Errorf("restart limit exceeded (%d restarts in %v)", maxProcessRestarts, processRestartWindow)
	}

	if p.logger != nil {
		p.logger.Info("Restarting shared ACP process",
			"restart_count", p.restartCount+1)
	}

	p.mu.Lock()
	p.killProcess()
	p.conn = nil
	p.capabilities = nil
	p.mu.Unlock()

	p.recordRestart()

	if err := p.startProcess(); err != nil {
		if p.logger != nil {
			p.logger.Error("Failed to restart shared ACP process", "error", err)
		}
		return err
	}

	if p.logger != nil {
		p.logger.Info("Shared ACP process restarted successfully")
	}

	return nil
}
