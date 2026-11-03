package acpproc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/procstart"
	"github.com/inercia/mitto/internal/agentbackend"
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

	// ModelCatalogRecorder, when set, receives the raw model catalog already
	// returned by an auxiliary session/new. It never triggers extra RPCs.
	ModelCatalogRecorder conversation.AgentModelCatalogRecorder
	// HiddenModelPolicies shares validated runtime snapshots with conversation
	// sessions. Auxiliary execution enforcement is a separate increment.
	HiddenModelPolicies *config.HiddenModelPolicies

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

	// GlobalAuxModelTagProvider returns the global conversations.auxiliary_model_tag
	// (mitto-ee2.9). It is a FALLBACK consulted only when the workspace sets none of
	// AuxiliaryModelProfile / AuxiliaryModelTag / AuxiliaryModelSelection. Called at
	// session-creation time so hot-reloaded settings take effect. May be nil or
	// return "" (aux sessions then keep the agent's default model).
	GlobalAuxModelTagProvider func() string

	// StderrPatternsResolver returns the compiled per-agent stderr regex patterns
	// for a given ACP server name (mitto-k6h). The web layer wires this to resolve
	// the ACP server → agent metadata → StderrPatterns → CompileStderrPatterns.
	// May be nil (all processes then use only the hardcoded baseline). Nil result
	// from the resolver is also valid (agent has no per-agent patterns).
	StderrPatternsResolver func(acpServer string) *procstart.CompiledStderrPatterns

	// AgentDefaultEnvResolver returns per-agent default environment variables
	// (metadata.yaml defaults.env, e.g. NODE_OPTIONS=--max-old-space-size=N)
	// for a given ACP server name (mitto-6dur). The web layer wires this to
	// resolve the ACP server → agent metadata → Defaults.Env, mirroring
	// StderrPatternsResolver above. May be nil (processes then get no
	// agent-authored defaults). Nil result from the resolver is also valid
	// (agent has no declared defaults). Kept separate from acpEnv/Env (rather
	// than merged) so it does NOT affect sharedProcessConfigMatchesWorkspace's
	// process-reuse comparison.
	AgentDefaultEnvResolver func(acpServer string) map[string]string

	// AgentInitializeTimeoutResolver returns the per-agent override for the
	// per-attempt ACP Initialize handshake deadline (metadata.yaml
	// defaults.initializeTimeout, mitto-sbj) for a given ACP server name. The
	// web layer wires this to resolve the ACP server → agent metadata →
	// Defaults.InitializeTimeout, mirroring AgentDefaultEnvResolver above. May
	// be nil or return 0 (processes then use the default
	// processInitializeAttemptTimeout).
	AgentInitializeTimeoutResolver func(acpServer string) time.Duration

	// Auxiliary session tracking
	auxMu       sync.Mutex
	auxSessions map[auxSessionKey]*auxiliarySessionState
	// auxCreateMu holds per-key creation locks (guarded by auxMu).
	// Lets different (workspace, purpose) pairs create concurrently while
	// same-key callers serialize, eliminating the need to hold auxMu across
	// slow NewSession and SetSessionModel RPCs. (mitto-w19)
	auxCreateMu      map[auxSessionKey]*sync.Mutex
	auxFollowUpMu    sync.Mutex
	auxFollowUpGates map[string]*auxiliaryOperationGate
	// auxQuiescenceWaits coalesces deferred title-session admission per workspace.
	auxQuiescenceMu    sync.Mutex
	auxQuiescenceWaits map[string]*processQuiescenceWait

	// ctx owns manager background workers and process startup; processCtx owns
	// established process lifetimes. cancel only stops the former so sessions
	// can detach cleanly before Close terminates tracked processes.
	ctx        context.Context
	processCtx context.Context
	cancel     context.CancelFunc

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
	gcMu            sync.Mutex // protects lastSessionSeen, descendantCountHistory, and gc lifecycle fields
	// descendantCountHistory tracks, per workspace UUID, the descendant
	// (MCP-child) process count observed on the previous Tier 4 GC cycle and
	// how many consecutive cycles it has strictly increased. Used to detect
	// an unbounded climb (mitto-52mt) that the RSS-based Tier 4 predicate
	// cannot see: a V8 heap-OOM inside a descendant is a per-process cap
	// decoupled from tree RSS, so a process can crash from descendant
	// sprawl while comfortably under MemoryRecycleThreshold.
	descendantCountHistory map[string]*descendantCountEntry

	// memorySampler returns effective memory plus the diagnostic RSS breakdown
	// from one process-tree topology snapshot. Tests override this seam.
	memorySampler func(p *SharedACPProcess) (processMemorySample, error)

	// onMemoryRecycled, if set, is called by the GC's Tier 4 memory-recycle path
	// when a memory-bloated idle shared ACP process is recycled. Used to broadcast
	// a toast notification to connected clients. Set after construction (see NewServer).
	onMemoryRecycled func(workspaceUUID string, rssBytes, threshold uint64, sessionCount int)

	// onHealthRecycled, if set, is called by the GC's Tier 5 (saturated + idle)
	// and Tier 6 (confirmed-degraded, even busy) recycle paths (mitto-aoo) when a
	// shared ACP process is stopped because it stopped completing session/new or
	// session/load RPCs. Used to broadcast a toast notification to connected
	// clients so a silently-wedged process (e.g. the "query closed before
	// response received" signature, which produced 38 consecutive failures over
	// 9h with no other user-visible signal) is surfaced instead of leaving users
	// to read a misleading agent error. reason is "saturated_idle" (Tier 5) or
	// "confirmed_degraded" (Tier 6). Set after construction (see NewServer).
	onHealthRecycled func(workspaceUUID, reason string, saturationLevel, sessionCount int)

	// degradedMu guards degradedState. Written from the single-threaded GC
	// loop (Tier 5, once per tick per workspace); a mutex is used anyway so
	// any future concurrent reader is safe.
	degradedMu sync.Mutex
	// degradedState tracks, per workspace UUID, the current non-empty reason
	// string from processDegradedState (mitto-13n.3), or is absent when the
	// workspace's process is healthy. Used to detect transition edges so
	// onDegraded fires exactly once per edge instead of once per GC tick.
	degradedState map[string]string
	// onDegraded, if set, is called on each healthy<->degraded transition
	// edge for a workspace's shared ACP process (mitto-13n.3): once when
	// processDegradedState first returns a non-empty reason (degraded=true,
	// state=reason), and once when it returns to "" (degraded=false,
	// state=""). Fired from GC Tier 5, BEFORE the idle safety gates that can
	// hold off an actual health recycle indefinitely — this is the signal
	// that closes the invisible pre-recycle window. Not fired on the
	// recycle-driven recovery edge (see dropDegradedStateSilently), since
	// onHealthRecycled already broadcasts a dedicated toast for that case.
	// Set after construction (see NewServer).
	onDegraded func(workspaceUUID, state string, degraded bool)

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
	// MCP servers to initialize. onMCPInitTimedOut is called (at most once per
	// process) when the agent reports its internal MCP-init wait budget elapsed;
	// the servers argument carries the per-server names extracted from the same
	// stderr chunk, or nil if none were present (mitto-m8nx AC2). Used by the web
	// layer to broadcast UI notifications (mitto-8ul.1).
	onMCPInitializing func(workspaceUUID string)
	onMCPInitTimedOut func(workspaceUUID string, servers []string)

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
	mu                sync.Mutex   // Serializes requests to this session
	modelMu           sync.RWMutex // Guards the model state and creation provenance below.
	sessionID         string
	client            *auxiliaryClient // Collects responses
	lastUsed          time.Time
	modelID           string
	rawModels         *conversation.SessionModelState
	workspaceUUID     string
	configuredServer  string
	sessionFolder     string
	process           *SharedACPProcess
	processGeneration int
	instructionsSent  bool // Tracks if system instructions have been sent to this session
}

// auxiliaryOperationGate serializes one-shot follow-up session lifecycles per
// workspace. refs keeps a gate alive while callers are waiting or using it.
type auxiliaryOperationGate struct {
	token chan struct{}
	refs  int
}

type processQuiescenceWait struct {
	done     chan struct{}
	observed bool
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
	managerCtx, cancel := context.WithCancel(ctx)
	m := &ACPProcessManager{
		processes:          make(map[string]*SharedACPProcess),
		auxSessions:        make(map[auxSessionKey]*auxiliarySessionState),
		auxCreateMu:        make(map[auxSessionKey]*sync.Mutex),
		auxFollowUpGates:   make(map[string]*auxiliaryOperationGate),
		auxQuiescenceWaits: make(map[string]*processQuiescenceWait),
		pinState:           make(map[string]*pinInfo),
		ctx:                managerCtx,
		processCtx:         ctx,
		cancel:             cancel,
		logger:             logger,
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

// SetOnHealthRecycled sets the callback invoked by the GC's Tier 5 (saturated
// + idle) and Tier 6 (confirmed-degraded) recycle paths when a wedged shared
// ACP process is recycled (mitto-aoo).
func (m *ACPProcessManager) SetOnHealthRecycled(fn func(workspaceUUID, reason string, saturationLevel, sessionCount int)) {
	m.onHealthRecycled = fn
}

// SetOnDegraded sets the callback invoked on each healthy<->degraded
// transition edge for a workspace's shared ACP process, fired by GC Tier 5
// before its idle safety gates (mitto-13n.3). See the onDegraded field doc
// for the exact semantics.
func (m *ACPProcessManager) SetOnDegraded(fn func(workspaceUUID, state string, degraded bool)) {
	m.onDegraded = fn
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
// when the agent reports its MCP-init wait has timed out (mitto-8ul.1). The
// servers argument carries the per-server names extracted from the stderr chunk,
// or nil if none were present (mitto-m8nx AC2).
func (m *ACPProcessManager) SetOnMCPInitTimedOut(fn func(workspaceUUID string, servers []string)) {
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

// Ensure ACPProcessManager implements the auxiliary provider capabilities.
var (
	_ auxiliary.ProcessProvider           = (*ACPProcessManager)(nil)
	_ auxiliary.ProcessQuiescenceProvider = (*ACPProcessManager)(nil)
)

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

	if err := checkACPExecutable(workspace, acpCommand, r); err != nil {
		m.mu.Unlock()
		return nil, err
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
	var onMCPInitTimeout func(servers []string)
	if timeoutCb != nil {
		onMCPInitTimeout = func(servers []string) { timeoutCb(wsUUID, servers) }
	}

	// Resolve per-agent stderr patterns for this ACP server (mitto-k6h). Nil is
	// a safe no-op — the process falls back to the hardcoded baseline.
	var stderrPatterns *procstart.CompiledStderrPatterns
	if m.StderrPatternsResolver != nil {
		stderrPatterns = m.StderrPatternsResolver(workspace.ACPServer)
	}

	// Resolve per-agent default env for this ACP server (mitto-6dur). Nil is a
	// safe no-op — the process gets no agent-authored defaults.
	var agentDefaultEnv map[string]string
	if m.AgentDefaultEnvResolver != nil {
		agentDefaultEnv = m.AgentDefaultEnvResolver(workspace.ACPServer)
	}

	// Resolve per-agent Initialize-timeout override for this ACP server
	// (mitto-sbj). Zero is a safe no-op — the process falls back to
	// processInitializeAttemptTimeout.
	var agentInitializeTimeout time.Duration
	if m.AgentInitializeTimeoutResolver != nil {
		agentInitializeTimeout = m.AgentInitializeTimeoutResolver(workspace.ACPServer)
	}

	createStart := time.Now()
	p, err := NewSharedACPProcess(m.processCtx, SharedACPProcessConfig{
		WorkspaceUUID:        workspace.UUID,
		ACPCommand:           acpCommand,
		ACPCwd:               acpCwd,
		ACPServer:            workspace.ACPServer,
		HiddenModelPolicies:  m.HiddenModelPolicies,
		WorkspaceHiddenRules: m.workspaceHiddenRulesResolver(workspace.UUID),
		WorkingDir:           workspace.WorkingDir,
		Env:                  acpEnv,
		Runner:               r,
		Logger:               processLogger,
		CanRestartGlobal:     m.CanRestartGlobally,
		RecordRestart:        m.RecordGlobalRestart,
		MCPInitTimeout:       mcpInitTimeout,
		OnMCPInitProgress:    onMCPInitProgress,
		OnMCPInitTimeout:     onMCPInitTimeout,
		StderrPatterns:       stderrPatterns,
		AgentDefaultEnv:      agentDefaultEnv,
		InitializeTimeout:    agentInitializeTimeout,
		StartupContext:       m.ctx,
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
	mcpServers []agentbackend.MCPServerDescriptor,
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
	mcpServers []agentbackend.MCPServerDescriptor,
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

	// mitto-13n.3: the process is gone, so any tracked degraded state for it
	// is stale — drop it here (the single choke point for every stop path:
	// GC Tiers 1/4/5/6 and manual stops) so a later workspace with the same
	// UUID starts from a clean edge. Dropped silently: every stop path that
	// the user should hear about already broadcasts its own dedicated toast
	// (onMemoryRecycled / onHealthRecycled), and an idle-timeout stop is not
	// a recovery worth notifying.
	m.dropDegradedStateSilently(workspaceUUID)

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

	// Manual/forced restart (e.g. the "Restart ACP" UI action): not reacting to
	// a specific observed death, so bypass the generation dedup (mitto-x611).
	return p.Restart(conversation.RestartAnyGeneration)
}

// BeginShutdown cancels manager-owned process startup and auxiliary/prewarm
// work without closing tracked processes. It is safe to call more than once.
// Server shutdown uses this before joining workers that may currently be
// blocked inside GetOrCreateProcess; Close performs the same step defensively.
func (m *ACPProcessManager) BeginShutdown() {
	m.cancel()
}

// Close stops all managed processes.
func (m *ACPProcessManager) Close() {
	// Cancel before taking m.mu. GetOrCreateProcess may be holding that lock
	// while starting a process with m.ctx; cancellation lets it return instead
	// of making shutdown wait for the full initialize/probe timeout.
	m.BeginShutdown()

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
	trace := newAuxPromptTrace(workspaceUUID, purpose, message)
	response, err := m.promptAuxiliary(ctx, workspaceUUID, purpose, message, trace)
	m.logAuxPromptOutcome(ctx, trace, err)
	return response, err
}

// promptAuxiliary is the PromptAuxiliary implementation; it records telemetry
// (never prompt or response content) into trace.

// SendAuxiliaryInstructions sends system instructions to an auxiliary session once.
// This is called only the first time a purpose-specific session is used to avoid
// repeating instructions on subsequent prompts to the same session.
func (m *ACPProcessManager) SendAuxiliaryInstructions(ctx context.Context, workspaceUUID, purpose, instructions string) error {
	if m.DisableAuxiliary {
		return fmt.Errorf("auxiliary sessions disabled")
	}

	// Get or create the auxiliary session
	auxState, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
	if err != nil {
		return fmt.Errorf("failed to get auxiliary session: %w", err)
	}

	// Try to acquire the mutex with context cancellation support
	if err := acquireAuxLock(ctx, auxState); err != nil {
		return err
	}
	defer auxState.mu.Unlock()

	if auxState.instructionsSent {
		// Instructions already sent to this session, skip
		return nil
	}

	auxState.lastUsed = time.Now()

	process := m.GetProcess(workspaceUUID)
	if process == nil {
		return fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
	}
	if err := m.prepareAuxiliaryDispatch(ctx, workspaceUUID, purpose, process, auxState); err != nil {
		return err
	}

	auxState.client.reset()

	_, err = process.PromptWithAdmission(ctx, auxState.sessionID, []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: instructions}}}, m.auxiliaryPromptAdmission(workspaceUUID, purpose, process, auxState))
	if err != nil {
		if ctx.Err() != nil {
			m.retireCancelledAuxSession(process, workspaceUUID, purpose, auxState)
			return fmt.Errorf("auxiliary instructions cancelled: %w", err)
		}
		if !mittoAcp.IsACPConnectionError(err) {
			return fmt.Errorf("failed to send auxiliary instructions: %w", err)
		}
		// Connection error - will be retried on next prompt
		return fmt.Errorf("auxiliary instructions connection error: %w", err)
	}

	auxState.instructionsSent = true
	return nil
}

func (m *ACPProcessManager) promptAuxiliary(ctx context.Context, workspaceUUID, purpose, message string, trace *auxPromptTrace) (string, error) {
	if m.DisableAuxiliary {
		return "", fmt.Errorf("auxiliary sessions disabled")
	}

	// Check context before doing any work
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled before auxiliary prompt: %w", err)
	}
	if purpose == auxiliary.PurposeFollowUp {
		return m.promptFollowUpAuxiliaryOnce(ctx, workspaceUUID, purpose, message, trace)
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
	if err := m.prepareAuxiliaryDispatch(ctx, workspaceUUID, purpose, process, auxState); err != nil {
		auxState.mu.Unlock()
		return "", err
	}

	// Reset the response buffer
	auxState.client.reset()

	// Send prompt to the auxiliary session
	if m.logger != nil {
		modelID, modelReported := auxState.modelForLog()
		m.logger.Info("Prompting auxiliary session",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", auxState.sessionID,
			"model_id", modelID,
			"model_reported", modelReported)
	}
	trace.beginAttempt(auxState, false)
	_, err = process.PromptWithAdmission(ctx, auxState.sessionID, []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: message}}}, m.auxiliaryPromptAdmission(workspaceUUID, purpose, process, auxState))
	trace.recordResponse(auxState.client)
	if err != nil {
		if ctx.Err() != nil {
			m.retireCancelledAuxSession(process, workspaceUUID, purpose, auxState)
			auxState.mu.Unlock()
			return "", fmt.Errorf("auxiliary prompt cancelled: %w", err)
		}
		// Always release the lock before returning or retrying.
		auxState.mu.Unlock()

		if !mittoAcp.IsACPConnectionError(err) {
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
		m.invalidateAuxSession(workspaceUUID, purpose, auxState)

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
		if err := m.prepareAuxiliaryDispatch(ctx, workspaceUUID, purpose, process, auxState); err != nil {
			auxState.mu.Unlock()
			return "", err
		}

		auxState.client.reset()
		if m.logger != nil {
			modelID, modelReported := auxState.modelForLog()
			m.logger.Info("Prompting auxiliary session",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"session_id", auxState.sessionID,
				"model_id", modelID,
				"model_reported", modelReported,
				"retry", true)
		}
		trace.beginAttempt(auxState, true)
		_, err = process.PromptWithAdmission(ctx, auxState.sessionID, []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: message}}}, m.auxiliaryPromptAdmission(workspaceUUID, purpose, process, auxState))
		trace.recordResponse(auxState.client)
		if err != nil {
			if ctx.Err() != nil {
				m.retireCancelledAuxSession(process, workspaceUUID, purpose, auxState)
				auxState.mu.Unlock()
				return "", fmt.Errorf("auxiliary prompt cancelled on retry: %w", err)
			}
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
	trace.recordResponse(auxState.client)
	auxState.mu.Unlock()
	return response, nil
}

// promptFollowUpAuxiliaryOnce gives each independent analysis a fresh ACP
// session. The workspace gate spans creation, prompt, cleanup, and the single
// connection retry so concurrent follow-ups cannot claim or retire each
// other's session.
func (m *ACPProcessManager) promptFollowUpAuxiliaryOnce(ctx context.Context, workspaceUUID, purpose, message string, trace *auxPromptTrace) (string, error) {
	releaseGate, err := m.acquireAuxFollowUpGate(ctx, workspaceUUID)
	if err != nil {
		return "", err
	}
	defer releaseGate()

	state, err := m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
	if err != nil {
		return "", fmt.Errorf("failed to get auxiliary session: %w", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := acquireAuxLock(ctx, state); err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			if process := m.GetProcess(workspaceUUID); process != nil {
				m.retireCancelledAuxSession(process, workspaceUUID, purpose, state)
			} else {
				m.retireAuxSession(nil, workspaceUUID, purpose, state)
			}
			state.mu.Unlock()
			return "", fmt.Errorf("context cancelled before follow-up prompt: %w", err)
		}

		process := m.GetProcess(workspaceUUID)
		if process == nil {
			m.retireAuxSession(nil, workspaceUUID, purpose, state)
			state.mu.Unlock()
			return "", fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
		}
		if err := m.prepareAuxiliaryDispatch(ctx, workspaceUUID, purpose, process, state); err != nil {
			m.retireAuxSession(process, workspaceUUID, purpose, state)
			state.mu.Unlock()
			return "", err
		}
		state.lastUsed = time.Now()
		state.client.reset()
		if m.logger != nil {
			modelID, modelReported := state.modelForLog()
			m.logger.Info("Prompting one-shot follow-up auxiliary session",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"session_id", state.sessionID,
				"model_id", modelID,
				"model_reported", modelReported,
				"retry", attempt > 0)
		}
		trace.beginAttempt(state, attempt > 0)
		_, promptErr := process.PromptWithAdmission(ctx, state.sessionID, []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: message}}}, m.auxiliaryPromptAdmission(workspaceUUID, purpose, process, state))
		trace.recordResponse(state.client)
		if promptErr == nil {
			response := state.client.getResponse()
			m.retireAuxSession(process, workspaceUUID, purpose, state)
			state.mu.Unlock()
			return response, nil
		}

		if ctx.Err() != nil {
			m.retireCancelledAuxSession(process, workspaceUUID, purpose, state)
			state.mu.Unlock()
			return "", fmt.Errorf("auxiliary follow-up prompt cancelled: %w", promptErr)
		}
		m.retireAuxSession(process, workspaceUUID, purpose, state)
		state.mu.Unlock()
		if !mittoAcp.IsACPConnectionError(promptErr) || attempt > 0 {
			return "", fmt.Errorf("auxiliary follow-up prompt failed: %w", promptErr)
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting to retry auxiliary follow-up prompt: %w", ctx.Err())
		case <-time.After(time.Second):
		}
		state, err = m.getOrCreateAuxiliarySession(ctx, workspaceUUID, purpose)
		if err != nil {
			return "", fmt.Errorf("failed to get auxiliary session on retry: %w", err)
		}
	}
	return "", fmt.Errorf("auxiliary follow-up prompt failed after retry")
}

func (m *ACPProcessManager) acquireAuxFollowUpGate(ctx context.Context, workspaceUUID string) (func(), error) {
	m.auxFollowUpMu.Lock()
	if m.auxFollowUpGates == nil {
		m.auxFollowUpGates = make(map[string]*auxiliaryOperationGate)
	}
	gate := m.auxFollowUpGates[workspaceUUID]
	if gate == nil {
		gate = &auxiliaryOperationGate{token: make(chan struct{}, 1)}
		m.auxFollowUpGates[workspaceUUID] = gate
	}
	gate.refs++
	m.auxFollowUpMu.Unlock()

	select {
	case gate.token <- struct{}{}:
		return func() {
			<-gate.token
			m.releaseAuxFollowUpGate(workspaceUUID, gate)
		}, nil
	case <-ctx.Done():
		m.releaseAuxFollowUpGate(workspaceUUID, gate)
		return nil, fmt.Errorf("context cancelled while waiting for follow-up analysis: %w", ctx.Err())
	}
}

func (m *ACPProcessManager) releaseAuxFollowUpGate(workspaceUUID string, gate *auxiliaryOperationGate) {
	m.auxFollowUpMu.Lock()
	defer m.auxFollowUpMu.Unlock()
	gate.refs--
	if gate.refs == 0 && m.auxFollowUpGates[workspaceUUID] == gate {
		delete(m.auxFollowUpGates, workspaceUUID)
	}
}

// retireAuxSession discards a completed or failed one-shot session without
// sending cancellation for a request that has already returned.
func (m *ACPProcessManager) retireAuxSession(process *SharedACPProcess, workspaceUUID, purpose string, state *auxiliarySessionState) {
	state.client.retire()
	if process != nil {
		process.UnregisterSession(state.sessionID)
	}
	m.invalidateAuxSession(workspaceUUID, purpose, state)
}

// PromptAuxiliaryAsync sends a prompt to an auxiliary session without waiting for the response.
// The session is created on-demand if it doesn't exist and reused for subsequent requests.
// The prompt is dispatched and the method returns immediately — the agent processes in the background.
// The session mutex is held until the agent finishes, ensuring subsequent prompts are serialized.
// This implements the auxiliary.ProcessProvider interface.
func (m *ACPProcessManager) PromptAuxiliaryAsync(ctx context.Context, workspaceUUID, purpose, message string) (err error) {
	trace := newAuxPromptTrace(workspaceUUID, purpose, message)
	trace.async = true
	defer func() {
		if err != nil {
			m.logAuxPromptOutcome(ctx, trace, err)
		}
	}()

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

	// Acquire with context cancellation support; the lock is released in the
	// background goroutine once the agent finishes.
	if err := acquireAuxLock(ctx, auxState); err != nil {
		return err
	}

	// Update last used time
	auxState.lastUsed = time.Now()

	process := m.GetProcess(workspaceUUID)
	if process == nil {
		auxState.mu.Unlock()
		return fmt.Errorf("shared process for workspace %s disappeared (process may have exited)", workspaceUUID)
	}
	if err := m.prepareAuxiliaryDispatch(ctx, workspaceUUID, purpose, process, auxState); err != nil {
		auxState.mu.Unlock()
		return err
	}

	// Reset the response buffer
	auxState.client.reset()

	if m.logger != nil {
		m.logger.Info("Dispatching async auxiliary prompt",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"prompt_length", len(message),
			"prompt_chars", utf8.RuneCountInString(message),
			"prompt_bytes", len(message),
			"prompt_messages", 1)
	}

	// Fire-and-forget: send the prompt and release the lock in the background when the agent finishes.
	// This ensures subsequent prompts to the same session are serialized.
	// process.Prompt blocks until the agent completes, so the lock is held for the duration.
	go func() {
		defer auxState.mu.Unlock()
		waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		trace.beginAttempt(auxState, false)
		_, promptErr := process.PromptWithAdmission(waitCtx, auxState.sessionID, []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: message}}}, m.auxiliaryPromptAdmission(workspaceUUID, purpose, process, auxState))
		trace.recordResponse(auxState.client)
		m.logAuxPromptOutcome(waitCtx, trace, promptErr)
	}()

	return nil
}

// auxSessionCreateBusyRPCThreshold is the number of in-flight user-facing RPCs on
// the shared process at which non-essential auxiliary session/new attempts bail
// proactively (mitto-9gt, mitto-6cz6). A healthy workspace at rest has 0 active
// RPCs. Even one foreground prompt can overlap several distinct auxiliary creators,
// and each session/new starts an asynchronous MCP handshake in Auggie. Threshold 1
// therefore protects the normal foreground-prompt case from background amplification.
// The IsSaturated() bail
// above is REACTIVE (fires only after sessionSaturationTimeoutThreshold=3
// consecutive timeouts); this proactive check closes the first-storm hole where
// saturation has not yet tripped and NewSession would otherwise burn the full
// auxSessionCreateBudget (60s) hitting the agent's internal deadline.
const auxSessionCreateBusyRPCThreshold = int32(1)

// Title generation uses no MCP servers and shares one serialized session/new
// per workspace. Allow it to use spare process capacity while other
// conversations are active; otherwise a continuously active workspace can
// leave every new conversation on its quick fallback title indefinitely.
const titleSessionCreateBusyRPCThreshold = int32(3)

// deferredAuxQuiescenceWindow gives queued foreground work a brief opportunity
// to claim the process before deferred title-session creation is admitted.
const deferredAuxQuiescenceWindow = 100 * time.Millisecond

// WaitForProcessQuiescence waits for a stable zero-RPC edge. Concurrent title jobs
// in one workspace share a single underlying wait; same-purpose createMu then
// coalesces their eventual auxiliary session creation.
func (m *ACPProcessManager) WaitForProcessQuiescence(ctx context.Context, workspaceUUID string) bool {
	m.auxQuiescenceMu.Lock()
	wait, ok := m.auxQuiescenceWaits[workspaceUUID]
	if !ok {
		wait = &processQuiescenceWait{done: make(chan struct{})}
		m.auxQuiescenceWaits[workspaceUUID] = wait
		go m.runProcessQuiescenceWait(workspaceUUID, wait)
	}
	m.auxQuiescenceMu.Unlock()

	select {
	case <-wait.done:
		return wait.observed
	case <-ctx.Done():
		return false
	}
}

func (m *ACPProcessManager) runProcessQuiescenceWait(workspaceUUID string, wait *processQuiescenceWait) {
	observed := false
	process := m.GetProcess(workspaceUUID)
	if process != nil {
		waitCtx := m.ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		for process.WaitForQuiescence(waitCtx) {
			timer := time.NewTimer(deferredAuxQuiescenceWindow)
			select {
			case <-timer.C:
				if m.GetProcess(workspaceUUID) == process && process.ActiveRPCs() == 0 {
					observed = true
				}
			case <-waitCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			if observed || waitCtx.Err() != nil {
				break
			}
		}
	}

	m.auxQuiescenceMu.Lock()
	if m.auxQuiescenceWaits[workspaceUUID] == wait {
		delete(m.auxQuiescenceWaits, workspaceUUID)
	}
	wait.observed = observed
	close(wait.done)
	m.auxQuiescenceMu.Unlock()
}

// isProactiveBailPurpose reports whether an auxiliary purpose is subject to the
// proactive ActiveRPCs()-based load-shed (mitto-9gt). Background pre-warming and
// analysis purposes (title-gen, follow-up, keepalive, mcp-tools,
// queue-title) return true: no user is actively waiting on their result, so
// bailing costs nothing except a deferred retry. User-triggered purposes where a
// human clicked "improve prompt" and is watching (improve-prompt) return false —
// they must proceed through the normal NewSession path even under load.
func isProactiveBailPurpose(purpose string) bool {
	switch purpose {
	case auxiliary.PurposeImprovePrompt:
		return false
	default:
		return true
	}
}

// processBusyByActiveRPCs reports whether process is already serving at least
// auxSessionCreateBusyRPCThreshold concurrent user-facing RPCs. Factored out
// so the live proactive bail below and WouldShedProactiveAux (mitto-z4w)
// share one definition and cannot drift apart.
func processBusyByActiveRPCs(process *SharedACPProcess) bool {
	return process.ActiveRPCs() >= auxSessionCreateBusyRPCThreshold
}

func auxSessionBusyThreshold(purpose string) int32 {
	if purpose == auxiliary.PurposeTitleGen {
		return titleSessionCreateBusyRPCThreshold
	}
	return auxSessionCreateBusyRPCThreshold
}

// processMCPInitGated reports whether process is currently gated on its MCP
// handshake (mitto-337) — either it has explicitly timed out, or a cold-start
// handshake is actively in progress and has never completed. Factored out so
// the live bail below and WouldShedProactiveAux (mitto-z4w) share one
// definition and cannot drift apart.
func processMCPInitGated(process *SharedACPProcess) bool {
	return process.MCPInitTimedOut() || (process.MCPInitInProgress() && !process.MCPInitDone())
}

// wouldShedProactiveAuxForProcess reports whether a proactive-bail auxiliary
// session create against process would currently be shed, mirroring EXACTLY
// the pre-RPC bails in getOrCreateAuxiliarySession: no live process, process
// saturation, the ActiveRPCs threshold, MCP-init gating, and a recent
// agent-internal-deadline hit. A nil process (no shared process for the
// workspace yet) is treated as "would shed" since any attempt would fail
// outright for a different reason (mitto-z4w).
func wouldShedProactiveAuxForProcess(process *SharedACPProcess) bool {
	if process == nil {
		return true
	}
	if process.IsSaturated() {
		return true
	}
	if processBusyByActiveRPCs(process) {
		return true
	}
	if processMCPInitGated(process) {
		return true
	}
	if process.RecentlyHitAgentInternalDeadline() {
		return true
	}
	return false
}

// WouldShedProactiveAux reports whether a proactive-bail auxiliary session
// create for workspaceUUID would currently be shed by getOrCreateAuxiliarySession
// (mitto-z4w). Callers with latency-tolerant, durably-spooled work — e.g.
// close-phase processor batches — can use this predicate to route straight to
// their durable spool instead of issuing a doomed session/new that would be
// shed immediately and then ride out a busy-window retry loop before
// eventually spooling anyway. A nil manager reports true (fail toward
// deferral, never toward a doomed attempt).
func (m *ACPProcessManager) WouldShedProactiveAux(workspaceUUID string) bool {
	if m == nil {
		return true
	}
	return wouldShedProactiveAuxForProcess(m.GetProcess(workspaceUUID))
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

	// ── First check: reconcile a matching cached session before reuse ─────────────
	m.auxMu.Lock()
	if state, ok := m.auxSessions[key]; ok {
		m.auxMu.Unlock()
		process := m.GetProcess(workspaceUUID)
		if process != nil && state.matchesProcess(process, workspaceUUID) {
			var ws *config.WorkspaceSettings
			if m.WorkspaceConfigProvider != nil {
				ws = m.WorkspaceConfigProvider(workspaceUUID)
			}
			if err := m.reconcileAuxiliarySession(ctx, workspaceUUID, purpose, process, state, ws); err != nil {
				return nil, err
			}
			return state, nil
		}
		// A cached session belongs to a replaced process or different effective
		// workspace/server provenance. Its model observation cannot authorize reuse.
		m.auxMu.Lock()
		if m.auxSessions[key] == state {
			delete(m.auxSessions, key)
		}
		m.auxMu.Unlock()
		// Continue through the normal create path with a fresh session/new catalog.
		m.auxMu.Lock()
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

	// Build MCP servers list. Processor auxiliary sessions need a way for the
	// agent to call Mitto tools (e.g., mitto_ui_notify for notifications).
	// Transport selection (mitto-8ip): if the agent advertises
	// mcp_capabilities.http (e.g. Auggie), use a native HTTP McpServer entry
	// pointing at the same MCP endpoint user sessions already use — no
	// subprocess, no stdio hop. Otherwise fall back to the stdio proxy, which
	// is the ACP-spec mandatory transport all agents MUST support.
	//
	// For the stdio fallback the command MUST be the mitto CLI binary, not
	// mitto-app: on the macOS app os.Executable() points at
	// Mitto.app/Contents/MacOS/mitto-app, which is not a cobra CLI and would
	// spawn a whole second Mitto app (webview + up-hook / cloudflared) instead
	// of the intended stdio proxy. resolveMittoCLIBinary rewrites to the
	// sibling `mitto` binary in that case.
	mcpServers, transportChoice, chosenExe := buildAuxProcessorMCPServers(
		purpose, m.MCPServerURL, process.rawCapabilities(), resolveMittoCLIBinary)
	if m.logger != nil {
		switch transportChoice {
		case auxMCPTransportHTTP:
			m.logger.Debug("Auxiliary processor session will use MCP HTTP",
				"purpose", purpose,
				"mcp_url", m.MCPServerURL)
		case auxMCPTransportStdio:
			m.logger.Debug("Auxiliary processor session will use MCP proxy",
				"purpose", purpose,
				"mcp_url", m.MCPServerURL,
				"proxy_command", chosenExe)
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
	// Fail fast with a clear sentinel so callers (title-gen, mcp-tools, etc.)
	// can back off and retry once the process has recovered.
	if process.IsSaturated() {
		if m.logger != nil {
			// mitto-13n.3: demoted from Info to Debug. This bail fires on every
			// aux-session attempt while a process is saturated — dozens of times
			// per incident (58 occurrences observed on 2026-08-05) — and is now
			// redundant with the once-per-transition WARN the GC's degraded-state
			// tracker emits (see updateDegradedState / acp_process_degraded.go).
			m.logger.Debug("Skipping auxiliary NewSession: shared process is saturated",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"reason", "process_saturated")
		}
		// Wrap ErrProcessSaturated (mitto-13n.2: the specific reactive
		// sentinel, which itself wraps the umbrella ErrSharedProcessSaturated
		// for transition-era callers with their own retry loops — e.g.
		// title-gen, mitto-ammz.1) AND context.DeadlineExceeded (preserved
		// for pre-existing callers that only checked DeadlineExceeded).
		return nil, fmt.Errorf("%w; skipping auxiliary session creation for purpose %q: %w", ErrProcessSaturated, purpose, context.DeadlineExceeded)
	}

	// Proactive load-based bail (mitto-9gt): the IsSaturated() guard above is
	// REACTIVE — it fires only after sessionSaturationTimeoutThreshold=3
	// consecutive timeouts or the rate-window trip. During the FIRST storm of a
	// parallel fan-out (parent + several concurrent user-facing RPCs), saturation
	// has not yet tripped, so a background aux session/new would slip past the
	// guard and burn the full auxSessionCreateBudget (60s) hitting the agent's
	// internal deadline — the exact log signature the bead documents
	// (new_session_ms=60002 for follow-up and keepalive fired during
	// concurrent_prompting=6).
	//
	// Complementary check: when process.ActiveRPCs() reports the shared process
	// is already serving N concurrent user-facing RPCs above a threshold, bail
	// non-essential aux purposes immediately with the same "process saturated /
	// defer aux" sentinel mitto-z70 returns. User-triggered purposes where a
	// human is actively waiting (improve-prompt) are exempt — they are not
	// background pre-warming and should not be sacrificed for load-shedding.
	if isProactiveBailPurpose(purpose) {
		threshold := auxSessionBusyThreshold(purpose)
		if active := process.ActiveRPCs(); active >= threshold {
			if m.logger != nil {
				// mitto-13n.3: demoted from Info to Debug (61 occurrences observed
				// on 2026-08-05) — deliberately NOT surfaced by the degraded-state
				// tracker either, since process_busy is momentary load, not a
				// degraded process (see processDegradedState doc).
				m.logger.Debug("Skipping auxiliary NewSession: shared process is busy",
					"workspace_uuid", workspaceUUID,
					"purpose", purpose,
					"active_rpcs", active,
					"threshold", threshold,
					"reason", "process_busy")
			}
			// Wrap ErrProcessBusy (mitto-13n.2: the specific proactive
			// load-shedding sentinel, which itself wraps the umbrella
			// ErrSharedProcessSaturated so caller-side retry loops
			// (mitto-ammz.1) can still classify and abandon), while keeping
			// the DeadlineExceeded chain for pre-existing callers.
			return nil, fmt.Errorf("%w: shared ACP process is busy (%d active RPCs >= threshold %d); skipping auxiliary session creation for purpose %q: %w", ErrProcessBusy, active, threshold, purpose, context.DeadlineExceeded)
		}
	}

	// MCP-init-aware bail (mitto-337): the two guards above are keyed on
	// saturation history / concurrent RPC load, so neither fires against a
	// QUIESCENT process that the agent has already reported is gated on its
	// MCP handshake. A serialized cold-start (first-user-session in flight)
	// leaves ActiveRPCs()==1 and IsSaturated()==false while an aux create
	// waits behind createMu, then proceeds straight into a NewSession RPC
	// doomed to burn the full auxSessionCreateBudget (60s) — the observed
	// "22:31/22:32/22:33 serialized 60s failures" log signature. Bail
	// immediately when either positive signal is present:
	//   - MCPInitTimedOut(): the agent has explicitly given up on MCP init;
	//     any session/new is certain to fail the same way.
	//   - MCPInitInProgress() && !MCPInitDone(): a cold-start handshake is
	//     actively running and has never completed. Deliberately NOT gating
	//     on !MCPInitDone() alone (would deadlock the very first aux session
	//     that could otherwise warm the process) and NOT on
	//     MCPInitInProgress() alone (would also bail the normal per-session
	//     warm re-handshake agents like Auggie run on every session/new,
	//     mitto-29q).
	if processMCPInitGated(process) {
		if m.logger != nil {
			// mitto-13n.3: demoted from Info to Debug, same rationale as the
			// process_saturated bail above.
			m.logger.Debug("Skipping auxiliary NewSession: shared process is MCP-init gated",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"mcp_init_timed_out", process.MCPInitTimedOut(),
				"mcp_init_in_progress", process.MCPInitInProgress(),
				"mcp_init_done", process.MCPInitDone(),
				"reason", "mcp_init_gated")
		}
		// Wrap ErrMCPInitGated (mitto-13n.2: the specific MCP-handshake
		// sentinel, which itself wraps the umbrella ErrSharedProcessSaturated
		// for transition-era callers), while keeping the DeadlineExceeded
		// chain for pre-existing callers.
		return nil, fmt.Errorf("%w: shared ACP process is mcp-init gated; skipping auxiliary session creation for purpose %q: %w", ErrMCPInitGated, purpose, context.DeadlineExceeded)
	}

	// Agent-internal-deadline fast-shed (mitto-pic): the three bails above are all
	// keyed on state that is NOT yet set on the FIRST agent-internal-deadline
	// timeout — IsSaturated() needs 3 consecutive timeouts (or an 8-sample rate-
	// window trip), the ActiveRPCs bail needs a concurrent in-flight RPC, and the
	// MCP-init bail needs an active/timed-out handshake. A quiescent-looking
	// process that just wedged on its OWN internal deadline therefore burns a full
	// ~60s per non-essential aux create until the reactive bail arms — and in the
	// intermittent-success case (a success resets consecutiveRPCTimeouts) it may
	// never arm at all. RecentlyHitAgentInternalDeadline() arms after a SINGLE
	// occurrence and self-clears after auxAgentDeadlineShedCooldown (or immediately
	// on the next real success), bridging that gap for non-essential purposes only
	// — improve-prompt (a human actively waiting) is exempt via isProactiveBailPurpose.
	if isProactiveBailPurpose(purpose) && process.RecentlyHitAgentInternalDeadline() {
		if m.logger != nil {
			// Debug, consistent with the process_saturated/process_busy/mcp_init_gated
			// bails above: this can fire repeatedly during a single incident and is
			// not itself a new degraded-state transition worth an Info/Warn line.
			m.logger.Debug("Skipping auxiliary NewSession: process recently hit agent-internal deadline",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose,
				"reason", "agent_deadline_shed")
		}
		// Wrap ErrProcessBusy (the existing proactive load-shedding sentinel — no
		// new sentinel class per the plan) so callers keep exactly the same
		// classification and bounded busy-wait retry behavior (mitto-hjx/mitto-qvs)
		// as the ActiveRPCs-based bail above, while keeping the DeadlineExceeded
		// chain for pre-existing callers.
		return nil, fmt.Errorf("%w: shared ACP process recently hit the agent's internal deadline; skipping auxiliary session creation for purpose %q: %w", ErrProcessBusy, purpose, context.DeadlineExceeded)
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
	newCtx, newCancel := context.WithTimeout(m.ctx, auxSessionCreateBudget)
	defer newCancel()
	newSessionStart := time.Now()
	catalogGen := process.Generation()
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

	m.forwardModelCatalog(process, process.config.ACPServer, catalogGen, sessionHandle.Models)

	initialModelID := ""
	if sessionHandle.Models != nil {
		initialModelID = sessionHandle.Models.CurrentModelId
	}
	state := &auxiliarySessionState{
		sessionID:         sessionHandle.SessionID,
		client:            newAuxiliaryClient(),
		lastUsed:          time.Now(),
		modelID:           initialModelID,
		rawModels:         cloneAuxiliaryModelState(sessionHandle.Models),
		workspaceUUID:     workspaceUUID,
		configuredServer:  process.config.ACPServer,
		sessionFolder:     auxCwd,
		process:           process,
		processGeneration: catalogGen,
	}

	var ws *config.WorkspaceSettings
	if m.WorkspaceConfigProvider != nil {
		ws = m.WorkspaceConfigProvider(workspaceUUID)
	}
	// A configured selector and a policy-hidden default are both safety
	// requirements. Do not register or publish this session until a safe model is
	// either already reported or confirmed by the agent.
	if err := m.reconcileAuxiliarySession(ctx, workspaceUUID, purpose, process, state, ws); err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(m.ctx, 2*time.Second)
		_ = process.Cancel(cleanupCtx, sessionHandle.SessionID)
		cleanupCancel()
		return nil, err
	}

	// Register the session with the multiplexer
	callbacks := &conversation.SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			return state.client.OnSessionUpdate(ctx, params)
		},
		OnRequestPermission: func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return state.client.OnRequestPermission(ctx, params)
		},
		OnReadTextFile: func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
			return state.client.OnReadTextFile(ctx, params)
		},
		OnWriteTextFile: func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
			return state.client.OnWriteTextFile(ctx, params)
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
	process.RegisterSession(sessionHandle.SessionID, callbacks)

	// Store the result under a brief auxMu lock.
	// Defensive double-check: if an entry somehow already exists (shouldn't happen
	// given createMu, but be safe), return the existing one to avoid duplicates.
	m.auxMu.Lock()
	if existing, ok := m.auxSessions[key]; ok {
		m.auxMu.Unlock()
		return existing, nil
	}
	m.auxSessions[key] = state
	m.auxMu.Unlock()

	if m.logger != nil {
		modelID, modelReported := state.modelForLog()
		m.logger.Info("Created auxiliary session",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", sessionHandle.SessionID,
			"model_id", modelID,
			"model_reported", modelReported,
			"new_session_ms", newSessionLatency.Milliseconds(),
			"total_ms", time.Since(createStart).Milliseconds())
	}

	return state, nil
}

func (s *auxiliarySessionState) setModelID(modelID string) {
	s.modelMu.Lock()
	s.modelID = modelID
	if s.rawModels != nil {
		s.rawModels.CurrentModelId = modelID
	}
	s.modelMu.Unlock()
}

func cloneAuxiliaryModelState(models *conversation.SessionModelState) *conversation.SessionModelState {
	if models == nil {
		return nil
	}
	copy := *models
	copy.AvailableModels = append([]conversation.ModelInfo(nil), models.AvailableModels...)
	return &copy
}

func (s *auxiliarySessionState) matchesProcess(process *SharedACPProcess, workspaceUUID string) bool {
	if process == nil {
		return false
	}
	folder := process.WorkingDir()
	if folder == "" {
		folder = "."
	}
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.process == process &&
		s.workspaceUUID == workspaceUUID &&
		s.configuredServer == process.config.ACPServer &&
		s.sessionFolder == folder &&
		s.processGeneration == process.Generation()
}

func (s *auxiliarySessionState) rawModelState() *conversation.SessionModelState {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return cloneAuxiliaryModelState(s.rawModels)
}

func (m *ACPProcessManager) auxiliaryPolicy(server string) *config.HiddenModelPolicy {
	if m.HiddenModelPolicies == nil {
		return nil
	}
	if snapshot := m.HiddenModelPolicies.Snapshot(); snapshot != nil {
		return snapshot.Policy(server)
	}
	return nil
}

// auxiliaryWorkspaceRules returns the workspace's live hidden-model rules
// (nil-safe on a missing workspace).
func (m *ACPProcessManager) auxiliaryWorkspaceRules(ws *config.WorkspaceSettings) []string {
	if ws == nil {
		return nil
	}
	return config.NormalizeHiddenModels(ws.HiddenModels)
}

// combinedAuxiliaryPolicy unions the agent-level policy with the workspace's
// live hidden-model rules. Workspace rules that fail to compile fail closed:
// the returned policy hides everything (mitto-zu4.3).
func (m *ACPProcessManager) combinedAuxiliaryPolicy(server string, ws *config.WorkspaceSettings) *config.HiddenModelPolicy {
	policy, err := config.CombineAgentWorkspacePolicies(server, m.auxiliaryPolicy(server), m.auxiliaryWorkspaceRules(ws))
	if err != nil {
		m.logAuxiliaryPolicyFailure(server, err)
	}
	return policy
}

// workspaceAuxiliaryPolicy compiles the workspace-level half alone; the agent
// half is re-read from a pinned snapshot inside admission callbacks.
func (m *ACPProcessManager) workspaceAuxiliaryPolicy(server string, ws *config.WorkspaceSettings) *config.HiddenModelPolicy {
	policy, err := config.CombineAgentWorkspacePolicies(server, nil, m.auxiliaryWorkspaceRules(ws))
	if err != nil {
		m.logAuxiliaryPolicyFailure(server, err)
	}
	return policy
}

// logAuxiliaryPolicyFailure records a fail-closed workspace rule compile error.
func (m *ACPProcessManager) logAuxiliaryPolicyFailure(server string, err error) {
	if m.logger != nil {
		m.logger.Warn("auxiliary workspace hidden-model policy failed to compile; failing closed", "acp_server", server, "error", err)
	}
}

func (m *ACPProcessManager) auxiliarySelector(ws *config.WorkspaceSettings) (bool, string) {
	if ws != nil && (ws.AuxiliaryModelProfile != "" || ws.AuxiliaryModelTag != "" || ws.AuxiliaryModelSelection != nil) {
		return true, "workspace"
	}
	if m.GlobalAuxModelTagProvider != nil {
		if tag := strings.TrimSpace(m.GlobalAuxModelTagProvider()); tag != "" {
			return true, "global tag " + tag
		}
	}
	return false, ""
}

func (m *ACPProcessManager) auxiliaryModelUnavailable(workspaceUUID, purpose, selector, modelID, reason string, cause error) error {
	err := &mittoAcp.AuxiliaryModelUnavailableError{Workspace: workspaceUUID, Purpose: purpose, Selector: selector, ModelID: modelID, Reason: reason}
	if cause != nil {
		return fmt.Errorf("%w: %v", err, cause)
	}
	return err
}

// resolveAuxiliaryModel filters a raw agent catalog through the policy of the
// configured server before resolving workspace/profile/tag selection. With an
// active policy, an unknown or hidden agent default cannot survive without an
// eligible catalog fallback.
func (m *ACPProcessManager) resolveAuxiliaryModel(ws *config.WorkspaceSettings, raw *conversation.SessionModelState, configuredServer, workspaceUUID, purpose string) (string, bool, error) {
	return m.resolveAuxiliaryModelWithPolicy(ws, raw, m.combinedAuxiliaryPolicy(configuredServer, ws), workspaceUUID, purpose)
}

// resolveAuxiliaryModelWithPolicy is the pure resolver for a specific policy
// snapshot. Callers capture the policy before entering any final admission
// callback so profile/tag provider callbacks never run under the policy lock.
func (m *ACPProcessManager) resolveAuxiliaryModelWithPolicy(ws *config.WorkspaceSettings, raw *conversation.SessionModelState, policy *config.HiddenModelPolicy, workspaceUUID, purpose string) (string, bool, error) {
	configured, selector := m.auxiliarySelector(ws)
	if policy == nil {
		if !configured {
			if raw == nil {
				return "", false, nil
			}
			return raw.CurrentModelId, false, nil
		}
		if raw == nil || raw.Synthesized {
			return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "agent did not provide a usable model catalog", nil)
		}
		constraint := m.resolveAuxConstraint(ws, raw, workspaceUUID, purpose)
		if constraint == nil || constraint.Pattern == "" {
			return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "configured selector does not resolve against the agent model catalog", nil)
		}
		matched, shouldSet := conversation.ResolveAuxModelSwitch(constraint, raw)
		if matched == "" {
			return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "configured selector matches no model in the agent catalog", nil)
		}
		return matched, shouldSet, nil
	}
	if raw == nil || raw.Synthesized || raw.CurrentModelId == "" {
		return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "agent did not provide a verifiable model catalog and current model", nil)
	}
	currentKnown := false
	for _, model := range raw.AvailableModels {
		if model.ModelId == raw.CurrentModelId {
			currentKnown = true
			break
		}
	}
	if !currentKnown {
		return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, raw.CurrentModelId, "agent current model is absent from the advertised catalog", nil)
	}
	eligible := *raw
	eligible.AvailableModels = config.FilterHiddenModels(policy, raw.AvailableModels, func(model conversation.ModelInfo) (string, string) {
		return model.ModelId, model.Name
	})
	if len(eligible.AvailableModels) == 0 {
		return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, raw.CurrentModelId, "all advertised models are hidden by policy", nil)
	}
	if configured {
		constraint := m.resolveAuxConstraint(ws, &eligible, workspaceUUID, purpose)
		if constraint == nil || constraint.Pattern == "" {
			return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "configured selector does not resolve against the eligible model catalog", nil)
		}
		matched, shouldSet := conversation.ResolveAuxModelSwitch(constraint, &eligible)
		if matched == "" {
			return "", false, m.auxiliaryModelUnavailable(workspaceUUID, purpose, selector, "", "configured selector matches no eligible model", nil)
		}
		return matched, shouldSet, nil
	}
	for _, model := range eligible.AvailableModels {
		if model.ModelId == raw.CurrentModelId {
			return model.ModelId, false, nil
		}
	}
	return eligible.AvailableModels[0].ModelId, true, nil
}

func (m *ACPProcessManager) reconcileAuxiliarySession(ctx context.Context, workspaceUUID, purpose string, process *SharedACPProcess, state *auxiliarySessionState, ws *config.WorkspaceSettings) error {
	if !state.matchesProcess(process, workspaceUUID) {
		return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", "", "auxiliary session provenance no longer matches the shared process", nil)
	}
	target, shouldSet, err := m.resolveAuxiliaryModel(ws, state.rawModelState(), process.config.ACPServer, workspaceUUID, purpose)
	if err != nil || !shouldSet {
		return err
	}
	setCtx, setCancel := context.WithTimeout(ctx, setModelAsyncCallerBudget)
	setErr := process.SetSessionModel(setCtx, state.sessionID, target)
	setCancel()
	if setErr != nil {
		if m.logger != nil {
			m.logger.Warn("Auxiliary session paused: eligible model was not confirmed", "workspace_uuid", workspaceUUID, "purpose", purpose, "model_id", target, "error", setErr)
		}
		return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", target, "agent rejected or did not confirm the eligible model", setErr)
	}
	if !state.matchesProcess(process, workspaceUUID) {
		return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", target, "shared process changed while confirming the eligible model", nil)
	}
	state.setModelID(target)
	return nil
}

// auxiliaryWorkspaceSettings returns the workspace settings via the provider,
// or nil when no provider is wired.
func (m *ACPProcessManager) auxiliaryWorkspaceSettings(workspaceUUID string) *config.WorkspaceSettings {
	if m.WorkspaceConfigProvider == nil {
		return nil
	}
	return m.WorkspaceConfigProvider(workspaceUUID)
}

// workspaceHiddenRulesResolver returns the callback process-level set_model
// admission uses to read a workspace's live hidden-model rules. The closure
// captures the manager so tests can swap the provider after process creation.
// Nil even when the provider or workspace is missing is a safe no-op.
func (m *ACPProcessManager) workspaceHiddenRulesResolver(workspaceUUID string) func() []string {
	return func() []string {
		ws := m.auxiliaryWorkspaceSettings(workspaceUUID)
		if ws == nil {
			return nil
		}
		return ws.HiddenModels
	}
}

// prepareAuxiliaryDispatch reconciles the auxiliary model after the caller has
// acquired the session turn lock. Queued work therefore re-evaluates the
// current policy instead of inheriting the decision made when it was enqueued.
func (m *ACPProcessManager) prepareAuxiliaryDispatch(ctx context.Context, workspaceUUID, purpose string, process *SharedACPProcess, state *auxiliarySessionState) error {
	return m.reconcileAuxiliarySession(ctx, workspaceUUID, purpose, process, state, m.auxiliaryWorkspaceSettings(workspaceUUID))
}

// auxiliaryPromptAdmission returns the final pre-RPC gate for one auxiliary
// prompt attempt. It resolves against a captured immutable policy snapshot
// first (profile/tag providers are callbacks and must not run under the policy
// lock), then pins that same snapshot to prove no publication slipped in
// between resolution and transport handoff.
func (m *ACPProcessManager) auxiliaryPromptAdmission(workspaceUUID, purpose string, process *SharedACPProcess, state *auxiliarySessionState) func() error {
	return func() error {
		return m.admitAuxiliaryDispatch(process, workspaceUUID, purpose, state, m.auxiliaryWorkspaceSettings(workspaceUUID))
	}
}

func (m *ACPProcessManager) admitAuxiliaryDispatch(process *SharedACPProcess, workspaceUUID, purpose string, state *auxiliarySessionState, ws *config.WorkspaceSettings) error {
	if !state.matchesProcess(process, workspaceUUID) {
		return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", "", "auxiliary session provenance no longer matches the shared process", nil)
	}
	server := process.config.ACPServer
	// The workspace half is resolved and compiled OUTSIDE the policy lock; the
	// agent half is merged from the pinned snapshot (no callbacks inside Admit).
	wsPolicy := m.workspaceAuxiliaryPolicy(server, ws)
	policies := m.HiddenModelPolicies
	var snapshot *config.HiddenModelSnapshot
	if policies != nil {
		if pinned := policies.Snapshot(); pinned != nil {
			snapshot = pinned
		}
	}
	snapshotPolicy := config.MergeHiddenModelPolicies(m.auxiliaryPolicy(server), wsPolicy)
	if snapshot == nil && wsPolicy == nil {
		return nil
	}
	raw := state.rawModelState()
	if snapshotPolicy != nil && (raw == nil || raw.Synthesized || raw.CurrentModelId == "") {
		return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", "", "agent did not provide a verifiable model catalog and current model", nil)
	}
	if raw != nil && !raw.Synthesized && raw.CurrentModelId != "" {
		target, shouldSet, err := m.resolveAuxiliaryModelWithPolicy(ws, raw, snapshotPolicy, workspaceUUID, purpose)
		if err != nil {
			return err
		}
		if shouldSet || target != raw.CurrentModelId {
			return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", raw.CurrentModelId, "hidden-model policy changed after reconciliation; a confirmed eligible model is required before dispatch", nil)
		}
	}
	if policies == nil {
		// Without snapshot infrastructure the workspace half still gated the
		// strict checks above; anything eligible under them may dispatch.
		return nil
	}
	return policies.Admit(func(current *config.HiddenModelSnapshot) error {
		if current != snapshot {
			return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", "", "hidden-model policy changed during final auxiliary admission", nil)
		}
		if current == nil {
			return nil
		}
		currentPolicy := config.MergeHiddenModelPolicies(current.Policy(server), wsPolicy)
		if currentPolicy == nil {
			return nil
		}
		if raw == nil || raw.Synthesized || raw.CurrentModelId == "" {
			return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", "", "agent did not provide a verifiable model catalog and current model", nil)
		}
		displayName := ""
		for _, model := range raw.AvailableModels {
			if model.ModelId == raw.CurrentModelId {
				displayName = model.Name
				break
			}
		}
		if currentPolicy.Hides(raw.CurrentModelId, displayName) {
			return m.auxiliaryModelUnavailable(workspaceUUID, purpose, "", raw.CurrentModelId, "current model is hidden by policy", nil)
		}
		return nil
	})
}

// currentModelHidden reports whether a policy hides the raw model state's
// current model. A nil policy or raw state hides nothing.
func currentModelHidden(policy *config.HiddenModelPolicy, raw *conversation.SessionModelState) bool {
	if policy == nil || raw == nil {
		return false
	}
	displayName := ""
	for _, model := range raw.AvailableModels {
		if model.ModelId == raw.CurrentModelId {
			displayName = model.Name
			break
		}
	}
	return policy.Hides(raw.CurrentModelId, displayName)
}

func (s *auxiliarySessionState) modelForLog() (string, bool) {
	s.modelMu.RLock()
	modelID := s.modelID
	s.modelMu.RUnlock()
	return auxiliaryModelForLog(modelID)
}

func auxiliaryModelForLog(modelID string) (string, bool) {
	if modelID == "" {
		return "unknown", false
	}
	return modelID, true
}

// resolveAuxConstraint picks the model constraint for a new auxiliary session.
// Precedence: workspace AuxiliaryModelProfile > AuxiliaryModelTag >
// AuxiliaryModelSelection; the global conversations.auxiliary_model_tag
// (GlobalAuxModelTagProvider) applies only when the workspace sets none of
// them (mitto-ee2.9). Returns nil when nothing resolves, leaving the agent's
// default model in place. Never fails the session.
func (m *ACPProcessManager) resolveAuxConstraint(ws *config.WorkspaceSettings, models *conversation.SessionModelState, workspaceUUID, purpose string) *config.ACPServerConstraint {
	if ws != nil && (ws.AuxiliaryModelProfile != "" || ws.AuxiliaryModelTag != "" || ws.AuxiliaryModelSelection != nil) {
		if ws.AuxiliaryModelProfile != "" {
			if m.ModelProfileResolver == nil {
				return nil
			}
			profile := m.ModelProfileResolver(ws.AuxiliaryModelProfile)
			if profile == nil || profile.Criteria == nil {
				return nil
			}
			return profile.Criteria
		}
		if ws.AuxiliaryModelTag != "" {
			if m.ModelProfilesByTagResolver == nil {
				return nil
			}
			// Priority axis is profile-list order: ModelProfilesByTagResolver returns
			// tagged profiles in Config.Models order (mirrors config.ProfilesByTag),
			// and resolveAuxTagConstraint picks the FIRST whose Criteria resolves
			// against the session's models (mitto-ex7 "list order = priority").
			return resolveAuxTagConstraint(m.ModelProfilesByTagResolver(ws.AuxiliaryModelTag), models)
		}
		return ws.AuxiliaryModelSelection
	}

	if m.GlobalAuxModelTagProvider == nil || m.ModelProfilesByTagResolver == nil {
		return nil
	}
	tag := strings.TrimSpace(m.GlobalAuxModelTagProvider())
	if tag == "" {
		return nil
	}
	// Never send ids from a locally synthesized catalog (mitto-a7wm): they are
	// profile display names, not agent-confirmed model ids.
	if models == nil || models.Synthesized {
		return nil
	}
	c := resolveAuxTagConstraint(m.ModelProfilesByTagResolver(tag), models)
	if c == nil && m.logger != nil {
		m.logger.Warn("Auxiliary session: global auxiliary_model_tag matched no available model, using server default",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"tag", tag)
	}
	return c
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
// The expected-state check prevents a delayed cleanup from deleting a replacement.
// Must be called WITHOUT holding auxMu.
func (m *ACPProcessManager) invalidateAuxSession(workspaceUUID, purpose string, expected *auxiliarySessionState) {
	key := auxSessionKey{workspaceUUID: workspaceUUID, purpose: purpose}
	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	if current, ok := m.auxSessions[key]; ok && current == expected {
		delete(m.auxSessions, key)
		if m.logger != nil {
			m.logger.Info("Invalidated stale auxiliary session for retry",
				"workspace_uuid", workspaceUUID,
				"purpose", purpose)
		}
	}
}

const auxiliaryCancelTimeout = 2 * time.Second

// retireCancelledAuxSession isolates a timed-out turn before the per-session
// lock is released. Retiring the callback first also rejects notifications
// already queued inside the ACP client before UnregisterSession takes effect.
func (m *ACPProcessManager) retireCancelledAuxSession(process *SharedACPProcess, workspaceUUID, purpose string, state *auxiliarySessionState) {
	state.client.retire()
	cancelCtx, cancel := context.WithTimeout(context.Background(), auxiliaryCancelTimeout)
	defer cancel()
	if err := process.Cancel(cancelCtx, state.sessionID); err != nil && m.logger != nil {
		m.logger.Warn("Failed to cancel timed-out auxiliary prompt",
			"workspace_uuid", workspaceUUID,
			"purpose", purpose,
			"session_id", state.sessionID,
			"error", err)
	}
	process.UnregisterSession(state.sessionID)
	m.invalidateAuxSession(workspaceUUID, purpose, state)
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
		// The waiter goroutine may still be blocked on the mutex. Hand
		// ownership of the eventual acquisition to a reaper so a cancelled
		// caller never leaves the session permanently locked.
		go func() {
			<-acquired
			auxState.mu.Unlock()
		}()
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
// returns entries in nondecreasing Delay order (tier-0 mcp-tools
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
	// three hardcoded purpose strings in internal/config/settings.go is caught
	// at compile time here. Not otherwise used at runtime.
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
	case err != nil && errors.Is(err, ErrSharedProcessSaturated):
		// mitto-xetv: getOrCreateAuxiliarySession's saturation-family bails
		// (ErrProcessSaturated / ErrProcessBusy / ErrMCPInitGated, all
		// wrapping the umbrella ErrSharedProcessSaturated) return WITHOUT
		// attempting the session/new RPC — the probe measured nothing about
		// the process's steady-state health. Collapsing this into
		// "session_new_failed" pinned an already-drowning process, which
		// only adds re-probe load to it and occupies a max_pinned slot that
		// a genuine mcp_timeout/slow_session_new pin could have used.
		reason = "probe_shed"
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

	if reason == "probe_shed" {
		// mitto-xetv: a shed probe measured nothing, so it must feed
		// neither the pin path nor the unpin hysteresis (a shed is not a
		// "bad health" signal that should reset progress toward unpinning
		// an otherwise slow-but-alive workspace).
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

// catalogProcess is the read-only process view needed to scope an observation.
type catalogProcess interface {
	WorkingDir() string
	Generation() int
}

// forwardModelCatalog forwards an already-returned authoritative session/new
// catalog to the recorder. Synthesized/nil catalogs are not observations, and
// data from a superseded process generation is dropped. It issues no RPC.
func (m *ACPProcessManager) forwardModelCatalog(process catalogProcess, server string, gen int, models *conversation.SessionModelState) {
	if m.ModelCatalogRecorder == nil || models == nil || models.Synthesized || gen <= 0 {
		return
	}
	if process.Generation() != gen {
		return
	}
	list := make([]conversation.ModelInfo, len(models.AvailableModels))
	copy(list, models.AvailableModels)
	err := m.ModelCatalogRecorder(conversation.ModelCatalogObservation{
		Server:     server,
		WorkingDir: process.WorkingDir(),
		Models:     list,
		Complete:   true,
		Generation: uint64(gen),
		ObservedAt: time.Now(),
	})
	if err != nil && m.logger != nil {
		m.logger.Warn("Failed to record auxiliary model catalog", "server", server, "error", err)
	}
}
