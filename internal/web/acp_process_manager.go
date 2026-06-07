package web

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
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
// Auxiliary sessions always run on the same process as the main workspace,
// with optional model selection via WorkspaceConfigProvider.
type ACPProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*SharedACPProcess // keyed by workspace UUID

	// WorkspaceConfigProvider returns workspace settings for a given UUID.
	// Used to look up AuxiliaryModelSelection for new auxiliary sessions.
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

	// MCPServerURL is the URL of Mitto's MCP server (e.g., "http://127.0.0.1:5757/mcp").
	// When set, processor auxiliary sessions get a stdio MCP proxy so the agent
	// can call Mitto tools like mitto_ui_notify.
	MCPServerURL string

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

	// rssSampler samples the RSS (in bytes) of a shared process tree for the GC's
	// memory-recycle tier. It defaults to (*SharedACPProcess).RSSBytes; tests
	// override it to exercise the tier without launching a real subprocess.
	rssSampler func(p *SharedACPProcess) (uint64, error)

	// onMemoryRecycled, if set, is called by the GC's Tier 4 memory-recycle path
	// when a memory-bloated idle shared ACP process is recycled. Used to broadcast
	// a toast notification to connected clients. Set after construction (see NewServer).
	onMemoryRecycled func(workspaceUUID string, rssBytes, threshold uint64, sessionCount int)

	// gcSuspendedSessions tracks session IDs that were intentionally suspended
	// by the GC's periodic-suspend heuristic. When a periodic session's next run
	// is far away, the GC closes it and adds it here. The WebSocket auto-resume
	// handler checks this set and skips resume for flagged sessions, preventing
	// a suspend/resume thrashing loop (GC closes → WS reconnects → auto-resume
	// → GC closes again). The flag is cleared by:
	//   - ensure_resumed (explicit user focus)
	//   - PeriodicRunner (when the prompt is due)
	//   - ResumeSession (any explicit resume call)
	gcSuspendedSessions map[string]bool // protected by gcMu

	// Global restart rate limiter — prevents cross-workspace restart cascades.
	// When multiple workspaces crash simultaneously (e.g., system-wide OOM), individual
	// per-process rate limiters are insufficient because each workspace independently
	// restarts, compounding memory pressure.
	globalRestartMu     sync.Mutex
	globalRestartTimes  []time.Time
	globalCooldownUntil time.Time
}

// MarkGCSuspended records that a session was intentionally suspended by the GC's
// periodic-suspend heuristic. The WebSocket auto-resume handler checks this flag
// and skips resume to prevent suspend/resume thrashing.
func (m *ACPProcessManager) MarkGCSuspended(sessionID string) {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()
	if m.gcSuspendedSessions == nil {
		m.gcSuspendedSessions = make(map[string]bool)
	}
	m.gcSuspendedSessions[sessionID] = true
}

// ClearGCSuspended removes the GC-suspended flag for a session, allowing
// WebSocket auto-resume to proceed normally. Called by ensure_resumed (explicit
// user focus), PeriodicRunner (when the prompt is due), and ResumeSession.
func (m *ACPProcessManager) ClearGCSuspended(sessionID string) {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()
	delete(m.gcSuspendedSessions, sessionID)
}

// IsGCSuspended returns true if the session was intentionally suspended by the
// GC and should not be auto-resumed by WebSocket reconnections.
func (m *ACPProcessManager) IsGCSuspended(sessionID string) bool {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()
	return m.gcSuspendedSessions[sessionID]
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

// sharedProcessConfigMatchesWorkspace returns true if the running process config
// matches the resolved ACP parameters for the workspace.
// acpCommand, acpCwd, and acpEnv are the runtime-resolved values (not stored on workspace).
//
// Comparison notes (intentional, to avoid spurious recreation):
//   - ACPCwd is compared as the RAW (unexpanded) value on both sides. The stored
//     p.config.ACPCwd and the freshly-resolved acpCwd both originate from the same
//     resolution path (config server.Cwd, see resolveWorkspaceACPLocked) and are
//     expanded ($MITTO_*) only later, at process start. Comparing raw-vs-raw is
//     therefore correct; we must NOT expand here (expanding only one side would
//     create a false mismatch).
//   - Env is compared by content via mapsEqual, which treats a nil map and an empty
//     map as equal. This is the only benign-equivalence normalization applied: a
//     config reload may rebuild the Env map (new reference) without changing its
//     contents, and a process started with no env (nil) must match a re-resolved
//     empty map. Any genuine env key/value change still triggers recreation.
func sharedProcessConfigMatchesWorkspace(p *SharedACPProcess, acpServer, acpCommand, acpCwd string, acpEnv map[string]string) bool {
	if p == nil {
		return false
	}
	if p.config.ACPServer != acpServer ||
		p.config.ACPCommand != acpCommand ||
		p.config.ACPCwd != acpCwd {
		return false
	}
	// Compare environment variables — a change to Env (e.g., NODE_OPTIONS)
	// should trigger process recreation so the new values take effect.
	return mapsEqual(p.config.Env, acpEnv)
}

// mapsEqual returns true if two string maps have identical key-value pairs.
// Two nil maps are considered equal, as are a nil and an empty map.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// diffEnvKeys compares two env maps and returns the sorted KEY NAMES that were
// added (present in b but not a), removed (present in a but not b), or changed
// (present in both with different values).
//
// SECURITY: only key names are ever returned — never values — because env values
// may hold secrets (API keys, tokens). Callers log these keys to make a config
// recreation diagnosable without leaking secrets.
func diffEnvKeys(a, b map[string]string) (added, removed, changed []string) {
	for k, bv := range b {
		if av, ok := a[k]; !ok {
			added = append(added, k)
		} else if av != bv {
			changed = append(changed, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

// NewACPProcessManager creates a new process manager.
// It does NOT perform orphan cleanup — call CleanupOrphanedProcesses() explicitly
// at server startup if orphan cleanup is desired.
func NewACPProcessManager(ctx context.Context, logger *slog.Logger) *ACPProcessManager {
	return &ACPProcessManager{
		processes:   make(map[string]*SharedACPProcess),
		auxSessions: make(map[auxSessionKey]*auxiliarySessionState),
		ctx:         ctx,
		logger:      logger,
	}
}

// CleanupOrphanedProcesses kills any ACP processes left over from a previous Mitto
// instance that crashed without running its shutdown sequence. Call this once at
// server startup, before creating any new processes. Not called in tests.
func (m *ACPProcessManager) CleanupOrphanedProcesses() {
	cleanupOrphanedACPProcesses(m.logger)
}

// Ensure ACPProcessManager implements auxiliary.ProcessProvider
var _ auxiliary.ProcessProvider = (*ACPProcessManager)(nil)

// GetOrCreateProcess returns the shared ACP process for the given workspace,
// creating one if it doesn't exist yet. If prewarm is true and a new process is
// created, auxiliary sessions are pre-warmed in the background.
//
// acpCommand, acpCwd, and acpEnv are the runtime-resolved ACP connection parameters.
// They must NOT be read from the workspace struct (those fields no longer exist) and
// must be resolved from global config by the caller (e.g. via resolveWorkspaceACPLocked).
func (m *ACPProcessManager) GetOrCreateProcess(workspace *config.WorkspaceSettings, acpCommand, acpCwd string, acpEnv map[string]string, r *runner.Runner, prewarm bool) (*SharedACPProcess, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	if workspace.UUID == "" {
		return nil, fmt.Errorf("workspace UUID is required")
	}

	lockStart := time.Now()
	m.mu.Lock()
	lockWait := time.Since(lockStart)

	recreated := false // Track whether we're replacing a dead/changed process

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
			recreated = true
		default:
			if !sharedProcessConfigMatchesWorkspace(p, workspace.ACPServer, acpCommand, acpCwd, acpEnv) {
				if m.logger != nil {
					// Log EXACTLY which field(s) differ so spurious recreations are
					// diagnosable. Env is logged as key names only (never values),
					// see diffEnvKeys.
					addedKeys, removedKeys, changedKeys := diffEnvKeys(p.config.Env, acpEnv)
					envChanged := len(addedKeys) > 0 || len(removedKeys) > 0 || len(changedKeys) > 0
					changedFields := make([]string, 0, 4)
					if p.config.ACPServer != workspace.ACPServer {
						changedFields = append(changedFields, "server")
					}
					if p.config.ACPCommand != acpCommand {
						changedFields = append(changedFields, "command")
					}
					if p.config.ACPCwd != acpCwd {
						changedFields = append(changedFields, "cwd")
					}
					if envChanged {
						changedFields = append(changedFields, "env")
					}
					m.logger.Warn("Shared ACP process config changed, recreating",
						"workspace_uuid", workspace.UUID,
						"existing_acp_server", p.config.ACPServer,
						"new_acp_server", workspace.ACPServer,
						"existing_acp_command", p.config.ACPCommand,
						"new_acp_command", acpCommand,
						"existing_acp_cwd", p.config.ACPCwd,
						"new_acp_cwd", acpCwd,
						"env_changed", envChanged,
						"env_keys_added", addedKeys,
						"env_keys_removed", removedKeys,
						"env_keys_changed", changedKeys,
						"changed_fields", changedFields)
				}
				p.Close()
				delete(m.processes, workspace.UUID)
				recreated = true
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
		WorkspaceUUID:    workspace.UUID,
		ACPCommand:       acpCommand,
		ACPCwd:           acpCwd,
		ACPServer:        workspace.ACPServer,
		WorkingDir:       workspace.WorkingDir,
		Env:              acpEnv,
		Runner:           r,
		Logger:           processLogger,
		CanRestartGlobal: m.CanRestartGlobally,
		RecordRestart:    m.RecordGlobalRestart,
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

	// Register restart callback so auxiliary sessions are invalidated when the
	// shared process restarts (e.g., after an OOM crash during streaming).
	// The callback captures workspaceUUID by value for use after m.mu is released.
	wuuid := workspace.UUID
	p.SetOnRestart(func() {
		m.invalidateAuxiliarySessions(wuuid)
	})

	// Release lock before pre-warming: prewarmAuxiliarySessions calls GetProcess
	// which also acquires m.mu, so the lock must be released first.
	m.mu.Unlock()

	// If the process was recreated (dead or config changed), invalidate cached
	// auxiliary sessions. Those sessions were on the old process and their IDs
	// are unknown to the new process. Must be called after m.mu is released to
	// respect lock ordering (auxMu → mu).
	if recreated {
		m.invalidateAuxiliarySessions(workspace.UUID)
	}

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

// CreateSession creates a new ACP session on the shared process for the given workspace.
// If no shared process exists yet, one is created.
// acpCommand, acpCwd, acpEnv are the runtime-resolved ACP connection parameters.
func (m *ACPProcessManager) CreateSession(
	ctx context.Context,
	workspace *config.WorkspaceSettings,
	acpCommand, acpCwd string,
	acpEnv map[string]string,
	r *runner.Runner,
	cwd string,
	mcpServers []acp.McpServer,
) (*SessionHandle, error) {
	process, err := m.GetOrCreateProcess(workspace, acpCommand, acpCwd, acpEnv, r, true)
	if err != nil {
		return nil, err
	}

	return process.NewSession(ctx, cwd, mcpServers)
}

// LoadSession attempts to load/resume an existing ACP session on the shared process.
// acpCommand, acpCwd, acpEnv are the runtime-resolved ACP connection parameters.
func (m *ACPProcessManager) LoadSession(
	ctx context.Context,
	workspace *config.WorkspaceSettings,
	acpCommand, acpCwd string,
	acpEnv map[string]string,
	r *runner.Runner,
	acpSessionID string,
	cwd string,
	mcpServers []acp.McpServer,
) (*SessionHandle, error) {
	process, err := m.GetOrCreateProcess(workspace, acpCommand, acpCwd, acpEnv, r, true)
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
	m.mu.Unlock()

	for uuid, p := range processes {
		if m.logger != nil {
			m.logger.Info("Stopping shared ACP process on shutdown",
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
	if err := acquireAuxLock(ctx, auxState); err != nil {
		return "", err
	}

	// Update last used time
	auxState.lastUsed = time.Now()

	process := m.GetProcess(workspaceUUID)
	if process == nil {
		auxState.mu.Unlock()
		return "", fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
	}

	// Reset the response buffer
	auxState.client.reset()

	// Send prompt to the auxiliary session
	_, err = process.Prompt(ctx, acp.SessionId(auxState.sessionID), []acp.ContentBlock{acp.TextBlock(message)})
	if err != nil {
		// Always release the lock before returning or retrying.
		auxState.mu.Unlock()

		if !isACPConnectionError(err) {
			return "", fmt.Errorf("auxiliary prompt failed: %w", err)
		}

		// The underlying ACP process died. Invalidate the stale session,
		// wait briefly for the process to potentially auto-restart, then retry once.
		if m.logger != nil {
			m.logger.Warn("Auxiliary prompt hit connection error, retrying after session invalidation",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"error", err)
		}
		m.invalidateAuxSession(workspaceUUID, purpose)

		// Wait 1 second for the process to auto-restart, honouring context cancellation.
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting to retry auxiliary prompt: %w", ctx.Err())
		case <-time.After(time.Second):
		}

		// Re-acquire a fresh session and its lock.
		auxState, err = m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
		if err != nil {
			return "", fmt.Errorf("failed to get auxiliary session on retry: %w", err)
		}

		if err := acquireAuxLock(ctx, auxState); err != nil {
			return "", err
		}

		auxState.lastUsed = time.Now()

		process = m.GetProcess(workspaceUUID)
		if process == nil {
			auxState.mu.Unlock()
			return "", fmt.Errorf("shared process for workspace %s disappeared on retry (process may have exited)", workspaceUUID)
		}

		auxState.client.reset()
		_, err = process.Prompt(ctx, acp.SessionId(auxState.sessionID), []acp.ContentBlock{acp.TextBlock(message)})
		if err != nil {
			auxState.mu.Unlock()
			if m.logger != nil {
				m.logger.Error("Auxiliary prompt failed after retry",
					"workspace_uuid", workspaceUUID,
					"purpose", purpose,
					"error", err)
			}
			return "", fmt.Errorf("auxiliary prompt failed: %w", err)
		}
	}

	// Get the collected response (lock is still held here)
	response := auxState.client.getResponse()
	auxState.mu.Unlock()
	return response, nil
}

// PromptAuxiliaryAsync sends a prompt to an auxiliary session without waiting for the response.
// The session is created on-demand if it doesn't exist and reused for subsequent requests.
// The prompt is dispatched and the method returns immediately — the agent processes in the background.
// The session mutex is held until the agent finishes, ensuring subsequent prompts are serialized.
// This implements the auxiliary.ProcessProvider interface.
func (m *ACPProcessManager) PromptAuxiliaryAsync(ctx context.Context, workspaceUUID, purpose, message string) error {
	if m.DisableAuxiliary {
		return fmt.Errorf("auxiliary sessions disabled")
	}

	// Check context before doing any work
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before auxiliary async prompt: %w", err)
	}

	// Get or create the auxiliary session
	auxState, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
	if err != nil {
		return fmt.Errorf("failed to get auxiliary session: %w", err)
	}

	// Try to acquire the mutex with context cancellation support
	acquired := make(chan struct{})
	go func() {
		auxState.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		// Successfully acquired the lock — we'll release it in the background goroutine
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while waiting for auxiliary session lock: %w", ctx.Err())
	}

	// Update last used time
	auxState.lastUsed = time.Now()

	process := m.GetProcess(workspaceUUID)
	if process == nil {
		auxState.mu.Unlock()
		return fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
	}

	// Reset the response buffer
	auxState.client.reset()

	if m.logger != nil {
		m.logger.Info("Dispatching async auxiliary prompt",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"prompt_length", len(message))
	}

	// Fire-and-forget: send the prompt and release the lock in the background when the agent finishes.
	// This ensures subsequent prompts to the same session are serialized.
	// process.Prompt blocks until the agent completes, so the lock is held for the duration.
	go func() {
		defer auxState.mu.Unlock()
		waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, _ = process.Prompt(waitCtx, acp.SessionId(auxState.sessionID), []acp.ContentBlock{acp.TextBlock(message)})
	}()

	return nil
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
	// Auxiliary sessions always use the main workspace process.
	// Note: This assumes the process was already created by a user session.
	// If not, this will fail - auxiliary sessions require an existing workspace process.
	process := m.GetProcess(workspaceUUID)
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

	// Build MCP servers list. Processor auxiliary sessions get a stdio MCP proxy
	// so the agent can call Mitto tools (e.g., mitto_ui_notify for notifications).
	mcpServers := []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	if strings.HasPrefix(purpose, auxiliary.PurposeProcessorPrefix) && m.MCPServerURL != "" {
		if exe, err := os.Executable(); err == nil {
			mcpServers = []acp.McpServer{{
				Stdio: &acp.McpServerStdio{
					Name:    "mitto",
					Command: exe,
					Args:    []string{"mcp", "--proxy-to", m.MCPServerURL},
					Env:     []acp.EnvVariable{}, // Must be empty array, not nil — ACP validates this
				},
			}}
			if m.logger != nil {
				m.logger.Debug("Auxiliary processor session will use MCP proxy",
					"purpose", purpose,
					"mcp_url", m.MCPServerURL)
			}
		}
	}
	sessionHandle, err := process.NewSession(ctx, auxCwd, mcpServers)
	if err != nil {
		return nil, fmt.Errorf("failed to create auxiliary session: %w", err)
	}

	// Apply auxiliary model selection if configured for this workspace.
	// If AuxiliaryModelSelection is set and a model matches, switch the session model.
	// On no match or nil selection, leave the ACP server's default model unchanged.
	if m.WorkspaceConfigProvider != nil {
		if ws := m.WorkspaceConfigProvider(workspaceUUID); ws != nil && ws.AuxiliaryModelSelection != nil && ws.AuxiliaryModelSelection.Pattern != "" {
			options := modelsToConfigOptions(sessionHandle.Models)
			if matched := matchConstraintOption(ws.AuxiliaryModelSelection, options); matched != "" {
				// Derive from m.ctx, NOT from ctx: NewSession above may have consumed most
				// of ctx's budget (e.g., in prewarmAuxiliarySessions where multiple goroutines
				// were previously sharing a single deadline), making ctx already expired by the
				// time SetSessionModel runs. Using m.ctx gives SetSessionModel its full 10-second
				// window regardless of caller-deadline pressure. m.ctx is cancelled on manager
				// shutdown, providing a safety backstop so this never hangs indefinitely.
				setCtx, setCancel := context.WithTimeout(m.ctx, 10*time.Second)
				defer setCancel()
				if setErr := process.SetSessionModel(setCtx, acp.SessionId(sessionHandle.SessionID), matched); setErr != nil {
					if m.logger != nil {
						m.logger.Warn("Auxiliary session: failed to set model",
							"workspace_uuid", workspaceUUID,
							"purpose", purpose,
							"model_id", matched,
							"error", setErr)
					}
				} else if m.logger != nil {
					m.logger.Info("Auxiliary session: model set via AuxiliaryModelSelection",
						"workspace_uuid", workspaceUUID,
						"purpose", purpose,
						"model_id", matched)
				}
			} else if m.logger != nil {
				m.logger.Debug("Auxiliary session: no model matched AuxiliaryModelSelection, using server default",
					"workspace_uuid", workspaceUUID,
					"purpose", purpose,
					"match_mode", ws.AuxiliaryModelSelection.MatchMode,
					"pattern", ws.AuxiliaryModelSelection.Pattern)
			}
		}
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
		OnKillTerminal: func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
			return auxTerminalStub.KillTerminal(ctx, params)
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

	return nil
}

// invalidateAuxiliarySessions removes cached auxiliary session entries for a workspace,
// forcing new sessions to be created on the next PromptAuxiliary call.
// Unlike CloseWorkspaceAuxiliary, this does NOT stop the dedicated aux process
// (which uses a separate ACP server and is unaffected by main process recreation).
// This must be called AFTER releasing m.mu to respect lock ordering (auxMu → mu).
func (m *ACPProcessManager) invalidateAuxiliarySessions(workspaceUUID string) {
	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	var count int
	for key := range m.auxSessions {
		if key.workspaceUUID == workspaceUUID {
			delete(m.auxSessions, key)
			count++
		}
	}

	if m.logger != nil && count > 0 {
		m.logger.Info("Invalidated stale auxiliary sessions due to process recreation",
			"workspace_uuid", workspaceUUID,
			"count", count)
	}
}

// invalidateAuxSession removes a single cached auxiliary session entry,
// forcing a new session to be created on the next PromptAuxiliary call.
// This is more surgical than invalidateAuxiliarySessions which removes all sessions for a workspace.
// Must be called WITHOUT holding auxMu.
func (m *ACPProcessManager) invalidateAuxSession(workspaceUUID, purpose string) {
	key := auxSessionKey{workspaceUUID: workspaceUUID, purpose: purpose}
	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	if _, ok := m.auxSessions[key]; ok {
		delete(m.auxSessions, key)
		if m.logger != nil {
			m.logger.Info("Invalidated stale auxiliary session for retry",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose)
		}
	}
}

// acquireAuxLock acquires the auxiliary session mutex with context cancellation support.
// This prevents indefinite blocking if a previous request is stuck.
// The caller is responsible for calling auxState.mu.Unlock() when done.
func acquireAuxLock(ctx context.Context, auxState *auxiliarySessionState) error {
	acquired := make(chan struct{})
	go func() {
		auxState.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while waiting for auxiliary session lock: %w", ctx.Err())
	}
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

// EnsurePrewarmed checks whether the workspace has pre-warmed auxiliary sessions
// (at minimum title-gen) and launches async pre-warming if not.
// This is cheap to call repeatedly — it only checks the auxSessions map under a lock.
//
// This should be called when creating a new BackgroundSession on an existing shared
// process. When a shared process is first created, prewarmAuxiliarySessions runs
// automatically. But auxiliary sessions can be lost (server restart, process recreation,
// idle reaping) and won't be re-created until something needs them. Without this,
// title generation can block for minutes waiting for a NewSession RPC while the agent
// is busy with extended thinking on the main prompt.
func (m *ACPProcessManager) EnsurePrewarmed(workspaceUUID string, logger *slog.Logger) {
	if m.DisableAuxiliary {
		return
	}

	m.auxMu.Lock()
	key := auxSessionKey{workspaceUUID, auxiliary.PurposeTitleGen}
	_, exists := m.auxSessions[key]
	m.auxMu.Unlock()

	if !exists {
		go m.prewarmAuxiliarySessions(workspaceUUID, logger)
	}
}

// prewarmAuxiliarySessions eagerly creates auxiliary sessions for the most commonly used
// purposes right after a workspace process starts. This one-time upfront cost means that
// later callers (MCP tool fetch, title generation, follow-up analysis) can find an existing
// aux session immediately without waiting for session creation.
//
// Run in a goroutine after releasing the ACPProcessManager lock.
func (m *ACPProcessManager) prewarmAuxiliarySessions(workspaceUUID string, logger *slog.Logger) {
	purposes := []string{
		auxiliary.PurposeTitleGen,
		auxiliary.PurposeMCPCheck,
		auxiliary.PurposeMCPTools,
		auxiliary.PurposeFollowUp,
	}

	// Fire off all prewarm requests in parallel so all sessions are created concurrently.
	// Each goroutine gets its OWN independent timeout context so that a slow or queued
	// NewSession for one purpose cannot drain the shared budget and starve the others.
	// Derived from m.ctx (not context.Background()) so manager shutdown propagates.
	var wg sync.WaitGroup
	for _, purpose := range purposes {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			// Independent per-goroutine timeout: avoids cross-session budget starvation.
			// 30 seconds is generous; in practice session creation completes in < 1s.
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			defer cancel()
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
