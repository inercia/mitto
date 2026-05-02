package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
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

	// onPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	onPlanStateChanged func(sessionID string, entries []PlanEntry)

	// onTitleGenerated is called when a title is auto-generated for this session.
	// Used to broadcast session_renamed events to all clients.
	onTitleGenerated func(sessionID, title string)

	// Available slash commands from the agent
	availableCommandsMu sync.RWMutex
	availableCommands   []AvailableCommand

	// ACP process restart tracking
	// When the ACP process dies unexpectedly, we attempt to restart it automatically.
	// To prevent infinite restart loops, we limit restarts to MaxACPRestarts within
	// ACPRestartWindow. The acpCommand and acpCwd are stored so we can restart the process.
	acpCommand        string          // Command used to start ACP process (for restart)
	acpCwd            string          // Working directory for ACP process (for restart)
	restartCount      int             // Total number of restarts across the session lifetime
	restartTimes      []time.Time     // Timestamps of recent restarts (for rate limiting)
	restartReasons    []RestartReason // Reasons for recent restarts (parallel to restartTimes)
	permanentlyFailed bool            // Circuit breaker: true when ACP cannot be restarted (permanent error or lifetime cap hit)
	restartMu         sync.Mutex      // Protects restart tracking fields (restartCount, restartTimes, restartReasons, permanentlyFailed)

	// Session config options - configurable settings for the session
	// This supports both legacy "modes" API and newer "configOptions" API.
	// See https://agentclientprotocol.com/protocol/session-config-options
	configMu        sync.RWMutex
	configOptions   []SessionConfigOption                          // All config options (includes mode if available)
	onConfigChanged func(sessionID string, configID, value string) // Called when any config option changes
	usesLegacyModes bool                                           // True if using legacy modes API (not configOptions)

	// Global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	globalMcpServer *mcpserver.Server

	// Auxiliary manager for workspace-scoped auxiliary tasks
	auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager

	// Active UI prompt state for MCP tool user prompts
	// When an MCP tool calls Prompt(), this holds the pending prompt until the user responds
	activePromptMu sync.Mutex
	activePrompt   *activeUIPrompt

	// sharedProcess is set when this session uses workspace-scoped process sharing.
	// When non-nil, this session does not own the OS process — it only owns a session
	// slot on the shared process. nil = legacy per-session process ownership.
	sharedProcess *SharedACPProcess

	// resumeMethod tracks which method was used to establish the ACP session:
	// "resume" (UNSTABLE resume API), "load" (history replay), or "new" (fresh session)
	resumeMethod string
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
	ACPCwd           string // Working directory for the ACP process (not the session working dir)
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
		onPlanStateChanged:      cfg.OnPlanStateChanged,
		onConfigChanged:         cfg.OnConfigOptionChanged,
		onTitleGenerated:        cfg.OnTitleGenerated,
		acpCommand:              cfg.ACPCommand,          // Store for restart
		acpCwd:                  cfg.ACPCwd,              // Store for restart
		globalMcpServer:         cfg.GlobalMCPServer,     // Global MCP server for session registration
		auxiliaryManager:        cfg.AuxiliaryManager,    // Workspace-scoped auxiliary manager
		availableACPServers:     cfg.AvailableACPServers, // Pre-computed workspace server list
	}

	// Wire prompt-mode processor execution to auxiliary sessions
	if bs.processorManager != nil && bs.auxiliaryManager != nil {
		bs.processorManager.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error {
			return bs.auxiliaryManager.PromptProcessorAsync(ctx, workspaceUUID, processorName, prompt)
		})
	}

	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Initialize activity timestamp
	bs.lastActivityAt.Store(time.Now().UnixNano())

	// Create recorder for persistence
	if cfg.Store != nil {
		bs.recorder = session.NewRecorder(cfg.Store)
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
	if cfg.SharedProcess != nil {
		if err := bs.startSharedACPSession(cfg.SharedProcess, cfg.WorkingDir); err != nil {
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
		onPlanStateChanged:      config.OnPlanStateChanged,
		onConfigChanged:         config.OnConfigOptionChanged,
		onTitleGenerated:        config.OnTitleGenerated,
		acpCommand:              config.ACPCommand,          // Store for restart
		acpCwd:                  config.ACPCwd,              // Store for restart
		globalMcpServer:         config.GlobalMCPServer,     // Global MCP server for session registration
		auxiliaryManager:        config.AuxiliaryManager,    // Workspace-scoped auxiliary manager
		availableACPServers:     config.AvailableACPServers, // Pre-computed workspace server list
	}

	// Wire prompt-mode processor execution to auxiliary sessions
	if bs.processorManager != nil && bs.auxiliaryManager != nil {
		bs.processorManager.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error {
			return bs.auxiliaryManager.PromptProcessorAsync(ctx, workspaceUUID, processorName, prompt)
		})
	}

	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

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

// buildProcessorHistory reads session events and returns a slice of HistoryEntry structs.
// This is used to populate ProcessorInput.History for processors that need conversation
// context (InputConversation processors and prompt-mode processors using @mitto:messages).
func (bs *BackgroundSession) buildProcessorHistory() []processors.HistoryEntry {
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}

	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read events for processor history", "error", err)
		}
		return nil
	}

	var entries []processors.HistoryEntry
	for _, event := range events {
		data, err := session.DecodeEventData(event)
		if err != nil {
			continue
		}
		switch event.Type {
		case session.EventTypeUserPrompt:
			if d, ok := data.(session.UserPromptData); ok && d.Message != "" {
				entries = append(entries, processors.HistoryEntry{
					Role:    "user",
					Content: d.Message,
				})
			}
		case session.EventTypeAgentMessage:
			if d, ok := data.(session.AgentMessageData); ok && d.Text != "" {
				// Agent messages are stored as HTML; strip tags for plain text context
				entries = append(entries, processors.HistoryEntry{
					Role:    "assistant",
					Content: session.StripHTML(d.Text),
				})
			}
		}
	}

	return entries
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
// Returns: processor count, total pipeline activations, last activation time.
func (bs *BackgroundSession) GetProcessorStats() (count int, activations int, lastAt time.Time) {
	if bs.processorManager == nil {
		return 0, 0, time.Time{}
	}
	return bs.processorManager.ProcessorCount(), bs.processorManager.TotalActivations(), bs.processorManager.LastActivationAt()
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
		if restartErr := bs.sharedProcess.Restart(); restartErr != nil {
			// Log but don't fail — the process may have been restarted by another session.
			if bs.logger != nil {
				bs.logger.Warn("Shared ACP process restart returned error, attempting new session anyway",
					"session_id", bs.persistedID,
					"error", restartErr)
			}
		}
		err = bs.resumeSharedACPSession(bs.sharedProcess, bs.workingDir, bs.acpID)
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
func startStderrMonitor(stderr runner.ReadCloser, collector *stderrCollector, onCrashDetected func()) {
	go func() {
		crashSignaled := false
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				collector.Write(buf[:n])

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
		stdin, stdout, stderr, wait, err = bs.runner.RunWithPipes(bs.ctx, args[0], args[1:], nil)
		if err != nil {
			return "", &sessionError{"failed to start with runner: " + err.Error()}
		}

		// Monitor stderr in background (with crash detection for Fix C)
		startStderrMonitor(stderr, stderrCollector, onCrashDetected)

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

		// Set MITTO_* environment variables for the ACP subprocess
		processEnv := os.Environ()
		for k, v := range mittoEnv {
			processEnv = append(processEnv, k+"="+v)
		}
		cmd.Env = processEnv

		if err := cmd.Start(); err != nil {
			return "", &sessionError{"failed to start ACP server: " + err.Error()}
		}

		// Monitor stderr in background (same as runner case, with crash detection for Fix C)
		startStderrMonitor(stderrPipe, stderrCollector, onCrashDetected)

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
			loadCtx, loadCancel := context.WithTimeout(initCtx, 30*time.Second)
			loadResp, err := bs.acpConn.LoadSession(loadCtx, acp.LoadSessionRequest{
				SessionId:  acp.SessionId(acpSessionID),
				Cwd:        cwd,
				McpServers: mcpServers,
			})
			loadCancel()
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

// startSharedACPSession sets up this BackgroundSession to use a session on the
// given shared ACP process instead of starting its own OS process.
// The session is registered with the shared process's MultiplexClient so that it
// receives only its own events.
func (bs *BackgroundSession) startSharedACPSession(sharedProcess *SharedACPProcess, workingDir string) error {
	bs.sharedProcess = sharedProcess

	// Register with global MCP server and get the (empty) mcpServers list.
	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := bs.startSessionMcpServer(bs.store, caps)

	// Create WebClient for stream processing (same callbacks as per-session path).
	bs.acpClient = NewWebClient(bs.buildWebClientConfig())

	// Create a session on the shared process.
	handle, err := sharedProcess.NewSession(bs.ctx, workingDir, mcpServers)
	if err != nil {
		bs.stopSessionMcpServer()
		bs.acpClient.Close()
		bs.acpClient = nil
		bs.sharedProcess = nil
		return fmt.Errorf("failed to create session on shared process: %w", err)
	}

	// Register this session's WebClient callbacks with the shared MultiplexClient.
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
	// bs.acpProcessDone is a bidirectional chan; sharedProcess.ProcessDone() is receive-only.
	// We bridge them with a goroutine so all existing select sites work unchanged.
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
		bs.logger.Info("Created ACP session on shared process",
			"session_id", bs.persistedID,
			"acp_session_id", bs.acpID,
			"supports_images", bs.agentSupportsImages)
		bs.logAgentModels(handle.Models)
	}
	return nil
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
			loadCtx, loadCancel := context.WithTimeout(bs.ctx, 30*time.Second)
			handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
			loadCancel()
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
		handle, err = sharedProcess.NewSession(bs.ctx, workingDir, mcpServers)
		if err != nil {
			bs.stopSessionMcpServer()
			bs.acpClient.Close()
			bs.acpClient = nil
			bs.sharedProcess = nil
			return fmt.Errorf("failed to create session on shared process: %w", err)
		}
	}

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

// GetWorkspaceUUID returns the workspace UUID associated with this session.
func (bs *BackgroundSession) GetWorkspaceUUID() string {
	return bs.workspaceUUID
}

// GetAuxiliaryManager returns the auxiliary manager associated with this session.
func (bs *BackgroundSession) GetAuxiliaryManager() *auxiliary.WorkspaceAuxiliaryManager {
	return bs.auxiliaryManager
}

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID   string          // Unique identifier of the sending client (for broadcast deduplication)
	PromptID   string          // Client-generated prompt ID (for delivery confirmation)
	ImageIDs   []string        // IDs of images attached to the prompt
	FileIDs    []string        // IDs of files attached to the prompt
	OnComplete func(err error) // Called when the async prompt goroutine finishes (nil = success)
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
	imageIDs := meta.ImageIDs
	fileIDs := meta.FileIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil && bs.sharedProcess == nil {
		return &sessionError{"The AI agent is still starting up. Please wait a moment and try again."}
	}

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

				// Restart succeeded - notify user to retry the prompt
				// Note: we say "restarted" (not "restarted successfully") because the
				// process may crash again on the next prompt — we don't want to give
				// false confidence.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Please resend your message.")
				})
				return &sessionError{"ACP process restarted - please resend your message"}
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

	// Check if we need to inject conversation history (first prompt of resumed session)
	shouldInjectHistory := bs.isResumed && !bs.historyInjected
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
		if err := bs.recorder.RecordUserPromptComplete(message, imageRefs, fileRefs, meta.PromptID); err != nil && bs.logger != nil {
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
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings)
	})

	// Build the actual prompt to send to ACP.
	// Apply the unified processor pipeline (text-mode + command-mode in priority order).
	promptMessage := message
	var procAttachmentBlocks []acp.ContentBlock

	// Fetch session metadata for @mitto:variable substitution.
	// Done unconditionally so substitution works even with no processors configured.
	// Best-effort: unavailable fields substitute to "".
	var sessionName, acpServer, parentSessionID, parentSessionName string
	var childSessions []processors.ChildSession
	var advancedSettings map[string]bool
	if bs.store != nil && bs.persistedID != "" {
		if sessionMeta, metaErr := bs.store.GetMetadata(bs.persistedID); metaErr == nil {
			sessionName = sessionMeta.Name
			acpServer = sessionMeta.ACPServer
			parentSessionID = sessionMeta.ParentSessionID
			advancedSettings = sessionMeta.AdvancedSettings
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
				childSessions = append(childSessions, processors.ChildSession{
					ID:          child.SessionID,
					Name:        child.Name,
					ACPServer:   child.ACPServer,
					IsAutoChild: child.IsAutoChild,
					ChildOrigin: string(child.ChildOrigin),
				})
			}
		}
	}
	// Get cached MCP tool names for enabledWhenMCP and tools.* CEL context
	var mcpToolNames []string
	if bs.auxiliaryManager != nil && bs.workspaceUUID != "" {
		if tools, ok := bs.auxiliaryManager.GetCachedMCPTools(bs.workspaceUUID); ok {
			mcpToolNames = make([]string, len(tools))
			for i, tool := range tools {
				mcpToolNames[i] = tool.Name
			}
		}
	}

	// Check if any processor needs conversation history (InputConversation or prompt-mode).
	// Prompt-mode processors may use @mitto:messages which requires history to be populated.
	var processorHistory []processors.HistoryEntry
	if bs.processorManager != nil {
		for _, proc := range bs.processorManager.Processors() {
			if proc.GetInput() == processors.InputConversation || proc.IsPromptMode() {
				processorHistory = bs.buildProcessorHistory()
				break
			}
		}
	}

	// Populate user data schema and current user data for processor variables
	var hasUserDataSchema bool
	var userDataSchemaJSON string
	var userDataJSON string
	if bs.workingDir != "" {
		if rc, err := config.LoadWorkspaceRC(bs.workingDir); err == nil && rc != nil &&
			rc.Metadata != nil && rc.Metadata.UserDataSchema != nil && len(rc.Metadata.UserDataSchema.Fields) > 0 {
			hasUserDataSchema = true
			if schemaBytes, err := json.Marshal(rc.Metadata.UserDataSchema.Fields); err == nil {
				userDataSchemaJSON = string(schemaBytes)
			}
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
		Message:             message,
		IsFirstMessage:      isFirst,
		SessionID:           bs.persistedID,
		WorkingDir:          bs.workingDir,
		ParentSessionID:     parentSessionID,
		ParentSessionName:   parentSessionName,
		SessionName:         sessionName,
		ACPServer:           acpServer,
		WorkspaceUUID:       bs.workspaceUUID,
		AvailableACPServers: bs.availableACPServers,
		ChildSessions:       childSessions,
		MCPToolNames:        mcpToolNames,
		IsPeriodic:          meta.SenderID == "periodic-runner",
		AdvancedSettings:    advancedSettings,
		History:             processorHistory,
		HasUserDataSchema:   hasUserDataSchema,
		UserDataSchemaJSON:  userDataSchemaJSON,
		UserDataJSON:        userDataJSON,
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
				_, procActivations, procLastAt := bs.GetProcessorStats()
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
		// Create a prompt context that gets cancelled when the ACP process dies.
		// This ensures we fail fast instead of waiting for the ACP server's internal
		// 60-second control request timeout when the CLI subprocess has crashed.
		// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
		promptCtx, promptCancel := context.WithCancel(bs.ctx)
		defer promptCancel()

		// Monitor ACP process health: if the connection's Done() channel closes
		// or the OS process exits (acpProcessDone), cancel the prompt context immediately.
		// The acpProcessDone channel provides faster detection than Done() because it
		// uses OS-level process liveness checks (signal 0) rather than waiting for
		// pipe EOF to propagate through the JSON-RPC transport layer.
		processDoneCh := bs.acpProcessDone // capture for goroutine
		var connDoneCh <-chan struct{}
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

		var promptResp acp.PromptResponse
		var err error
		if bs.sharedProcess != nil {
			promptResp, err = bs.sharedProcess.Prompt(promptCtx, acp.SessionId(bs.acpID), finalBlocks)
		} else {
			promptResp, err = bs.acpConn.Prompt(promptCtx, acp.PromptRequest{
				SessionId: acp.SessionId(bs.acpID),
				Prompt:    finalBlocks,
			})
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

			if acpDead && bs.canRestartACP() {
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
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError("AI agent restarted. Please resend your message.")
					})
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

			// Process next queued message if queue processing is enabled
			bs.processNextQueuedMessage()

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
		}

		// Invoke OnComplete callback if set.
		// Called after all observers have been notified and state is consistent,
		// so the caller can accurately track the final outcome (nil = success, non-nil = failure).
		if meta.OnComplete != nil {
			meta.OnComplete(err)
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
	if bs.sharedProcess != nil {
		return bs.sharedProcess.Cancel(bs.ctx, acp.SessionId(bs.acpID))
	}
	if bs.acpConn == nil {
		return nil
	}
	return bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
		SessionId: acp.SessionId(bs.acpID),
	})
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
func (bs *BackgroundSession) processNextQueuedMessage() {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return
	}

	// Get the queue for this session
	if bs.store == nil {
		return
	}
	queue := bs.store.Queue(bs.persistedID)

	// Pop the next message from the queue
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty or error - nothing to do
		return
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
		SenderID: "queue",
		PromptID: msg.ID,
		ImageIDs: msg.ImageIDs,
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
			bs.logger.Error("Failed to persist agent message", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist agent thought", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist tool call", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist tool call update", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist plan", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist file write", "seq", seq, "error", err)
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
			bs.logger.Error("Failed to persist file read", "seq", seq, "error", err)
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
	options := make([]SessionConfigOptionValue, len(models.AvailableModels))
	for i, m := range models.AvailableModels {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		options[i] = SessionConfigOptionValue{
			Value:       string(m.ModelId),
			Name:        m.Name,
			Description: desc,
		}
	}

	modelOption := SessionConfigOption{
		ID:           ConfigOptionCategoryModel,
		Name:         "Model",
		Description:  "AI model for this session (UNSTABLE)",
		Category:     ConfigOptionCategoryModel,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: string(models.CurrentModelId),
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

	// Determine how to set the value based on the category and API availability
	if found.Category == ConfigOptionCategoryMode && bs.usesLegacyModes {
		// Use legacy SetSessionMode API
		var err error
		if bs.sharedProcess != nil {
			err = bs.sharedProcess.SetSessionMode(ctx, acp.SessionId(bs.acpID), value)
		} else {
			_, err = bs.acpConn.SetSessionMode(ctx, acp.SetSessionModeRequest{
				SessionId: acp.SessionId(bs.acpID),
				ModeId:    acp.SessionModeId(value),
			})
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
	} else if found.Category == ConfigOptionCategoryModel {
		// Use UNSTABLE SetSessionModel API
		var err error
		if bs.sharedProcess != nil {
			// Shared process doesn't support model switching yet
			return fmt.Errorf("model switching is not supported on shared process sessions")
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

	// Generic JSON-RPC internal error (-32603).
	// Previously this required "details" to be present in the message; without it the
	// raw JSON-RPC error string was shown to the user. Now we always return a
	// user-friendly message whenever the -32603 code is detected.
	if strings.Contains(errMsg, "-32603") && strings.Contains(errMsg, "Internal error") {
		return "The AI agent encountered an internal error. Please try again, " +
			"or simplify your request if the problem persists."
	}

	// Default: return original error with prefix
	return "Prompt failed: " + errMsg
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
				"request_id", req.RequestID)
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
