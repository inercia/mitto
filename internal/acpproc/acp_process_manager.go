package acpproc

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
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

	// PrewarmConfigProvider returns the effective adaptive pre-warming
	// thresholds (mitto-mw0). May be nil, in which case the built-in defaults
	// from config.PrewarmConfig helpers are used. The web layer wires this to
	// the global Config.Prewarm.
	PrewarmConfigProvider func() *config.PrewarmConfig

	// ForkPerSessionProvider reports whether the ACP agent backing the given
	// workspace forks a fresh OS process per ACP session (e.g. Claude Code)
	// vs multiplexing all sessions over one long-lived process (auggie). Nil
	// or false → standard multiplex schedule. Consumed by
	// prewarmAuxiliarySessions to pick the widely-spread fork default set
	// (mitto-7yj).
	ForkPerSessionProvider func(workspaceUUID string) bool

	// ModelProfileResolver resolves a named Model profile (Config.Models) by name.
	// Used to look up AuxiliaryModelProfile for new auxiliary sessions (mitto-hke).
	// May be nil, in which case AuxiliaryModelProfile is ignored and
	// AuxiliaryModelSelection is used as-is.
	ModelProfileResolver func(name string) *config.ModelProfile

	// ModelProfilesByTagResolver returns all Model profiles (Config.Models) carrying
	// a given capability tag, in definition order. Used to resolve AuxiliaryModelTag
	// for new auxiliary sessions (mitto-9vz). May be nil, in which case
	// AuxiliaryModelTag is ignored.
	ModelProfilesByTagResolver func(tag string) []config.ModelProfile

	// StderrPatternsResolver returns the compiled per-agent stderr regex patterns
	// for a given ACP server name (mitto-k6h). The web layer wires this to resolve
	// the ACP server → agent metadata → StderrPatterns → CompileStderrPatterns.
	// May be nil (all processes then use only the hardcoded baseline). Nil result
	// from the resolver is also valid (agent has no per-agent patterns).
	StderrPatternsResolver func(acpServer string) *conversation.CompiledStderrPatterns

	// Auxiliary session tracking
	auxMu       sync.Mutex
	auxSessions map[auxSessionKey]*auxiliarySessionState
	// auxCreateMu holds per-key creation locks (guarded by auxMu).
	// Lets different (workspace, purpose) pairs create concurrently while
	// same-key callers serialize, eliminating the need to hold auxMu across
	// slow NewSession and SetSessionModel RPCs. (mitto-w19)
	auxCreateMu map[auxSessionKey]*sync.Mutex

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
	// by the GC's loop-suspend heuristic. When a loop session's next run
	// is far away, the GC closes it and adds it here. The WebSocket auto-resume
	// handler checks this set and skips resume for flagged sessions, preventing
	// a suspend/resume thrashing loop (GC closes → WS reconnects → auto-resume
	// → GC closes again). The flag is cleared by:
	//   - ensure_resumed (explicit user focus)
	//   - LoopRunner (when the prompt is due)
	//   - ResumeSession (any explicit resume call)
	gcSuspendedSessions map[string]bool // protected by gcMu

	// Global restart rate limiter — prevents cross-workspace restart cascades.
	// When multiple workspaces crash simultaneously (e.g., system-wide OOM), individual
	// per-process rate limiters are insufficient because each workspace independently
	// restarts, compounding memory pressure.
	globalRestartMu     sync.Mutex
	globalRestartTimes  []time.Time
	globalCooldownUntil time.Time

	// MCPInitTimeout is the extended per-attempt/total budget passed to every new
	// SharedACPProcess so cold session/new calls with MCP servers do not hit the
	// standard 25s deadline before the agent finishes its own MCP-init wait
	// (mitto-8ul.1). Zero disables the feature. Guarded by mu.
	mcpInitTimeoutMu sync.RWMutex
	mcpInitTimeout   time.Duration

	// onMCPInitializing, if set, is called (at most once per process) from the
	// stderr-monitor goroutine when the agent reports it is blocked waiting for
	// MCP servers to initialize. onMCPInitTimeout is called (at most once per
	// process) when the agent reports its internal MCP-init wait budget elapsed.
	// Used by the web layer to broadcast UI notifications (mitto-8ul.1).
	onMCPInitializing func(workspaceUUID string)
	onMCPInitTimedOut func(workspaceUUID string)

	// Adaptive pre-warming pin state (mitto-mw0). One entry per pinned
	// workspace. Guarded by pinMu. A pinned workspace exempts its
	// PurposeKeepAlive auxiliary session from AuxIdleTimeout and its Tier 2
	// sessionless process shutdown, keeping the agent warm for the first real
	// prompt on a slow/broken workspace.
	pinMu             sync.Mutex
	pinState          map[string]*pinInfo
	onPrewarmPinAlert func(workspaceUUID, reason string, expired bool)
}

// pinInfo tracks a workspace's pin metadata for the adaptive pre-warming
// controller (mitto-mw0).
type pinInfo struct {
	Reason   string
	PinnedAt time.Time
	Expiry   *time.Time // nil = no cap; otherwise, when the pin auto-expires
	Healthy  int        // consecutive healthy probes observed while pinned (hysteresis)
	Alerted  bool       // whether an alert has been fired for this pin
}

// MarkGCSuspended records that a session was intentionally suspended by the GC's
// loop-suspend heuristic. The WebSocket auto-resume handler checks this flag
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
// user focus), LoopRunner (when the prompt is due), and ResumeSession.
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
	m := &ACPProcessManager{
		processes:   make(map[string]*SharedACPProcess),
		auxSessions: make(map[auxSessionKey]*auxiliarySessionState),
		auxCreateMu: make(map[auxSessionKey]*sync.Mutex),
		pinState:    make(map[string]*pinInfo),
		ctx:         ctx,
		logger:      logger,
	}
	// Diagnostic: expose the live shared-ACP-process count to the coldstart
	// sampler (mitto-3mv). Latest manager wins; benign in tests.
	coldstart.SetLiveACPCounter(func() int {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.processes)
	})
	return m
}

// CleanupOrphanedProcesses kills any ACP processes left over from a previous Mitto
// instance that crashed without running its shutdown sequence. Call this once at
// server startup, before creating any new processes. Not called in tests.
func (m *ACPProcessManager) CleanupOrphanedProcesses() {
	cleanupOrphanedACPProcesses(m.logger)
}

// SetOnMemoryRecycled sets the callback invoked by the GC's Tier 4 memory-recycle
// path when a memory-bloated idle shared ACP process is recycled.
func (m *ACPProcessManager) SetOnMemoryRecycled(fn func(workspaceUUID string, rssBytes, threshold uint64, sessionCount int)) {
	m.onMemoryRecycled = fn
}

// UpdateMCPInitTimeout sets the extended MCP-init budget passed to every new
// SharedACPProcess. Zero disables the feature. Existing processes are not
// updated (the process-level MCPInitTimeout is captured at NewSharedACPProcess
// time). mitto-8ul.1.
func (m *ACPProcessManager) UpdateMCPInitTimeout(d time.Duration) {
	m.mcpInitTimeoutMu.Lock()
	defer m.mcpInitTimeoutMu.Unlock()
	m.mcpInitTimeout = d
}

// getMCPInitTimeout returns the current extended MCP-init budget.
func (m *ACPProcessManager) getMCPInitTimeout() time.Duration {
	m.mcpInitTimeoutMu.RLock()
	defer m.mcpInitTimeoutMu.RUnlock()
	return m.mcpInitTimeout
}

// SetOnMCPInitializing registers the callback invoked (at most once per process)
// when the agent reports it is blocked waiting for MCP servers to initialize.
// Used by the web layer to broadcast an "MCP initializing" UI notification
// (mitto-8ul.1).
func (m *ACPProcessManager) SetOnMCPInitializing(fn func(workspaceUUID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMCPInitializing = fn
}

// SetOnMCPInitTimedOut registers the callback invoked (at most once per process)
// when the agent reports its MCP-init wait has timed out (mitto-8ul.1).
func (m *ACPProcessManager) SetOnMCPInitTimedOut(fn func(workspaceUUID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMCPInitTimedOut = fn
}

// SetOnPrewarmPinAlert registers the callback invoked by the adaptive
// pre-warming controller (mitto-mw0) when a workspace is pinned due to a
// slow/broken MCP init, or when a stuck pin expires because its
// MaxPinDuration cap elapsed. expired=true distinguishes the two cases so
// the web layer can pick the right UI toast copy.
func (m *ACPProcessManager) SetOnPrewarmPinAlert(fn func(workspaceUUID, reason string, expired bool)) {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	m.onPrewarmPinAlert = fn
}

// PinWorkspace marks a workspace as pinned by the adaptive pre-warming
// controller (mitto-mw0). While pinned, the workspace's PurposeKeepAlive
// auxiliary session is exempt from AuxIdleTimeout and Tier 2 process
// shutdown. reason describes why (e.g. "slow_session_new", "mcp_timeout").
// maxDuration=0 disables the auto-expiry cap. maxPinned=0 disables the
// blast-radius cap. Returns true iff the pin was applied; false when the
// cap was reached (workspace is not pinned in that case).
//
// If the workspace was already pinned, the reason/expiry are refreshed and
// the healthy-probe hysteresis counter is reset (any bad signal restarts
// the count). This is intentional — an unhealthy probe should immediately
// undo hysteresis progress.
func (m *ACPProcessManager) PinWorkspace(workspaceUUID, reason string, maxDuration time.Duration, maxPinned int) bool {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()

	if _, ok := m.pinState[workspaceUUID]; !ok {
		if maxPinned > 0 && len(m.pinState) >= maxPinned {
			if m.logger != nil {
				m.logger.Warn("prewarm: pin refused (max_pinned cap reached)",
					"workspace_uuid", workspaceUUID,
					"reason", reason,
					"pinned_count", len(m.pinState),
					"max_pinned", maxPinned)
			}
			return false
		}
	}

	now := time.Now()
	var expiry *time.Time
	if maxDuration > 0 {
		e := now.Add(maxDuration)
		expiry = &e
	}
	m.pinState[workspaceUUID] = &pinInfo{
		Reason:   reason,
		PinnedAt: now,
		Expiry:   expiry,
		Healthy:  0,
	}
	if m.logger != nil {
		m.logger.Info("prewarm: pinned workspace",
			"workspace_uuid", workspaceUUID,
			"reason", reason,
			"expiry", expiry,
			"pinned_count", len(m.pinState))
	}
	return true
}

// UnpinWorkspace clears a workspace's pin. No-op if the workspace is not
// pinned. The PurposeKeepAlive auxiliary session is left in place; the next
// AuxIdleTimeout sweep will reap it.
func (m *ACPProcessManager) UnpinWorkspace(workspaceUUID string) {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	if _, ok := m.pinState[workspaceUUID]; !ok {
		return
	}
	delete(m.pinState, workspaceUUID)
	if m.logger != nil {
		m.logger.Info("prewarm: unpinned workspace",
			"workspace_uuid", workspaceUUID,
			"pinned_count", len(m.pinState))
	}
}

// IsPinned returns true if the workspace is currently pinned by the
// adaptive pre-warming controller (mitto-mw0) and its pin has not yet
// expired.
func (m *ACPProcessManager) IsPinned(workspaceUUID string) bool {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	pi, ok := m.pinState[workspaceUUID]
	if !ok {
		return false
	}
	if pi.Expiry != nil && !time.Now().Before(*pi.Expiry) {
		return false
	}
	return true
}

// PinnedCount returns the number of currently pinned workspaces (including
// pins whose Expiry has passed but that have not yet been reaped by the
// controller's re-evaluation round).
func (m *ACPProcessManager) PinnedCount() int {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	return len(m.pinState)
}

// RecordPrewarmProbeResult feeds the outcome of a health probe into the
// pin controller's hysteresis state machine (mitto-mw0). healthy=true
// increments the consecutive-healthy counter for the workspace; when the
// counter reaches probesToUnpin the workspace is unpinned and the method
// returns true. healthy=false resets the counter to zero (any bad signal
// undoes hysteresis progress). Returns false when the workspace is not
// currently pinned.
func (m *ACPProcessManager) RecordPrewarmProbeResult(workspaceUUID string, healthy bool, probesToUnpin int) (unpinned bool) {
	m.pinMu.Lock()
	pi, ok := m.pinState[workspaceUUID]
	if !ok {
		m.pinMu.Unlock()
		return false
	}
	if !healthy {
		pi.Healthy = 0
		m.pinMu.Unlock()
		return false
	}
	pi.Healthy++
	if probesToUnpin > 0 && pi.Healthy >= probesToUnpin {
		delete(m.pinState, workspaceUUID)
		count := len(m.pinState)
		m.pinMu.Unlock()
		if m.logger != nil {
			m.logger.Info("prewarm: unpinned workspace (hysteresis satisfied)",
				"workspace_uuid", workspaceUUID,
				"probes_to_unpin", probesToUnpin,
				"pinned_count", count)
		}
		return true
	}
	m.pinMu.Unlock()
	return false
}

// ExpirePinsAndAlert scans pinState for pins whose MaxPinDuration cap has
// elapsed, removes them, and fires onPrewarmPinAlert(expired=true) for
// each. Called by the pre-warming controller's re-evaluation round to
// self-heal stuck pins (mitto-mw0). Returns the workspaces that were
// expired.
func (m *ACPProcessManager) ExpirePinsAndAlert() []string {
	now := time.Now()
	m.pinMu.Lock()
	var expired []string
	var alerts []struct {
		uuid, reason string
	}
	for uuid, pi := range m.pinState {
		if pi.Expiry == nil || now.Before(*pi.Expiry) {
			continue
		}
		expired = append(expired, uuid)
		alerts = append(alerts, struct{ uuid, reason string }{uuid, pi.Reason})
		delete(m.pinState, uuid)
	}
	cb := m.onPrewarmPinAlert
	m.pinMu.Unlock()

	for _, a := range alerts {
		if m.logger != nil {
			m.logger.Warn("prewarm: pin expired (max_pin_duration cap)",
				"workspace_uuid", a.uuid,
				"reason", a.reason)
		}
		if cb != nil {
			cb(a.uuid, a.reason, true)
		}
	}
	return expired
}

// FirePrewarmPinAlert invokes the registered onPrewarmPinAlert callback for
// a pin caused by an MCP-related failure (expired=false). At-most-once per
// pin — subsequent calls for the same pin are no-ops. No-op when the
// workspace is not pinned or no callback is registered (mitto-mw0).
func (m *ACPProcessManager) FirePrewarmPinAlert(workspaceUUID string) {
	m.pinMu.Lock()
	pi, ok := m.pinState[workspaceUUID]
	if !ok || pi.Alerted {
		m.pinMu.Unlock()
		return
	}
	pi.Alerted = true
	reason := pi.Reason
	cb := m.onPrewarmPinAlert
	m.pinMu.Unlock()

	if cb != nil {
		cb(workspaceUUID, reason, false)
	}
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

	// Snapshot MCP-init callbacks and timeout while holding m.mu (we release it
	// below). The callbacks close over workspace.UUID so a stderr signal from the
	// specific process can be routed to the correct workspace (mitto-8ul.1).
	mcpInitTimeout := m.getMCPInitTimeout()
	initCb := m.onMCPInitializing
	timeoutCb := m.onMCPInitTimedOut
	wsUUID := workspace.UUID
	var onMCPInitProgress func()
	if initCb != nil {
		onMCPInitProgress = func() { initCb(wsUUID) }
	}
	var onMCPInitTimeout func()
	if timeoutCb != nil {
		onMCPInitTimeout = func() { timeoutCb(wsUUID) }
	}

	// Resolve per-agent stderr patterns for this ACP server (mitto-k6h). Nil is
	// a safe no-op — the process falls back to the hardcoded baseline.
	var stderrPatterns *conversation.CompiledStderrPatterns
	if m.StderrPatternsResolver != nil {
		stderrPatterns = m.StderrPatternsResolver(workspace.ACPServer)
	}

	createStart := time.Now()
	p, err := NewSharedACPProcess(m.ctx, SharedACPProcessConfig{
		WorkspaceUUID:     workspace.UUID,
		ACPCommand:        acpCommand,
		ACPCwd:            acpCwd,
		ACPServer:         workspace.ACPServer,
		WorkingDir:        workspace.WorkingDir,
		Env:               acpEnv,
		Runner:            r,
		Logger:            processLogger,
		CanRestartGlobal:  m.CanRestartGlobally,
		RecordRestart:     m.RecordGlobalRestart,
		MCPInitTimeout:    mcpInitTimeout,
		OnMCPInitProgress: onMCPInitProgress,
		OnMCPInitTimeout:  onMCPInitTimeout,
		StderrPatterns:    stderrPatterns,
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

// HasLiveProcess reports whether a live shared ACP process exists for the
// workspace. It returns true only when a process exists and its underlying
// connection has not yet exited (non-blocking Done() check).
func (m *ACPProcessManager) HasLiveProcess(workspaceUUID string) bool {
	p := m.GetProcess(workspaceUUID)
	if p == nil {
		return false
	}
	select {
	case <-p.Done():
		return false // process has exited
	default:
		return true
	}
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
) (*conversation.SessionHandle, error) {
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
) (*conversation.SessionHandle, error) {
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

// ColdProcessCount returns the number of active shared processes whose MCP-init
// window has NOT yet closed (MCPInitDone() == false). Used by SessionManager to
// enrich the resume-storm log with the count of cold processes that concurrent
// handshakes are competing for (mitto-7o2).
func (m *ACPProcessManager) ColdProcessCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, p := range m.processes {
		if p != nil && !p.MCPInitDone() {
			n++
		}
	}
	return n
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

		if !conversation.IsACPConnectionError(err) {
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
//
// Locking design (mitto-w19): auxMu is held ONLY briefly around map reads/writes, never
// across the slow NewSession or SetSessionModel RPCs. A per-(workspace,purpose) createMu
// (stored in auxCreateMu, itself guarded by auxMu) serialises concurrent creators of the
// SAME key while allowing different keys to create in parallel.
//
// Lock-ordering rule: NEVER acquire auxMu while holding createMu for an extended section;
// only brief auxMu critical sections (map lookup / store) are taken while createMu is held.
// GetProcess acquires m.mu internally and must run without auxMu held — this is safe and
// preserves the existing auxMu → mu ordering.
func (m *ACPProcessManager) getOrCreateAuxiliarySession(ctx context.Context, workspaceUUID, purpose string) (*auxiliarySessionState, error) {
	key := auxSessionKey{
		workspaceUUID: workspaceUUID,
		purpose:       purpose,
	}

	// ── First check: return early if the session already exists ──────────────────
	m.auxMu.Lock()
	if state, ok := m.auxSessions[key]; ok {
		m.auxMu.Unlock()
		return state, nil
	}
	// Get-or-create the per-key creation mutex while still under auxMu.
	createMu, ok := m.auxCreateMu[key]
	if !ok {
		createMu = &sync.Mutex{}
		m.auxCreateMu[key] = createMu
	}
	m.auxMu.Unlock()

	// ── Serialize concurrent creators of the same key ─────────────────────────────
	// Different keys can create in parallel; same-key callers wait here.
	createMu.Lock()
	defer createMu.Unlock()

	// ── Second check: another goroutine may have finished while we waited ─────────
	m.auxMu.Lock()
	if state, ok := m.auxSessions[key]; ok {
		m.auxMu.Unlock()
		return state, nil
	}
	m.auxMu.Unlock()

	// ── Everything below runs WITHOUT any lock held ───────────────────────────────
	// (only createMu is held — the per-key serializer, not the global auxMu)

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
	// The command MUST be the mitto CLI binary, not mitto-app: on the macOS app
	// os.Executable() points at Mitto.app/Contents/MacOS/mitto-app, which is not
	// a cobra CLI and would spawn a whole second Mitto app (webview + up-hook /
	// cloudflared) instead of the intended stdio proxy. resolveMittoCLIBinary
	// rewrites to the sibling `mitto` binary in that case.
	mcpServers := []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	if strings.HasPrefix(purpose, auxiliary.PurposeProcessorPrefix) && m.MCPServerURL != "" {
		if exe, err := resolveMittoCLIBinary(); err == nil {
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
					"mcp_url", m.MCPServerURL,
					"proxy_command", exe)
			}
		}
	}

	// Guard: honour an explicitly-cancelled caller (e.g. shutdown signal) without
	// forwarding a drained deadline into the RPC.  This is a quick non-blocking
	// check only; the actual RPC uses a fresh budget below (mitto-rlk).
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before auxiliary NewSession: %w", err)
	}

	// Saturation bail (mitto-z70): if the shared process is already flagged
	// saturated (repeated RPC timeouts / cold-MCP wedge), do NOT issue an
	// auxiliary NewSession RPC. Aux sessions are non-essential background
	// work; issuing a session/new here would pile another cold-init request
	// onto an agent that is already struggling to initialise its MCP servers,
	// amplifying the wedge and starving the user's foreground session.
	// Fail fast with a clear sentinel so callers (title-gen, mcp-check, etc.)
	// can back off and retry once the process has recovered.
	if process.IsSaturated() {
		if m.logger != nil {
			m.logger.Info("Skipping auxiliary NewSession: shared process is saturated",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"reason", "process_saturated")
		}
		return nil, fmt.Errorf("shared ACP process is saturated; skipping auxiliary session creation for purpose %q: %w", purpose, context.DeadlineExceeded)
	}

	// Instrument auxiliary session creation so cold-start / prewarm timing is
	// observable in mitto.log. createStart brackets the whole create path from
	// the cache-miss decision through the NewSession RPC; newSessionStart isolates
	// the RPC itself (the dominant cost — the agent may block it behind MCP init).
	createStart := time.Now()
	if m.logger != nil {
		m.logger.Info("Creating auxiliary session",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"cwd", auxCwd,
			"mcp_server_count", len(mcpServers))
	}

	// Derive a fresh budget from m.ctx (manager lifetime), NOT from the caller ctx.
	// With the per-key createMu design (mitto-w19), different keys create concurrently
	// so there is no global serialization to drain the caller ctx. However, same-key
	// callers still serialize on createMu, so the guard above and this fresh budget
	// from m.ctx remain important: if a dead/slow MCP server burns the full 30 s window
	// for a prior same-key caller, the next same-key caller's ctx may arrive near
	// expiry. Using m.ctx gives every NewSession call its full 30-second window.
	// m.ctx is cancelled on manager shutdown, so this never hangs indefinitely. (mitto-rlk)
	newCtx, newCancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer newCancel()
	newSessionStart := time.Now()
	sessionHandle, err := process.NewSession(newCtx, auxCwd, mcpServers)
	newSessionLatency := time.Since(newSessionStart)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("Failed to create auxiliary session",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"new_session_ms", newSessionLatency.Milliseconds(),
				"elapsed_ms", time.Since(createStart).Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to create auxiliary session: %w", err)
	}
	if m.logger != nil {
		m.logger.Info("Auxiliary session NewSession RPC completed",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", sessionHandle.SessionID,
			"new_session_ms", newSessionLatency.Milliseconds())
	}

	// Apply auxiliary model selection if configured for this workspace.
	// Precedence: AuxiliaryModelProfile > AuxiliaryModelTag > AuxiliaryModelSelection
	// (mitto-hke, mitto-9vz). If AuxiliaryModelProfile is set, it takes precedence and
	// its resolved Criteria is used in place of the legacy AuxiliaryModelSelection
	// matchMode/pattern. Else, if AuxiliaryModelTag is set, the first Model profile
	// carrying that tag whose Criteria matches an available model is used. Falls back
	// to AuxiliaryModelSelection when neither resolves. On no match or nil selection,
	// leave the ACP server's default model unchanged.
	if m.WorkspaceConfigProvider != nil {
		if ws := m.WorkspaceConfigProvider(workspaceUUID); ws != nil {
			auxConstraint := ws.AuxiliaryModelSelection
			if ws.AuxiliaryModelProfile != "" && m.ModelProfileResolver != nil {
				if profile := m.ModelProfileResolver(ws.AuxiliaryModelProfile); profile != nil && profile.Criteria != nil {
					auxConstraint = profile.Criteria
				}
			} else if ws.AuxiliaryModelTag != "" && m.ModelProfilesByTagResolver != nil {
				// Priority axis is profile-list order: ModelProfilesByTagResolver returns
				// tagged profiles in Config.Models order (mirrors config.ProfilesByTag),
				// and resolveAuxTagConstraint picks the FIRST whose Criteria resolves
				// against sessionHandle.Models. Reordering profiles in Config.Models
				// flips which model wins for the same tag (mitto-ex7 "list order =
				// priority" contract).
				if c := resolveAuxTagConstraint(m.ModelProfilesByTagResolver(ws.AuxiliaryModelTag), sessionHandle.Models); c != nil {
					auxConstraint = c
				}
			}
			if auxConstraint != nil && auxConstraint.Pattern != "" {
				matched, shouldSet := conversation.ResolveAuxModelSwitch(auxConstraint, sessionHandle.Models)
				switch {
				case shouldSet:
					// Best-effort async model switch (mitto-f7q, Option 4): return the aux
					// session immediately on the server-default model and perform the preferred-
					// model switch in a background goroutine. This prevents the capacity-1
					// setModelSem from blocking aux-session creation — and all callers queued
					// behind it — during server wakeup when several concurrent aux sessions
					// start simultaneously.
					//
					// The first aux prompt may run on the default model; this is explicitly
					// acceptable per the bead.
					//
					// Budget: setModelAsyncCallerBudget (90s) derived from m.ctx (NOT the caller
					// ctx, which is short-lived and may expire before the goroutine runs).
					// Worst-case: setModelSem queued behind ~3 other holders each taking up to
					// the schedule {20s,15s,8s} + jitter backoff (~44s each) before the semaphore
					// is acquired. Since this is off the critical path, a generous budget has no
					// UX cost. m.ctx cancels on manager shutdown as a safety backstop.
					capturedWorkspaceUUID := workspaceUUID
					capturedPurpose := purpose
					capturedMatched := matched
					capturedProcess := process
					capturedSessionID := acp.SessionId(sessionHandle.SessionID)
					capturedLogger := m.logger
					go func() {
						// De-stagger concurrent prewarmed aux model-set goroutines (mitto-xicp).
						// All 4 purposes fire at nearly the same instant during prewarmAuxiliarySessions;
						// without jitter they all queue on the capacity-1 setModelSem simultaneously and
						// the last one exhausts its 90 s budget before the semaphore is released.
						// The jitter waits on m.ctx — NOT inside the budget context — so it does not
						// consume the setModelAsyncCallerBudget (mitto-f7q: per-attempt deadline unchanged).
						// Mirrors the child-session de-stagger pattern from mitto-x4e.
						if jitter := auxStartupJitter(auxModelSwitchStartupJitter); jitter > 0 {
							if capturedLogger != nil {
								capturedLogger.Debug("Auxiliary session: staggering startup model switch",
									"workspace_uuid", capturedWorkspaceUUID,
									"purpose", capturedPurpose,
									"jitter_ms", jitter.Milliseconds())
							}
							select {
							case <-time.After(jitter):
							case <-m.ctx.Done():
								return
							}
						}
						setCtx, setCancel := context.WithTimeout(m.ctx, setModelAsyncCallerBudget)
						defer setCancel()
						if setErr := capturedProcess.SetSessionModel(setCtx, capturedSessionID, capturedMatched); setErr != nil {
							if capturedLogger != nil {
								capturedLogger.Warn("Auxiliary session: failed to set model",
									"workspace_uuid", capturedWorkspaceUUID,
									"purpose", capturedPurpose,
									"model_id", capturedMatched,
									"error", setErr)
							}
						} else if capturedLogger != nil {
							capturedLogger.Info("Auxiliary session: model set via AuxiliaryModelSelection",
								"workspace_uuid", capturedWorkspaceUUID,
								"purpose", capturedPurpose,
								"model_id", capturedMatched)
						}
					}()
				case matched != "":
					// The freshly-created session already runs the preferred model, so the
					// set_model RPC is needless — skip it to avoid the per-process serialisation
					// contention that drives the 8s deadline cascade at server wakeup (mitto-ykb).
					if m.logger != nil {
						m.logger.Debug("Auxiliary session: model already matches AuxiliaryModelSelection, skipping set_model",
							"workspace_uuid", workspaceUUID,
							"purpose", purpose,
							"model_id", matched)
					}
				default:
					if m.logger != nil {
						m.logger.Debug("Auxiliary session: no model matched AuxiliaryModelSelection, using server default",
							"workspace_uuid", workspaceUUID,
							"purpose", purpose,
							"match_mode", auxConstraint.MatchMode,
							"pattern", auxConstraint.Pattern)
					}
				}
			}
		}
	}

	// Create auxiliary client to collect responses
	client := newAuxiliaryClient()

	// Register the session with the multiplexer
	callbacks := &conversation.SessionCallbacks{
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

	// Store the result under a brief auxMu lock.
	// Defensive double-check: if an entry somehow already exists (shouldn't happen
	// given createMu, but be safe), return the existing one to avoid duplicates.
	state := &auxiliarySessionState{
		sessionID: sessionHandle.SessionID,
		client:    client,
		lastUsed:  time.Now(),
	}
	m.auxMu.Lock()
	if existing, ok := m.auxSessions[key]; ok {
		m.auxMu.Unlock()
		return existing, nil
	}
	m.auxSessions[key] = state
	m.auxMu.Unlock()

	if m.logger != nil {
		m.logger.Info("Created auxiliary session",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", sessionHandle.SessionID,
			"new_session_ms", newSessionLatency.Milliseconds(),
			"total_ms", time.Since(createStart).Milliseconds())
	}

	return state, nil
}

// resolveAuxTagConstraint walks the given tag-filtered profiles in slice order
// and returns the Criteria of the FIRST profile whose Criteria resolves against
// the available models. Returns nil when no profile resolves. Callers pass the
// output of ModelProfilesByTagResolver, which preserves Config.Models order —
// so profile-list order is the priority axis for AuxiliaryModelTag resolution
// (mitto-ex7 "list order = priority" contract).
func resolveAuxTagConstraint(profiles []config.ModelProfile, models *conversation.SessionModelState) *config.ACPServerConstraint {
	for i := range profiles {
		if profiles[i].Criteria == nil {
			continue
		}
		if conversation.ResolveProfileModel(&profiles[i], models) != "" {
			return profiles[i].Criteria
		}
	}
	return nil
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
//
// Pinned PurposeKeepAlive sessions (mitto-mw0) are exempt while the
// workspace's pin has not expired: keeping the aux session hot is the whole
// point of the pin. An expired pin (Expiry non-nil and in the past) falls
// through so the max-pin-duration cap self-heals a stuck keepalive.
func (m *ACPProcessManager) CleanupStaleAuxiliarySessions(maxIdleTime time.Duration) int {
	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	now := time.Now()
	var staleKeys []auxSessionKey

	// Find stale sessions
	for key, state := range m.auxSessions {
		if now.Sub(state.lastUsed) <= maxIdleTime {
			continue
		}
		if key.purpose == auxiliary.PurposeKeepAlive && m.IsPinned(key.workspaceUUID) {
			continue
		}
		staleKeys = append(staleKeys, key)
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
// This should be called when creating a new conversation.BackgroundSession on an existing shared
// process. When a shared process is first created, prewarmAuxiliarySessions runs
// automatically. But auxiliary sessions can be lost (server restart, process recreation,
// idle reaping) and won't be re-created until something needs them. Without this,
// title generation can block for minutes waiting for a NewSession RPC while the agent
// is busy with extended thinking on the main prompt.
func (m *ACPProcessManager) EnsurePrewarmed(workspaceUUID string, logger *slog.Logger) {
	if m.DisableAuxiliary {
		return
	}

	// Non-blocking: if auxMu is held (a prewarm/aux-create is in progress), skip —
	// we must never block the caller behind a slow getOrCreateAuxiliarySession.
	if !m.auxMu.TryLock() {
		return
	}
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
// The adaptive pre-warming controller (mitto-mw0) additionally creates a
// PurposeKeepAlive aux session, measures its NewSession latency, checks
// the shared process's MCP-init signals, and pins the workspace when the
// verdict is unhealthy.
//
// Run in a goroutine after releasing the ACPProcessManager lock.
//
// mitto-cgc: creation is single-worker + priority-ordered + time-staggered.
// The scheduler comes from config.PrewarmConfig.AuxPrewarmSchedule() which
// returns entries in nondecreasing Delay order (tier-0 mcp-check/mcp-tools
// first, then tier-1 title-gen, then tier-2 follow-up by default). Only ONE
// session/new is ever in flight — this replaces the earlier parallel
// sync.WaitGroup burst that aggravated the auggie cold-init fork-burst wedge
// (mitto-54k.7).
func (m *ACPProcessManager) prewarmAuxiliarySessions(workspaceUUID string, logger *slog.Logger) {
	pc := m.effectivePrewarmConfig()
	// Pick the per-agent schedule variant (mitto-7yj): fork-per-session
	// agents (Claude Code) get a widely spread default so real user demand
	// preempts the schedule; multiplex agents (auggie) keep the aggressive
	// non-simultaneous defaults.
	forking := m.forkPerSession(workspaceUUID)
	schedule := pc.AuxPrewarmSchedule(forking)

	// Reference the auxiliary.Purpose* constants so a rename of any of the
	// four hardcoded purpose strings in internal/config/settings.go is caught
	// at compile time here. Not otherwise used at runtime.
	_ = auxiliary.PurposeMCPCheck
	_ = auxiliary.PurposeMCPTools
	_ = auxiliary.PurposeTitleGen
	_ = auxiliary.PurposeFollowUp

	start := time.Now()
	for _, entry := range schedule {
		// Rush-on-demand skip (mitto-7yj): if a real caller already created
		// this aux session (via getOrCreateAuxiliarySession) before the
		// scheduler reached its slot, skip the sleep + redundant get-or-create
		// entirely. The existing per-key createMu already makes create
		// idempotent; this just avoids wasting the stagger delay.
		if m.auxSessionExists(auxSessionKey{workspaceUUID: workspaceUUID, purpose: entry.Purpose}) {
			if logger != nil {
				logger.Debug("auxiliary prewarm rushed — already created on demand",
					"workspace_uuid", workspaceUUID,
					"purpose", entry.Purpose)
			}
			continue
		}

		// Sleep until this entry's target offset from the anchor. Respect
		// manager shutdown so we exit promptly instead of holding the process
		// up to the last scheduled delay.
		target := start.Add(entry.Delay)
		if remaining := time.Until(target); remaining > 0 {
			select {
			case <-time.After(remaining):
			case <-m.ctx.Done():
				return
			}
		}

		// Re-check after sleeping — a caller may have rushed during the wait.
		if m.auxSessionExists(auxSessionKey{workspaceUUID: workspaceUUID, purpose: entry.Purpose}) {
			if logger != nil {
				logger.Debug("auxiliary prewarm rushed — already created on demand",
					"workspace_uuid", workspaceUUID,
					"purpose", entry.Purpose)
			}
			continue
		}

		if logger != nil {
			logger.Debug("scheduling auxiliary prewarm",
				"workspace_uuid", workspaceUUID,
				"purpose", entry.Purpose,
				"planned_offset_ms", entry.Delay.Milliseconds())
		}

		// Synchronous creation inside the single worker guarantees at most one
		// session/new in flight at a time. 30s is generous; in practice session
		// creation completes in <1s. Derived from m.ctx so shutdown propagates.
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		_, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, entry.Purpose)
		cancel()

		if logger != nil {
			if err != nil {
				logger.Debug("auxiliary session pre-warm failed",
					"workspace_uuid", workspaceUUID,
					"purpose", entry.Purpose,
					"error", err)
			} else {
				logger.Debug("auxiliary session pre-warmed",
					"workspace_uuid", workspaceUUID,
					"purpose", entry.Purpose)
			}
		}

		// Bail out early if the manager is shutting down between entries.
		if m.ctx.Err() != nil {
			return
		}
	}

	// Adaptive pre-warming health probe (mitto-mw0). Runs after the initial
	// aux prewarm so the shared process has had a chance to complete MCP init
	// and the mcp{Init,Timed}Out signals are up to date. The keepalive session
	// creation is what actually measures session/new latency for pin decisions.
	m.probePrewarmHealth(workspaceUUID, logger)
}

// probePrewarmHealth runs the adaptive pre-warming health probe for a
// workspace (mitto-mw0): it creates a PurposeKeepAlive aux session,
// measures NewSession latency, checks the shared process's MCP-init
// signals, and pins the workspace when the verdict is unhealthy. Called at
// the tail of prewarmAuxiliarySessions and periodically by the pin
// controller (see ReevaluatePrewarmPin).
//
// mitto-clc: the proactive always-on keep-warm pin (mitto-54k.7) was
// inactivated — it did not address the auggie fork-burst spawn-timing wedge
// and was net-negative (piled load onto already-saturated processes). Only
// the REACTIVE unhealthy pin path (session_new_failed / mcp_timeout /
// mcp_not_ready / slow_session_new + FirePrewarmPinAlert) remains.
func (m *ACPProcessManager) probePrewarmHealth(workspaceUUID string, logger *slog.Logger) {
	pc := m.effectivePrewarmConfig()
	tFast, _ := pc.ParseSessionNewFast()
	tMcp, _ := pc.ParseMcpReady()
	maxDur, _ := pc.ParseMaxPinDuration()
	maxPinned := pc.GetMaxPinnedWorkspaces()

	// Create the keepalive session and time the NewSession round-trip.
	// Use a budget slightly greater than the MCP-ready threshold so a broken
	// MCP does not artificially trip session/new latency alone.
	probeBudget := tMcp + 20*time.Second
	if probeBudget < 30*time.Second {
		probeBudget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(m.ctx, probeBudget)
	defer cancel()

	start := time.Now()
	_, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, auxiliary.PurposeKeepAlive)
	sessionNewLatency := time.Since(start)

	// Sample MCP-init signals on the shared process (may be nil if the
	// process was torn down between prewarm start and now — treat as
	// unhealthy so the retry loop re-evaluates).
	var mcpTimedOut, mcpDone bool
	if p := m.GetProcess(workspaceUUID); p != nil {
		mcpTimedOut = p.MCPInitTimedOut()
		mcpDone = p.MCPInitDone()
	}

	// Verdict: healthy iff session/new completed under T_fast AND MCP init
	// did not time out AND MCP init has finished (i.e. the agent is not
	// still blocked on MCP). A failed session/new is always unhealthy.
	healthy := err == nil && sessionNewLatency <= tFast && !mcpTimedOut && mcpDone

	reason := ""
	switch {
	case err != nil:
		reason = "session_new_failed"
	case mcpTimedOut:
		reason = "mcp_timeout"
	case !mcpDone:
		reason = "mcp_not_ready"
	case sessionNewLatency > tFast:
		reason = "slow_session_new"
	}

	if logger != nil {
		logger.Info("prewarm: health probe",
			"workspace_uuid", workspaceUUID,
			"session_new_ms", sessionNewLatency.Milliseconds(),
			"session_new_fast_ms", tFast.Milliseconds(),
			"mcp_ready_ms", tMcp.Milliseconds(),
			"mcp_init_done", mcpDone,
			"mcp_timed_out", mcpTimedOut,
			"err", err,
			"healthy", healthy,
			"reason", reason)
	}

	if healthy {
		// mitto-clc: proactive pin removed. Feed the healthy probe into
		// hysteresis (unpins after N consecutive healthy probes; no-op if
		// not pinned) and return without pinning.
		m.RecordPrewarmProbeResult(workspaceUUID, true, pc.GetHealthyProbesToUnpin())
		return
	}

	// Unhealthy → pin the workspace (or refresh the pin if already pinned).
	// Reactive pin behavior is unchanged: the reactive reason (e.g.
	// mcp_timeout) takes precedence because it carries the alert semantics.
	if m.PinWorkspace(workspaceUUID, reason, maxDur, maxPinned) && reason == "mcp_timeout" {
		// Fire the alert only for MCP-timeout pins on the initial pin event
		// (FirePrewarmPinAlert is at-most-once per pin).
		m.FirePrewarmPinAlert(workspaceUUID)
	}
}

// effectivePrewarmConfig returns the caller-supplied PrewarmConfig or nil.
// The config.PrewarmConfig accessors themselves handle nil safely (returning
// defaults), so callers can pass the result directly to the helper methods.
func (m *ACPProcessManager) effectivePrewarmConfig() *config.PrewarmConfig {
	if m.PrewarmConfigProvider == nil {
		return nil
	}
	return m.PrewarmConfigProvider()
}

// forkPerSession reports whether the ACP agent backing the given workspace
// forks a fresh OS process per ACP session (Claude Code) vs multiplexing over
// one process (auggie). Nil provider or unknown workspace → false (safe
// default = aggressive multiplex schedule). (mitto-7yj)
func (m *ACPProcessManager) forkPerSession(workspaceUUID string) bool {
	if m.ForkPerSessionProvider == nil {
		return false
	}
	return m.ForkPerSessionProvider(workspaceUUID)
}

// auxSessionExists returns true if an auxiliary session for the given
// (workspace, purpose) is already tracked. Used by prewarmAuxiliarySessions
// to skip slots whose session was rushed to creation on demand (mitto-7yj).
// Reads auxSessions under auxMu — safe to call from any goroutine.
func (m *ACPProcessManager) auxSessionExists(key auxSessionKey) bool {
	m.auxMu.Lock()
	_, ok := m.auxSessions[key]
	m.auxMu.Unlock()
	return ok
}

// ReevaluatePrewarmPin runs the health probe for a currently-pinned
// workspace and applies the pin/unpin decision (mitto-mw0). It is intended
// to be called from the MCP backoff retry loop (EnsureMCPBackoffRetry) so
// pin/unpin decisions ride the same schedule as MCP reachability probes.
// Expired pins are self-healed via ExpirePinsAndAlert before the probe.
func (m *ACPProcessManager) ReevaluatePrewarmPin(workspaceUUID string, logger *slog.Logger) {
	m.ExpirePinsAndAlert()
	m.probePrewarmHealth(workspaceUUID, logger)
}
