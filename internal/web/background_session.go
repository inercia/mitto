package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversion"
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
	promptMu             sync.Mutex
	promptCond           *sync.Cond // Condition variable for waiting on prompt completion
	isPrompting          bool
	promptCount          int
	promptStartTime      time.Time // When the current prompt started (for logging)
	lastResponseComplete time.Time // When the agent last completed a response (for queue delay)

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
	restartCount         int                                    // Total number of restarts across the session lifetime
	restartTimes         []time.Time                            // Timestamps of recent restarts (for rate limiting)
	restartReasons       []RestartReason                        // Reasons for recent restarts (parallel to restartTimes)
	permanentlyFailed    bool                                   // Circuit breaker: true when ACP cannot be restarted (permanent error or lifetime cap hit)
	restartMu            sync.Mutex                             // Protects restart tracking fields (restartCount, restartTimes, restartReasons, permanentlyFailed)

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
	sharedProcess *SharedACPProcess

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
	promptResolver PromptResolverFunc

	// preferredModelsResolver resolves a prompt name to its preferredModels list.
	// Used in PromptWithMeta to auto-select models for named prompts without a
	// PreferredModels field already set in PromptMeta.
	preferredModelsResolver func(name, workingDir string) []string

	// Model preference override tracking (guarded by modelMu).
	modelMu        sync.Mutex // Protects baselineModel and overrideActive
	baselineModel  string     // User's intended model; never mutated by per-prompt overrides
	overrideActive bool       // True when active session model differs from baselineModel
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
	SharedProcess *SharedACPProcess

	// PruneConfig is the pruning configuration for the session recorder.
	// When set, the recorder automatically prunes old events after each recording
	// to keep the session within the configured limits (max messages, max size).
	PruneConfig *session.PruneConfig

	// PromptResolver resolves a named workspace prompt to its full text at send time.
	// When set, PromptMeta.PromptName is resolved via this function in PromptWithMeta.
	PromptResolver PromptResolverFunc

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
			if isACPConnectionError(err) && bs.canRestartACP() {
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

// refreshNextSeq updates nextSeq from the current max sequence number.
// This should be called after events are persisted outside the normal buffer flow
// (e.g., after user prompts are persisted directly).
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

	// Use the higher of MaxSeq or EventCount to determine next seq.
	// MaxSeq tracks the highest seq persisted, while EventCount tracks the number of events.
	// Due to coalescing, MaxSeq can be much higher than EventCount.
	if maxSeq > eventCount {
		bs.nextSeq = maxSeq + 1
	} else {
		bs.nextSeq = eventCount + 1
	}

	// L1: Log seq refresh
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

// logAgentModels logs the agent's model state at DEBUG level.
func (bs *BackgroundSession) logAgentModels(models *acp.UnstableSessionModelState) {
	if bs.logger == nil || models == nil {
		return
	}
	modelNames := make([]string, len(models.AvailableModels))
	for i, m := range models.AvailableModels {
		modelNames[i] = m.Name
	}
	bs.logger.Debug("Agent model state (UNSTABLE)",
		"current_model", string(models.CurrentModelId),
		"available_models", modelNames,
		"model_count", len(models.AvailableModels))
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

// sendCachedActionButtonsTo sends cached action buttons to a single observer.
// Called when a new client connects to ensure they see the current suggestions,
// even if they connected after the suggestions were originally generated.
// This solves the problem of users switching devices or refreshing and missing suggestions.
func (bs *BackgroundSession) sendCachedActionButtonsTo(observer SessionObserver) {
	buttons := bs.GetActionButtons()
	if len(buttons) == 0 {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Sending cached action buttons to new observer", "button_count", len(buttons))
	}

	observer.OnActionButtons(buttons)
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

// buildPromptWithHistory prepends conversation history to the user's message.
// This is used when resuming a session to give the ACP agent context about
// the previous conversation.
func (bs *BackgroundSession) buildPromptWithHistory(message string) string {
	if bs.store == nil {
		return message
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read events for history injection", "error", err)
		}
		return message
	}

	// Build conversation history (limit to last 5 turns to avoid token limits)
	history := session.BuildConversationHistory(events, 5)
	if history == "" {
		return message
	}

	if bs.logger != nil {
		bs.logger.Debug("Injecting conversation history into resumed session",
			"history_length", len(history))
	}

	return history + message
}

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

// sessionCreationRPCTimeout is the default timeout for the initial ACP session creation RPC
// (NewSession call). It is intentionally shorter than the HTTP middleware's 30s request
// timeout so that if the RPC times out, the HTTP handler can still return a proper error
// response instead of a generic "Request timeout" from the middleware.
const sessionCreationRPCTimeout = 25 * time.Second

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

// onContextUsageUpdate stores the latest context window usage and notifies all observers.
func (bs *BackgroundSession) onContextUsageUpdate(size, used int) {
	bs.contextUsageMu.Lock()
	bs.contextSize = size
	bs.contextUsed = used
	bs.contextUsageMu.Unlock()

	bs.notifyObservers(func(o SessionObserver) {
		o.OnContextUsageUpdate(size, used)
	})
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
		delay := backoffDelay(recentCount-1, ACPRestartBaseDelay, ACPRestartMaxDelay, acpStartRetryJitterRatio)
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
			delay := backoffDelay(attempt-1, acpStartRetryBaseDelay, acpStartRetryMaxDelay, acpStartRetryJitterRatio)
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
		lastClassified = classifyACPError(processErr, stderr)

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
// stderrCollector collects stderr output from the ACP process for error reporting.
// It stores the last N bytes of stderr output that can be retrieved when errors occur.
type stderrCollector struct {
	mu       sync.Mutex
	buffer   []byte
	maxSize  int
	logger   *slog.Logger
	isClosed bool
}

// newStderrCollector creates a new stderr collector with the given max buffer size.
func newStderrCollector(maxSize int, logger *slog.Logger) *stderrCollector {
	return &stderrCollector{
		buffer:  make([]byte, 0, maxSize),
		maxSize: maxSize,
		logger:  logger,
	}
}

// Write implements io.Writer to collect stderr output.
func (c *stderrCollector) Write(p []byte) (n int, err error) {
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
func (c *stderrCollector) GetOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buffer)
}

// Close marks the collector as closed and logs any remaining output at warn level if non-empty.
func (c *stderrCollector) Close() {
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
// httpStatusRegex matches HTTP status codes in ACP error strings.
// It looks for patterns like "HTTP error: NNN", `"httpStatus":NNN`, or "HTTP/1.1 NNN".
var httpStatusRegex = regexp.MustCompile(`(?:HTTP error:\s*|"httpStatus"\s*:\s*|HTTP/[12](?:\.[01])?\s+)(\d{3})`)

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

// startStderrMonitor starts a goroutine that reads from stderr and writes to the collector.
// If onCrashDetected is non-nil, it is called (at most once) when crash patterns are
// detected in the stderr output, enabling early process death signaling.
// If onFirstActivity is non-nil, it is called (at most once) the first time any bytes
// are observed on stderr — used by the startup watchdog to detect "live" processes.
func startStderrMonitor(stderr runner.ReadCloser, collector *stderrCollector, onCrashDetected func(), onFirstActivity func()) {
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

// startACPStartupWatchdog runs a background goroutine that emits a WARN log if no stderr
// activity is observed within acpStartupWatchdogWarnDelay, and an ERROR log if the process
// is still unresponsive after acpStartupWatchdogErrorDelay. The returned signalActivity
// callback should be wired to stderr first-activity AND called when the Initialize
// handshake completes (success or failure); callers should also defer-cancel ctx so the
// watchdog is torn down when startup finishes. Returns a no-op if logger is nil.
func startACPStartupWatchdog(ctx context.Context, logger *slog.Logger, command, acpServer string, pid int) func() {
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

// buildACPProcessEnv constructs the environment slice for an ACP subprocess.
// Keys are replaced in-place via mittoAcp.MergeEnv; precedence is:
//
//  1. os.Environ() — inherited from the Mitto process (lowest).
//  2. serverEnv — server-specific env from settings.json (acp_servers[].env).
//  3. mittoEnv — MITTO_* vars set by Mitto (highest precedence).
//
// This is shared between the direct-exec and restricted-runner branches so that
// the runner branch sees the same env as the non-runner branch.
func buildACPProcessEnv(serverEnv map[string]string, mittoEnv map[string]string) []string {
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
	stderrCollector := newStderrCollector(8192, bs.logger)

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
		runnerEnv := buildACPProcessEnv(bs.serverEnv, mittoEnv)
		stdin, stdout, stderr, wait, err = bs.runner.RunWithPipes(bs.ctx, args[0], args[1:], runnerEnv)
		if err != nil {
			return "", &sessionError{"failed to start with runner: " + err.Error()}
		}

		signalStartupActivity = startACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", -1)

		// Monitor stderr in background (with crash detection for Fix C and watchdog wake-up)
		startStderrMonitor(stderr, stderrCollector, onCrashDetected, signalStartupActivity)

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
		cmd.Env = buildACPProcessEnv(bs.serverEnv, mittoEnv)

		if err := cmd.Start(); err != nil {
			return "", &sessionError{"failed to start ACP server: " + err.Error()}
		}

		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		signalStartupActivity = startACPStartupWatchdog(watchdogCtx, bs.logger, acpCommand, "", pid)

		// Monitor stderr in background (same as runner case, with crash detection for Fix C
		// and watchdog wake-up on first stderr activity)
		startStderrMonitor(stderrPipe, stderrCollector, onCrashDetected, signalStartupActivity)

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
				bs.setAgentModels(stableToUnstableModelState(loadResp.Models))
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
	bs.setAgentModels(stableToUnstableModelState(sessResp.Models))

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

// buildWebClientConfig assembles the WebClientConfig from this session's callbacks and settings.
// Used by both the per-session and shared-process paths to create a WebClient.
func (bs *BackgroundSession) buildWebClientConfig() WebClientConfig {
	cfg := WebClientConfig{
		AutoApprove:          bs.autoApprove,
		SeqProvider:          bs,
		Logger:               bs.logger,
		OnAgentMessage:       bs.onAgentMessage,
		OnAgentThought:       bs.onAgentThought,
		OnToolCall:           bs.onToolCall,
		OnToolUpdate:         bs.onToolUpdate,
		OnPlan:               bs.onPlan,
		OnFileWrite:          bs.onFileWrite,
		OnFileRead:           bs.onFileRead,
		OnPermission:         bs.onPermission,
		OnAvailableCommands:  bs.onAvailableCommands,
		OnCurrentModeChanged: bs.onCurrentModeChanged,
		OnMittoToolCall:      bs.onMittoToolCall,
		OnContextUsageUpdate: bs.onContextUsageUpdate,
		OnActivity:           bs.signalAgentActivity,
	}
	if bs.fileLinksConfig.IsEnabled() {
		cfg.FileLinksConfig = &conversion.FileLinkerConfig{
			WorkingDir:            bs.workingDir,
			WorkspacePath:         bs.workingDir,
			WorkspaceUUID:         bs.workspaceUUID,
			Enabled:               true,
			AllowOutsideWorkspace: bs.fileLinksConfig.IsAllowOutsideWorkspace(),
			APIPrefix:             bs.apiPrefix,
		}
	}
	return cfg
}

// creationRPCCtx returns a context suitable for the initial ACP session creation RPC.
// It uses CreationCtx from the config if it already has a deadline; otherwise it
// applies sessionCreationRPCTimeout.  The returned cancel function must be called.
//
// Design rationale: The 25s default is shorter than the HTTP middleware's 30s request
// timeout so that if the RPC times out, the HTTP handler can still return a proper
// error response (503 with a helpful message) rather than a generic "Request timeout".
func (bs *BackgroundSession) creationRPCCtx() (context.Context, context.CancelFunc) {
	base := bs.creationCtx
	if base == nil {
		base = bs.ctx
	}
	if _, hasDeadline := base.Deadline(); hasDeadline {
		// Caller already set a deadline — honour it, just make it cancellable.
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, sessionCreationRPCTimeout)
}

// prepareSharedACPSession sets up this BackgroundSession to use a session on the
// given shared ACP process WITHOUT issuing the blocking session/new RPC.
// All eager setup (capabilities, MCP server, acpClient, death-channel bridge) is
// done here; the session/new RPC is deferred to the first prompt via
// ensureSharedACPSession so that creating a conversation never blocks on a busy agent.
func (bs *BackgroundSession) prepareSharedACPSession(sharedProcess *SharedACPProcess, workingDir string) error {
	bs.sharedProcess = sharedProcess

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := bs.startSessionMcpServer(bs.store, caps)
	if mcpServers == nil {
		mcpServers = []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	}

	bs.acpClient = NewWebClient(bs.buildWebClientConfig())
	bs.agentSupportsImages = caps.PromptCapabilities.Image

	// Store what ensureSharedACPSession will need for the deferred RPC.
	bs.pendingSharedWorkingDir = workingDir
	bs.pendingSharedMcpServers = mcpServers
	bs.pendingShared = true

	// Release the creation context — it is the HTTP request context and will be
	// cancelled as soon as the create handler returns. The deferred session/new uses
	// bs.ctx instead (see ensureSharedACPSession). resumeSharedACPSession (called on
	// crash restart) also uses creationRPCCtx(), so this nil ensures it falls back to
	// bs.ctx rather than the long-expired HTTP request context.
	bs.creationCtx = nil

	// Bridge the shared process's death channel to bs.acpProcessDone.
	done := make(chan struct{})
	bs.acpProcessDone = done
	bs.acpProcessDoneOnce = sync.Once{}
	sharedDone := sharedProcess.ProcessDone()
	go func() {
		select {
		case <-sharedDone:
			bs.acpProcessDoneOnce.Do(func() { close(done) })
		case <-bs.ctx.Done():
		}
	}()

	if bs.logger != nil {
		bs.logger.Info("Prepared shared ACP session (session/new deferred to first prompt)",
			"session_id", bs.persistedID,
			"supports_images", bs.agentSupportsImages)
	}
	return nil
}

// ensureSharedACPSession performs the deferred session/new RPC for a shared-process
// session. It is idempotent and safe under concurrent callers (guarded by pendingSharedMu).
// Returns nil immediately if the handshake already completed or was handled by a restart.
// On error, the session is left in a retryable state — the caller should surface a clear
// error to the user and allow the next prompt to retry.
func (bs *BackgroundSession) ensureSharedACPSession() error {
	bs.pendingSharedMu.Lock()
	defer bs.pendingSharedMu.Unlock()

	// Return if already done or if a restart path already set bs.acpID.
	if !bs.pendingShared || bs.acpID != "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(bs.ctx, sessionCreationRPCTimeout)
	handle, err := bs.sharedProcess.NewSession(ctx, bs.pendingSharedWorkingDir, bs.pendingSharedMcpServers)
	cancel()
	if err != nil {
		// Leave pendingShared=true so the next prompt can retry.
		return fmt.Errorf("failed to create session on shared process: %w", err)
	}

	bs.sharedProcess.RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       bs.acpClient.SessionUpdate,
		OnReadTextFile:        bs.acpClient.ReadTextFile,
		OnWriteTextFile:       bs.acpClient.WriteTextFile,
		OnRequestPermission:   bs.acpClient.RequestPermission,
		OnCreateTerminal:      bs.acpClient.CreateTerminal,
		OnTerminalOutput:      bs.acpClient.TerminalOutput,
		OnReleaseTerminal:     bs.acpClient.ReleaseTerminal,
		OnWaitForTerminalExit: bs.acpClient.WaitForTerminalExit,
		OnKillTerminal:        bs.acpClient.KillTerminal,
	})

	bs.acpID = handle.SessionID

	// Stash modes and models for applyPendingSharedModes to apply from the prompt
	// goroutine. We must NOT call setSessionModes / setAgentModels here because
	// they trigger store writes (via persistConfigValue / applyConfigConstraints)
	// that may race with concurrent store access from other goroutines (e.g., the
	// test event-injector using a separate Store instance on the same directory).
	bs.pendingSharedModes = handle.Modes
	bs.pendingSharedModels = handle.Models

	bs.pendingShared = false

	if bs.logger != nil {
		bs.logger.Info("Completed deferred session/new on shared process",
			"session_id", bs.persistedID,
			"acp_session_id", bs.acpID)
		bs.logAgentModels(handle.Models)
	}
	return nil
}

// applyPendingSharedModes applies the modes and models that were stashed by
// ensureSharedACPSession. Safe to call only from a single goroutine (the prompt
// goroutine) because setSessionModes and setAgentModels trigger store writes via
// persistConfigValue / applyConfigConstraints.
// Calling this more than once is a no-op once the fields are cleared.
func (bs *BackgroundSession) applyPendingSharedModes() {
	bs.pendingSharedMu.Lock()
	modes := bs.pendingSharedModes
	models := bs.pendingSharedModels
	bs.pendingSharedModes = nil
	bs.pendingSharedModels = nil
	bs.pendingSharedMu.Unlock()

	if modes != nil {
		bs.setSessionModes(modes)
	}
	if models != nil {
		bs.setAgentModels(models)
	}
}

// completeDeferredHandshake performs the deferred session/new RPC for a shared-
// process session, persists the ACP session ID, applies the session's modes and
// models (which populate the config options surfaced to the UI as model/mode
// selectors), and notifies observers that ACP is ready. It serialises these store
// writes via handshakeMu so it is safe to call from either the first-prompt
// goroutine or the background prewarm goroutine (see PrewarmACPSession). It returns
// nil — without notifying — when there is nothing to do (not a deferred shared
// session, or the handshake already completed).
func (bs *BackgroundSession) completeDeferredHandshake() error {
	bs.handshakeMu.Lock()
	defer bs.handshakeMu.Unlock()

	// Nothing to do if this is not a deferred shared session, or the handshake has
	// already completed. pendingShared is flipped to false (under pendingSharedMu)
	// by ensureSharedACPSession once the RPC succeeds.
	bs.pendingSharedMu.Lock()
	pending := bs.pendingShared
	bs.pendingSharedMu.Unlock()
	if bs.sharedProcess == nil || !pending {
		return nil
	}

	if err := bs.ensureSharedACPSession(); err != nil {
		return err
	}

	// Persist the ACP session ID. Done here (not inside ensureSharedACPSession) so
	// that store writes happen from a single serialised goroutine (handshakeMu).
	if bs.store != nil && bs.persistedID != "" && bs.acpID != "" {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to persist ACP session ID after deferred handshake", "error", err)
		}
	}

	bs.applyPendingSharedModes()

	// Notify observers that ACP is now ready and config options (model, mode) are
	// available, so the UI can render the model/mode selectors.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStarted()
	})
	return nil
}

// PrewarmACPSession completes the deferred ACP session/new handshake in the
// background so the model and mode selectors become available before the first
// prompt is sent. It is best-effort and idempotent: a no-op for non-deferred or
// already-started sessions, and on failure it leaves the session retryable so the
// first prompt re-attempts the handshake. Intended to be called from a goroutine.
func (bs *BackgroundSession) PrewarmACPSession() {
	if bs == nil || bs.sharedProcess == nil {
		return
	}
	if err := bs.completeDeferredHandshake(); err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Background ACP prewarm failed (will retry on first prompt)",
				"session_id", bs.persistedID,
				"error", err)
		}
	}
}

// resumeSharedACPSession sets up this BackgroundSession to use a session on the
// given shared ACP process, trying to resume the specified ACP session ID first.
// Falls back to creating a new session if resumption fails.
func (bs *BackgroundSession) resumeSharedACPSession(sharedProcess *SharedACPProcess, workingDir, acpSessionID string) error {
	bs.sharedProcess = sharedProcess

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := bs.startSessionMcpServer(bs.store, caps)

	bs.acpClient = NewWebClient(bs.buildWebClientConfig())

	var handle *SessionHandle
	var err error

	// Try to resume an existing session if we have an ID.
	// Prefer Resume over Load for speed (no history replay).
	if acpSessionID != "" {
		// Check capabilities
		supportsResume := caps.SessionCapabilities.Resume != nil
		supportsLoad := caps.LoadSession

		// Try Resume first (fast path)
		if supportsResume {
			resumeCtx, resumeCancel := context.WithTimeout(bs.ctx, 10*time.Second)
			handle, err = sharedProcess.ResumeSession(resumeCtx, acpSessionID, workingDir, mcpServers)
			resumeCancel()
			if err != nil {
				logFields := []any{
					"acp_session_id", acpSessionID,
					"error", err,
					"method", "resume",
				}
				if resumeCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if bs.logger != nil {
					bs.logger.Info("Resume failed, will try Load or New",
						logFields...)
				}
				// Fall through to try Load
			} else {
				bs.resumeMethod = "resume"
				if bs.logger != nil {
					bs.logger.Info("Successfully resumed session using UNSTABLE resume API",
						"acp_session_id", acpSessionID,
						"resume_method", "resume")
				}
			}
		}

		// Fallback to Load (slow path with history replay)
		if handle == nil && supportsLoad {
			// Suppress event processing during Load to prevent notification queue overflow.
			// See comment in startACPProcess for details.
			bs.acpClient.SetLoadingSession(true)
			loadCtx, loadCancel := context.WithTimeout(bs.ctx, 30*time.Second)
			handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
			loadCancel()
			bs.acpClient.SetLoadingSession(false)
			if err != nil {
				logFields := []any{
					"acp_session_id", acpSessionID,
					"error", err,
					"method", "load",
				}
				if loadCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if bs.logger != nil {
					bs.logger.Info("Load failed, creating new session",
						logFields...)
				}
			} else {
				bs.resumeMethod = "load"
				if bs.logger != nil {
					bs.logger.Info("Successfully loaded session (with history replay)",
						"acp_session_id", acpSessionID,
						"resume_method", "load")
				}
			}
		}
	}

	// Final fallback: create new session
	if handle == nil {
		bs.resumeMethod = "new"
		// Use the creation context so the HTTP handler's timeout can cancel this RPC.
		rpcCtx, rpcCancel := bs.creationRPCCtx()
		handle, err = sharedProcess.NewSession(rpcCtx, workingDir, mcpServers)
		rpcCancel()
		if err != nil {
			bs.stopSessionMcpServer()
			bs.acpClient.Close()
			bs.acpClient = nil
			bs.sharedProcess = nil
			return fmt.Errorf("failed to create session on shared process: %w", err)
		}
	}
	bs.creationCtx = nil // Release reference — only needed for the creation RPCs above.

	sharedProcess.RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       bs.acpClient.SessionUpdate,
		OnReadTextFile:        bs.acpClient.ReadTextFile,
		OnWriteTextFile:       bs.acpClient.WriteTextFile,
		OnRequestPermission:   bs.acpClient.RequestPermission,
		OnCreateTerminal:      bs.acpClient.CreateTerminal,
		OnTerminalOutput:      bs.acpClient.TerminalOutput,
		OnReleaseTerminal:     bs.acpClient.ReleaseTerminal,
		OnWaitForTerminalExit: bs.acpClient.WaitForTerminalExit,
		OnKillTerminal:        bs.acpClient.KillTerminal,
	})

	bs.acpID = handle.SessionID
	bs.agentSupportsImages = caps.PromptCapabilities.Image
	bs.setSessionModes(handle.Modes)
	bs.setAgentModels(handle.Models)

	// Bridge the shared process's death channel to bs.acpProcessDone.
	done := make(chan struct{})
	bs.acpProcessDone = done
	bs.acpProcessDoneOnce = sync.Once{}
	sharedDone := sharedProcess.ProcessDone()
	go func() {
		select {
		case <-sharedDone:
			bs.acpProcessDoneOnce.Do(func() { close(done) })
		case <-bs.ctx.Done():
		}
	}()

	if bs.logger != nil {
		bs.logger.Info("Resumed ACP session on shared process",
			"session_id", bs.persistedID,
			"acp_session_id", bs.acpID,
			"requested_acp_session_id", acpSessionID,
			"resume_method", bs.resumeMethod,
			"supports_images", bs.agentSupportsImages)
		bs.logAgentModels(handle.Models)
	}

	// Notify observers that ACP is now ready to accept prompts.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStarted()
	})

	return nil
}

// logSessionModes logs the session modes/config options at DEBUG level.
// This helps with debugging which modes are available from the ACP server.
func (bs *BackgroundSession) logSessionModes(modes *acp.SessionModeState) {
	if bs.logger == nil || modes == nil {
		return
	}

	// Log current mode
	bs.logger.Debug("Session mode state",
		"current_mode", modes.CurrentModeId,
		"available_modes_count", len(modes.AvailableModes))

	// Log each available mode
	for _, mode := range modes.AvailableModes {
		desc := ""
		if mode.Description != nil {
			desc = *mode.Description
		}
		bs.logger.Debug("Available session mode",
			"mode_id", mode.Id,
			"mode_name", mode.Name,
			"mode_description", desc)
	}
}

// logAgentInfo logs the agent information and capabilities from the Initialize response at DEBUG level.
// This helps with debugging which agent is being used and what features it supports.
func (bs *BackgroundSession) logAgentInfo(resp acp.InitializeResponse) {
	if bs.logger == nil {
		return
	}

	// Log agent info if available
	if resp.AgentInfo != nil {
		bs.logger.Debug("Agent info",
			"agent_name", resp.AgentInfo.Name,
			"agent_version", resp.AgentInfo.Version)
	}

	// Log protocol version
	bs.logger.Debug("ACP protocol version",
		"protocol_version", resp.ProtocolVersion)

	// Log and store agent capabilities
	caps := resp.AgentCapabilities
	bs.agentSupportsImages = caps.PromptCapabilities.Image
	bs.logger.Debug("Agent capabilities",
		"load_session", caps.LoadSession,
		"mcp_http", caps.McpCapabilities.Http,
		"mcp_sse", caps.McpCapabilities.Sse,
		"prompt_audio", caps.PromptCapabilities.Audio,
		"prompt_embedded_context", caps.PromptCapabilities.EmbeddedContext,
		"prompt_image", caps.PromptCapabilities.Image)

	// Log authentication methods if available
	if len(resp.AuthMethods) > 0 {
		authMethods := make([]string, len(resp.AuthMethods))
		for i, auth := range resp.AuthMethods {
			if auth.Agent != nil {
				authMethods[i] = auth.Agent.Name
			} else if auth.EnvVar != nil {
				authMethods[i] = "env_var"
			} else if auth.Terminal != nil {
				authMethods[i] = "terminal"
			} else {
				authMethods[i] = "unknown"
			}
		}
		bs.logger.Debug("Agent auth methods",
			"count", len(resp.AuthMethods),
			"methods", authMethods)
	}
}

// sessionError is a simple error type for session errors.
type sessionError struct {
	msg string
}

func (e *sessionError) Error() string {
	return e.msg
}

// NeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func (bs *BackgroundSession) NeedsTitle() bool {
	if bs.store == nil || bs.persistedID == "" {
		return false
	}
	meta, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		return false
	}
	return meta.Name == ""
}

// retryTitleGenerationIfNeeded checks if the session still needs a title and
// triggers async title generation. This is called after prompt completion to catch:
// (1) failed initial title generation attempts (e.g., context deadline exceeded)
// (2) prompts that arrived via paths that don't trigger title generation
//
//	(queue processing, MCP send_prompt, periodic prompts)
func (bs *BackgroundSession) retryTitleGenerationIfNeeded(message string) {
	if !bs.NeedsTitle() {
		return
	}

	if bs.logger != nil {
		bs.logger.Info("Session still has no title after prompt completion, retrying title generation",
			"session_id", bs.persistedID)
	}

	GenerateAndSetTitle(TitleGenerationConfig{
		Store:            bs.store,
		SessionID:        bs.persistedID,
		Message:          message,
		Logger:           bs.logger,
		WorkspaceUUID:    bs.workspaceUUID,
		AuxiliaryManager: bs.auxiliaryManager,
		OnTitleGenerated: bs.onTitleGenerated,
	})
}

// TriggerTitleGeneration triggers async title generation if the session has no title yet.
// This is the public interface used by MCP tools and API handlers to generate titles
// for sessions that received prompts via paths that don't normally trigger title generation
// (e.g., periodic prompt configuration, queue processing).
func (bs *BackgroundSession) TriggerTitleGeneration(message string) {
	bs.retryTitleGenerationIfNeeded(message)
}

// TriggerTitleGenerationFromPeriodic chooses the best source text for title
// generation given a periodic-style draft. The inline `prompt` may be empty,
// whitespace, or the UI placeholder "(pending)" — all three are treated as
// "no inline prompt". When only `promptName` is meaningful, it is resolved
// to its full text via the configured prompt resolver (workingDir-scoped)
// before being passed to the auxiliary title generator. If resolution fails
// or no resolver is configured, the bare prompt name is used as a fallback.
// No-op when neither source yields any text.
func (bs *BackgroundSession) TriggerTitleGenerationFromPeriodic(prompt, promptName string) {
	inline := strings.TrimSpace(prompt)
	if inline != "" && inline != "(pending)" {
		bs.retryTitleGenerationIfNeeded(inline)
		return
	}
	name := strings.TrimSpace(promptName)
	if name == "" {
		return
	}
	if bs.promptResolver != nil {
		if resolved, err := bs.promptResolver(name, bs.workingDir); err == nil && strings.TrimSpace(resolved) != "" {
			bs.retryTitleGenerationIfNeeded(strings.TrimSpace(resolved))
			return
		} else if err != nil && bs.logger != nil {
			bs.logger.Warn("Could not resolve periodic prompt name for title generation; falling back to name",
				"prompt_name", name, "error", err)
		}
	}
	bs.retryTitleGenerationIfNeeded(name)
}

// GetWorkspaceUUID returns the workspace UUID associated with this session.
func (bs *BackgroundSession) GetWorkspaceUUID() string {
	return bs.workspaceUUID
}

// GetAuxiliaryManager returns the auxiliary manager associated with this session.
func (bs *BackgroundSession) GetAuxiliaryManager() *auxiliary.WorkspaceAuxiliaryManager {
	return bs.auxiliaryManager
}

// SetPromptResolver sets the function used to resolve named workspace prompts to their full text.
// This is called by the server setup code (same resolver used by PeriodicRunner).
func (bs *BackgroundSession) SetPromptResolver(resolver PromptResolverFunc) {
	bs.promptResolver = resolver
}

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID         string          // Unique identifier of the sending client (for broadcast deduplication)
	PromptID         string          // Client-generated prompt ID (for delivery confirmation)
	PromptName       string          // Name of workspace prompt (resolved to full text before ACP; empty for ad-hoc prompts)
	ImageIDs         []string        // IDs of images attached to the prompt
	FileIDs          []string        // IDs of files attached to the prompt
	OnComplete       func(err error) // Called when the async prompt goroutine finishes (nil = success)
	IsPeriodicForced bool            // True when this periodic prompt was triggered manually via "run now"
	FreshContext     bool            // True to suppress history injection and use a new ACP session for this prompt
	// Arguments, when non-empty, triggers bash-like ${VAR}/${VAR:-default}
	// substitution on the resolved prompt text before persistence and broadcast.
	// Only set for named/scenario prompts; ad-hoc messages leave this nil so that
	// pasted shell/code containing ${...} is never corrupted.
	Arguments map[string]string
	// PreferredModels is an ordered list of case-insensitive glob patterns matched against
	// available model IDs and display names. The first match wins; absent/empty uses the
	// session's baseline model. When empty and PromptName is set, the list is resolved
	// from the prompt definition via preferredModelsResolver inside PromptWithMeta.
	PreferredModels []string
}

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	return bs.PromptWithMeta(message, PromptMeta{})
}

// PromptWithImages sends a message with optional images to the agent. This runs asynchronously.
// The imageIDs should be IDs of images previously uploaded to this session.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithImages(message string, imageIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs})
}

// PromptWithAttachments sends a message with optional images and files to the agent.
// This runs asynchronously. The IDs should be of previously uploaded images/files.
func (bs *BackgroundSession) PromptWithAttachments(message string, imageIDs, fileIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs, FileIDs: fileIDs})
}

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	// Resolve prompt name to full text before any other processing.
	// meta.PromptName is UI metadata only; the ACP agent always receives the full text.
	if meta.PromptName != "" && message == "" {
		if bs.promptResolver == nil {
			return fmt.Errorf("prompt %q cannot be resolved: no prompt resolver configured", meta.PromptName)
		}
		resolved, err := bs.promptResolver(meta.PromptName, bs.workingDir)
		if err != nil {
			return fmt.Errorf("failed to resolve prompt %q: %w", meta.PromptName, err)
		}
		message = resolved
	}

	// Apply bash-like ${VAR}/${VAR:-default} argument substitution when the caller
	// supplied an arguments map. Done here (the single chokepoint for all entry
	// paths) and before persistence/broadcast so the transcript shows the
	// substituted text. Guarded on len > 0 so ad-hoc messages are untouched.
	if len(meta.Arguments) > 0 {
		message = processors.SubstituteArguments(message, meta.Arguments)
	}

	imageIDs := meta.ImageIDs
	fileIDs := meta.FileIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil && bs.sharedProcess == nil {
		return &sessionError{"The AI agent is still starting up. Please wait a moment and try again."}
	}

retryAfterRestart:
	bs.promptMu.Lock()
	if bs.isPrompting {
		// Check if the ACP connection is dead (process crashed)
		// We use non-blocking checks on both Done() and acpProcessDone channels.
		// acpProcessDone fires faster than Done() because it uses OS-level process
		// liveness checks rather than waiting for pipe EOF propagation.
		acpDead := false
		if bs.acpConn != nil {
			select {
			case <-bs.acpConn.Done():
				acpDead = true
			default:
				// Connection still alive
			}
		} else if bs.sharedProcess != nil {
			select {
			case <-bs.sharedProcess.Done():
				acpDead = true
			default:
				// Shared connection still alive
			}
		} else {
			acpDead = true // No connection at all
		}
		// Also check OS-level process death (faster detection)
		if !acpDead && bs.acpProcessDone != nil {
			select {
			case <-bs.acpProcessDone:
				acpDead = true
			default:
			}
		}

		if acpDead {
			elapsed := time.Since(bs.promptStartTime)
			if bs.logger != nil {
				bs.logger.Warn("Detected dead ACP connection",
					"prompt_start_time", bs.promptStartTime,
					"elapsed", elapsed)
			}
			bs.isPrompting = false
			bs.lastResponseComplete = time.Now()
			bs.promptMu.Unlock()

			// Check if we can restart automatically
			if bs.canRestartACP() {
				// Notify observers that we're restarting (include attempt count so
				// the user understands this is a retry loop, not a one-off)
				restartInfo := bs.getRestartInfo()
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
				})

				// Attempt to restart the ACP process
				if err := bs.restartACPProcess(RestartReasonCrashDuringPrompt); err != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + err.Error() + ". Please switch to another conversation and back to retry."
					if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = formatClassifiedError(classified)
					}
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError(errMsg)
					})
					return &sessionError{"ACP process died and restart failed: " + err.Error()}
				}

				// Restart succeeded — automatically retry the prompt.
				// Note: we say "restarted" (not "restarted successfully") because the
				// process may crash again on the next prompt — we don't want to give
				// false confidence.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Retrying your message automatically...")
				})
				if bs.logger != nil {
					bs.logger.Info("Auto-retrying prompt after ACP restart",
						"session_id", bs.persistedID,
						"reason", "crash_during_prompt")
				}
				// isPrompting was cleared above; re-acquire promptMu and proceed
				// through the normal prompt path below.
				goto retryAfterRestart
			}

			// Restart limit exceeded - notify user to manually restart
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
			})
			return &sessionError{"ACP process died repeatedly - switch conversations to restart"}
		} else {
			bs.promptMu.Unlock()
			return &sessionError{"prompt already in progress"}
		}
	}
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptCount++
	bs.TouchActivity()

	// Check if we need to inject conversation history (first prompt of resumed session).
	// FreshContext suppresses history injection so each periodic run starts clean.
	shouldInjectHistory := bs.isResumed && !bs.historyInjected && !meta.FreshContext
	if shouldInjectHistory {
		bs.historyInjected = true
	}

	// Capture first prompt state for message processors
	isFirst := bs.isFirstPrompt
	if isFirst {
		bs.isFirstPrompt = false
	}
	bs.promptMu.Unlock()

	// Notify about streaming state change (prompt started)
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, true)
	}

	// Load images and build content blocks
	var imageRefs []session.ImageRef
	var contentBlocks []acp.ContentBlock

	if len(imageIDs) > 0 && !bs.agentSupportsImages {
		if bs.logger != nil {
			bs.logger.Warn("Agent did not advertise image support, sending images anyway",
				"image_count", len(imageIDs),
				"session_id", bs.persistedID)
		}
		// Warn the user but still send images — models sometimes misreport capabilities
		bs.notifyObservers(func(o SessionObserver) {
			o.OnError("⚠️ The current AI agent did not advertise image support. " +
				"Images will be sent anyway, but may not be processed correctly.")
		})
	}

	if len(imageIDs) > 0 && bs.store != nil {
		for _, imageID := range imageIDs {
			imagePath, err := bs.store.GetImagePath(bs.persistedID, imageID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get image path", "image_id", imageID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(imageID, "."); idx >= 0 {
				ext = imageID[idx:]
			}
			mimeType := session.GetMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "image/png" // Default fallback
			}

			// Load image and create attachment
			att, err := mittoAcp.ImageAttachmentFromFile(imagePath, mimeType)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to load image", "image_id", imageID, "error", err)
				}
				continue
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			imageRefs = append(imageRefs, session.ImageRef{
				ID:       imageID,
				MimeType: mimeType,
			})
		}
	}

	// Load files and build content blocks
	var fileRefs []session.FileRef
	if len(fileIDs) > 0 && bs.store != nil {
		for _, fileID := range fileIDs {
			filePath, err := bs.store.GetFilePath(bs.persistedID, fileID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get file path", "file_id", fileID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(fileID, "."); idx >= 0 {
				ext = fileID[idx:]
			}
			mimeType := session.GetFileMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			// Determine file category and create appropriate attachment
			category := session.GetFileCategory(mimeType)
			var att mittoAcp.Attachment
			if category == session.FileCategoryText {
				// Text files are embedded inline
				att, err = mittoAcp.TextFileAttachmentFromFile(filePath, mimeType)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Warn("Failed to load text file", "file_id", fileID, "error", err)
					}
					continue
				}
			} else {
				// Binary files are referenced by path
				att = mittoAcp.BinaryFileAttachment(filePath, mimeType)
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			fileRefs = append(fileRefs, session.FileRef{
				ID:       fileID,
				Name:     att.Name,
				MimeType: mimeType,
				Category: category,
			})
		}
	}

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Clear cached plan state when new prompt starts
	// The existing plan becomes stale; a new plan will be generated for this prompt
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, nil)
	}

	// Persist user prompt with image/file references and prompt ID
	// User prompts are persisted immediately (not buffered), so we need to
	// refresh nextSeq after persistence to get the correct seq for the prompt
	// The prompt ID is included so clients can clear pending prompts on reconnect
	var userPromptSeq int64
	if bs.recorder != nil {
		if err := bs.recorder.RecordUserPromptComplete(message, imageRefs, fileRefs, meta.PromptID, meta.PromptName); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
		// Get the seq that was assigned to the user prompt (it's the current event count)
		userPromptSeq = int64(bs.recorder.EventCount())
		// Update nextSeq for subsequent agent events
		bs.refreshNextSeq()
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	fileIDStrings := make([]string, len(fileRefs))
	for i, f := range fileRefs {
		fileIDStrings[i] = f.ID
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings, meta.PromptName)
	})

	// Build the actual prompt to send to ACP.
	// Apply the unified processor pipeline (text-mode + command-mode in priority order).
	promptMessage := message
	var procAttachmentBlocks []acp.ContentBlock

	// Fetch session metadata for @mitto:variable substitution.
	// Done unconditionally so substitution works even with no processors configured.
	// Best-effort: unavailable fields substitute to "".
	var sessionName, acpServer, parentSessionID, parentSessionName, beadsIssue string
	var childSessions []processors.ChildSession
	var advancedSettings map[string]bool
	if bs.store != nil && bs.persistedID != "" {
		if sessionMeta, metaErr := bs.store.GetMetadata(bs.persistedID); metaErr == nil {
			sessionName = sessionMeta.Name
			acpServer = sessionMeta.ACPServer
			parentSessionID = sessionMeta.ParentSessionID
			advancedSettings = sessionMeta.AdvancedSettings
			beadsIssue = sessionMeta.BeadsIssue
		}
		// Resolve parent session name for @mitto:parent variable
		if parentSessionID != "" {
			if parentMeta, parentErr := bs.store.GetMetadata(parentSessionID); parentErr == nil {
				parentSessionName = parentMeta.Name
			}
		}
		// Resolve child sessions for @mitto:children variable
		if children, childErr := bs.store.ListChildSessions(bs.persistedID); childErr == nil {
			for _, child := range children {
				isPrompting := false
				if bs.isChildPrompting != nil {
					isPrompting = bs.isChildPrompting(child.SessionID)
				}
				childSessions = append(childSessions, processors.ChildSession{
					ID:          child.SessionID,
					Name:        child.Name,
					ACPServer:   child.ACPServer,
					IsAutoChild: child.ChildOrigin == session.ChildOriginAuto,
					ChildOrigin: string(child.ChildOrigin),
					IsPrompting: isPrompting,
				})
			}
		}
	}
	// Get cached MCP tool names for tools.* CEL context
	var mcpToolNames []string
	if bs.auxiliaryManager != nil && bs.workspaceUUID != "" {
		if tools, ok := bs.auxiliaryManager.GetCachedMCPTools(bs.workspaceUUID); ok {
			mcpToolNames = make([]string, len(tools))
			for i, tool := range tools {
				mcpToolNames[i] = tool.Name
			}
		}
	}

	// Populate user data schema and current user data for processor variables
	var hasUserDataSchema bool
	var hasMittoRC bool
	var hasMetadataDescription bool
	var userDataSchemaJSON string
	var userDataJSON string
	if bs.workingDir != "" {
		rc, rcErr := config.LoadWorkspaceRC(bs.workingDir)
		if rcErr == nil && rc != nil &&
			rc.Metadata != nil && rc.Metadata.UserDataSchema != nil && len(rc.Metadata.UserDataSchema.Fields) > 0 {
			hasUserDataSchema = true
			if schemaBytes, err := json.Marshal(rc.Metadata.UserDataSchema.Fields); err == nil {
				userDataSchemaJSON = string(schemaBytes)
			}
		}
		// Check if .mittorc exists (regardless of content)
		if rcPath, _, err := config.FindWorkspaceRCPath(bs.workingDir); err == nil && rcPath != "" {
			hasMittoRC = true
		}
		// Check if metadata description is set
		if rcErr == nil && rc != nil && rc.Metadata != nil && rc.Metadata.Description != "" {
			hasMetadataDescription = true
		}
	}
	if bs.store != nil && bs.persistedID != "" {
		if ud, err := bs.store.GetUserData(bs.persistedID); err == nil && ud != nil && len(ud.Attributes) > 0 {
			if udBytes, err := json.Marshal(ud.Attributes); err == nil {
				userDataJSON = string(udBytes)
			}
		}
	}

	processorInput := &processors.ProcessorInput{
		Message:                message,
		IsFirstMessage:         isFirst,
		SessionID:              bs.persistedID,
		WorkingDir:             bs.workingDir,
		ParentSessionID:        parentSessionID,
		ParentSessionName:      parentSessionName,
		SessionName:            sessionName,
		ACPServer:              acpServer,
		WorkspaceUUID:          bs.workspaceUUID,
		BeadsIssue:             beadsIssue,
		AvailableACPServers:    bs.availableACPServers,
		ChildSessions:          childSessions,
		MCPToolNames:           mcpToolNames,
		IsPeriodic:             meta.SenderID == "periodic-runner",
		IsPeriodicForced:       meta.IsPeriodicForced,
		AdvancedSettings:       advancedSettings,
		HasUserDataSchema:      hasUserDataSchema,
		HasMittoRC:             hasMittoRC,
		HasMetadataDescription: hasMetadataDescription,
		UserDataSchemaJSON:     userDataSchemaJSON,
		UserDataJSON:           userDataJSON,
	}

	if bs.processorManager != nil {
		procResult, procErr := bs.processorManager.Apply(bs.ctx, processorInput)
		if procErr != nil {
			if bs.logger != nil {
				bs.logger.Error("Processor execution failed", "error", procErr)
			}
			// Continue with original message on processor failure
		} else {
			// Persist processor activation count to metadata after each successful Apply
			if bs.store != nil && bs.persistedID != "" {
				_, procActivations, procLastAt, _ := bs.GetProcessorStats()
				_ = bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
					m.ProcessorActivations = procActivations
					m.ProcessorLastActivation = procLastAt
				})
			}
		}
		if procResult != nil {
			promptMessage = procResult.Message

			// Convert processor attachments to content blocks
			if len(procResult.Attachments) > 0 {
				acpAttachments, err := procResult.ToACPAttachments(bs.workingDir)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Error("Failed to resolve processor attachments", "error", err)
					}
				} else {
					for _, att := range acpAttachments {
						if att.Type == "image" {
							procAttachmentBlocks = append(procAttachmentBlocks, acp.ImageBlock(att.Data, att.MimeType))
						}
						// Note: Non-image attachments could be handled differently in the future
					}
				}
			}
		}
	}

	// Apply @mitto:variable substitution unconditionally on the assembled message.
	// This covers both the case where processors ran (substitution on assembled output)
	// and the case where no processors are configured (substitution on the raw user message).
	promptMessage = processors.SubstituteVariables(promptMessage, processorInput)

	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(promptMessage)
	}

	// Build final content blocks: images first (from uploads and processors), then text
	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+len(procAttachmentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, procAttachmentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	// Log content block summary for debugging image delivery issues
	if bs.logger != nil {
		var imageBlockCount, textBlockCount, otherBlockCount int
		for _, block := range finalBlocks {
			if block.Image != nil {
				imageBlockCount++
			} else if block.Text != nil {
				textBlockCount++
			} else {
				otherBlockCount++
			}
		}
		bs.logger.Info("Sending prompt to ACP agent",
			"total_blocks", len(finalBlocks),
			"image_blocks", imageBlockCount,
			"text_blocks", textBlockCount,
			"other_blocks", otherBlockCount,
			"processor_attachment_blocks", len(procAttachmentBlocks),
			"agent_supports_images", bs.agentSupportsImages,
			"session_id", bs.persistedID)
	}

	// Run prompt in background
	go func() {
		// autoRetried guards a single automatic retry after an ACP crash during
		// streaming. On the first crash we restart the process and jump back to
		// retryPrompt; if the retry also crashes we fall through to the normal
		// "please resend" message instead of looping forever.
		autoRetried := false

		// For shared-process sessions, complete the deferred session/new handshake
		// before the first prompt. This runs after the HTTP create path has already
		// returned, so a busy agent delays the prompt — not conversation creation.
		// The background prewarm (see PrewarmACPSession) may have already completed
		// this when the client opened the conversation; completeDeferredHandshake is
		// idempotent and a no-op in that case.
		if bs.sharedProcess != nil {
			const maxHandshakeAttempts = 3
			var handshakeErr error
			for attempt := 1; attempt <= maxHandshakeAttempts; attempt++ {
				handshakeErr = bs.completeDeferredHandshake()
				if handshakeErr == nil {
					break
				}
				errStr := strings.ToLower(handshakeErr.Error())
				transient := strings.Contains(errStr, "deadline") ||
					strings.Contains(errStr, "timeout") ||
					strings.Contains(errStr, "timed out")
				if !transient || attempt == maxHandshakeAttempts {
					break
				}
				if bs.logger != nil {
					bs.logger.Warn("Deferred session/new transient failure, retrying",
						"session_id", bs.persistedID,
						"attempt", attempt,
						"error", handshakeErr)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if handshakeErr != nil {
				if bs.logger != nil {
					bs.logger.Error("Deferred session/new failed",
						"session_id", bs.persistedID,
						"error", handshakeErr)
				}
				friendlyMsg := "Could not start the agent session: " + formatACPError(handshakeErr) + " Please resend your message."
				if bs.recorder != nil {
					seq := bs.getNextSeq()
					if recErr := bs.recorder.RecordEventWithSeq(session.Event{
						Seq:       seq,
						Type:      session.EventTypeError,
						Timestamp: time.Now(),
						Data:      session.ErrorData{Message: friendlyMsg},
					}); recErr != nil && bs.logger != nil {
						bs.logger.Error("Failed to persist deferred handshake error", "error", recErr)
					}
					bs.refreshNextSeq()
				}
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(friendlyMsg)
				})
				bs.promptMu.Lock()
				bs.isPrompting = false
				bs.promptStartTime = time.Time{}
				bs.promptCond.Broadcast()
				bs.promptMu.Unlock()
				if bs.onStreamingStateChanged != nil {
					bs.onStreamingStateChanged(bs.persistedID, false)
				}
				return
			}
		}

		// For fresh-context runs, create a new ACP session so the agent has no
		// in-memory context from prior interactions. Only supported on non-shared
		// connections; shared-process sessions fall back to history suppression only.
		freshContextSessionID := ""
		if meta.FreshContext && bs.acpConn != nil {
			cwd := bs.workingDir
			if cwd == "" {
				cwd = "."
			}
			freshCtx, freshCancel := context.WithTimeout(bs.ctx, 10*time.Second)
			freshSess, freshErr := bs.acpConn.NewSession(freshCtx, acp.NewSessionRequest{
				Cwd:        cwd,
				McpServers: []acp.McpServer{}, // Must be empty array, not nil — ACP validates this
			})
			freshCancel()
			if freshErr == nil {
				freshContextSessionID = string(freshSess.SessionId)
				if bs.logger != nil {
					bs.logger.Info("Created fresh ACP session for periodic run",
						"fresh_session_id", freshContextSessionID,
						"session_id", bs.persistedID)
				}
			} else if bs.logger != nil {
				bs.logger.Warn("Failed to create fresh ACP session, using existing",
					"error", freshErr,
					"session_id", bs.persistedID)
			}
		}

		// Per-prompt model preference: ensure the correct model is active before sending.
		// Implements set-if-different: only one SetSessionModel call per model change,
		// never per-prompt (lazy). No-match and absent preferredModels both resolve to
		// baseline so a prior override is always cleared when not reused.
		if bs.agentModels != nil {
			preferredModels := meta.PreferredModels
			if len(preferredModels) == 0 && meta.PromptName != "" && bs.preferredModelsResolver != nil {
				preferredModels = bs.preferredModelsResolver(meta.PromptName, bs.workingDir)
			}

			bs.modelMu.Lock()
			baseline := bs.baselineModel
			bs.modelMu.Unlock()

			desired := baseline // default: use user's baseline
			isOverride := false
			if len(preferredModels) > 0 {
				if matched := matchPreferredModels(preferredModels, bs.agentModels); matched != "" {
					desired = matched
					isOverride = true
				}
				// no match → desired stays as baseline (prevents override leakage)
			}

			currentModel := string(bs.agentModels.CurrentModelId)
			if desired != "" && desired != currentModel {
				setCtx, setCancel := context.WithTimeout(bs.ctx, 15*time.Second)
				if setErr := bs.setActiveModelOnly(setCtx, desired); setErr != nil && bs.logger != nil {
					bs.logger.Warn("Failed to apply model preference",
						"model", desired, "error", setErr)
				}
				setCancel()
			}

			bs.modelMu.Lock()
			bs.overrideActive = isOverride
			bs.modelMu.Unlock()
		}

		// Declare all variables that are live across the retryPrompt goto target
		// here, before the label, so that Go's "no jumping over declarations" rule
		// is satisfied. They are assigned (not declared) inside the loop body.
		var (
			promptCtx       context.Context
			promptCancel    context.CancelFunc
			promptResp      acp.PromptResponse
			err             error
			promptStartedAt time.Time
			promptEndedAt   time.Time
			processDoneCh   <-chan struct{}
			connDoneCh      <-chan struct{}
			// inactivityWatchdogFired is set by the prompt inactivity watchdog when it
			// cancels the prompt because the agent stopped streaming (live-but-unresponsive).
			// The error-handling path below reads it to surface a recoverable message and
			// skip the crash-restart logic (the process is alive, not dead).
			inactivityWatchdogFired atomic.Bool
		)

	retryPrompt:
		// Reset the inactivity flag for this attempt (a goto retryPrompt reuses it).
		inactivityWatchdogFired.Store(false)
		// Create a prompt context that gets cancelled when the ACP process dies.
		// This ensures we fail fast instead of waiting for the ACP server's internal
		// 60-second control request timeout when the CLI subprocess has crashed.
		// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
		promptCtx, promptCancel = context.WithCancel(bs.ctx)
		// NOTE: no defer — we call promptCancel() explicitly after the prompt
		// returns so that (a) we clean up the health-monitor goroutine eagerly,
		// and (b) a goto back to retryPrompt doesn't accumulate extra defers.

		// Monitor ACP process health: if the connection's Done() channel closes
		// or the OS process exits (acpProcessDone), cancel the prompt context immediately.
		// The acpProcessDone channel provides faster detection than Done() because it
		// uses OS-level process liveness checks (signal 0) rather than waiting for
		// pipe EOF to propagate through the JSON-RPC transport layer.
		processDoneCh = bs.acpProcessDone // refresh on each retry (new process after restart)
		connDoneCh = nil                  // reset before assigning below
		if bs.acpConn != nil {
			connDoneCh = bs.acpConn.Done()
		} else if bs.sharedProcess != nil {
			connDoneCh = bs.sharedProcess.Done()
		}
		if connDoneCh != nil {
			go func() {
				select {
				case <-connDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP connection closed during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-processDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP process exited during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-promptCtx.Done():
					// Prompt completed normally or was cancelled for another reason
				}
			}()
		}

		// Monitor for a live-but-unresponsive agent: if the agent stops streaming any
		// updates for the configured window (and is not blocked on a UI prompt), cancel
		// the prompt so is_prompting clears and the user can resend. This catches the
		// "stuck, still responding" state that the process-death/connection monitors miss.
		bs.startPromptInactivityWatchdog(promptCtx, promptCancel, &inactivityWatchdogFired)

		// On retry after ACP crash, freshContextSessionID is from the old (dead)
		// connection; fall back to bs.acpID which holds the new session.
		acpSessionIDForPrompt := bs.acpID
		if freshContextSessionID != "" && !autoRetried {
			acpSessionIDForPrompt = freshContextSessionID
		}

		promptStartedAt = time.Now() // captured for after-phase processors
		if bs.sharedProcess != nil {
			promptResp, err = bs.sharedProcess.Prompt(promptCtx, acp.SessionId(acpSessionIDForPrompt), finalBlocks)
		} else {
			promptResp, err = bs.acpConn.Prompt(promptCtx, acp.PromptRequest{
				SessionId: acp.SessionId(acpSessionIDForPrompt),
				Prompt:    finalBlocks,
			})
		}
		promptCancel()             // cancel context to unblock the health-monitor goroutine
		promptEndedAt = time.Now() // captured for after-phase processors

		// Store token usage from the prompt response (if available).
		if promptResp.Usage != nil {
			bs.lastUsageMu.Lock()
			bs.lastUsage = promptResp.Usage
			bs.lastUsageMu.Unlock()
		}

		// Accumulate token usage for processor rerun tracking.
		if bs.processorManager != nil {
			if promptResp.Usage != nil {
				bs.processorManager.AccumulateTokenUsage(promptResp.Usage.TotalTokens)
			} else {
				// Fallback: estimate tokens from message text when ACP doesn't report usage.
				estimated := processors.EstimateTokens(message)
				// Also estimate from the agent's response if available.
				if bs.store != nil {
					if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
						agentMsg := session.GetLastAgentMessage(events)
						estimated += processors.EstimateTokens(agentMsg)
					}
				}
				if estimated > 0 {
					bs.processorManager.AccumulateTokenUsage(estimated)
				}
			}
		}

		// Mark prompt as complete BEFORE any further processing
		// This must happen before processNextQueuedMessage so the next message can be sent
		bs.promptMu.Lock()
		bs.isPrompting = false
		bs.promptStartTime = time.Time{}
		bs.lastResponseComplete = time.Now()
		bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
		bs.promptMu.Unlock()

		// Notify about streaming state change (prompt completed)
		if bs.onStreamingStateChanged != nil {
			bs.onStreamingStateChanged(bs.persistedID, false)
		}

		if bs.IsClosed() {
			return
		}

		// DEBUG: Log prompt completion sequence
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_sequence_start",
				"session_id", bs.persistedID,
				"observer_count", bs.ObserverCount(),
				"is_prompting", bs.IsPrompting())
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_start",
					"session_id", bs.persistedID)
			}
			bs.acpClient.FlushMarkdown()
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_done",
					"session_id", bs.persistedID)
			}
		}

		// Notify all observers
		eventCount := bs.GetEventCount()
		observerCount := bs.ObserverCount()
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_notify_start",
				"session_id", bs.persistedID,
				"event_count", eventCount,
				"observer_count", observerCount)
		}

		// sessionIdle becomes true only on the success path when the turn ended and
		// no further queued message was dispatched. It gates the on-completion periodic
		// idle hook invoked after OnComplete below.
		sessionIdle := false

		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("prompt_failed",
					"session_id", bs.persistedID,
					"error", err.Error(),
					"observer_count", observerCount)
			}

			// Check if the ACP process died (connection closed or OS process exited).
			// If so, attempt automatic restart rather than just showing an error.
			// We check both acpConn.Done() (JSON-RPC layer) and acpProcessDone
			// (OS-level process liveness) for faster detection.
			acpDead := false
			if bs.acpConn != nil {
				select {
				case <-bs.acpConn.Done():
					acpDead = true
				default:
				}
			} else if bs.sharedProcess != nil {
				select {
				case <-bs.sharedProcess.Done():
					acpDead = true
				default:
				}
			}
			if !acpDead && bs.acpProcessDone != nil {
				select {
				case <-bs.acpProcessDone:
					acpDead = true
				default:
				}
			}

			if inactivityWatchdogFired.Load() {
				// The agent stayed alive and connected but stopped streaming updates.
				// The watchdog already cancelled the prompt and is_prompting was cleared
				// above. Surface a recoverable message and do NOT auto-restart (the
				// process is healthy, not crashed) or auto-advance the queue (the next
				// queued message would likely wedge the same way).
				if bs.logger != nil {
					bs.logger.Warn("prompt_cancelled_by_inactivity_watchdog",
						"session_id", bs.persistedID)
				}
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("The AI agent stopped responding (no activity for a while), so the conversation was reset. Please resend your message. If this keeps happening, switch to another conversation and back to restart the agent.")
				})
			} else if acpDead && autoRetried {
				// The auto-retry already happened and the process crashed again.
				// Don't consume another restart slot — let the next user-triggered prompt
				// handle the restart. This ensures each user message uses at most one
				// restart slot, so MaxACPRestarts behaves predictably from the user's POV.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Please resend your message.")
				})
			} else if acpDead && bs.canRestartACP() {
				// First crash on this prompt — restart and automatically retry.
				restartInfo := bs.getRestartInfo()
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
				})
				if restartErr := bs.restartACPProcess(RestartReasonCrashDuringStream); restartErr != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + restartErr.Error() +
						". Please switch to another conversation and back to retry."
					if classified, ok := restartErr.(*ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = formatClassifiedError(classified)
					}
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError(errMsg)
					})
				} else {
					// Restart succeeded — automatically retry the prompt.
					autoRetried = true
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError("AI agent restarted. Retrying your message automatically...")
					})
					if bs.logger != nil {
						bs.logger.Info("Auto-retrying prompt after ACP restart during stream",
							"session_id", bs.persistedID)
					}
					// Re-acquire the prompting state so the retry runs under the
					// same invariants as the original prompt call.
					bs.promptMu.Lock()
					bs.isPrompting = true
					bs.promptStartTime = time.Now()
					bs.promptMu.Unlock()
					if bs.onStreamingStateChanged != nil {
						bs.onStreamingStateChanged(bs.persistedID, true)
					}
					goto retryPrompt
				}
			} else if acpDead {
				// ACP process died but restart limit exceeded — tell user to manually restart
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
				})
			} else {
				userFriendlyErr := formatACPError(err)
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(userFriendlyErr)
				})

				// Advance the queue for transient errors where the ACP process is
				// still healthy.  Skip queue processing for errors that indicate a
				// hard capacity or rate limit — sending the next queued message
				// immediately would cause the same failure again, creating a cascade
				// that drains the queue while showing a stream of identical errors.
				//
				// Context-too-large (413): all queued messages will fail until the
				//   user starts a fresh conversation — stop the queue.
				// Rate-limit: the API will reject the next message too — stop the
				//   queue; the keepalive-driven TryProcessQueuedMessage will retry
				//   once the session becomes idle and the delay has elapsed.
				if !isContextTooLargeError(err) && !isRateLimitError(err) {
					// Apply any config changes deferred during this turn before
					// dispatching the next queued message.
					bs.flushPendingConfig()
					bs.processNextQueuedMessage()
				}
			}
		} else {
			if bs.logger != nil {
				bs.logger.Debug("prompt_complete",
					"session_id", bs.persistedID,
					"event_count", eventCount,
					"observer_count", observerCount,
					"stop_reason", promptResp.StopReason)
			}
			bs.notifyObservers(func(o SessionObserver) {
				o.OnPromptComplete(eventCount)
			})

			// Apply any config changes deferred during this turn before dispatching
			// the next queued message, so the queued prompt runs under the new config.
			bs.flushPendingConfig()

			// Process next queued message if queue processing is enabled.
			// dispatched is true when another queued turn was started (the session is
			// not yet idle); it gates agentIdle after-phase processors below.
			dispatched := bs.processNextQueuedMessage()
			sessionIdle = !dispatched

			// Retry title generation if session still has no title.
			// This catches failed initial attempts (e.g. context deadline exceeded)
			// and prompts that arrived via paths that don't trigger title generation
			// (queue, MCP send_prompt, periodic).
			bs.retryTitleGenerationIfNeeded(message)

			// Async follow-up analysis (non-blocking)
			// This runs after prompt_complete so the user sees the response immediately
			// Note: 'message' is captured from the outer function scope (the user's prompt)
			isEndTurn := promptResp.StopReason == acp.StopReasonEndTurn
			if bs.actionButtonsConfig.IsEnabled() && isEndTurn {
				// Get the agent message from stored events (events are persisted immediately)
				var agentMessage string
				if bs.store != nil {
					if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
						agentMessage = session.GetLastAgentMessage(events)
					}
				}
				if agentMessage != "" {
					// Skip follow-up analysis if there are queued messages that will be processed immediately
					// (no delay configured). The suggestions would be stale by the time they arrive.
					if bs.hasImmediateQueuedMessages() {
						bs.logger.Debug("follow-up analysis: skipped due to pending immediate queue messages")
					} else {
						go bs.analyzeFollowUpQuestions(message, agentMessage)
					}
				}
			}

			// Apply after-phase processors (agentResponded + agentIdle pipeline).
			// Runs after follow-up analysis so all event state is fully persisted.
			// This is synchronous — processors are fast (command execution with timeouts).
			// sessionIdle is true when no further queued message was dispatched, so
			// agentIdle processors fire only once the queue has drained.
			if bs.processorManager != nil {
				bs.applyAfterProcessors(bs.ctx, message, meta.SenderID,
					string(promptResp.StopReason), promptStartedAt, promptEndedAt, promptResp, !dispatched)
			}
		}

		// Invoke OnComplete callback if set.
		// Called after all observers have been notified and state is consistent,
		// so the caller can accurately track the final outcome (nil = success, non-nil = failure).
		if meta.OnComplete != nil {
			meta.OnComplete(err)
		}

		// Notify the on-completion periodic hook once the agent has stopped and the
		// session is fully idle. Fired after OnComplete so any iteration accounting
		// (RecordSent / auto-stop) is applied before the next run is armed.
		if sessionIdle && bs.onTurnIdle != nil {
			bs.onTurnIdle(bs.persistedID)
		}

		// Self-destruct: if the agent requested deletion of its own conversation
		// during this turn, delete it now that the turn has fully completed and
		// observers have seen the final response. Run asynchronously so this
		// goroutine can unwind before the session (and its ACP connection) is
		// torn down by the deletion path.
		if bs.IsSelfDestructRequested() && bs.onSelfDestruct != nil {
			if bs.logger != nil {
				bs.logger.Info("self_destruct_triggered", "session_id", bs.persistedID)
			}
			go bs.onSelfDestruct(bs.persistedID)
		}
	}()

	return nil
}

// analyzeFollowUpQuestions asynchronously analyzes an agent message for follow-up questions.
// It uses the auxiliary conversation to identify questions and sends suggested responses
// to observers via OnActionButtons. This is non-blocking and runs in a goroutine.
// userPrompt provides context about what the user asked.
func (bs *BackgroundSession) analyzeFollowUpQuestions(userPrompt, agentMessage string) {
	// Prevent concurrent analysis — only one goroutine should analyze at a time.
	// If another analysis is already in progress, skip this one.
	// The in-progress analysis will produce the same results since the session
	// state hasn't changed (no new prompts while both are running).
	if !bs.followUpInProgress.CompareAndSwap(false, true) {
		if bs.logger != nil {
			bs.logger.Debug("follow-up analysis: skipped, another analysis already in progress")
		}
		return
	}
	defer bs.followUpInProgress.Store(false)

	// Use a generous timeout for the auxiliary follow-up prompt.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Check if session is still valid before starting
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis skipped: session closed")
		return
	}

	bs.logger.Debug("follow-up analysis: starting",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage),
		"workspace_uuid", bs.workspaceUUID)

	// Check if we have an auxiliary manager
	if bs.auxiliaryManager == nil {
		bs.logger.Debug("follow-up analysis: no auxiliary manager available")
		return
	}

	// Use the workspace-scoped auxiliary conversation to analyze the message
	suggestions, err := bs.auxiliaryManager.AnalyzeFollowUpQuestions(ctx, bs.workspaceUUID, userPrompt, agentMessage)
	if err != nil {
		bs.logger.Debug("follow-up analysis failed",
			"error", err,
			"workspace_uuid", bs.workspaceUUID)
		return
	}

	if len(suggestions) == 0 {
		bs.logger.Debug("follow-up analysis: no suggestions found")
		return
	}

	// Check again if session is still valid and not prompting
	// If the user has already sent a new message, don't show stale suggestions
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis: session closed before sending buttons")
		return
	}
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up analysis: session is prompting, discarding buttons")
		return
	}

	// Convert auxiliary suggestions to ActionButton format
	buttons := make([]ActionButton, 0, len(suggestions))
	for _, s := range suggestions {
		buttons = append(buttons, ActionButton{
			Label:    s.Label,
			Response: s.Value,
		})
	}

	// Cache in memory
	bs.actionButtonsMu.Lock()
	bs.cachedActionButtons = buttons
	bs.actionButtonsMu.Unlock()

	// Persist to disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		// Convert to session.ActionButton for storage
		sessionButtons := make([]session.ActionButton, len(buttons))
		for i, b := range buttons {
			sessionButtons[i] = session.ActionButton{
				Label:    b.Label,
				Response: b.Response,
			}
		}
		eventCount := bs.GetEventCount()
		if err := abStore.Set(sessionButtons, int64(eventCount)); err != nil {
			bs.logger.Debug("failed to persist action buttons", "error", err)
		}
	}

	bs.logger.Debug("follow-up analysis: sending buttons to observers", "count", len(buttons))
	bs.notifyObservers(func(o SessionObserver) {
		o.OnActionButtons(buttons)
	})
}

// promptOriginFromSenderID maps a PromptMeta.SenderID to the canonical origin tag used
// by after-phase processors in their excludeOrigins filter.
//
// Canonical origin strings (kept in sync with processors.AfterProcessorInput.Origin docs):
//
//	"user"             – direct user prompt from a WebSocket client
//	"queue"            – message injected via the queue (includes mcp-send-prompt, which
//	                     cannot be distinguished from regular queue messages at this layer)
//	"periodic-runner"  – message sent by the periodic runner goroutine
//
// If a new origin is introduced (e.g. mcp-send-prompt queued with a dedicated SenderID),
// add it here and update the AfterProcessorInput.Origin godoc in types.go.
func promptOriginFromSenderID(senderID string) string {
	switch senderID {
	case "periodic-runner":
		return "periodic-runner"
	case "queue":
		// Covers both direct queue messages and MCP mitto_conversation_send_prompt,
		// which are indistinguishable at this layer (both use SenderID="queue").
		// TODO: when mcp-send-prompt gets a dedicated SenderID, add a case here.
		return "queue"
	default:
		// Empty SenderID (Prompt/PromptWithImages) or a WebSocket client UUID.
		return "user"
	}
}

// applyAfterProcessors runs the after-phase processor pipeline (agentResponded + agentIdle)
// after an ACP turn completes. It is called synchronously in the prompt goroutine, after
// follow-up suggestion analysis, so all events are already flushed and persisted at this point.
// sessionIdle reports whether the queue was drained after this turn; it gates agentIdle
// processors so they fire only once the agent has finished its burst of work.
//
// Results are dispatched as follows:
//   - Notifications → bs.UINotify (fire-and-forget toast)
//   - ActionButtons → appended to the existing action-buttons cache/store and broadcast
//   - UserDataPatch → merged into the session's user-data file
//   - Errors        → logged as warnings (non-fatal)
func (bs *BackgroundSession) applyAfterProcessors(
	ctx context.Context,
	userPrompt string,
	senderID string,
	stopReason string,
	startedAt, endedAt time.Time,
	promptResp acp.PromptResponse,
	sessionIdle bool,
) {
	// Build agent messages from the last persisted agent message.
	var agentMessages []string
	if bs.store != nil {
		if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
			if msg := session.GetLastAgentMessage(events); msg != "" {
				agentMessages = []string{msg}
			}
		}
	}

	// Build token usage snapshot.
	// Use actual ACP usage when available; otherwise estimate from message text
	// so that cadence token thresholds (everyNTokens) can still be met.
	var tokenUsage *processors.AfterTokenUsage
	if promptResp.Usage != nil {
		tokenUsage = &processors.AfterTokenUsage{
			Input:  int64(promptResp.Usage.InputTokens),
			Output: int64(promptResp.Usage.OutputTokens),
			Total:  int64(promptResp.Usage.TotalTokens),
		}
	} else {
		// Fallback: estimate tokens from user prompt + agent response text.
		estimated := int64(processors.EstimateTokens(userPrompt))
		for _, msg := range agentMessages {
			estimated += int64(processors.EstimateTokens(msg))
		}
		if estimated > 0 {
			tokenUsage = &processors.AfterTokenUsage{
				Total: estimated,
			}
		}
	}

	// Resolve session directory for processor state persistence (cadence + match:first).
	var sessionDir string
	if bs.store != nil && bs.persistedID != "" {
		sessionDir = bs.store.SessionDir(bs.persistedID)
	}

	input := processors.AfterProcessorInput{
		SessionID:     bs.persistedID,
		SessionDir:    sessionDir,
		WorkspaceUUID: bs.workspaceUUID,
		WorkingDir:    bs.workingDir,
		Origin:        promptOriginFromSenderID(senderID),
		StopReason:    stopReason,
		UserPrompt:    userPrompt,
		AgentMessages: agentMessages,
		ToolCalls:     nil, // TODO: populate from turn events in a future pass
		TokenUsage:    tokenUsage,
		StartedAt:     startedAt,
		EndedAt:       endedAt,
		SessionIdle:   sessionIdle,
	}

	result := bs.processorManager.ApplyAfter(ctx, input)

	// Log non-fatal processor errors as warnings.
	for _, pe := range result.Errors {
		if bs.logger != nil {
			bs.logger.Warn("after-phase processor error (non-fatal)",
				"processor", pe.ProcessorName,
				"error", pe.Error)
		}
	}

	// Dispatch notifications via UINotify (uses OnNotification observer path).
	for _, n := range result.Notifications {
		req := UINotifyRequest{
			Title:   n.Title,
			Message: n.Message,
			Style:   n.Style,
		}
		if err := bs.UINotify(req); err != nil && bs.logger != nil {
			bs.logger.Warn("after-phase: failed to dispatch notification",
				"title", n.Title,
				"error", err)
		}
	}

	// Append action buttons to the existing store and notify observers.
	if len(result.ActionButtons) > 0 {
		buttons := make([]ActionButton, 0, len(result.ActionButtons))
		for _, ab := range result.ActionButtons {
			buttons = append(buttons, ActionButton{
				Label:    ab.Label,
				Response: ab.Prompt,
			})
		}

		// Merge with any existing cached buttons (e.g. from follow-up analysis).
		bs.actionButtonsMu.Lock()
		merged := make([]ActionButton, 0, len(bs.cachedActionButtons)+len(buttons))
		merged = append(merged, bs.cachedActionButtons...)
		merged = append(merged, buttons...)
		bs.cachedActionButtons = merged
		bs.actionButtonsMu.Unlock()

		// Persist to disk.
		if bs.store != nil && bs.persistedID != "" {
			abStore := bs.store.ActionButtons(bs.persistedID)
			sessionButtons := make([]session.ActionButton, len(merged))
			for i, b := range merged {
				sessionButtons[i] = session.ActionButton{Label: b.Label, Response: b.Response}
			}
			if err := abStore.Set(sessionButtons, int64(bs.GetEventCount())); err != nil && bs.logger != nil {
				bs.logger.Debug("after-phase: failed to persist action buttons", "error", err)
			}
		}

		bs.notifyObservers(func(o SessionObserver) {
			o.OnActionButtons(merged)
		})
	}

	// Merge UserDataPatch into the session's user-data file.
	if len(result.UserDataPatch) > 0 && bs.store != nil && bs.persistedID != "" {
		// Read current user data.
		current, err := bs.store.GetUserData(bs.persistedID)
		if err != nil {
			if bs.logger != nil {
				bs.logger.Warn("after-phase: failed to read user data for patch", "error", err)
			}
		} else {
			// Build a name→value map of existing attributes for fast lookup.
			attrMap := make(map[string]string, len(current.Attributes))
			for _, a := range current.Attributes {
				attrMap[a.Name] = a.Value
			}
			// Apply patch (later processors override earlier on key collision).
			patchedKeys := 0
			for k, v := range result.UserDataPatch {
				attrMap[k] = v
				patchedKeys++
			}
			// Reconstruct ordered slice: keep existing order, then append new keys.
			newAttrs := make([]session.UserDataAttribute, 0, len(attrMap))
			seen := make(map[string]bool)
			for _, a := range current.Attributes {
				newAttrs = append(newAttrs, session.UserDataAttribute{Name: a.Name, Value: attrMap[a.Name]})
				seen[a.Name] = true
			}
			for k, v := range result.UserDataPatch {
				if !seen[k] {
					newAttrs = append(newAttrs, session.UserDataAttribute{Name: k, Value: v})
				}
			}
			if err := bs.store.SetUserData(bs.persistedID, &session.UserData{Attributes: newAttrs}); err != nil {
				if bs.logger != nil {
					bs.logger.Warn("after-phase: failed to persist user data patch",
						"patched_keys", patchedKeys,
						"error", err)
				}
			} else if bs.logger != nil {
				bs.logger.Debug("after-phase: user data patched",
					"patched_keys", patchedKeys,
					"total_keys", len(newAttrs))
			}
		}
	}
}

// TriggerFollowUpSuggestions triggers follow-up suggestions analysis for a resumed session.
// This reads the last agent message from stored events and analyzes it asynchronously.
// It only works for sessions with message history and when follow-up suggestions are enabled.
// If cached action buttons already exist, they are loaded and no new analysis is triggered.
// This is non-blocking and runs the analysis in a goroutine.
// Returns true if the analysis was triggered or cached buttons were loaded, false if skipped.
func (bs *BackgroundSession) TriggerFollowUpSuggestions() bool {
	// Check if follow-up suggestions are enabled
	if !bs.actionButtonsConfig.IsEnabled() {
		bs.logger.Debug("follow-up suggestions: disabled in config")
		return false
	}

	// Check if session is prompting (don't interfere with active prompts)
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up suggestions: session is prompting, skipping")
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		bs.logger.Debug("follow-up suggestions: session is closed, skipping")
		return false
	}

	// Need store to read events
	if bs.store == nil {
		bs.logger.Debug("follow-up suggestions: no store, skipping")
		return false
	}

	// Check if we already have cached action buttons (from disk)
	// If so, load them into memory cache - no need to re-analyze
	cachedButtons := bs.GetActionButtons()
	if len(cachedButtons) > 0 {
		bs.logger.Debug("follow-up suggestions: using cached buttons from disk",
			"button_count", len(cachedButtons))
		return true
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		bs.logger.Debug("follow-up suggestions: failed to read events", "error", err)
		return false
	}

	// Get the last user prompt and agent message from stored events
	userPrompt := session.GetLastUserPrompt(events)
	agentMessage := session.GetLastAgentMessage(events)
	if agentMessage == "" {
		bs.logger.Debug("follow-up suggestions: no agent message found in history")
		return false
	}

	bs.logger.Debug("follow-up suggestions: triggering analysis for resumed session",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Check if analysis is already in progress (e.g., from prompt completion racing with session resume)
	if bs.followUpInProgress.Load() {
		bs.logger.Debug("follow-up suggestions: analysis already in progress, skipping")
		return true
	}

	// Run analysis asynchronously
	go bs.analyzeFollowUpQuestions(userPrompt, agentMessage)
	return true
}

// clearActionButtons clears the cached action buttons from memory and disk.
// Called when new conversation activity occurs (user sends a prompt) because
// the existing suggestions become stale—they were generated for the previous
// agent response, not the upcoming one. New suggestions will be generated
// when the agent completes its next response.
func (bs *BackgroundSession) clearActionButtons() {
	// Clear in-memory cache
	bs.actionButtonsMu.Lock()
	hadButtons := len(bs.cachedActionButtons) > 0
	bs.cachedActionButtons = nil
	bs.actionButtonsMu.Unlock()

	// Clear from disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		if err := abStore.Clear(); err != nil && bs.logger != nil {
			bs.logger.Debug("failed to clear action buttons from disk", "error", err)
		}
	}

	// Notify observers that buttons are cleared (send empty array)
	if hadButtons {
		bs.notifyObservers(func(o SessionObserver) {
			o.OnActionButtons([]ActionButton{})
		})
	}
}

// GetActionButtons returns the current action buttons.
// Uses a two-tier lookup: memory cache first (fast), then disk (persistent).
// The disk fallback ensures suggestions survive server restarts.
// Returns nil if no suggestions are available.
func (bs *BackgroundSession) GetActionButtons() []ActionButton {
	// Check in-memory cache first
	bs.actionButtonsMu.RLock()
	if bs.cachedActionButtons != nil {
		result := make([]ActionButton, len(bs.cachedActionButtons))
		copy(result, bs.cachedActionButtons)
		bs.actionButtonsMu.RUnlock()
		return result
	}
	bs.actionButtonsMu.RUnlock()

	// Fall back to disk
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}

	abStore := bs.store.ActionButtons(bs.persistedID)
	buttons, err := abStore.Get()
	if err != nil {
		if bs.logger != nil {
			bs.logger.Debug("failed to read action buttons from disk", "error", err)
		}
		return nil
	}

	// Convert session.ActionButton to web.ActionButton
	result := make([]ActionButton, len(buttons))
	for i, b := range buttons {
		result[i] = ActionButton{
			Label:    b.Label,
			Response: b.Response,
		}
	}

	// Cache in memory for future access
	if len(result) > 0 {
		bs.actionButtonsMu.Lock()
		bs.cachedActionButtons = result
		bs.actionButtonsMu.Unlock()
	}

	return result
}

// Cancel cancels the current prompt and resets the prompting state.
// This sends a cancel notification to the ACP agent and resets the isPrompting flag
// so the session can accept new prompts even if the agent doesn't respond to the cancel.
func (bs *BackgroundSession) Cancel() error {
	// Dismiss any active UI prompt first (MCP tool questions, permissions, etc.)
	// This ensures the UI is cleaned up when the user presses Stop.
	bs.DismissActiveUIPrompt()

	// Reset prompting state regardless of whether cancel succeeds
	// This ensures the session can accept new prompts even if the agent is unresponsive
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	if wasPrompting {
		// Flush any buffered content before notifying completion
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
		}

		// Notify observers that the prompt was cancelled
		eventCount := bs.GetEventCount()
		bs.notifyObservers(func(o SessionObserver) {
			o.OnPromptComplete(eventCount)
		})

		if bs.logger != nil {
			bs.logger.Info("Session cancelled, prompting state reset")
		}
	}

	// Send cancel notification to ACP agent (best effort)
	var cancelErr error
	if bs.sharedProcess != nil {
		cancelErr = bs.sharedProcess.Cancel(bs.ctx, acp.SessionId(bs.acpID))
	} else if bs.acpConn != nil {
		cancelErr = bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
			SessionId: acp.SessionId(bs.acpID),
		})
	}

	// Apply any config changes deferred during the cancelled turn now that the
	// session is idle.
	if wasPrompting {
		bs.flushPendingConfig()
	}

	return cancelErr
}

// ForceReset forcefully resets the session's prompting state.
// This is used when the agent is completely unresponsive and Cancel doesn't work.
// It resets the isPrompting flag, flushes any buffered content, and notifies observers.
// Unlike Cancel, this does NOT send a cancel notification to the agent.
func (bs *BackgroundSession) ForceReset() {
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	if !wasPrompting {
		if bs.logger != nil {
			bs.logger.Debug("ForceReset called but session was not prompting")
		}
		return
	}

	// Flush any buffered content
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}

	// Notify observers that the prompt was forcefully reset
	eventCount := bs.GetEventCount()
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPromptComplete(eventCount)
	})

	// Apply any config changes deferred during the reset turn now that the session
	// is idle (best effort; the RPC fails fast if the agent connection is dead).
	bs.flushPendingConfig()

	if bs.logger != nil {
		bs.logger.Warn("Session forcefully reset due to unresponsive agent")
	}
}

// --- Queue processing methods ---

// hasImmediateQueuedMessages returns true if there are queued messages that will be processed
// immediately (queue processing is enabled, queue is not empty, and no delay is configured).
// This is used to skip follow-up suggestion analysis when the suggestions would be stale
// by the time they arrive (because the next message will be sent immediately).
func (bs *BackgroundSession) hasImmediateQueuedMessages() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if there's a delay configured - if so, suggestions might still be useful
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		return false
	}

	// Check if we have a store and queue
	if bs.store == nil || bs.persistedID == "" {
		return false
	}

	// Check if queue has messages
	queue := bs.store.Queue(bs.persistedID)
	queueLen, err := queue.Len()
	if err != nil {
		return false
	}

	return queueLen > 0
}

// processNextQueuedMessage checks the queue and sends the next message if queue processing is enabled.
// This is called after a prompt completes and applies the configured delay before sending.
// It returns true if a queued message was popped and dispatched (a new turn is starting,
// so the session is NOT idle), and false if the queue was empty/disabled (the session is idle).
func (bs *BackgroundSession) processNextQueuedMessage() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		bs.restoreBaselineIfOverride()
		return false
	}

	// Get the queue for this session
	if bs.store == nil {
		bs.restoreBaselineIfOverride()
		return false
	}
	queue := bs.store.Queue(bs.persistedID)

	// Pop the next message from the queue
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty: restore the baseline model if a per-prompt override is active.
		bs.restoreBaselineIfOverride()
		return false
	}

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	// Apply delay if configured
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		time.Sleep(time.Duration(bs.queueConfig.GetDelaySeconds()) * time.Second)
	}

	bs.sendQueuedMessage(queue, msg)
	return true
}

// TryProcessQueuedMessage checks if the session is idle and enough time has passed since the last
// response, then processes the next queued message. This is used for startup initialization
// and periodic queue checking. Returns true if a message was sent.
func (bs *BackgroundSession) TryProcessQueuedMessage() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if session is currently prompting
	if bs.IsPrompting() {
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		return false
	}

	// Get the queue for this session
	if bs.store == nil {
		return false
	}
	queue := bs.store.Queue(bs.persistedID)

	// Check if queue has messages
	queueLen, err := queue.Len()
	if err != nil || queueLen == 0 {
		return false
	}

	// Check if delay has elapsed since last response
	delaySeconds := 0
	if bs.queueConfig != nil {
		delaySeconds = bs.queueConfig.GetDelaySeconds()
	}

	if delaySeconds > 0 {
		lastResponse := bs.GetLastResponseCompleteTime()
		// If lastResponse is zero, we can proceed (no previous response means agent is idle)
		if !lastResponse.IsZero() {
			elapsed := time.Since(lastResponse)
			if elapsed < time.Duration(delaySeconds)*time.Second {
				// Not enough time has passed
				return false
			}
		}
	}

	// Pop and send the next message
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty or error
		return false
	}

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	bs.sendQueuedMessage(queue, msg)
	return true
}

// sendQueuedMessage sends a message that was popped from the queue.
func (bs *BackgroundSession) sendQueuedMessage(queue *session.Queue, msg session.QueuedMessage) {
	if bs.logger != nil {
		bs.logger.Info("Sending queued message", "session_id", bs.persistedID, "message_id", msg.ID, "message", msg.Message)
	}
	// Get updated queue length for notification
	queueLen, _ := queue.Len()

	// Notify observers about queue update (message removed)
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLen, "removed", msg.ID)
	})

	// Send the queued message
	meta := PromptMeta{
		SenderID:   "queue",
		PromptID:   msg.ID,
		ImageIDs:   msg.ImageIDs,
		Arguments:  msg.Arguments,
		PromptName: msg.PromptName,
	}
	if err := bs.PromptWithMeta(msg.Message, meta); err != nil {
		if bs.logger != nil {
			bs.logger.Error("Failed to send queued message", "error", err, "message_id", msg.ID)
		}
		bs.notifyObservers(func(o SessionObserver) {
			o.OnError("Failed to send queued message: " + err.Error())
		})
		return
	}

	// Notify observers that the message was sent
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSent(msg.ID)
	})
}

// NotifyQueueUpdated notifies all observers about a queue state change.
// This is called by the queue API handlers when the queue is modified externally.
func (bs *BackgroundSession) NotifyQueueUpdated(queueLength int, action string, messageID string) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLength, action, messageID)
	})
}

// NotifyQueueReordered notifies all observers about a queue reorder.
// This is called by the queue API handlers when the queue order changes.
func (bs *BackgroundSession) NotifyQueueReordered(messages []session.QueuedMessage) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueReordered(messages)
	})
}

// --- Callback methods for WebClient ---

func (bs *BackgroundSession) onAgentMessage(seq int64, html string) {
	if bs.IsClosed() {
		return
	}

	htmlLen := len(html)

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeAgentMessage,
			Timestamp: time.Now(),
			Data:      session.AgentMessageData{Text: html},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist agent message", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist agent message", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	observerCount := bs.ObserverCount()

	// Enhanced logging for debugging message content issues
	if bs.logger != nil {
		if htmlLen > 1000 {
			// Large message - log with preview
			preview := html
			if len(preview) > 200 {
				preview = html[:100] + "..." + html[htmlLen-100:]
			}
			bs.logger.Debug("agent_message_to_observers_large",
				"seq", seq,
				"html_len", htmlLen,
				"observer_count", observerCount,
				"session_id", bs.persistedID,
				"preview", preview)
		} else if observerCount > 1 {
			bs.logger.Debug("Notifying multiple observers of agent message",
				"observer_count", observerCount,
				"html_len", htmlLen,
				"seq", seq)
		}
	}

	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(seq, html)
	})
}

func (bs *BackgroundSession) onAgentThought(seq int64, text string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeAgentThought,
			Timestamp: time.Now(),
			Data:      session.AgentThoughtData{Text: text},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist agent thought", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist agent thought", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentThought(seq, text)
	})
}

func (bs *BackgroundSession) onToolCall(seq int64, id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeToolCall,
			Timestamp: time.Now(),
			Data: session.ToolCallData{
				ToolCallID: id,
				Title:      title,
				Status:     status,
			},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist tool call", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist tool call", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(seq, id, title, status)
	})
}

// onMittoToolCall is called when any mitto_* tool call is detected.
// It registers a correlation ID (requestID) with the global MCP server to associate
// MCP tool requests with this ACP session. This enables session-aware tool behavior
// even when the MCP client doesn't know which session it's operating in.
// Note: requestID here is a correlation ID, not to be confused with session_id.

func (bs *BackgroundSession) onMittoToolCall(requestID string) {
	if bs.IsClosed() {
		return
	}

	if bs.globalMcpServer == nil {
		if bs.logger != nil {
			bs.logger.Debug("Cannot register mitto tool request: no global MCP server",
				"request_id", requestID,
				"session_id", bs.persistedID)
		}
		return
	}

	// Register the pending request with the global MCP server
	// This allows the MCP handler to correlate the request_id with this session
	bs.globalMcpServer.RegisterPendingRequest(requestID, bs.persistedID)

	if bs.logger != nil {
		bs.logger.Debug("Registered mitto tool request",
			"request_id", requestID,
			"session_id", bs.persistedID)
	}
}

func (bs *BackgroundSession) onToolUpdate(seq int64, id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeToolCallUpdate,
			Timestamp: time.Now(),
			Data: session.ToolCallUpdateData{
				ToolCallID: id,
				Status:     status,
			},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist tool call update", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist tool call update", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(seq, id, status)
	})
}

func (bs *BackgroundSession) onPlan(seq int64, entries []PlanEntry) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		// Convert web.PlanEntry to session.PlanEntry
		sessionEntries := make([]session.PlanEntry, len(entries))
		for i, entry := range entries {
			sessionEntries[i] = session.PlanEntry{
				Content:  entry.Content,
				Priority: entry.Priority,
				Status:   entry.Status,
			}
		}
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypePlan,
			Timestamp: time.Now(),
			Data:      session.PlanData{Entries: sessionEntries},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist plan", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist plan", "seq", seq, "error", err)
			}
		}
	}

	// Cache plan state in SessionManager for restoration on conversation switch
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, entries)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan(seq, entries)
	})
}

func (bs *BackgroundSession) onFileWrite(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeFileWrite,
			Timestamp: time.Now(),
			Data:      session.FileOperationData{Path: path, Size: size},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist file write", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist file write", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(seq, path, size)
	})
}

func (bs *BackgroundSession) onFileRead(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeFileRead,
			Timestamp: time.Now(),
			Data:      session.FileOperationData{Path: path, Size: size},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist file read", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist file read", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(seq, path, size)
	})
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if bs.IsClosed() {
		bs.logger.Debug("permission_request_rejected", "reason", "session_closed")
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	bs.logger.Debug("permission_request_received",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"auto_approve", bs.autoApprove,
		"has_observers", bs.HasObservers(),
		"options_count", len(params.Options))

	// Check if auto-approve is enabled (global flag OR per-session setting)
	autoApprove := bs.autoApprove
	if !autoApprove && bs.store != nil && bs.persistedID != "" {
		// Check per-session auto-approve flag
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
			autoApprove = session.GetFlagValue(meta.AdvancedSettings, session.FlagAutoApprovePermissions)
			if autoApprove {
				bs.logger.Debug("permission_using_session_auto_approve",
					"title", title,
					"tool_call_id", params.ToolCall.ToolCallId,
					"session_id", bs.persistedID)
			}
		}
	}

	if autoApprove {
		resp := mittoAcp.AutoApprovePermission(params.Options)
		selectedOption := ""
		if resp.Outcome.Selected != nil {
			selectedOption = string(resp.Outcome.Selected.OptionId)
		}
		bs.logger.Info("permission_auto_approved",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"selected_option", selectedOption)
		// Record the permission decision
		if bs.recorder != nil && resp.Outcome.Selected != nil {
			bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "auto_approved")
		}
		return resp, nil
	}

	// Check if we have any observers to show the permission dialog
	hasObservers := bs.HasObservers()
	if !hasObservers {
		bs.logger.Warn("permission_cancelled",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"reason", "no_observers")
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Convert ACP permission options to unified UIPromptOptions
	options := make([]UIPromptOption, len(params.Options))
	for i, opt := range params.Options {
		// Determine button style based on option kind
		var style UIPromptOptionStyle
		switch opt.Kind {
		case acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways:
			style = UIPromptOptionStyleSuccess
		case acp.PermissionOptionKindRejectOnce:
			style = UIPromptOptionStyleDanger
		default:
			style = UIPromptOptionStyleSecondary
		}

		options[i] = UIPromptOption{
			ID:    string(opt.OptionId),
			Label: opt.Name,
			Kind:  string(opt.Kind),
			Style: style,
		}
	}

	// Create a UIPromptRequest for the permission dialog
	toolCallID := string(params.ToolCall.ToolCallId)
	promptReq := UIPromptRequest{
		RequestID:      toolCallID,
		Type:           UIPromptTypePermission,
		Question:       "Permission requested",
		Title:          title,
		Options:        options,
		TimeoutSeconds: 300, // 5 minute timeout for permissions
		Blocking:       true,
		ToolCallID:     toolCallID,
	}

	bs.logger.Debug("permission_showing_ui_prompt",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"option_count", len(options))

	// Use the unified UIPrompt system to show the permission dialog and wait for response
	resp, err := bs.UIPrompt(ctx, promptReq)
	if err != nil {
		bs.logger.Warn("permission_prompt_error",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"error", err)
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Handle timeout
	if resp.TimedOut {
		bs.logger.Warn("permission_timed_out",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId)
		if bs.recorder != nil {
			bs.recorder.RecordPermission(title, "", "timed_out")
		}
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Convert the UIPromptResponse back to ACP permission response
	bs.logger.Info("permission_user_selected",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"selected_option", resp.OptionID)

	// Record the permission decision
	if bs.recorder != nil {
		bs.recorder.RecordPermission(title, resp.OptionID, "user_selected")
	}

	// Build ACP response
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: acp.PermissionOptionId(resp.OptionID),
			},
		},
	}, nil
}

// onAvailableCommands handles the available slash commands update from the agent.
// It stores the commands and notifies all observers.
func (bs *BackgroundSession) onAvailableCommands(commands []AvailableCommand) {
	if bs.IsClosed() {
		return
	}

	// Store the commands (sorted alphabetically by name)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	bs.availableCommandsMu.Lock()
	bs.availableCommands = commands
	bs.availableCommandsMu.Unlock()

	if bs.logger != nil {
		// Build list of command names for logging
		commandNames := make([]string, len(commands))
		for i, cmd := range commands {
			commandNames[i] = "/" + cmd.Name
		}
		bs.logger.Debug("Available slash commands updated",
			"count", len(commands),
			"commands", commandNames)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAvailableCommandsUpdated(commands)
	})
}

// AvailableCommands returns the current list of available slash commands.
// The commands are sorted alphabetically by name.
func (bs *BackgroundSession) AvailableCommands() []AvailableCommand {
	bs.availableCommandsMu.RLock()
	defer bs.availableCommandsMu.RUnlock()

	// Return a copy to avoid mutation
	if bs.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(bs.availableCommands))
	copy(result, bs.availableCommands)
	return result
}

// onCurrentModeChanged handles the session mode change notification from the agent.
// This updates the stored config option and notifies observers.
// This is called for legacy modes API - converts to config option format internally.
func (bs *BackgroundSession) onCurrentModeChanged(modeID string) {
	if bs.IsClosed() {
		return
	}

	// Update the mode config option's current value
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == ConfigOptionCategoryMode {
			bs.configOptions[i].CurrentValue = modeID
			break
		}
	}
	bs.configMu.Unlock()

	// Persist to metadata
	bs.persistConfigValue(ConfigOptionCategoryMode, modeID)

	if bs.logger != nil {
		bs.logger.Debug("Session mode changed (via agent)",
			"mode_id", modeID)
	}

	// Notify callback - use "mode" as the configID for legacy mode changes
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, ConfigOptionCategoryMode, modeID)
	}
}

// setSessionModes converts legacy modes API response to config options format.
// This allows transparent support for both legacy modes and newer configOptions.
func (bs *BackgroundSession) setSessionModes(modes *acp.SessionModeState) {
	if modes == nil {
		return
	}

	// Convert legacy modes to a single "mode" config option
	options := make([]SessionConfigOptionValue, len(modes.AvailableModes))
	for i, m := range modes.AvailableModes {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		options[i] = SessionConfigOptionValue{
			Value:       string(m.Id),
			Name:        m.Name,
			Description: desc,
		}
	}

	modeOption := SessionConfigOption{
		ID:           ConfigOptionCategoryMode, // Use "mode" as ID for legacy modes
		Name:         "Mode",
		Description:  "Session operating mode",
		Category:     ConfigOptionCategoryMode,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: string(modes.CurrentModeId),
		Options:      options,
	}

	bs.configMu.Lock()
	bs.configOptions = []SessionConfigOption{modeOption}
	bs.usesLegacyModes = true
	bs.configMu.Unlock()

	// Persist initial value to metadata
	bs.persistConfigValue(ConfigOptionCategoryMode, string(modes.CurrentModeId))
}

// setAgentModels converts agent model state to a "model" config option.
// This allows model switching to reuse the config option infrastructure.
func (bs *BackgroundSession) setAgentModels(models *acp.UnstableSessionModelState) {
	bs.agentModels = models
	if models == nil || len(models.AvailableModels) == 0 {
		return
	}

	// Convert models to config option values
	options := modelsToConfigOptions(models)

	// Start with the agent's reported current model.
	// Pre-apply any matching constraint to local state immediately, so the UI shows
	// the desired model from the very first acp_started message — before the async
	// RPC in applyConfigConstraints completes. agentModels.CurrentModelId is NOT
	// updated here; applyConfigConstraints compares against it to know whether the
	// agent-side change still needs to happen.
	currentValue := string(models.CurrentModelId)
	if constraint, ok := bs.acpServerConstraints[ConfigOptionCategoryModel]; ok && constraint != nil && constraint.Pattern != "" {
		if matched := matchConstraintOption(constraint, options); matched != "" && matched != currentValue {
			if bs.logger != nil {
				bs.logger.Debug("ACP server constraint: pre-applying model to local state",
					"category", ConfigOptionCategoryModel,
					"agent_model", currentValue,
					"desired_model", matched)
			}
			currentValue = matched
		}
	}

	modelOption := SessionConfigOption{
		ID:           ConfigOptionCategoryModel,
		Name:         "Model",
		Description:  "AI model for this session (UNSTABLE)",
		Category:     ConfigOptionCategoryModel,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: currentValue,
		Options:      options,
	}

	bs.configMu.Lock()
	// Remove any existing model option, then append the new one
	filtered := make([]SessionConfigOption, 0, len(bs.configOptions)+1)
	for _, opt := range bs.configOptions {
		if opt.Category != ConfigOptionCategoryModel {
			filtered = append(filtered, opt)
		}
	}
	bs.configOptions = append(filtered, modelOption)
	bs.configMu.Unlock()

	// Initialize baselineModel from persisted metadata (survive suspend/resume) or from the
	// agent's reported current model. Only set when empty so a prior call isn't overwritten.
	// applyConfigConstraints (called async below) will update baseline via SetConfigOption
	// if a constraint selects a different model.
	bs.modelMu.Lock()
	if bs.baselineModel == "" {
		baseline := string(models.CurrentModelId)
		if bs.store != nil && bs.persistedID != "" {
			if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil && meta.BaselineModel != "" {
				baseline = meta.BaselineModel
			}
		}
		bs.baselineModel = baseline
	}
	bs.modelMu.Unlock()

	// Apply any ACP server constraints for the model category
	go bs.applyConfigConstraints(ConfigOptionCategoryModel)
}

// lookupACPServerConstraints returns the auto-selection constraints for the named
// ACP server in the given config, or nil if cfg is nil or no matching server is found.
func lookupACPServerConstraints(cfg *config.Config, serverName string) map[string]*config.ACPServerConstraint {
	if cfg == nil {
		return nil
	}
	for _, srv := range cfg.ACPServers {
		if srv.Name == serverName {
			return srv.Constraints
		}
	}
	return nil
}

// applyConfigConstraints checks ACP server constraints and auto-selects matching config option values.
// Called after config options (like models) become available during ACP initialization.
// Only applies constraints for config option categories that are present in the constraints map.
func (bs *BackgroundSession) applyConfigConstraints(category string) {
	if len(bs.acpServerConstraints) == 0 {
		return
	}

	constraint, ok := bs.acpServerConstraints[category]
	if !ok || constraint == nil || constraint.Pattern == "" {
		return
	}

	bs.configMu.RLock()
	var targetOption *SessionConfigOption
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == category {
			targetOption = &bs.configOptions[i]
			break
		}
	}
	bs.configMu.RUnlock()

	if targetOption == nil || len(targetOption.Options) == 0 {
		return
	}

	matchedValue := matchConstraintOption(constraint, targetOption.Options)

	if matchedValue == "" {
		if bs.logger != nil {
			bs.logger.Warn("ACP server constraint: no matching option found",
				"category", category,
				"match_mode", constraint.MatchMode,
				"pattern", constraint.Pattern,
				"available_count", len(targetOption.Options))
		}
		return
	}

	// Skip if the agent already has the matching value.
	// For the model category, compare against agentModels.CurrentModelId (the agent's actual
	// current model) rather than the local configOption.CurrentValue, which may have been
	// pre-applied optimistically in setAgentModels before the RPC completed. This ensures
	// the RPC still fires even when local state was eagerly set to the desired model.
	alreadySet := targetOption.CurrentValue == matchedValue
	if category == ConfigOptionCategoryModel && bs.agentModels != nil {
		alreadySet = string(bs.agentModels.CurrentModelId) == matchedValue
	}
	if alreadySet {
		if bs.logger != nil {
			bs.logger.Debug("ACP server constraint: already set to matching value",
				"category", category,
				"value", matchedValue)
		}
		return
	}

	if bs.logger != nil {
		bs.logger.Info("ACP server constraint: auto-selecting option",
			"category", category,
			"match_mode", constraint.MatchMode,
			"pattern", constraint.Pattern,
			"selected_value", matchedValue)
	}

	// Use a background context since this is called during initialization.
	// 30s budget accommodates up to 3 set_model retry attempts (≤8s each + backoff)
	// that may queue behind concurrent callers on the same shared ACP process (mitto-3q9).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bs.SetConfigOption(ctx, category, matchedValue); err != nil {
		if bs.logger != nil {
			bs.logger.Error("ACP server constraint: failed to auto-select option",
				"category", category,
				"value", matchedValue,
				"error", err)
		}
	}
}

// ConfigOptions returns a copy of all session config options.
func (bs *BackgroundSession) ConfigOptions() []SessionConfigOption {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()

	if bs.configOptions == nil {
		return nil
	}
	result := make([]SessionConfigOption, len(bs.configOptions))
	copy(result, bs.configOptions)
	return result
}

// GetConfigValue returns the current value for a specific config option.
func (bs *BackgroundSession) GetConfigValue(configID string) string {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()

	for _, opt := range bs.configOptions {
		if opt.ID == configID {
			return opt.CurrentValue
		}
	}
	return ""
}

// SetConfigOption changes a session config option value.
// For legacy modes (category "mode"), this calls SetSessionMode.
// For future configOptions API, it would call SetConfigOption.
func (bs *BackgroundSession) SetConfigOption(ctx context.Context, configID, value string) error {
	if bs.IsClosed() {
		return fmt.Errorf("session is closed")
	}

	if bs.acpConn == nil && bs.sharedProcess == nil {
		return fmt.Errorf("no ACP connection")
	}

	// Find the config option and validate the value
	bs.configMu.RLock()
	var found *SessionConfigOption
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			found = &bs.configOptions[i]
			break
		}
	}
	bs.configMu.RUnlock()

	if found == nil {
		return fmt.Errorf("unknown config option: %s", configID)
	}

	// Validate the value is one of the allowed options
	valid := false
	for _, opt := range found.Options {
		if opt.Value == value {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid value for %s: %s", configID, value)
	}

	// While the agent is prompting, defer the real ACP RPC to the prompting→idle
	// transition (flushPendingConfig). We still reflect the new value optimistically
	// in local state and broadcast it so the UI updates immediately. Last-write-wins
	// per configID. The isPrompting check and the pending-store write are performed
	// under promptMu (with pendingConfigMu nested) so a change racing turn-end is not
	// silently dropped: the completion path flips isPrompting under the same promptMu
	// before flushing, so either we record the pending value before the flip (flush
	// will drain it) or we observe the post-flip idle state and apply immediately.
	bs.promptMu.Lock()
	if bs.isPrompting {
		bs.pendingConfigMu.Lock()
		bs.pendingConfig[configID] = value
		bs.pendingConfigMu.Unlock()
		bs.promptMu.Unlock()

		// Optimistically reflect the pending value locally and broadcast it.
		bs.configMu.Lock()
		for i := range bs.configOptions {
			if bs.configOptions[i].ID == configID {
				bs.configOptions[i].CurrentValue = value
				break
			}
		}
		bs.configMu.Unlock()

		bs.persistConfigValue(configID, value)

		if bs.logger != nil {
			bs.logger.Info("Config option change deferred while prompting",
				"config_id", configID,
				"value", value)
		}

		// User-originated model change: update baseline immediately so that the restore-on-idle
		// path targets the new model, not the previously selected one.
		if found.Category == ConfigOptionCategoryModel {
			bs.modelMu.Lock()
			bs.baselineModel = value
			bs.overrideActive = false
			bs.modelMu.Unlock()
			bs.persistBaselineModel(value)
		}

		if bs.onConfigChanged != nil {
			bs.onConfigChanged(bs.persistedID, configID, value)
		}

		return nil
	}
	bs.promptMu.Unlock()

	// Idle: a fresh immediate change supersedes any value still parked in the pending
	// store from a just-finished turn, so it cannot be overwritten by a later flush.
	bs.pendingConfigMu.Lock()
	delete(bs.pendingConfig, configID)
	bs.pendingConfigMu.Unlock()

	return bs.applyConfigOption(ctx, configID, value)
}

// applyConfigOption issues the real ACP RPC for a config change, then updates local
// state, persists, and broadcasts. The value must already be validated by the caller.
// It is used both for the immediate (idle) path and the deferred flush path.
func (bs *BackgroundSession) applyConfigOption(ctx context.Context, configID, value string) error {
	bs.configMu.RLock()
	category := ""
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			category = bs.configOptions[i].Category
			break
		}
	}
	bs.configMu.RUnlock()

	// Determine how to set the value based on the category and API availability
	if category == ConfigOptionCategoryMode && bs.usesLegacyModes {
		// Use legacy SetSessionMode API
		var err error
		if bs.sharedProcess != nil {
			err = bs.sharedProcess.SetSessionMode(ctx, acp.SessionId(bs.acpID), value)
		} else if bs.acpConn != nil {
			_, err = bs.acpConn.SetSessionMode(ctx, acp.SetSessionModeRequest{
				SessionId: acp.SessionId(bs.acpID),
				ModeId:    acp.SessionModeId(value),
			})
		} else {
			return fmt.Errorf("no ACP connection")
		}
		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to set session mode",
					"config_id", configID,
					"value", value,
					"error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
	} else if category == ConfigOptionCategoryModel {
		// Use UNSTABLE SetSessionModel API
		var err error
		if bs.sharedProcess != nil {
			err = bs.sharedProcess.SetSessionModel(ctx, acp.SessionId(bs.acpID), value)
		} else if bs.acpConn != nil {
			_, err = bs.acpConn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
				SessionId: acp.SessionId(bs.acpID),
				ModelId:   acp.UnstableModelId(value),
			})
		} else {
			return fmt.Errorf("no ACP connection")
		}
		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to set session model",
					"config_id", configID,
					"value", value,
					"error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}

		// Update the internal agentModels state to reflect the new current model
		if bs.agentModels != nil {
			bs.agentModels.CurrentModelId = acp.UnstableModelId(value)
		}

		// User-originated model change: update baseline so restore-on-idle targets the
		// right model. This covers both the immediate path and the deferred-flush path
		// (flushPendingConfig calls applyConfigOption after the prompt goroutine exits).
		bs.modelMu.Lock()
		bs.baselineModel = value
		bs.overrideActive = false
		bs.modelMu.Unlock()
		bs.persistBaselineModel(value)
	} else {
		// Future: Use SetConfigOption API when available in SDK
		return fmt.Errorf("config option %s is not supported by current agent", configID)
	}

	// Update local state
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			bs.configOptions[i].CurrentValue = value
			break
		}
	}
	bs.configMu.Unlock()

	// Persist to metadata
	bs.persistConfigValue(configID, value)

	if bs.logger != nil {
		bs.logger.Info("Config option changed",
			"config_id", configID,
			"value", value)
	}

	// Notify callback
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, configID, value)
	}

	return nil
}

// flushPendingConfig issues the real ACP RPC for any config changes that were
// deferred while the agent was prompting. It runs on the prompting→idle transition,
// BEFORE the next queued message is dispatched, so the queued prompt runs under the
// new configuration. Last-write-wins per configID (one value per option).
func (bs *BackgroundSession) flushPendingConfig() {
	bs.pendingConfigMu.Lock()
	if len(bs.pendingConfig) == 0 {
		bs.pendingConfigMu.Unlock()
		return
	}
	pending := bs.pendingConfig
	bs.pendingConfig = make(map[string]string)
	bs.pendingConfigMu.Unlock()

	// SetSessionModel can be slow; mirror the 30s budget used by the handler.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for configID, value := range pending {
		if err := bs.applyConfigOption(ctx, configID, value); err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to flush deferred config option",
					"config_id", configID,
					"value", value,
					"error", err)
			}
		}
	}
}

// persistConfigValue saves a config option value to metadata.
func (bs *BackgroundSession) persistConfigValue(configID, value string) {
	if bs.store == nil {
		return
	}

	// For mode category, store in CurrentModeID for backward compatibility
	if configID == ConfigOptionCategoryMode {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.CurrentModeID = value
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to persist config value to metadata",
				"config_id", configID,
				"error", err)
		}
	}
	// Future: For other config options, store in a ConfigValues map
}

// persistBaselineModel persists the user's intended model to metadata so it survives
// suspend/resume cycles.
func (bs *BackgroundSession) persistBaselineModel(value string) {
	if bs.store == nil {
		return
	}
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		m.BaselineModel = value
	}); err != nil && bs.logger != nil {
		bs.logger.Warn("Failed to persist baseline model", "model", value, "error", err)
	}
}

// setActiveModelOnly issues a SetSessionModel ACP call and updates local state, but does
// NOT update baselineModel or overrideActive. Used exclusively for per-prompt model
// overrides driven by preferredModels frontmatter.
func (bs *BackgroundSession) setActiveModelOnly(ctx context.Context, modelID string) error {
	var err error
	if bs.sharedProcess != nil {
		err = bs.sharedProcess.SetSessionModel(ctx, acp.SessionId(bs.acpID), modelID)
	} else if bs.acpConn != nil {
		_, err = bs.acpConn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
			SessionId: acp.SessionId(bs.acpID),
			ModelId:   acp.UnstableModelId(modelID),
		})
	} else {
		return fmt.Errorf("no ACP connection")
	}
	if err != nil {
		return fmt.Errorf("failed to set model: %w", err)
	}

	// Update agentModels and local config option state (mirrors applyConfigOption for model).
	if bs.agentModels != nil {
		bs.agentModels.CurrentModelId = acp.UnstableModelId(modelID)
	}
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == ConfigOptionCategoryModel {
			bs.configOptions[i].CurrentValue = modelID
			break
		}
	}
	bs.configMu.Unlock()

	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, ConfigOptionCategoryModel, modelID)
	}
	return nil
}

// restoreBaselineIfOverride restores the session model to baselineModel when an override
// is active (set by a prior preferredModels prompt). Called in processNextQueuedMessage
// when the queue drains so the UI always reflects the user's intended model while idle.
func (bs *BackgroundSession) restoreBaselineIfOverride() {
	bs.modelMu.Lock()
	if !bs.overrideActive {
		bs.modelMu.Unlock()
		return
	}
	baseline := bs.baselineModel
	bs.overrideActive = false
	bs.modelMu.Unlock()

	if baseline == "" || bs.agentModels == nil {
		return
	}
	if string(bs.agentModels.CurrentModelId) == baseline {
		return // Already at baseline, no RPC needed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if setErr := bs.setActiveModelOnly(ctx, baseline); setErr != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to restore baseline model after queue drain",
				"baseline", baseline, "error", setErr)
		}
	} else if bs.logger != nil {
		bs.logger.Info("Restored baseline model after queue drain", "model", baseline)
	}
}

// isContextTooLargeError returns true if the error indicates the AI model
// rejected the prompt because the conversation context is too large (HTTP 413
// or an equivalent model-specific error phrase).
//
// The ACP server forwards HTTP 413 responses as JSON-RPC -32603 "Internal error"
// messages, so the numeric status code or the model-specific phrase may appear
// anywhere in the error string.  We keep the list of patterns here (rather than
// inlining them in formatACPError) so that the queue-advancement logic can reuse
// the same predicate without duplicating strings.
func isContextTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)
	return strings.Contains(errMsg, "413") ||
		strings.Contains(errMsgLower, "context too large") ||
		strings.Contains(errMsgLower, "context_too_long") ||
		strings.Contains(errMsgLower, "context_length_exceeded") ||
		strings.Contains(errMsgLower, "context window is full") ||
		strings.Contains(errMsgLower, "prompt is too long") ||
		strings.Contains(errMsgLower, "maximum context length") ||
		strings.Contains(errMsgLower, "context too large for model")
}

// isRateLimitError returns true if the error indicates the upstream API is
// rate-limiting the session.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errMsgLower := strings.ToLower(err.Error())
	return strings.Contains(errMsgLower, "rate limit") || strings.Contains(errMsgLower, "too many requests")
}

// formatACPError transforms ACP errors into user-friendly messages.
// It detects common error patterns and provides actionable guidance.
func formatACPError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// SDK control request timeout (CLI subprocess died, ACP tried to reconnect and timed out)
	// This is the 60s DEFAULT_CONTROL_REQUEST_TIMEOUT in claude-code-agent-sdk
	if strings.Contains(errMsg, "Control request timed out") ||
		strings.Contains(errMsg, "control request timed out") {
		return "The AI agent's internal connection to the CLI timed out. " +
			"This usually means the CLI subprocess crashed. The agent will attempt to restart automatically."
	}

	// HTTP 413 / context-too-large errors from the AI model.
	// Checked before the generic -32603 catch-all so users get an actionable message.
	if isContextTooLargeError(err) {
		return "⚠️ The conversation context is too large for the model. " +
			"Please start a new conversation. You can ask the agent to summarize the key points first if needed."
	}

	// Timeout errors from ACP server (tool execution took too long)
	if strings.Contains(errMsg, "aborted due to timeout") {
		return "A tool operation timed out. The AI agent's tool call took too long to complete. " +
			"Try breaking your request into smaller steps, or ask for a more specific task."
	}

	// Connection/transport errors
	if strings.Contains(errMsg, "peer disconnected") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "stream ended unexpectedly") {
		return "Lost connection to the AI agent. The agent process may have crashed or been restarted. " +
			"Please try sending your message again."
	}

	// Context cancelled (user cancelled or session closed)
	if strings.Contains(errMsg, "context canceled") ||
		strings.Contains(errMsg, "context deadline exceeded") {
		return "The request was cancelled. Please try again."
	}

	// Rate limiting
	if isRateLimitError(err) {
		return "Rate limit reached. Please wait a moment before sending another message."
	}

	// JSON-RPC internal error (-32603) — try to extract HTTP status for better messages.
	// Previously this required "details" to be present in the message; without it the
	// raw JSON-RPC error string was shown to the user. Now we always return a
	// user-friendly message whenever the -32603 code is detected.
	if strings.Contains(errMsg, "-32603") && strings.Contains(errMsg, "Internal error") {
		if httpStatus := extractHTTPStatus(errMsg); httpStatus > 0 {
			switch httpStatus {
			case 408:
				return fmt.Sprintf("The AI service request timed out (HTTP %d). The service may be overloaded — please try again in a moment.", httpStatus)
			case 500:
				return fmt.Sprintf("The AI service encountered a server error (HTTP %d). Please try again.", httpStatus)
			case 502, 503:
				return fmt.Sprintf("The AI service is temporarily unavailable (HTTP %d). Please try again shortly.", httpStatus)
			case 504:
				return fmt.Sprintf("The AI service gateway timed out (HTTP %d). Please try again.", httpStatus)
			default:
				return fmt.Sprintf("The AI service returned an error (HTTP %d). Please try again, or simplify your request if the problem persists.", httpStatus)
			}
		}
		return "The AI agent encountered an internal error. Please try again, " +
			"or simplify your request if the problem persists."
	}

	// Default: return original error with prefix
	return "Prompt failed: " + errMsg
}

// extractHTTPStatus tries to extract an HTTP status code from an error string.
// It searches for common patterns like "HTTP error: NNN", `"httpStatus":NNN`, or "HTTP/1.1 NNN".
// Returns 0 if no HTTP status code is found or the extracted value is outside the 4xx–5xx range.
func extractHTTPStatus(errMsg string) int {
	matches := httpStatusRegex.FindStringSubmatch(errMsg)
	if len(matches) >= 2 {
		status, err := strconv.Atoi(matches[1])
		if err == nil && status >= 400 && status < 600 {
			return status
		}
	}
	return 0
}

// =============================================================================
// UIPrompter Implementation
// =============================================================================

// UIPrompt displays an interactive prompt to the user and blocks until they respond
// or the timeout expires. This implements the mcpserver.UIPrompter interface.
//
// If a new prompt is sent while one is pending, the previous prompt is
// dismissed (with reason "replaced") and replaced by the new one.
func (bs *BackgroundSession) UIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	bs.activePromptMu.Lock()

	// Dismiss any existing prompt (new prompt replaces old one)
	if bs.activePrompt != nil {
		bs.dismissActivePromptLocked("replaced")
	}

	// Create timeout context
	timeoutDuration := time.Duration(req.TimeoutSeconds) * time.Second
	if timeoutDuration <= 0 {
		timeoutDuration = 5 * time.Minute // Default timeout
	}
	promptCtx, cancel := context.WithTimeout(ctx, timeoutDuration)

	// Create response channel
	responseCh := make(chan UIPromptResponse, 1)
	bs.activePrompt = &activeUIPrompt{
		request:    req,
		responseCh: responseCh,
		cancelFn:   cancel,
	}

	bs.activePromptMu.Unlock()

	if bs.logger != nil {
		bs.logger.Info("UI prompt started",
			"session_id", bs.persistedID,
			"request_id", req.RequestID,
			"prompt_type", req.Type,
			"question", req.Question,
			"option_count", len(req.Options),
			"timeout_seconds", req.TimeoutSeconds)
	}

	// Flush markdown buffer before sending UI prompt.
	// This ensures any buffered content (tables, lists, code blocks) is sent to
	// observers before the prompt, so users see the full context of what the
	// agent said before being asked to make a decision.
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}

	// Broadcast to all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPrompt(req)
	})

	// Broadcast UI prompt state change for sidebar display (only for blocking prompts)
	if req.Blocking && bs.onUIPromptStateChanged != nil {
		bs.onUIPromptStateChanged(bs.persistedID, true)
		defer bs.onUIPromptStateChanged(bs.persistedID, false)
	}

	// Wait for response, timeout, or cancellation
	select {
	case resp := <-responseCh:
		cancel()
		if bs.logger != nil {
			bs.logger.Info("UI prompt answered",
				"session_id", bs.persistedID,
				"request_id", req.RequestID,
				"option_id", resp.OptionID,
				"label", resp.Label)
		}
		return resp, nil

	case <-promptCtx.Done():
		bs.activePromptMu.Lock()
		// Only dismiss if this prompt is still the active one. When a prompt is
		// replaced by a newer one, both responseCh and promptCtx.Done() fire
		// simultaneously (the replacer cancels our context). If select picks
		// Done(), we must not dismiss the replacement prompt.
		if bs.activePrompt != nil && bs.activePrompt.request.RequestID == req.RequestID {
			bs.dismissActivePromptLocked("timeout")
		}
		bs.activePromptMu.Unlock()
		if bs.logger != nil {
			bs.logger.Info("UI prompt timed out",
				"session_id", bs.persistedID,
				"request_id", req.RequestID,
				"has_observers", bs.HasObservers())
		}
		// Notify all clients if the user was not actively viewing this session.
		// This triggers a native OS notification so the user knows they missed a prompt.
		if req.Blocking && !bs.HasObservers() && bs.onUIPromptTimeout != nil {
			sessionName := ""
			if bs.store != nil {
				if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
					sessionName = meta.Name
				}
			}
			go bs.onUIPromptTimeout(bs.persistedID, req, sessionName)
		}
		return UIPromptResponse{RequestID: req.RequestID, TimedOut: true}, nil

	case <-bs.ctx.Done():
		// Session closed
		bs.activePromptMu.Lock()
		if bs.activePrompt != nil && bs.activePrompt.request.RequestID == req.RequestID {
			bs.dismissActivePromptLocked("cancelled")
		}
		bs.activePromptMu.Unlock()
		return UIPromptResponse{}, bs.ctx.Err()
	}
}

// DismissPrompt cancels any active prompt with the given request ID.
// This is called when the prompt should be dismissed (e.g., session activity).
func (bs *BackgroundSession) DismissPrompt(requestID string) {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil || bs.activePrompt.request.RequestID != requestID {
		return
	}

	bs.dismissActivePromptLocked("cancelled")
}

// DismissActiveUIPrompt dismisses any active UI prompt, regardless of its request ID.
// This is called when the session is cancelled (e.g., user presses Stop button)
// to clean up any MCP tool UI prompts that are waiting for user input.
func (bs *BackgroundSession) DismissActiveUIPrompt() {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Dismissing active UI prompt due to session cancel",
			"session_id", bs.persistedID,
			"request_id", bs.activePrompt.request.RequestID)
	}

	bs.dismissActivePromptLocked("cancelled")
}

// HandleUIPromptAnswer processes a user's response to a UI prompt.
// This is called by SessionWSClient when it receives a ui_prompt_answer message.
func (bs *BackgroundSession) HandleUIPromptAnswer(requestID, optionID, label, freeText string) {
	bs.activePromptMu.Lock()

	if bs.activePrompt == nil || bs.activePrompt.request.RequestID != requestID {
		if bs.logger != nil {
			bs.logger.Debug("UI prompt answer ignored (no matching prompt)",
				"session_id", bs.persistedID,
				"request_id", requestID)
		}
		bs.activePromptMu.Unlock()
		return
	}

	// Send response (non-blocking - channel has buffer of 1)
	select {
	case bs.activePrompt.responseCh <- UIPromptResponse{
		RequestID: requestID,
		OptionID:  optionID,
		Label:     label,
		FreeText:  freeText,
		Aborted:   optionID == "abort",
	}:
	default:
		// Already received a response - ignore duplicate
	}

	// Record in history
	if bs.recorder != nil {
		bs.recorder.RecordUIPromptAnswer(requestID, optionID, label)
	}

	// Clean up
	bs.activePrompt.cancelFn()
	bs.activePrompt = nil

	bs.activePromptMu.Unlock()

	// Notify frontend to dismiss (do this in a goroutine to avoid blocking,
	// matching the pattern used in dismissActivePromptLocked)
	// The frontend also clears optimistically, but this ensures the prompt
	// is dismissed even if there's a race condition
	go bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPromptDismiss(requestID, "answered")
	})
}

// dismissActivePromptLocked dismisses the active prompt with the given reason.
// Must be called with activePromptMu held.
func (bs *BackgroundSession) dismissActivePromptLocked(reason string) {
	if bs.activePrompt == nil {
		return
	}

	requestID := bs.activePrompt.request.RequestID
	bs.activePrompt.cancelFn()

	// Send timeout response to unblock the waiting goroutine
	select {
	case bs.activePrompt.responseCh <- UIPromptResponse{RequestID: requestID, TimedOut: true}:
	default:
	}

	bs.activePrompt = nil

	// Notify frontend to dismiss (do this outside the lock to avoid deadlock)
	go bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPromptDismiss(requestID, reason)
	})
}

// GetActiveUIPrompt returns the currently active UI prompt, if any.
// Used to send cached prompt to new observers.
func (bs *BackgroundSession) GetActiveUIPrompt() *UIPromptRequest {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil {
		return nil
	}

	// Return a copy
	req := bs.activePrompt.request
	return &req
}

// UINotify sends a fire-and-forget notification to all UI observers.
// This implements the mcpserver.UIPrompter interface (UINotify method).
// Unlike UIPrompt, this is non-blocking — it dispatches the notification
// to all observers and returns immediately without waiting for any response.
func (bs *BackgroundSession) UINotify(req UINotifyRequest) error {
	if bs.IsClosed() {
		return fmt.Errorf("session is closed")
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnNotification(req)
	})
	return nil
}
