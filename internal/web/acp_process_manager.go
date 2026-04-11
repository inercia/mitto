package web

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// ACPProcessManager manages shared ACP processes, one per workspace.
// Instead of starting a new ACP process for each conversation, conversations
// within the same workspace share a single ACP process with multiple sessions.
//
// It also implements auxiliary.ProcessProvider to manage auxiliary sessions
// (title generation, follow-up analysis, etc.) within workspace processes.
type ACPProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*SharedACPProcess // keyed by workspace UUID

	// Dedicated auxiliary processes, keyed by workspace UUID.
	// Only populated when workspace has AuxiliaryACPServer configured.
	auxProcesses map[string]*SharedACPProcess

	// WorkspaceConfigProvider returns workspace settings for a given UUID.
	// Set by SessionManager during initialization to resolve auxiliary config.
	WorkspaceConfigProvider func(workspaceUUID string) *config.WorkspaceSettings

	// Auxiliary session tracking
	auxMu       sync.Mutex
	auxSessions map[auxSessionKey]*auxiliarySessionState

	// Global context for all managed processes.
	ctx context.Context

	// DisableAuxiliary disables all auxiliary session features (pre-warming,
	// MCP tools fetch, title generation, follow-up analysis).
	// Used in tests to avoid interference with mock ACP servers.
	DisableAuxiliary bool

	logger *slog.Logger

	// GC fields — see acp_process_gc.go
	gcConfig        GCConfig
	gcStop          chan struct{}
	gcDone          chan struct{}
	gcRunning       bool
	lastSessionSeen map[string]time.Time // per workspace UUID, when sessions were last present
	sessionQuery    SessionQueryFunc
	sessionClose    SessionCloseFunc
	gcMu            sync.Mutex // protects lastSessionSeen and gc lifecycle fields
}

// auxSessionKey uniquely identifies an auxiliary session.
type auxSessionKey struct {
	workspaceUUID string
	purpose       string // e.g., "title-gen", "follow-up", "improve-prompt"
}

// auxiliarySessionState tracks an auxiliary session's state.
type auxiliarySessionState struct {
	mu        sync.Mutex // Serializes requests to this session
	sessionID string
	client    *auxiliaryClient // Collects responses
	lastUsed  time.Time
}

func sharedProcessConfigMatchesWorkspace(p *SharedACPProcess, workspace *config.WorkspaceSettings) bool {
	if p == nil || workspace == nil {
		return false
	}
	return p.config.ACPServer == workspace.ACPServer &&
		p.config.ACPCommand == workspace.ACPCommand &&
		p.config.ACPCwd == workspace.ACPCwd
}

// NewACPProcessManager creates a new process manager.
func NewACPProcessManager(ctx context.Context, logger *slog.Logger) *ACPProcessManager {
	return &ACPProcessManager{
		processes:    make(map[string]*SharedACPProcess),
		auxProcesses: make(map[string]*SharedACPProcess),
		auxSessions:  make(map[auxSessionKey]*auxiliarySessionState),
		ctx:          ctx,
		logger:       logger,
	}
}

// Ensure ACPProcessManager implements auxiliary.ProcessProvider
var _ auxiliary.ProcessProvider = (*ACPProcessManager)(nil)

// GetOrCreateProcess returns the shared ACP process for the given workspace,
// creating one if it doesn't exist yet. If prewarm is true and a new process is
// created, auxiliary sessions are pre-warmed in the background.
func (m *ACPProcessManager) GetOrCreateProcess(workspace *config.WorkspaceSettings, r *runner.Runner, prewarm bool) (*SharedACPProcess, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	if workspace.UUID == "" {
		return nil, fmt.Errorf("workspace UUID is required")
	}

	lockStart := time.Now()
	m.mu.Lock()
	lockWait := time.Since(lockStart)

	// Check if process already exists and is alive
	if p, ok := m.processes[workspace.UUID]; ok {
		select {
		case <-p.Done():
			// Process is dead, clean up and recreate
			if m.logger != nil {
				m.logger.Info("Shared ACP process found dead, recreating",
					"workspace_uuid", workspace.UUID,
					"acp_server", workspace.ACPServer)
			}
			delete(m.processes, workspace.UUID)
		default:
			if !sharedProcessConfigMatchesWorkspace(p, workspace) {
				if m.logger != nil {
					m.logger.Warn("Shared ACP process config changed, recreating",
						"workspace_uuid", workspace.UUID,
						"existing_acp_server", p.config.ACPServer,
						"new_acp_server", workspace.ACPServer,
						"existing_acp_command", p.config.ACPCommand,
						"new_acp_command", workspace.ACPCommand)
				}
				p.Close()
				delete(m.processes, workspace.UUID)
				break
			}

			// Process is alive, return it
			m.mu.Unlock()
			if m.logger != nil && lockWait > 10*time.Millisecond {
				m.logger.Info("GetOrCreateProcess returning existing (lock contention)",
					"workspace_uuid", workspace.UUID,
					"lock_wait_ms", lockWait.Milliseconds())
			}
			return p, nil
		}
	}

	// Create new shared process
	processLogger := m.logger
	if processLogger != nil {
		processLogger = processLogger.With("workspace_uuid", workspace.UUID)
	}

	createStart := time.Now()
	p, err := NewSharedACPProcess(m.ctx, SharedACPProcessConfig{
		ACPCommand: workspace.ACPCommand,
		ACPCwd:     workspace.ACPCwd,
		ACPServer:  workspace.ACPServer,
		WorkingDir: workspace.WorkingDir,
		Runner:     r,
		Logger:     processLogger,
	})
	createDuration := time.Since(createStart)

	if err != nil {
		m.mu.Unlock()
		if m.logger != nil {
			m.logger.Warn("GetOrCreateProcess failed to create process",
				"workspace_uuid", workspace.UUID,
				"lock_wait_ms", lockWait.Milliseconds(),
				"create_ms", createDuration.Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to start shared ACP process for workspace %s: %w", workspace.UUID, err)
	}

	m.processes[workspace.UUID] = p
	// Release lock before pre-warming: prewarmAuxiliarySessions calls GetProcess
	// which also acquires m.mu, so the lock must be released first.
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("Created shared ACP process for workspace",
			"workspace_uuid", workspace.UUID,
			"acp_server", workspace.ACPServer,
			"lock_wait_ms", lockWait.Milliseconds(),
			"create_process_ms", createDuration.Milliseconds())
	}

	// Pre-warm auxiliary sessions so they're ready when needed.
	if !m.DisableAuxiliary && prewarm {
		go m.prewarmAuxiliarySessions(workspace.UUID, processLogger)
	}

	return p, nil
}

// GetProcess returns the shared process for a workspace, or nil if none exists.
func (m *ACPProcessManager) GetProcess(workspaceUUID string) *SharedACPProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.processes[workspaceUUID]
}

// GetOrCreateAuxProcess returns or creates a dedicated auxiliary ACP process for the workspace.
// auxACPCommand is the resolved shell command for the workspace's AuxiliaryACPServer.
// Returns (nil, nil) if no dedicated aux process is needed for this workspace.
func (m *ACPProcessManager) GetOrCreateAuxProcess(workspace *config.WorkspaceSettings, auxACPCommand string, r *runner.Runner) (*SharedACPProcess, error) {
	if workspace == nil || workspace.UUID == "" {
		return nil, fmt.Errorf("workspace with UUID is required")
	}
	if auxACPCommand == "" {
		return nil, nil
	}

	lockStart := time.Now()
	m.mu.Lock()
	lockWait := time.Since(lockStart)

	// Check if dedicated aux process already exists and is alive
	if p, ok := m.auxProcesses[workspace.UUID]; ok {
		select {
		case <-p.Done():
			// Process is dead, clean up and recreate
			if m.logger != nil {
				m.logger.Info("Dedicated aux ACP process found dead, recreating",
					"workspace_uuid", workspace.UUID)
			}
			delete(m.auxProcesses, workspace.UUID)
		default:
			if p.config.ACPCommand != auxACPCommand {
				// Config changed, close and recreate
				if m.logger != nil {
					m.logger.Warn("Dedicated aux ACP process config changed, recreating",
						"workspace_uuid", workspace.UUID,
						"existing_command", p.config.ACPCommand,
						"new_command", auxACPCommand)
				}
				p.Close()
				delete(m.auxProcesses, workspace.UUID)
			} else {
				// Process is alive and config matches, return it
				m.mu.Unlock()
				if m.logger != nil && lockWait > 10*time.Millisecond {
					m.logger.Info("GetOrCreateAuxProcess returning existing (lock contention)",
						"workspace_uuid", workspace.UUID,
						"lock_wait_ms", lockWait.Milliseconds())
				}
				return p, nil
			}
		}
	}

	processLogger := m.logger
	if processLogger != nil {
		processLogger = processLogger.With("workspace_uuid", workspace.UUID, "aux", true)
	}

	createStart := time.Now()
	p, err := NewSharedACPProcess(m.ctx, SharedACPProcessConfig{
		ACPCommand: auxACPCommand,
		ACPServer:  workspace.AuxiliaryACPServer,
		WorkingDir: workspace.WorkingDir,
		Runner:     r,
		Logger:     processLogger,
	})
	createDuration := time.Since(createStart)

	if err != nil {
		m.mu.Unlock()
		if m.logger != nil {
			m.logger.Warn("GetOrCreateAuxProcess failed to create process",
				"workspace_uuid", workspace.UUID,
				"lock_wait_ms", lockWait.Milliseconds(),
				"create_ms", createDuration.Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to start dedicated aux ACP process for workspace %s: %w", workspace.UUID, err)
	}

	m.auxProcesses[workspace.UUID] = p
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("Created dedicated aux ACP process for workspace",
			"workspace_uuid", workspace.UUID,
			"aux_acp_server", workspace.AuxiliaryACPServer,
			"lock_wait_ms", lockWait.Milliseconds(),
			"create_process_ms", createDuration.Milliseconds())
	}

	// Pre-warm auxiliary sessions on the dedicated process.
	// getOrCreateAuxiliarySession will find the aux process via getAuxProcess.
	if !m.DisableAuxiliary {
		go m.prewarmAuxiliarySessions(workspace.UUID, processLogger)
	}

	return p, nil
}

// getAuxProcess returns the dedicated auxiliary process for a workspace, or nil.
func (m *ACPProcessManager) getAuxProcess(workspaceUUID string) *SharedACPProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auxProcesses[workspaceUUID]
}

// StopAuxProcess stops the dedicated auxiliary process for a workspace.
func (m *ACPProcessManager) StopAuxProcess(workspaceUUID string) {
	m.mu.Lock()
	p, ok := m.auxProcesses[workspaceUUID]
	if ok {
		delete(m.auxProcesses, workspaceUUID)
	}
	m.mu.Unlock()
	if ok && p != nil {
		if m.logger != nil {
			m.logger.Info("Stopping dedicated aux ACP process",
				"workspace_uuid", workspaceUUID)
		}
		p.Close()
	}
}

// CreateSession creates a new ACP session on the shared process for the given workspace.
// If no shared process exists yet, one is created.
func (m *ACPProcessManager) CreateSession(
	ctx context.Context,
	workspace *config.WorkspaceSettings,
	r *runner.Runner,
	cwd string,
	mcpServers []acp.McpServer,
) (*SessionHandle, error) {
	process, err := m.GetOrCreateProcess(workspace, r, true)
	if err != nil {
		return nil, err
	}

	return process.NewSession(ctx, cwd, mcpServers)
}

// LoadSession attempts to load/resume an existing ACP session on the shared process.
func (m *ACPProcessManager) LoadSession(
	ctx context.Context,
	workspace *config.WorkspaceSettings,
	r *runner.Runner,
	acpSessionID string,
	cwd string,
	mcpServers []acp.McpServer,
) (*SessionHandle, error) {
	process, err := m.GetOrCreateProcess(workspace, r, true)
	if err != nil {
		return nil, err
	}

	return process.LoadSession(ctx, acpSessionID, cwd, mcpServers)
}

// StopProcess stops the shared process for a workspace.
// This should be called when the last session in a workspace is closed.
func (m *ACPProcessManager) StopProcess(workspaceUUID string) {
	// Close auxiliary sessions first
	m.CloseWorkspaceAuxiliary(workspaceUUID)

	m.mu.Lock()
	p, ok := m.processes[workspaceUUID]
	if ok {
		delete(m.processes, workspaceUUID)
	}
	m.mu.Unlock()

	if ok && p != nil {
		if m.logger != nil {
			m.logger.Info("Stopping shared ACP process",
				"workspace_uuid", workspaceUUID)
		}
		p.Close()
	}
}

// RestartProcess restarts the shared process for a workspace.
// All sessions on the process will need to re-register and LoadSession.
func (m *ACPProcessManager) RestartProcess(workspaceUUID string) error {
	m.mu.Lock()
	p, ok := m.processes[workspaceUUID]
	m.mu.Unlock()

	if !ok || p == nil {
		return fmt.Errorf("no shared process for workspace %s", workspaceUUID)
	}

	return p.Restart()
}

// Close stops all managed processes.
func (m *ACPProcessManager) Close() {
	m.mu.Lock()
	processes := make(map[string]*SharedACPProcess, len(m.processes))
	for k, v := range m.processes {
		processes[k] = v
	}
	m.processes = make(map[string]*SharedACPProcess)

	// Also collect all dedicated auxiliary processes
	auxProcesses := make(map[string]*SharedACPProcess, len(m.auxProcesses))
	for k, v := range m.auxProcesses {
		auxProcesses[k] = v
	}
	m.auxProcesses = make(map[string]*SharedACPProcess)
	m.mu.Unlock()

	for uuid, p := range processes {
		if m.logger != nil {
			m.logger.Info("Stopping shared ACP process on shutdown",
				"workspace_uuid", uuid)
		}
		p.Close()
	}

	// Close all dedicated auxiliary processes
	for uuid, p := range auxProcesses {
		if m.logger != nil {
			m.logger.Info("Stopping dedicated aux ACP process on shutdown",
				"workspace_uuid", uuid)
		}
		p.Close()
	}
}

// ProcessCount returns the number of active shared processes.
func (m *ACPProcessManager) ProcessCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.processes)
}

// ============================================================================
// Auxiliary Session Management (implements auxiliary.ProcessProvider)
// ============================================================================

// PromptAuxiliary sends a prompt to an auxiliary session for the given workspace and purpose.
// The session is created on-demand if it doesn't exist and reused for subsequent requests.
// This implements the auxiliary.ProcessProvider interface.
func (m *ACPProcessManager) PromptAuxiliary(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
	if m.DisableAuxiliary {
		return "", fmt.Errorf("auxiliary sessions disabled")
	}

	// Check context before doing any work
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled before auxiliary prompt: %w", err)
	}

	// Get or create the auxiliary session
	auxState, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
	if err != nil {
		return "", fmt.Errorf("failed to get auxiliary session: %w", err)
	}

	// Try to acquire the mutex with context cancellation support
	// This prevents indefinite blocking if a previous request is stuck
	acquired := make(chan struct{})
	go func() {
		auxState.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		// Successfully acquired the lock
		defer auxState.mu.Unlock()
	case <-ctx.Done():
		// Context cancelled while waiting for lock
		return "", fmt.Errorf("context cancelled while waiting for auxiliary session lock: %w", ctx.Err())
	}

	// Update last used time
	auxState.lastUsed = time.Now()

	// Use the dedicated aux process if available, otherwise fall back to the main process.
	process := m.getAuxProcess(workspaceUUID)
	if process == nil {
		// Fall back to main workspace process
		process = m.GetProcess(workspaceUUID)
	}
	if process == nil {
		return "", fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
	}

	// Reset the response buffer
	auxState.client.reset()

	// Send prompt to the auxiliary session
	_, err = process.Prompt(ctx, acp.SessionId(auxState.sessionID), []acp.ContentBlock{acp.TextBlock(message)})
	if err != nil {
		return "", fmt.Errorf("auxiliary prompt failed: %w", err)
	}

	// Get the collected response
	response := auxState.client.getResponse()
	return response, nil
}

// getOrCreateAuxiliarySession returns an existing auxiliary session or creates a new one.
// The entire function holds auxMu to prevent a TOCTOU race where two concurrent callers
// both observe a missing entry and each create a duplicate session.
// Auxiliary sessions are created rarely (prewarm + on-demand), so holding the lock during
// creation is acceptable.
func (m *ACPProcessManager) getOrCreateAuxiliarySession(ctx context.Context, workspaceUUID, purpose string) (*auxiliarySessionState, error) {
	key := auxSessionKey{
		workspaceUUID: workspaceUUID,
		purpose:       purpose,
	}

	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	// Check if session already exists (double-check under lock).
	if state, ok := m.auxSessions[key]; ok {
		return state, nil
	}

	// Need to create a new auxiliary session.
	// Use dedicated aux process if available, otherwise fall back to the main workspace process.
	process := m.getAuxProcess(workspaceUUID)
	if process == nil {
		// Fall back: main workspace process
		// Note: This assumes the process was already created by a user session.
		// If not, this will fail - auxiliary sessions require an existing workspace process.
		process = m.GetProcess(workspaceUUID)
	}
	if process == nil {
		return nil, fmt.Errorf("no shared process for workspace %s (auxiliary sessions require an active workspace)", workspaceUUID)
	}

	// Create a new ACP session for auxiliary use.
	// Use the workspace's actual working directory so the agent discovers the same
	// MCP servers as regular sessions (the agent uses the cwd for MCP server discovery).
	auxCwd := process.WorkingDir()
	if auxCwd == "" {
		auxCwd = "."
	}

	sessionHandle, err := process.NewSession(ctx, auxCwd, []acp.McpServer{})
	if err != nil {
		return nil, fmt.Errorf("failed to create auxiliary session: %w", err)
	}

	// Create auxiliary client to collect responses
	client := newAuxiliaryClient()

	// Register the session with the multiplexer
	callbacks := &SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			return client.OnSessionUpdate(ctx, params)
		},
		OnRequestPermission: func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return client.OnRequestPermission(ctx, params)
		},
		OnReadTextFile: func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
			return client.OnReadTextFile(ctx, params)
		},
		OnWriteTextFile: func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
			return client.OnWriteTextFile(ctx, params)
		},
		OnCreateTerminal: func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
			return auxTerminalStub.CreateTerminal(ctx, params)
		},
		OnTerminalOutput: func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
			return auxTerminalStub.TerminalOutput(ctx, params)
		},
		OnReleaseTerminal: func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
			return auxTerminalStub.ReleaseTerminal(ctx, params)
		},
		OnWaitForTerminalExit: func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
			return auxTerminalStub.WaitForTerminalExit(ctx, params)
		},
		OnKillTerminalCommand: func(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
			return auxTerminalStub.KillTerminalCommand(ctx, params)
		},
	}
	process.RegisterSession(acp.SessionId(sessionHandle.SessionID), callbacks)

	// Create and store the auxiliary session state.
	// auxMu is already held for the duration of this function.
	state := &auxiliarySessionState{
		sessionID: sessionHandle.SessionID,
		client:    client,
		lastUsed:  time.Now(),
	}
	m.auxSessions[key] = state

	if m.logger != nil {
		m.logger.Info("Created auxiliary session",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", sessionHandle.SessionID)
	}

	return state, nil
}

// CloseWorkspaceAuxiliary closes all auxiliary sessions for a workspace.
// This implements the auxiliary.ProcessProvider interface.
func (m *ACPProcessManager) CloseWorkspaceAuxiliary(workspaceUUID string) error {
	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	// Find and remove all auxiliary sessions for this workspace
	var sessionsToClose []auxSessionKey
	for key := range m.auxSessions {
		if key.workspaceUUID == workspaceUUID {
			sessionsToClose = append(sessionsToClose, key)
		}
	}

	// Remove from map
	for _, key := range sessionsToClose {
		delete(m.auxSessions, key)
	}

	if m.logger != nil && len(sessionsToClose) > 0 {
		m.logger.Info("Closed auxiliary sessions for workspace",
			"workspace_uuid", workspaceUUID,
			"session_count", len(sessionsToClose))
	}

	// Stop dedicated aux process if exists
	m.StopAuxProcess(workspaceUUID)

	return nil
}

// CleanupStaleAuxiliarySessions removes auxiliary sessions that haven't been used recently.
// This helps recover from stuck sessions and free up resources.
// maxIdleTime specifies how long a session can be idle before being cleaned up.
func (m *ACPProcessManager) CleanupStaleAuxiliarySessions(maxIdleTime time.Duration) int {
	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	now := time.Now()
	var staleKeys []auxSessionKey

	// Find stale sessions
	for key, state := range m.auxSessions {
		if now.Sub(state.lastUsed) > maxIdleTime {
			staleKeys = append(staleKeys, key)
		}
	}

	// Remove stale sessions
	for _, key := range staleKeys {
		delete(m.auxSessions, key)
	}

	if m.logger != nil && len(staleKeys) > 0 {
		m.logger.Info("Cleaned up stale auxiliary sessions",
			"count", len(staleKeys),
			"max_idle_time", maxIdleTime)
	}

	return len(staleKeys)
}

// prewarmAuxiliarySessions eagerly creates auxiliary sessions for the most commonly used
// purposes right after a workspace process starts. This one-time upfront cost means that
// later callers (MCP tool fetch, title generation, follow-up analysis) can find an existing
// aux session immediately without waiting for session creation.
//
// Run in a goroutine after releasing the ACPProcessManager lock.
func (m *ACPProcessManager) prewarmAuxiliarySessions(workspaceUUID string, logger *slog.Logger) {
	// Use a short timeout: session creation should complete quickly after process start.
	// 30 seconds is generous; in practice session creation completes in < 1 second per session.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	purposes := []string{
		auxiliary.PurposeTitleGen,
		auxiliary.PurposeMCPCheck,
		auxiliary.PurposeMCPTools,
		auxiliary.PurposeFollowUp,
	}

	// Fire off all prewarm requests in parallel so all sessions are created concurrently.
	var wg sync.WaitGroup
	for _, purpose := range purposes {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if _, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, p); err != nil {
				if logger != nil {
					logger.Debug("auxiliary session pre-warm failed",
						"workspace_uuid", workspaceUUID,
						"purpose", p,
						"error", err)
				}
			} else {
				if logger != nil {
					logger.Debug("auxiliary session pre-warmed",
						"workspace_uuid", workspaceUUID,
						"purpose", p)
				}
			}
		}(purpose)
	}
	wg.Wait()
}
