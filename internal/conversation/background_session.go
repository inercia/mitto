package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

// BackgroundSession manages an ACP session that runs independently of WebSocket connections.
// It continues running even when no client is connected, persisting all events to disk.
// Multiple observers can subscribe to receive real-time updates.
//
// Events are persisted immediately when received from ACP, preserving the sequence numbers
// assigned at streaming time. This ensures streaming and persisted events have identical seq.
type BackgroundSession struct {
	// Immutable identifiers
	persistedID string // Session ID for persistence and routing
	acpID       string // ACP protocol session ID

	// ACP connection state
	acpCmd    *exec.Cmd
	acpConn   *acp.ClientSideConnection
	acpClient *WebClient
	acpWait   func() error // cleanup function from runner.RunWithPipes or cmd.Wait

	// ACP process death detection (Fix A: faster crash detection)
	// acpProcessDone is closed when the ACP OS process exits, providing sub-second
	// detection of process death. This is faster than acpConn.Done() which only
	// closes when the JSON-RPC transport layer detects EOF — which may be delayed
	// if the ACP wrapper process stays alive after the inner CLI subprocess dies.
	// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
	acpProcessDone     chan struct{} // Closed when ACP OS process exits
	acpProcessDoneOnce sync.Once     // Ensures acpProcessDone is closed exactly once

	// ACP agent capabilities (set during initialization)
	agentSupportsImages bool // True if agent advertises prompt_image capability

	// agentModels holds the model state (UNSTABLE) from NewSession/LoadSession/ResumeSession.
	// Uses UnstableSessionModelState to unify both stable and unstable response variants.
	// May be nil if the agent doesn't advertise model information.
	agentModels *acp.UnstableSessionModelState

	// Session persistence
	recorder *session.Recorder

	// Sequence number tracking for event ordering
	// nextSeq is the next sequence number to assign to a new event.
	// It's initialized from the store's EventCount + 1 and incremented for each new event.
	// This ensures streaming events have the same seq as their persisted counterparts.
	nextSeq int64
	seqMu   sync.Mutex // Protects nextSeq

	// Session lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	closed    atomic.Int32
	startedAt time.Time // When this session was started/resumed (for GC grace period)

	// Observers (multiple clients can observe this session)
	observersMu           sync.RWMutex
	observers             map[SessionObserver]struct{}
	lastObserverRemovedAt atomic.Int64 // Unix nanos when observer count last dropped to 0 (for GC grace period)

	// Connected WebSocket client tracking (separate from observers)
	// Clients are counted from initial WS connection until disconnect,
	// even before they send load_events and become observers.
	connectedClients atomic.Int32

	// Activity tracking — updated on observer add and prompt start.
	// Used by GC to determine if a session has been recently used.
	lastActivityAt atomic.Int64 // Unix nanos

	// Prompt state
	promptMu                 sync.Mutex
	promptCond               *sync.Cond // Condition variable for waiting on prompt completion
	isPrompting              bool
	promptCount              int
	promptStartTime          time.Time // When the current prompt started (for logging)
	lastResponseComplete     time.Time // When the agent last completed a response (for queue delay)
	queuedDeliveryInProgress bool      // true while a popped message is sleeping through delay

	// lastAgentActivityAt records the time (Unix nanos) of the most recent streamed
	// update received from the agent during a prompt. It is reset when a prompt starts
	// and updated on every ACP SessionUpdate. The prompt inactivity watchdog reads it
	// to detect a live-but-unresponsive agent (one that stops streaming without crashing).
	lastAgentActivityAt atomic.Int64

	// Configuration
	autoApprove bool
	logger      *slog.Logger

	// Resume state - for injecting conversation history
	isResumed       bool           // True if this session was resumed from storage
	store           *session.Store // Store reference for reading history
	historyInjected bool           // True after history has been injected

	// Conversation processing
	processorManager    *processors.Manager             // Unified processor pipeline (text-mode + command-mode)
	workingDir          string                          // Working directory for processor execution
	isFirstPrompt       bool                            // True until first prompt is sent (for processor conditions)
	availableACPServers []processors.AvailableACPServer // ACP servers available in this workspace folder

	// Queue processing
	queueConfig *config.QueueConfig // Queue configuration (nil means use defaults)

	// Action buttons (follow-up suggestions)
	// These are AI-generated response options shown after the agent completes a response.
	// We use a two-tier cache (memory + disk) so suggestions persist across client
	// reconnections and server restarts. See docs/devel/follow-up-suggestions.md.
	actionButtonsConfig *config.ActionButtonsConfig // Configuration (nil means disabled)
	actionButtonsMu     sync.RWMutex                // Protects cachedActionButtons
	cachedActionButtons []ActionButton              // In-memory cache for fast access
	followUpInProgress  atomic.Bool                 // Prevents concurrent follow-up analyses (prompt completion vs session resume race)

	// selfDestructRequested is set when the agent requests deletion of its own
	// conversation via the mitto_conversation_delete MCP tool. The deletion is
	// deferred until the current turn completes (see PromptWithMeta), at which
	// point onSelfDestruct is invoked to actually remove the conversation.
	selfDestructRequested atomic.Bool

	// File links configuration
	fileLinksConfig *config.FileLinksConfig // Configuration for file path linking
	apiPrefix       string                  // URL prefix for API endpoints (for HTTP file links)
	workspaceUUID   string                  // Workspace UUID for secure file links

	// Restricted runner for sandboxed execution
	runner *runner.Runner // Optional runner for restricted execution (nil = direct execution)

	// onStreamingStateChanged is called when the session's streaming state changes.
	onStreamingStateChanged func(sessionID string, isStreaming bool)

	// onUIPromptStateChanged is called when a blocking UI prompt starts or ends.
	onUIPromptStateChanged func(sessionID string, isWaiting bool)

	// onUIPromptTimeout is called when a blocking UI prompt times out with no active viewer.
	// Used to broadcast a native notification to all connected clients.
	onUIPromptTimeout func(sessionID string, req UIPromptRequest, sessionName string)

	// onPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	onPlanStateChanged func(sessionID string, entries []PlanEntry)

	// onTitleGenerated is called when a title is auto-generated for this session.
	// Used to broadcast session_renamed events to all clients.
	onTitleGenerated func(sessionID, title string)

	// onSelfDestruct is called after a turn completes when the agent has
	// requested deletion of its own conversation. It performs the actual
	// deletion (close ACP, remove from disk, broadcast) via SessionManager.
	onSelfDestruct func(sessionID string)

	// onTurnIdle is called after a turn completes and the session is fully idle
	// (turn succeeded and no further queued message was dispatched). Used to arm
	// the on-completion periodic timer.
	onTurnIdle func(sessionID string)

	// isChildPrompting checks if a child session is currently prompting.
	// Set by SessionManager to enable children.promptingCount CEL context.
	isChildPrompting func(childSessionID string) bool

	// Available slash commands from the agent
	availableCommandsMu sync.RWMutex
	availableCommands   []AvailableCommand

	// ACP process restart tracking
	// When the ACP process dies unexpectedly, we attempt to restart it automatically.
	// To prevent infinite restart loops, we limit restarts to MaxACPRestarts within
	// ACPRestartWindow. The acpCommand and acpCwd are stored so we can restart the process.
	acpCommand           string                                 // Command used to start ACP process (for restart)
	acpCwd               string                                 // Working directory for ACP process (for restart)
	serverEnv            map[string]string                      // Server-specific env vars from settings.json (for restart)
	acpServerConstraints map[string]*config.ACPServerConstraint // Auto-selection constraints from the ACP server config
	procCtl              acpProcessController                   // ACP restart policy collaborator (composition)
	titleCoord           titleCoordinator                       // Auto-title generation triggers collaborator (composition)
	queueDisp            queueDispatcher                        // Queue tick / dispatch logic collaborator (composition)
	callbackSink         acpCallbackSink                        // WebClient callback cluster collaborator (composition)
	uiPromptCtr          uiPromptCenter                         // UI prompt + notify collaborator (composition)
	followUpCoord        followUpCoordinator                    // Follow-up suggestions + action-button collaborator (composition)
	configMgr            configManager                          // Session-config / model-baseline collaborator (composition)
	handshaker           sharedSessionHandshaker                // Shared-process session handshake collaborator (composition)
	promptDisp           promptDispatcher                       // PromptWithMeta helper-split collaborator (composition)

	// Session config options - configurable settings for the session
	// This supports both legacy "modes" API and newer "configOptions" API.
	// See https://agentclientprotocol.com/protocol/session-config-options
	configMu        sync.RWMutex
	configOptions   []SessionConfigOption                          // All config options (includes mode if available)
	onConfigChanged func(sessionID string, configID, value string) // Called when any config option changes
	usesLegacyModes bool                                           // True if using legacy modes API (not configOptions)

	// pendingConfig holds config changes (configID→value) recorded while the agent
	// is prompting. The real ACP RPC is deferred and issued on the prompting→idle
	// transition (flushPendingConfig), ordered before the next queued message is
	// dispatched. Last-write-wins per configID. Guarded by pendingConfigMu; lock
	// order is promptMu → pendingConfigMu (never the reverse).
	pendingConfigMu sync.Mutex
	pendingConfig   map[string]string

	// Global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	globalMcpServer *mcpserver.Server

	// Auxiliary manager for workspace-scoped auxiliary tasks
	auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager

	// Active UI prompt state for MCP tool user prompts
	// When an MCP tool calls Prompt(), this holds the pending prompt until the user responds
	activePromptMu sync.Mutex
	activePrompt   *activeUIPrompt

	// Last prompt token usage — updated after each successful prompt completes.
	lastUsage   *acp.Usage
	lastUsageMu sync.Mutex

	// Context window usage — updated from SessionUsageUpdate notifications.
	contextSize    int
	contextUsed    int
	contextUsageMu sync.Mutex

	// sharedProcess is set when this session uses workspace-scoped process sharing.
	// When non-nil, this session does not own the OS process — it only owns a session
	// slot on the shared process. nil = legacy per-session process ownership.
	sharedProcess SharedProcess

	// Lazy ACP session handshake for shared-process sessions.
	// When pendingShared is true, session/new has not yet been called;
	// it is deferred to the first prompt to avoid blocking the create path
	// when the shared agent process is busy.
	pendingShared           bool
	pendingSharedMu         sync.Mutex                     // Guards the lazy handshake (idempotency)
	pendingSharedWorkingDir string                         // Stored for deferred session/new RPC
	pendingSharedMcpServers []acp.McpServer                // Must be empty array, not nil — ACP validates this
	pendingSharedModes      *acp.SessionModeState          // Modes from NewSession, applied by applyPendingSharedModes
	pendingSharedModels     *acp.UnstableSessionModelState // Models from NewSession, applied by applyPendingSharedModes

	// handshakeMu serialises the full deferred-handshake completion (session/new
	// RPC + store writes + mode/model application + acp_started notification) so the
	// first-prompt goroutine and the background-prewarm goroutine never run it
	// concurrently. See completeDeferredHandshake and PrewarmACPSession.
	handshakeMu sync.Mutex

	// resumeMethod tracks which method was used to establish the ACP session:
	// "resume" (UNSTABLE resume API), "load" (history replay), or "new" (fresh session)
	resumeMethod string

	// creationCtx is the context passed for the initial ACP session creation RPC.
	// It is set from BackgroundSessionConfig.CreationCtx and nil'd out after the RPC
	// completes so we don't hold a reference longer than necessary.
	// It is ONLY used in startSharedACPSession and resumeSharedACPSession, never for
	// the session's lifetime. Use creationRPCCtx() to obtain a ready-to-use context.
	creationCtx context.Context

	// promptResolver resolves a named workspace prompt to its full text at send time.
	// Set via SetPromptResolver or BackgroundSessionConfig.PromptResolver.
	// When nil, PromptMeta.PromptName resolution is skipped.
	promptResolver PromptResolver

	// preferredModelsResolver resolves a prompt name to its preferredModels list.
	// Used in PromptWithMeta to auto-select models for named prompts without a
	// PreferredModels field already set in PromptMeta.
	preferredModelsResolver func(name, workingDir string) []string

	// Model preference override tracking (guarded by modelMu).
	modelMu        sync.Mutex // Protects baselineModel and overrideActive
	baselineModel  string     // User's intended model; never mutated by per-prompt overrides
	overrideActive bool       // True when active session model differs from baselineModel

	// Last queued-send failure — set by queueRecordErrorEvent, read by parent wait loop.
	queueErrMu         sync.Mutex
	lastQueueSendError string
	lastQueueSendErrAt time.Time
}

// activeUIPrompt holds the state for a pending UI prompt from an MCP tool.
type activeUIPrompt struct {
	request    UIPromptRequest
	responseCh chan UIPromptResponse
	cancelFn   context.CancelFunc
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID      string
	ACPCommand       string
	ACPCwd           string            // Working directory for the ACP process (not the session working dir)
	Env              map[string]string // Server-specific env vars from settings.json (acp_servers[].env)
	ACPServer        string
	ACPSessionID     string // ACP-assigned session ID for resumption (optional)
	WorkingDir       string
	AutoApprove      bool
	Logger           *slog.Logger
	Store            *session.Store
	SessionName      string
	ProcessorManager *processors.Manager // Unified processor pipeline (text-mode + command-mode)
	QueueConfig      *config.QueueConfig // Queue processing configuration
	Runner           *runner.Runner      // Optional restricted runner for sandboxed execution

	ActionButtonsConfig *config.ActionButtonsConfig // Action buttons configuration
	FileLinksConfig     *config.FileLinksConfig     // File path linking configuration
	APIPrefix           string                      // URL prefix for API endpoints (for HTTP file links)
	WorkspaceUUID       string                      // Workspace UUID for secure file links

	// MittoConfig is the full Mitto configuration (used for default flags)
	MittoConfig *config.Config

	// AvailableACPServers is the pre-computed list of ACP servers that have workspaces
	// configured for the session's working directory. Populated by SessionManager using
	// the same logic as the mitto_conversation_get_current MCP tool.
	AvailableACPServers []processors.AvailableACPServer

	// OnStreamingStateChanged is called when the session's streaming state changes.
	// It's called with true when streaming starts (user sends prompt) and false when it ends.
	OnStreamingStateChanged func(sessionID string, isStreaming bool)

	// OnUIPromptStateChanged is called when a blocking UI prompt starts or ends.
	OnUIPromptStateChanged func(sessionID string, isWaiting bool)

	// OnUIPromptTimeout is called when a blocking UI prompt times out with no active viewer.
	// Used to trigger a native OS notification so the user knows the session needed input.
	OnUIPromptTimeout func(sessionID string, req UIPromptRequest, sessionName string)

	// OnPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	OnPlanStateChanged func(sessionID string, entries []PlanEntry)

	// OnConfigOptionChanged is called when any session config option changes.
	// Used to broadcast config changes to all connected clients.
	// The configID identifies which option changed, and value is the new value.
	OnConfigOptionChanged func(sessionID string, configID, value string)

	// OnTitleGenerated is called when a title is auto-generated for this session.
	// Used to broadcast session_renamed events to all connected clients.
	OnTitleGenerated func(sessionID, title string)

	// OnSelfDestruct is called after a turn completes when the agent has requested
	// deletion of its own conversation via the mitto_conversation_delete MCP tool.
	// It should permanently delete the conversation (and its children).
	OnSelfDestruct func(sessionID string)

	// OnTurnIdle is called after a turn completes and the session is fully idle.
	// Used to drive event-driven on-completion periodic firing via the runner.
	OnTurnIdle func(sessionID string)

	// GlobalMCPServer is the global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	// If nil, per-session MCP server is used as fallback (legacy behavior).
	GlobalMCPServer *mcpserver.Server

	// AuxiliaryManager is the workspace-scoped auxiliary manager for title generation,
	// follow-up analysis, and other auxiliary tasks.
	AuxiliaryManager *auxiliary.WorkspaceAuxiliaryManager

	// SharedProcess is the shared ACP process for this workspace (nil = legacy per-session process).
	SharedProcess SharedProcess

	// PruneConfig is the pruning configuration for the session recorder.
	// When set, the recorder automatically prunes old events after each recording
	// to keep the session within the configured limits (max messages, max size).
	PruneConfig *session.PruneConfig

	// PromptResolver resolves a named workspace prompt to its full text at send time.
	// When set, PromptMeta.PromptName is resolved via this function in PromptWithMeta.
	PromptResolver PromptResolver

	// PreferredModelsResolver resolves a named workspace prompt to its preferredModels list.
	// When set and PromptMeta.PreferredModels is empty, the list is resolved from the
	// prompt name in PromptWithMeta before the per-prompt model-switching logic runs.
	PreferredModelsResolver func(name, workingDir string) []string

	// IsChildPrompting checks if a child session's agent is currently responding.
	// Used to populate children.promptingCount in the CEL context for enabledWhen.
	IsChildPrompting func(childSessionID string) bool

	// CreationCtx is the context for the initial ACP session creation RPC (NewSession).
	// If nil or missing a deadline, a default sessionCreationRPCTimeout is applied.
	// This context is NOT used for the session's lifetime — only for the blocking
	// NewSession RPC call in startSharedACPSession / resumeSharedACPSession.
	// Pass r.Context() from HTTP handlers so that the 30s request-timeout middleware
	// can cancel the RPC and free the goroutine if the agent is busy.
	CreationCtx context.Context
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
// NewMinimalBackgroundSession creates a BackgroundSession with only the session identity
// fields set. This is intended for tests that need a BackgroundSession in the sessions
// map without starting an ACP process.
func NewMinimalBackgroundSession(sessionID, workingDir, workspaceUUID string) *BackgroundSession {
	return &BackgroundSession{
		persistedID:   sessionID,
		workingDir:    workingDir,
		workspaceUUID: workspaceUUID,
	}
}

// NewMinimalBackgroundSessionPrompting creates a BackgroundSession that reports itself
// as prompting (or not). Intended for tests that check prompting-state guards.
func NewMinimalBackgroundSessionPrompting(sessionID string, prompting bool) *BackgroundSession {
	return &BackgroundSession{
		persistedID: sessionID,
		isPrompting: prompting,
	}
}

// NewTestBackgroundSessionWithCtx creates a BackgroundSession with a context and
// a properly initialized promptCond. Intended for tests that exercise Close/archive flows.
func NewTestBackgroundSessionWithCtx(sessionID string, ctx context.Context, cancel context.CancelFunc) *BackgroundSession {
	bs := &BackgroundSession{
		persistedID: sessionID,
		ctx:         ctx,
		cancel:      cancel,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	return bs
}

// NewTestBackgroundSessionPromptingWithCtx creates a BackgroundSession with a prompting
// state, context, and initialized promptCond.
func NewTestBackgroundSessionPromptingWithCtx(sessionID string, prompting bool, ctx context.Context, cancel context.CancelFunc) *BackgroundSession {
	bs := &BackgroundSession{
		persistedID: sessionID,
		isPrompting: prompting,
		ctx:         ctx,
		cancel:      cancel,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	return bs
}

// SimulatePromptComplete atomically clears the isPrompting flag and broadcasts on
// promptCond, simulating what happens when an ACP prompt response completes.
// Intended for use in tests that need to unblock WaitForResponseComplete.
func (bs *BackgroundSession) SimulatePromptComplete() {
	bs.promptMu.Lock()
	bs.isPrompting = false
	if bs.promptCond != nil {
		bs.promptCond.Broadcast()
	}
	bs.promptMu.Unlock()
}

// SimulateClose marks the session as closed (sets the closed atomic to 1).
// Intended for tests that check IsClosed / ActiveSessionCount behavior.
func (bs *BackgroundSession) SimulateClose() {
	bs.closed.Store(1)
}

// BackgroundSessionTestOpts carries optional fields for NewTestBackgroundSession.
// Only set the fields your test needs; zero values are used for the rest.
type BackgroundSessionTestOpts struct {
	SessionID      string
	WorkingDir     string
	WorkspaceUUID  string
	ACPID          string
	IsPrompting    bool
	NextSeq        int64
	Store          *session.Store
	PromptResolver PromptResolver
}

// NewTestBackgroundSession creates a BackgroundSession from test options.
// Use this for tests that need to set multiple private fields.
func NewTestBackgroundSession(opts BackgroundSessionTestOpts) *BackgroundSession {
	bs := &BackgroundSession{
		persistedID:    opts.SessionID,
		workingDir:     opts.WorkingDir,
		workspaceUUID:  opts.WorkspaceUUID,
		acpID:          opts.ACPID,
		isPrompting:    opts.IsPrompting,
		nextSeq:        opts.NextSeq,
		store:          opts.Store,
		promptResolver: opts.PromptResolver,
	}
	return bs
}

func NewBackgroundSession(cfg BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:                     ctx,
		cancel:                  cancel,
		startedAt:               time.Now(),
		autoApprove:             cfg.AutoApprove,
		logger:                  cfg.Logger,
		observers:               make(map[SessionObserver]struct{}),
		processorManager:        cfg.ProcessorManager,
		workingDir:              cfg.WorkingDir,
		isFirstPrompt:           true, // New session starts with first prompt pending
		queueConfig:             cfg.QueueConfig,
		actionButtonsConfig:     cfg.ActionButtonsConfig,
		fileLinksConfig:         cfg.FileLinksConfig,
		apiPrefix:               cfg.APIPrefix,
		workspaceUUID:           cfg.WorkspaceUUID,
		runner:                  cfg.Runner,
		onStreamingStateChanged: cfg.OnStreamingStateChanged,
		onUIPromptStateChanged:  cfg.OnUIPromptStateChanged,
		onUIPromptTimeout:       cfg.OnUIPromptTimeout,
		onPlanStateChanged:      cfg.OnPlanStateChanged,
		onConfigChanged:         cfg.OnConfigOptionChanged,
		onTitleGenerated:        cfg.OnTitleGenerated,
		onSelfDestruct:          cfg.OnSelfDestruct,
		onTurnIdle:              cfg.OnTurnIdle,
		acpCommand:              cfg.ACPCommand,              // Store for restart
		acpCwd:                  cfg.ACPCwd,                  // Store for restart
		serverEnv:               cfg.Env,                     // Store for restart
		globalMcpServer:         cfg.GlobalMCPServer,         // Global MCP server for session registration
		auxiliaryManager:        cfg.AuxiliaryManager,        // Workspace-scoped auxiliary manager
		availableACPServers:     cfg.AvailableACPServers,     // Pre-computed workspace server list
		promptResolver:          cfg.PromptResolver,          // Named prompt resolver (resolves name → text at send time)
		preferredModelsResolver: cfg.PreferredModelsResolver, // Named prompt resolver (resolves name → preferredModels)
		isChildPrompting:        cfg.IsChildPrompting,        // Callback to check if a child session is prompting
		creationCtx:             cfg.CreationCtx,             // Context for initial ACP session creation RPC only
	}

	// Look up ACP server constraints from config
	bs.acpServerConstraints = lookupACPServerConstraints(cfg.MittoConfig, cfg.ACPServer)

	// Wire prompt-mode processor execution to auxiliary sessions
	if bs.processorManager != nil && bs.auxiliaryManager != nil {
		bs.processorManager.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error {
			return bs.auxiliaryManager.PromptProcessorAsync(ctx, workspaceUUID, processorName, prompt)
		})
	}

	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Initialize the deferred-config store
	bs.pendingConfig = make(map[string]string)

	// Initialize activity timestamp
	bs.lastActivityAt.Store(time.Now().UnixNano())

	// Create recorder for persistence. Honor a caller-supplied PersistedID so the
	// session can reuse an ID that was pre-generated before construction;
	// otherwise generate a fresh ID.
	if cfg.Store != nil {
		if cfg.PersistedID != "" {
			bs.recorder = session.NewRecorderWithID(cfg.Store, cfg.PersistedID)
		} else {
			bs.recorder = session.NewRecorder(cfg.Store)
		}
		bs.persistedID = bs.recorder.SessionID()
		bs.store = cfg.Store
		// Set pruning configuration so the recorder auto-prunes after each event
		if cfg.PruneConfig != nil {
			bs.recorder.SetPruneConfig(cfg.PruneConfig)
		}
		if err := bs.recorder.Start(cfg.ACPServer, cfg.WorkingDir, cfg.WorkspaceUUID); err != nil {
			cancel()
			return nil, err
		}
		// Update session name, runner info, and default flags
		runnerType := "exec"
		isRestricted := false
		if cfg.Runner != nil {
			runnerType = cfg.Runner.Type()
			isRestricted = cfg.Runner.IsRestricted()
		}
		cfg.Store.UpdateMetadata(bs.persistedID, func(meta *session.Metadata) {
			if cfg.SessionName != "" {
				meta.Name = cfg.SessionName
			}
			meta.RunnerType = runnerType
			meta.RunnerRestricted = isRestricted

			// Initialize AdvancedSettings with configured default flags
			// Only apply defaults for new sessions (not resumed sessions)
			if meta.AdvancedSettings == nil {
				meta.AdvancedSettings = make(map[string]bool)
			}

			// Apply configured default flags from config
			if cfg.MittoConfig != nil && cfg.MittoConfig.Conversations != nil {
				for flagName, flagValue := range cfg.MittoConfig.Conversations.DefaultFlags {
					// Only set the flag if it's not already set (preserve existing values)
					if _, exists := meta.AdvancedSettings[flagName]; !exists {
						meta.AdvancedSettings[flagName] = flagValue
					}
				}
			}

			// Apply compile-time defaults for flags not explicitly configured
			// This ensures all flags have a value (either from config or compile-time default)
			for _, flagDef := range session.AvailableFlags {
				if _, exists := meta.AdvancedSettings[flagDef.Name]; !exists {
					// Only set if the compile-time default is true (false is the zero value)
					if flagDef.Default {
						meta.AdvancedSettings[flagDef.Name] = true
					}
				}
			}
		})

		// Initialize nextSeq from MaxSeq if available, otherwise from EventCount
		// MaxSeq tracks the highest seq persisted, which may be higher than EventCount
		// if events were assigned seq at streaming time but not all were persisted
		maxSeq := bs.recorder.MaxSeq()
		eventCount := int64(bs.recorder.EventCount())
		if maxSeq > eventCount {
			bs.nextSeq = maxSeq + 1
		} else {
			bs.nextSeq = eventCount + 1
		}

		// Create session-scoped logger with context
		if bs.logger != nil {
			bs.logger = logging.WithSessionContext(bs.logger, bs.persistedID, cfg.WorkingDir, cfg.ACPServer)
		}
	} else {
		// No store - initialize nextSeq to 1 to prevent seq=0 errors
		bs.nextSeq = 1
	}

	// Log runner information
	if bs.logger != nil {
		runnerType := "exec"
		isRestricted := false
		if cfg.Runner != nil {
			runnerType = cfg.Runner.Type()
			isRestricted = cfg.Runner.IsRestricted()
		}
		bs.logger.Debug("session created",
			"session_id", bs.persistedID,
			"workspace", cfg.WorkingDir,
			"acp_server", cfg.ACPServer,
			"runner_type", runnerType,
			"runner_restricted", isRestricted)
	}

	// Use shared process if available, otherwise start a new per-session process.
	// For shared sessions, defer the session/new RPC to the first prompt so that
	// creating a conversation never blocks on a busy agent process.
	if cfg.SharedProcess != nil {
		if err := bs.prepareSharedACPSession(cfg.SharedProcess, cfg.WorkingDir); err != nil {
			cancel()
			if bs.recorder != nil {
				bs.recorder.End(session.SessionEndData{Reason: "failed_to_start"})
			}
			return nil, err
		}
	} else {
		// Start ACP process (no ACP session ID for new sessions)
		if err := bs.startACPProcess(cfg.ACPCommand, cfg.ACPCwd, cfg.WorkingDir, ""); err != nil {
			cancel()
			if bs.recorder != nil {
				bs.recorder.End(session.SessionEndData{Reason: "failed_to_start"})
			}
			return nil, err
		}
	}

	// Store the ACP session ID in metadata for future resumption
	if cfg.Store != nil && bs.acpID != "" {
		if err := cfg.Store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to store ACP session ID in metadata", "error", err)
		}
	}

	return bs, nil
}

// ResumeBackgroundSession creates a background session for an existing persisted session.
// This is used when switching to an old conversation - we create a new ACP connection
// but continue recording to the existing session.
// If the agent supports session loading and we have a stored ACP session ID, we attempt
// to resume the ACP session on the server side as well.
func ResumeBackgroundSession(config BackgroundSessionConfig) (*BackgroundSession, error) {
	if config.PersistedID == "" {
		return nil, &sessionError{"persisted session ID is required for resume"}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create session-scoped logger with context
	sessionLogger := config.Logger
	if sessionLogger != nil {
		sessionLogger = logging.WithSessionContext(sessionLogger, config.PersistedID, config.WorkingDir, config.ACPServer)
	}

	bs := &BackgroundSession{
		persistedID:             config.PersistedID,
		ctx:                     ctx,
		cancel:                  cancel,
		startedAt:               time.Now(),
		autoApprove:             config.AutoApprove,
		logger:                  sessionLogger,
		observers:               make(map[SessionObserver]struct{}),
		isResumed:               true, // Mark as resumed session
		store:                   config.Store,
		processorManager:        config.ProcessorManager,
		workingDir:              config.WorkingDir,
		isFirstPrompt:           true, // Treat first prompt after resume as "first" for processors (re-inject context)
		queueConfig:             config.QueueConfig,
		actionButtonsConfig:     config.ActionButtonsConfig,
		fileLinksConfig:         config.FileLinksConfig,
		apiPrefix:               config.APIPrefix,
		workspaceUUID:           config.WorkspaceUUID,
		runner:                  config.Runner,
		onStreamingStateChanged: config.OnStreamingStateChanged,
		onUIPromptStateChanged:  config.OnUIPromptStateChanged,
		onUIPromptTimeout:       config.OnUIPromptTimeout,
		onPlanStateChanged:      config.OnPlanStateChanged,
		onConfigChanged:         config.OnConfigOptionChanged,
		onTitleGenerated:        config.OnTitleGenerated,
		onSelfDestruct:          config.OnSelfDestruct,
		acpCommand:              config.ACPCommand,              // Store for restart
		acpCwd:                  config.ACPCwd,                  // Store for restart
		serverEnv:               config.Env,                     // Store for restart
		globalMcpServer:         config.GlobalMCPServer,         // Global MCP server for session registration
		auxiliaryManager:        config.AuxiliaryManager,        // Workspace-scoped auxiliary manager
		availableACPServers:     config.AvailableACPServers,     // Pre-computed workspace server list
		promptResolver:          config.PromptResolver,          // Named prompt resolver (resolves name → text at send time)
		preferredModelsResolver: config.PreferredModelsResolver, // Named prompt resolver (resolves name → preferredModels)
		isChildPrompting:        config.IsChildPrompting,        // Callback to check if a child session is prompting
		creationCtx:             config.CreationCtx,             // Context for initial ACP session creation RPC only
	}

	// Look up ACP server constraints from config
	bs.acpServerConstraints = lookupACPServerConstraints(config.MittoConfig, config.ACPServer)

	// Wire prompt-mode processor execution to auxiliary sessions
	if bs.processorManager != nil && bs.auxiliaryManager != nil {
		bs.processorManager.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error {
			return bs.auxiliaryManager.PromptProcessorAsync(ctx, workspaceUUID, processorName, prompt)
		})
	}

	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Initialize the deferred-config store
	bs.pendingConfig = make(map[string]string)

	// Initialize activity timestamp
	bs.lastActivityAt.Store(time.Now().UnixNano())

	// Resume recorder for the existing session
	if config.Store != nil {
		bs.recorder = session.NewRecorderWithID(config.Store, config.PersistedID)
		// Set pruning configuration so the recorder auto-prunes after each event
		if config.PruneConfig != nil {
			bs.recorder.SetPruneConfig(config.PruneConfig)
		}
		if err := bs.recorder.Resume(); err != nil {
			cancel()
			return nil, &sessionError{"failed to resume session recording: " + err.Error()}
		}
		// Update the metadata to mark it as active again and store runner info
		runnerType := "exec"
		isRestricted := false
		if config.Runner != nil {
			runnerType = config.Runner.Type()
			isRestricted = config.Runner.IsRestricted()
		}
		if err := config.Store.UpdateMetadata(config.PersistedID, func(m *session.Metadata) {
			m.Status = "active"
			m.RunnerType = runnerType
			m.RunnerRestricted = isRestricted
		}); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to update session status", "error", err)
		}

		// Initialize nextSeq from MaxSeq if available, otherwise from EventCount
		// MaxSeq tracks the highest seq persisted, which may be higher than EventCount
		// if events were assigned seq at streaming time but not all were persisted
		maxSeq := bs.recorder.MaxSeq()
		eventCount := int64(bs.recorder.EventCount())
		if maxSeq > eventCount {
			bs.nextSeq = maxSeq + 1
		} else {
			bs.nextSeq = eventCount + 1
		}

		// Restore processor activation stats from persisted metadata
		if bs.processorManager != nil {
			if meta, err := config.Store.GetMetadata(config.PersistedID); err == nil {
				bs.processorManager.SetStats(meta.ProcessorActivations, meta.ProcessorLastActivation)
			}
		}
	} else {
		// No store - initialize nextSeq to 1 to prevent seq=0 errors
		bs.nextSeq = 1
	}

	// Log runner information
	if bs.logger != nil {
		runnerType := "exec"
		isRestricted := false
		if config.Runner != nil {
			runnerType = config.Runner.Type()
			isRestricted = config.Runner.IsRestricted()
		}
		bs.logger.Debug("session resumed",
			"session_id", config.PersistedID,
			"workspace", config.WorkingDir,
			"acp_server", config.ACPServer,
			"runner_type", runnerType,
			"runner_restricted", isRestricted)
	}

	// Use shared process if available, otherwise start a new per-session process.
	if config.SharedProcess != nil {
		if err := bs.resumeSharedACPSession(config.SharedProcess, config.WorkingDir, config.ACPSessionID); err != nil {
			// Auto-restart the shared process if we hit a pipe/connection error.
			// This happens when the OS killed the ACP subprocess during app backgrounding
			// (sleep/screen-lock) and the app has now resumed. The Go conn object is still
			// non-nil but the underlying pipes are dead, so the first resume attempt fails
			// with "broken pipe" or "file already closed".
			// We detect this, restart the shared OS process, and retry once — matching the
			// same auto-recovery pattern used by PromptWithMeta and the streaming loop.
			if IsACPConnectionError(err) && bs.canRestartACP() {
				if bs.logger != nil {
					bs.logger.Info("Shared ACP process appears dead on resume, restarting",
						"session_id", bs.persistedID,
						"error", err)
				}
				bs.recordRestart(RestartReasonResumeFailure)

				// Restart the shared OS process. SharedACPProcess.Restart() is rate-limited
				// and idempotent — if another session already triggered a restart, this
				// returns the already-restarted process without starting another one.
				if restartErr := config.SharedProcess.Restart(); restartErr != nil {
					if bs.logger != nil {
						bs.logger.Warn("Failed to restart shared ACP process on resume",
							"session_id", bs.persistedID,
							"error", restartErr)
					}
					cancel()
					if bs.recorder != nil {
						bs.recorder.Suspend()
					}
					return nil, fmt.Errorf("ACP process restart failed on resume: %w", restartErr)
				}

				// Retry session creation on the now-running process.
				if err = bs.resumeSharedACPSession(config.SharedProcess, config.WorkingDir, config.ACPSessionID); err != nil {
					cancel()
					if bs.recorder != nil {
						bs.recorder.Suspend()
					}
					return nil, err
				}
			} else {
				cancel()
				if bs.recorder != nil {
					bs.recorder.Suspend()
				}
				return nil, err
			}
		}
	} else {
		// Start ACP process, passing the ACP session ID for potential resumption
		if err := bs.startACPProcess(config.ACPCommand, config.ACPCwd, config.WorkingDir, config.ACPSessionID); err != nil {
			cancel()
			if bs.recorder != nil {
				bs.recorder.Suspend()
			}
			return nil, err
		}
	}

	// If we created a new ACP session (different from the one we tried to resume),
	// update the metadata with the new ACP session ID
	if config.Store != nil && bs.acpID != "" && bs.acpID != config.ACPSessionID {
		if err := config.Store.UpdateMetadata(config.PersistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to update ACP session ID in metadata", "error", err)
		}
	}

	return bs, nil
}

// GetSessionID returns the persisted session ID.
func (bs *BackgroundSession) GetSessionID() string {
	return bs.persistedID
}

// GetWorkingDir returns the working directory for this session.
func (bs *BackgroundSession) GetWorkingDir() string {
	return bs.workingDir
}

// GetACPID returns the ACP protocol session ID.
func (bs *BackgroundSession) GetACPID() string {
	return bs.acpID
}

// StartedAt returns when this session was started or resumed.
// Used by the GC to apply a grace period to freshly started sessions.
func (bs *BackgroundSession) StartedAt() time.Time {
	return bs.startedAt
}

// IsClosed returns true if the session has been closed.
func (bs *BackgroundSession) IsClosed() bool {
	return bs.closed.Load() != 0
}

// RequestSelfDestruct marks this conversation for deletion once the current
// turn completes. It is invoked by the mitto_conversation_delete MCP handler
// when the agent requests deletion of its own conversation. The deletion is
// deferred (rather than performed immediately) so the agent's in-flight tool
// call can return cleanly before the conversation and its ACP connection are
// torn down.
func (bs *BackgroundSession) RequestSelfDestruct() {
	bs.selfDestructRequested.Store(true)
}

// IsSelfDestructRequested returns true if the agent has requested deletion of
// its own conversation during the current turn.
func (bs *BackgroundSession) IsSelfDestructRequested() bool {
	return bs.selfDestructRequested.Load()
}

// HasParent returns true if this session was spawned by another session (has a parent).
// Used by the GC to apply ChildIdleTimeout instead of IdleTimeout for faster child GC.
func (bs *BackgroundSession) HasParent() bool {
	if bs.store == nil || bs.persistedID == "" {
		return false
	}
	meta, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		return false
	}
	return meta.ParentSessionID != ""
}

// IsACPReady returns true if the ACP connection is initialized and ready for prompts.
// This returns false while the session is starting up (ACP process not yet initialized)
// or after the session has been closed.
func (bs *BackgroundSession) IsACPReady() bool {
	if bs.IsClosed() {
		return false
	}
	return bs.acpConn != nil || bs.sharedProcess != nil
}

// IsPrompting returns true if a prompt is currently being processed.
func (bs *BackgroundSession) IsPrompting() bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.isPrompting
}

// GetPromptCount returns the number of prompts sent in this session.
func (bs *BackgroundSession) GetPromptCount() int {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.promptCount
}

// GetLastResponseCompleteTime returns when the agent last completed a response.
// Returns zero time if no response has completed yet.
func (bs *BackgroundSession) GetLastResponseCompleteTime() time.Time {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.lastResponseComplete
}

// setQueuedDeliveryInProgress sets or clears the queuedDeliveryInProgress flag under promptMu.
func (bs *BackgroundSession) setQueuedDeliveryInProgress(v bool) {
	bs.promptMu.Lock()
	bs.queuedDeliveryInProgress = v
	bs.promptMu.Unlock()
}

// HasQueuedDeliveryInProgress returns true if a queued message has been popped and is in the
// process of being delivered (e.g. sleeping through a configured delay). The session appears
// idle during this window even though it will become prompting shortly.
func (bs *BackgroundSession) HasQueuedDeliveryInProgress() bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.queuedDeliveryInProgress
}

// WaitForResponseComplete waits for the current prompt to complete, if one is in progress.
// Returns true if the prompt completed within the timeout, false if it timed out.
// If no prompt is in progress, returns immediately with true.
func (bs *BackgroundSession) WaitForResponseComplete(timeout time.Duration) bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()

	// If not prompting, return immediately
	if !bs.isPrompting {
		return true
	}

	// Create a timer for the timeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Use a goroutine to wait on the condition with timeout
	done := make(chan bool, 1)
	go func() {
		bs.promptMu.Lock()
		for bs.isPrompting && bs.closed.Load() == 0 {
			bs.promptCond.Wait()
		}
		completed := !bs.isPrompting || bs.closed.Load() != 0
		bs.promptMu.Unlock()
		done <- completed
	}()

	// Release the lock while waiting (the goroutine will re-acquire it)
	bs.promptMu.Unlock()

	select {
	case completed := <-done:
		bs.promptMu.Lock() // Re-acquire to satisfy defer
		return completed
	case <-timer.C:
		bs.promptMu.Lock() // Re-acquire to satisfy defer
		return false
	}
}

// GetEventCount returns the current event count for the session.
// Returns 0 if the recorder is not available or there's an error.
func (bs *BackgroundSession) GetEventCount() int {
	if bs.recorder == nil {
		return 0
	}
	return bs.recorder.EventCount()
}

// getNextSeq returns the next sequence number and increments the counter.
// This is used to assign sequence numbers to streaming events.
func (bs *BackgroundSession) getNextSeq() int64 {
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()

	// Defensive check: ensure nextSeq is at least 1
	// This can happen if Store was nil during session creation
	if bs.nextSeq < 1 {
		bs.nextSeq = 1
		if bs.logger != nil {
			bs.logger.Warn("nextSeq was < 1, reset to 1",
				"session_id", bs.persistedID)
		}
	}

	seq := bs.nextSeq
	bs.nextSeq++

	// L1: Structured logging for seq assignment
	if bs.logger != nil {
		bs.logger.Debug("seq_assigned",
			"seq", seq,
			"next_seq", bs.nextSeq,
			"session_id", bs.persistedID)
	}

	return seq
}

// GetNextSeq implements the SeqProvider interface.
// It returns the next sequence number for event ordering.
func (bs *BackgroundSession) GetNextSeq() int64 {
	return bs.getNextSeq()
}

// refreshNextSeq updates nextSeq from the current persisted max sequence number.
// It is monotonic: nextSeq is never lowered below its current value.
//
// IMPORTANT: Uses MaxSeq (highest seq persisted) not EventCount (number of events)
// because seq numbers can be sparse due to coalescing (multiple chunks share the same seq).
// Using EventCount would cause seq number reuse after session resume.
func (bs *BackgroundSession) refreshNextSeq() {
	if bs.recorder == nil {
		return
	}
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()

	oldSeq := bs.nextSeq
	maxSeq := bs.recorder.MaxSeq()
	eventCount := int64(bs.recorder.EventCount())

	// Derive store-based candidate: use whichever is higher.
	// Due to coalescing, MaxSeq can be much higher than EventCount.
	var candidate int64
	if maxSeq > eventCount {
		candidate = maxSeq + 1
	} else {
		candidate = eventCount + 1
	}

	// Monotonic: never lower nextSeq below what has already been handed out.
	if candidate > bs.nextSeq {
		bs.nextSeq = candidate
	}

	// L1: Log seq refresh only when it changes
	if bs.logger != nil && oldSeq != bs.nextSeq {
		bs.logger.Debug("seq_refreshed",
			"old_next_seq", oldSeq,
			"new_next_seq", bs.nextSeq,
			"max_seq", maxSeq,
			"event_count", eventCount,
			"session_id", bs.persistedID)
	}
}

// CreatedAt returns when the session was created.
func (bs *BackgroundSession) CreatedAt() time.Time {
	if bs.recorder != nil {
		// This would need to be stored; for now return zero
		return time.Time{}
	}
	return time.Time{}
}

// GetQueueConfig returns the queue configuration for this session.
// May return nil if no queue config is set (use defaults in that case).
func (bs *BackgroundSession) GetQueueConfig() *config.QueueConfig {
	return bs.queueConfig
}

// AgentSupportsImages returns whether the ACP agent advertised image prompt support.
// This is determined during agent initialization from AgentCapabilities.PromptCapabilities.Image.
func (bs *BackgroundSession) AgentSupportsImages() bool {
	return bs.agentSupportsImages
}

// AgentModels returns the agent's model state (available models and current model).
// This is from the UNSTABLE SessionModelState API and may be nil if the agent doesn't support it.
func (bs *BackgroundSession) AgentModels() *acp.UnstableSessionModelState {
	return bs.agentModels
}

// --- Observer Management ---

// AddObserver adds an observer to receive session events.
// Multiple observers can be added to the same session.
// If the session is currently streaming, the observer will receive all buffered
// events (thoughts, messages, tool calls, file operations) in the order they occurred.
// If there are cached action buttons, they will be sent to the new observer.
func (bs *BackgroundSession) AddObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	if bs.observers == nil {
		bs.observers = make(map[SessionObserver]struct{})
	}
	bs.observers[observer] = struct{}{}
	observerCount := len(bs.observers)
	bs.observersMu.Unlock()

	bs.TouchActivity()

	if bs.logger != nil {
		bs.logger.Debug("Observer added", "observer_count", observerCount)
	}

	// If the session is currently prompting, replay all buffered events to the new observer
	// so they can catch up with what's been streamed since the last persisted event.
	bs.promptMu.Lock()
	isPrompting := bs.isPrompting
	bs.promptMu.Unlock()

	// If not prompting, send cached action buttons to the new observer
	// This ensures new clients see the follow-up suggestions
	// Note: Events are persisted immediately, so clients can get them from the store
	// via the load_events WebSocket message. No need to replay buffered events.
	if !isPrompting {
		bs.sendCachedActionButtonsTo(observer)
	}
}

// GetMaxAssignedSeq returns the highest sequence number that has been assigned
// to any event in this session. This is nextSeq - 1, since nextSeq is the NEXT
// seq to be assigned.
//
// This is used for keepalive reporting and to determine the server's max seq
// for client synchronization.
//
// Returns 0 if no events have been assigned yet.
func (bs *BackgroundSession) GetMaxAssignedSeq() int64 {
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()
	if bs.nextSeq <= 1 {
		return 0
	}
	return bs.nextSeq - 1
}

// RemoveObserver removes an observer from the session.
func (bs *BackgroundSession) RemoveObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	defer bs.observersMu.Unlock()
	delete(bs.observers, observer)
	remaining := len(bs.observers)
	if remaining == 0 {
		bs.lastObserverRemovedAt.Store(time.Now().UnixNano())
	}
	if bs.logger != nil {
		bs.logger.Debug("Observer removed", "observer_count", remaining)
	}
}

// LastObserverRemovedAt returns the time when the observer count last dropped to zero.
// Returns zero time if observers have never been fully removed.
func (bs *BackgroundSession) LastObserverRemovedAt() time.Time {
	nanos := bs.lastObserverRemovedAt.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// ObserverCount returns the number of attached observers.
func (bs *BackgroundSession) ObserverCount() int {
	bs.observersMu.RLock()
	defer bs.observersMu.RUnlock()
	return len(bs.observers)
}

// AddConnectedClient increments the connected WebSocket client counter.
// Called when a client establishes a WebSocket connection, before load_events.
func (bs *BackgroundSession) AddConnectedClient() {
	count := bs.connectedClients.Add(1)
	if bs.logger != nil {
		bs.logger.Debug("Connected client added", "connected_client_count", count)
	}
}

// RemoveConnectedClient decrements the connected WebSocket client counter.
// Called when a WebSocket client disconnects (readPump exits).
func (bs *BackgroundSession) RemoveConnectedClient() {
	count := bs.connectedClients.Add(-1)
	if bs.logger != nil {
		bs.logger.Debug("Connected client removed", "connected_client_count", count)
	}
}

// HasConnectedClients returns true if any WebSocket clients are currently connected.
func (bs *BackgroundSession) HasConnectedClients() bool {
	return bs.connectedClients.Load() > 0
}

// ConnectedClientCount returns the number of currently connected WebSocket clients.
func (bs *BackgroundSession) ConnectedClientCount() int {
	return int(bs.connectedClients.Load())
}

// TouchActivity records the current time as the session's last activity timestamp.
// Called on observer add and at the start of each prompt.
func (bs *BackgroundSession) TouchActivity() {
	bs.lastActivityAt.Store(time.Now().UnixNano())
}

// LastActivityAt returns the time of the most recent session activity.
// Returns zero time if lastActivityAt was never set (should not happen after construction).
func (bs *BackgroundSession) LastActivityAt() time.Time {
	nanos := bs.lastActivityAt.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// HasObservers returns true if any observers are attached.
func (bs *BackgroundSession) HasObservers() bool {
	return bs.ObserverCount() > 0
}

// notifyObservers calls a function on all observers.
func (bs *BackgroundSession) notifyObservers(fn func(SessionObserver)) {
	bs.observersMu.RLock()
	defer bs.observersMu.RUnlock()
	observerCount := len(bs.observers)
	if observerCount > 1 && bs.logger != nil {
		bs.logger.Debug("Notifying multiple observers", "count", observerCount)
	}
	for observer := range bs.observers {
		fn(observer)
	}
}

// Close shuts down the session and releases all resources.
func (bs *BackgroundSession) Close(reason string) {
	if !bs.closed.CompareAndSwap(0, 1) {
		return // Already closed
	}

	// Notify all observers that the ACP connection is being stopped.
	// This must happen BEFORE we cancel the context or close resources,
	// so observers can update their state and prevent further prompts.
	// This fixes a race condition where a prompt could be sent while
	// the session is being archived.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStopped(reason)
	})

	// Cancel context to stop any ongoing operations
	bs.cancel()

	// Stop session MCP server (must happen before killing ACP process)
	bs.stopSessionMcpServer()

	// Close ACP client
	if bs.acpClient != nil {
		bs.acpClient.Close()
	}

	// Kill ACP process and clean up resources
	bs.killACPProcess()

	// Handle recording based on close reason
	if bs.recorder != nil {
		// For server_shutdown, use Suspend() instead of End() to avoid
		// recording multiple session_end events when the session is resumed
		// after server restart. The session can be resumed later, so we
		// don't want to mark it as permanently ended.
		if reason == "server_shutdown" || reason == "acp_server_reconfigured" {
			bs.recorder.Suspend()
		} else {
			// Build session end data with context about the session state
			endData := session.SessionEndData{
				Reason:       reason,
				WasPrompting: bs.IsPrompting(),
				ACPConnected: bs.acpClient != nil,
			}

			// Extract signal from reason if present (e.g., "signal:SIGINT")
			if strings.HasPrefix(reason, "signal:") {
				endData.Signal = strings.TrimPrefix(reason, "signal:")
				endData.Reason = "server_shutdown"
			}

			// Get event count from store if available
			if bs.store != nil {
				if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
					endData.EventCount = meta.EventCount
				}
			}

			bs.recorder.End(endData)
		}
	}
}

// Suspend suspends the session without ending it (for temporary disconnects).
func (bs *BackgroundSession) Suspend() {
	// Suspend recording (keeps status as "active")
	if bs.recorder != nil {
		bs.recorder.Suspend()
	}
}

// registerWithGlobalMCP registers this session with the global MCP server.
// This enables session-scoped MCP tools to be called by the agent.
// The MCP server is configured globally (e.g., in ~/.augment/settings.json),
// so we don't pass McpServers to the ACP session.
func (bs *BackgroundSession) registerWithGlobalMCP(store *session.Store) {
	if bs.globalMcpServer == nil {
		return // No global MCP server - fall back to per-session server
	}

	// Register with global MCP server
	// BackgroundSession implements mcpserver.UIPrompter
	if err := bs.globalMcpServer.RegisterSession(bs.persistedID, bs, bs.logger); err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to register session with global MCP server", "error", err)
		}
		return
	}

	if bs.logger != nil {
		bs.logger.Info("Session registered with global MCP server",
			"session_id", bs.persistedID)
	}
}

// unregisterFromGlobalMCP unregisters this session from the global MCP server.
func (bs *BackgroundSession) unregisterFromGlobalMCP() {
	if bs.globalMcpServer != nil {
		bs.globalMcpServer.UnregisterSession(bs.persistedID)
		if bs.logger != nil {
			bs.logger.Info("Session unregistered from global MCP server",
				"session_id", bs.persistedID)
		}
	}
}

// startSessionMcpServer registers with the global MCP server.
// We don't pass McpServers to ACP - the agent should have the MCP server
// pre-configured globally (e.g., in ~/.augment/settings.json).
// Returns empty McpServers slice.
func (bs *BackgroundSession) startSessionMcpServer(
	store *session.Store,
	agentCapabilities acp.AgentCapabilities,
) []acp.McpServer {
	// Register with the global MCP server
	bs.registerWithGlobalMCP(store)
	// Return empty - MCP is configured globally, not passed per-session
	return []acp.McpServer{}
}

// stopSessionMcpServer unregisters from global MCP server.
func (bs *BackgroundSession) stopSessionMcpServer() {
	bs.unregisterFromGlobalMCP()
}

// GetProcessorStats returns processor statistics for this session.
// Returns: processor count, total pipeline activations, last activation time, last applied processor names.
func (bs *BackgroundSession) GetProcessorStats() (count int, activations int, lastAt time.Time, lastNames []string) {
	if bs.processorManager == nil {
		return 0, 0, time.Time{}, nil
	}
	return bs.processorManager.ProcessorCount(), bs.processorManager.TotalActivations(), bs.processorManager.LastActivationAt(), bs.processorManager.LastAppliedNames()
}

// GetLastUsage returns the last prompt's token usage, or nil if no prompt has completed yet.
// This method is thread-safe.
func (bs *BackgroundSession) GetLastUsage() *acp.Usage {
	bs.lastUsageMu.Lock()
	defer bs.lastUsageMu.Unlock()
	return bs.lastUsage
}

// GetContextUsage returns the last known context window usage.
// Returns (0, 0) if no usage update has been received yet.
// This method is thread-safe.
func (bs *BackgroundSession) GetContextUsage() (size, used int) {
	bs.contextUsageMu.Lock()
	defer bs.contextUsageMu.Unlock()
	return bs.contextSize, bs.contextUsed
}

// sessionError is a simple error type for session errors.
type sessionError struct {
	msg string
}

func (e *sessionError) Error() string {
	return e.msg
}

// GetWorkspaceUUID returns the workspace UUID associated with this session.
func (bs *BackgroundSession) GetWorkspaceUUID() string {
	return bs.workspaceUUID
}

// GetAuxiliaryManager returns the auxiliary manager associated with this session.
func (bs *BackgroundSession) GetAuxiliaryManager() *auxiliary.WorkspaceAuxiliaryManager {
	return bs.auxiliaryManager
}
