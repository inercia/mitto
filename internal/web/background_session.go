package web

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversion"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/msghooks"
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

	// Session persistence
	recorder *session.Recorder

	// Sequence number tracking for event ordering
	// nextSeq is the next sequence number to assign to a new event.
	// It's initialized from the store's EventCount + 1 and incremented for each new event.
	// This ensures streaming events have the same seq as their persisted counterparts.
	nextSeq int64
	seqMu   sync.Mutex // Protects nextSeq

	// Session lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Int32

	// Observers (multiple clients can observe this session)
	observersMu sync.RWMutex
	observers   map[SessionObserver]struct{}

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
	processors    []config.MessageProcessor // Merged processors from global and workspace config
	hookManager   *msghooks.Manager         // External command hooks for message transformation
	workingDir    string                    // Working directory for hook execution
	isFirstPrompt bool                      // True until first prompt is sent (for processor conditions)

	// Queue processing
	queueConfig *config.QueueConfig // Queue configuration (nil means use defaults)

	// Action buttons (follow-up suggestions)
	// These are AI-generated response options shown after the agent completes a response.
	// We use a two-tier cache (memory + disk) so suggestions persist across client
	// reconnections and server restarts. See docs/devel/follow-up-suggestions.md.
	actionButtonsConfig *config.ActionButtonsConfig // Configuration (nil means disabled)
	actionButtonsMu     sync.RWMutex                // Protects cachedActionButtons
	cachedActionButtons []ActionButton              // In-memory cache for fast access

	// File links configuration
	fileLinksConfig *config.FileLinksConfig // Configuration for file path linking
	apiPrefix       string                  // URL prefix for API endpoints (for HTTP file links)
	workspaceUUID   string                  // Workspace UUID for secure file links

	// Restricted runner for sandboxed execution
	runner *runner.Runner // Optional runner for restricted execution (nil = direct execution)

	// onStreamingStateChanged is called when the session's streaming state changes.
	onStreamingStateChanged func(sessionID string, isStreaming bool)

	// onPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	onPlanStateChanged func(sessionID string, entries []PlanEntry)

	// Available slash commands from the agent
	availableCommandsMu sync.RWMutex
	availableCommands   []AvailableCommand

	// ACP process restart tracking
	// When the ACP process dies unexpectedly, we attempt to restart it automatically.
	// To prevent infinite restart loops, we limit restarts to maxACPRestarts within
	// acpRestartWindow. The acpCommand and acpCwd are stored so we can restart the process.
	acpCommand   string      // Command used to start ACP process (for restart)
	acpCwd       string      // Working directory for ACP process (for restart)
	restartCount int         // Number of restarts in current window
	restartTimes []time.Time // Timestamps of recent restarts (for rate limiting)
	restartMu    sync.Mutex  // Protects restart tracking fields

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

	// Active UI prompt state for MCP tool user prompts
	// When an MCP tool calls Prompt(), this holds the pending prompt until the user responds
	activePromptMu sync.Mutex
	activePrompt   *activeUIPrompt
}

// activeUIPrompt holds the state for a pending UI prompt from an MCP tool.
type activeUIPrompt struct {
	request    UIPromptRequest
	responseCh chan UIPromptResponse
	cancelFn   context.CancelFunc
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID  string
	ACPCommand   string
	ACPCwd       string // Working directory for the ACP process (not the session working dir)
	ACPServer    string
	ACPSessionID string // ACP-assigned session ID for resumption (optional)
	WorkingDir   string
	AutoApprove  bool
	Logger       *slog.Logger
	Store        *session.Store
	SessionName  string
	Processors   []config.MessageProcessor // Merged processors for message transformation
	HookManager  *msghooks.Manager         // External command hooks for message transformation
	QueueConfig  *config.QueueConfig       // Queue processing configuration
	Runner       *runner.Runner            // Optional restricted runner for sandboxed execution

	ActionButtonsConfig *config.ActionButtonsConfig // Action buttons configuration
	FileLinksConfig     *config.FileLinksConfig     // File path linking configuration
	APIPrefix           string                      // URL prefix for API endpoints (for HTTP file links)
	WorkspaceUUID       string                      // Workspace UUID for secure file links

	// OnStreamingStateChanged is called when the session's streaming state changes.
	// It's called with true when streaming starts (user sends prompt) and false when it ends.
	OnStreamingStateChanged func(sessionID string, isStreaming bool)

	// OnPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	OnPlanStateChanged func(sessionID string, entries []PlanEntry)

	// OnConfigOptionChanged is called when any session config option changes.
	// Used to broadcast config changes to all connected clients.
	// The configID identifies which option changed, and value is the new value.
	OnConfigOptionChanged func(sessionID string, configID, value string)

	// GlobalMCPServer is the global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	// If nil, per-session MCP server is used as fallback (legacy behavior).
	GlobalMCPServer *mcpserver.Server
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
func NewBackgroundSession(cfg BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:                     ctx,
		cancel:                  cancel,
		autoApprove:             cfg.AutoApprove,
		logger:                  cfg.Logger,
		observers:               make(map[SessionObserver]struct{}),
		processors:              cfg.Processors,
		hookManager:             cfg.HookManager,
		workingDir:              cfg.WorkingDir,
		isFirstPrompt:           true, // New session starts with first prompt pending
		queueConfig:             cfg.QueueConfig,
		actionButtonsConfig:     cfg.ActionButtonsConfig,
		fileLinksConfig:         cfg.FileLinksConfig,
		apiPrefix:               cfg.APIPrefix,
		workspaceUUID:           cfg.WorkspaceUUID,
		runner:                  cfg.Runner,
		onStreamingStateChanged: cfg.OnStreamingStateChanged,
		onPlanStateChanged:      cfg.OnPlanStateChanged,
		onConfigChanged:         cfg.OnConfigOptionChanged,
		acpCommand:              cfg.ACPCommand,      // Store for restart
		acpCwd:                  cfg.ACPCwd,          // Store for restart
		globalMcpServer:         cfg.GlobalMCPServer, // Global MCP server for session registration
	}
	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Create recorder for persistence
	if cfg.Store != nil {
		bs.recorder = session.NewRecorder(cfg.Store)
		bs.persistedID = bs.recorder.SessionID()
		bs.store = cfg.Store
		if err := bs.recorder.Start(cfg.ACPServer, cfg.WorkingDir); err != nil {
			cancel()
			return nil, err
		}
		// Update session name and runner info
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

	// Start ACP process (no ACP session ID for new sessions)
	if err := bs.startACPProcess(cfg.ACPCommand, cfg.ACPCwd, cfg.WorkingDir, ""); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End(session.SessionEndData{Reason: "failed_to_start"})
		}
		return nil, err
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
		autoApprove:             config.AutoApprove,
		logger:                  sessionLogger,
		observers:               make(map[SessionObserver]struct{}),
		isResumed:               true, // Mark as resumed session
		store:                   config.Store,
		processors:              config.Processors,
		hookManager:             config.HookManager,
		workingDir:              config.WorkingDir,
		isFirstPrompt:           false, // Resumed session = first prompt already sent
		queueConfig:             config.QueueConfig,
		actionButtonsConfig:     config.ActionButtonsConfig,
		fileLinksConfig:         config.FileLinksConfig,
		apiPrefix:               config.APIPrefix,
		workspaceUUID:           config.WorkspaceUUID,
		runner:                  config.Runner,
		onStreamingStateChanged: config.OnStreamingStateChanged,
		onPlanStateChanged:      config.OnPlanStateChanged,
		onConfigChanged:         config.OnConfigOptionChanged,
		acpCommand:              config.ACPCommand,      // Store for restart
		acpCwd:                  config.ACPCwd,          // Store for restart
		globalMcpServer:         config.GlobalMCPServer, // Global MCP server for session registration
	}
	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Resume recorder for the existing session
	if config.Store != nil {
		bs.recorder = session.NewRecorderWithID(config.Store, config.PersistedID)
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

	// Start ACP process, passing the ACP session ID for potential resumption
	if err := bs.startACPProcess(config.ACPCommand, config.ACPCwd, config.WorkingDir, config.ACPSessionID); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End(session.SessionEndData{Reason: "failed_to_start"})
		}
		return nil, err
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

// IsClosed returns true if the session has been closed.
func (bs *BackgroundSession) IsClosed() bool {
	return bs.closed.Load() != 0
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
	if bs.logger != nil {
		bs.logger.Debug("Observer removed", "observer_count", len(bs.observers))
	}
}

// ObserverCount returns the number of attached observers.
func (bs *BackgroundSession) ObserverCount() int {
	bs.observersMu.RLock()
	defer bs.observersMu.RUnlock()
	return len(bs.observers)
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
		if reason == "server_shutdown" {
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
func (bs *BackgroundSession) killACPProcess() {
	// Kill direct process if using one
	if bs.acpCmd != nil && bs.acpCmd.Process != nil {
		bs.acpCmd.Process.Kill()
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
const maxACPStartRetries = 2

// acpStartRetryDelay is the delay between ACP start retries.
const acpStartRetryDelay = 500 * time.Millisecond

// maxACPRestarts is the maximum number of automatic restarts allowed within acpRestartWindow.
// If this limit is exceeded, the user must manually restart the session.
const maxACPRestarts = 3

// acpRestartWindow is the time window for counting restart attempts.
// Restarts older than this are not counted toward the limit.
const acpRestartWindow = 5 * time.Minute

// canRestartACP checks if we can restart the ACP process based on rate limiting.
// Returns true if restart is allowed, false if we've exceeded the limit.
// This method is thread-safe.
func (bs *BackgroundSession) canRestartACP() bool {
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-acpRestartWindow)

	// Filter out old restart times
	var recentRestarts []time.Time
	for _, t := range bs.restartTimes {
		if t.After(cutoff) {
			recentRestarts = append(recentRestarts, t)
		}
	}
	bs.restartTimes = recentRestarts

	return len(recentRestarts) < maxACPRestarts
}

// recordRestart records a restart attempt for rate limiting.
// This method is thread-safe.
func (bs *BackgroundSession) recordRestart() {
	bs.restartMu.Lock()
	defer bs.restartMu.Unlock()

	bs.restartCount++
	bs.restartTimes = append(bs.restartTimes, time.Now())
}

// restartACPProcess attempts to restart the ACP process after it has died.
// It kills the old process, cleans up resources, and starts a new one.
// The new process will attempt to resume the ACP session if the agent supports it.
// Returns nil on success, or an error if restart fails.
func (bs *BackgroundSession) restartACPProcess() error {
	if bs.logger != nil {
		bs.logger.Info("Restarting ACP process",
			"session_id", bs.persistedID,
			"acp_id", bs.acpID,
			"restart_count", bs.restartCount+1)
	}

	// Kill the old process and clean up
	bs.killACPProcess()

	// Close the old ACP client if it exists
	if bs.acpClient != nil {
		bs.acpClient.Close()
		bs.acpClient = nil
	}

	// Clear the old connection
	bs.acpConn = nil

	// Record this restart attempt
	bs.recordRestart()

	// Start a new ACP process, attempting to resume the session
	err := bs.startACPProcess(bs.acpCommand, bs.acpCwd, bs.workingDir, bs.acpID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Error("Failed to restart ACP process",
				"session_id", bs.persistedID,
				"error", err)
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
			"acp_id", bs.acpID)
	}

	return nil
}

// startACPProcess starts the ACP server process and initializes the connection.
// If acpSessionID is provided and the agent supports session loading, it attempts
// to resume that session. Otherwise, it creates a new session.
// The acpCwd parameter sets the working directory for the ACP process itself.
// This method includes retry logic for transient failures during startup.
func (bs *BackgroundSession) startACPProcess(acpCommand, acpCwd, workingDir, acpSessionID string) error {
	var lastErr error
	for attempt := 0; attempt < maxACPStartRetries; attempt++ {
		if attempt > 0 {
			if bs.logger != nil {
				bs.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxACPStartRetries,
					"last_error", lastErr)
			}
			// Wait before retry
			select {
			case <-bs.ctx.Done():
				return &sessionError{"context cancelled during retry: " + bs.ctx.Err().Error()}
			case <-time.After(acpStartRetryDelay):
			}
		}

		err := bs.doStartACPProcess(acpCommand, acpCwd, workingDir, acpSessionID)
		if err == nil {
			return nil
		}
		lastErr = err

		// Only retry on connection/initialization failures, not on validation errors
		if strings.Contains(err.Error(), "empty ACP command") {
			return err // Don't retry validation errors
		}

		if bs.logger != nil {
			bs.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"error", err)
		}
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

	// Log at debug level as it comes in
	if c.logger != nil && len(p) > 0 {
		c.logger.Debug("agent stderr", "output", string(p))
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

// startStderrMonitor starts a goroutine that reads from stderr and writes to the collector.
func startStderrMonitor(stderr runner.ReadCloser, collector *stderrCollector) {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				collector.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		collector.Close()
	}()
}

func (bs *BackgroundSession) doStartACPProcess(acpCommand, acpCwd, workingDir, acpSessionID string) error {
	// Parse command using shell-aware tokenization
	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return &sessionError{err.Error()}
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	// Create stderr collector to capture output for error reporting
	// Keep last 8KB of stderr output
	stderrCollector := newStderrCollector(8192, bs.logger)

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
			return &sessionError{"failed to start with runner: " + err.Error()}
		}

		// Monitor stderr in background
		startStderrMonitor(stderr, stderrCollector)

		// Store wait function for cleanup
		// We'll call it in Close() method
		bs.acpCmd = nil // No cmd when using runner
	} else {
		// Direct execution (no restrictions)
		cmd = exec.CommandContext(bs.ctx, args[0], args[1:]...)

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
			return &sessionError{"failed to create stdin pipe: " + err.Error()}
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return &sessionError{"failed to create stdout pipe: " + err.Error()}
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return &sessionError{"failed to create stderr pipe: " + err.Error()}
		}

		if err := cmd.Start(); err != nil {
			return &sessionError{"failed to start ACP server: " + err.Error()}
		}

		// Monitor stderr in background (same as runner case)
		startStderrMonitor(stderrPipe, stderrCollector)

		bs.acpCmd = cmd

		// Create wait function for direct execution
		wait = func() error {
			return cmd.Wait()
		}
	}

	// Store wait function for cleanup
	bs.acpWait = wait

	// Create web client with callbacks that route to attached client or persist.
	// BackgroundSession implements SeqProvider, so seq is assigned at ACP receive time.
	webClientConfig := WebClientConfig{
		AutoApprove:          bs.autoApprove,
		SeqProvider:          bs, // BackgroundSession implements SeqProvider
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

	// Configure file linking if enabled
	// Web UI always uses HTTP links (file:// URLs are blocked by browsers for security)
	// Note: IsEnabled() and IsAllowOutsideWorkspace() are safe to call on nil receivers
	// and return sensible defaults (enabled=true, allowOutsideWorkspace=false)
	if bs.fileLinksConfig.IsEnabled() {
		webClientConfig.FileLinksConfig = &conversion.FileLinkerConfig{
			WorkingDir:            bs.workingDir,
			WorkspaceUUID:         bs.workspaceUUID, // Use UUID instead of path for security
			Enabled:               true,
			AllowOutsideWorkspace: bs.fileLinksConfig.IsAllowOutsideWorkspace(),
			UseHTTPLinks:          true,         // Web UI requires HTTP links
			APIPrefix:             bs.apiPrefix, // URL prefix for file server endpoint
		}
	}

	bs.acpClient = NewWebClient(webClientConfig)

	// Wrap stdout with a JSON line filter to discard non-JSON output
	// (e.g., ANSI escape sequences, terminal UI from crashed agents)
	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, bs.logger)

	// Create ACP connection with filtered stdout
	bs.acpConn = acp.NewClientSideConnection(bs.acpClient, stdin, filteredStdout)
	if bs.logger != nil {
		// Use a downgraded logger for the SDK to convert INFO to DEBUG
		// This prevents verbose SDK logs (e.g., "peer connection closed") from
		// appearing in stdout when log level is INFO.
		bs.acpConn.SetLogger(logging.DowngradeInfoToDebug(bs.logger))
	}

	// Initialize and get agent capabilities
	initResp, err := bs.acpConn.Initialize(bs.ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
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
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		bs.killACPProcess()
		return &sessionError{"failed to initialize: " + err.Error()}
	}

	// Log agent information at DEBUG level
	bs.logAgentInfo(initResp)

	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}

	// Build MCP servers list based on session settings and agent capabilities
	mcpServers := bs.startSessionMcpServer(bs.store, initResp.AgentCapabilities)

	// Try to load existing session if we have an ACP session ID and the agent supports it
	if acpSessionID != "" && initResp.AgentCapabilities.LoadSession {
		loadResp, err := bs.acpConn.LoadSession(bs.ctx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(acpSessionID),
			Cwd:        cwd,
			McpServers: mcpServers,
		})
		if err == nil {
			bs.acpID = acpSessionID
			// Store available modes from session load
			bs.setSessionModes(loadResp.Modes)
			if bs.logger != nil {
				bs.logger.Info("Resumed ACP session",
					"acp_session_id", acpSessionID)
				bs.logSessionModes(loadResp.Modes)
			}
			return nil
		}
		// Log the error but fall through to create a new session
		if bs.logger != nil {
			bs.logger.Warn("Failed to load ACP session, creating new session",
				"acp_session_id", acpSessionID,
				"error", err)
		}
	}

	// Create new session
	sessResp, err := bs.acpConn.NewSession(bs.ctx, acp.NewSessionRequest{
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
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP session creation failed", logAttrs...)
		}

		bs.killACPProcess()
		return &sessionError{"failed to create session: " + err.Error()}
	}

	bs.acpID = string(sessResp.SessionId)

	// Store available modes from session setup
	bs.setSessionModes(sessResp.Modes)

	if bs.logger != nil {
		bs.logger.Info("Created new ACP session",
			"acp_session_id", bs.acpID)
		bs.logSessionModes(sessResp.Modes)
	}

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

	// Log agent capabilities
	caps := resp.AgentCapabilities
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
			authMethods[i] = auth.Name
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

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID string   // Unique identifier of the sending client (for broadcast deduplication)
	PromptID string   // Client-generated prompt ID (for delivery confirmation)
	ImageIDs []string // IDs of images attached to the prompt
	FileIDs  []string // IDs of files attached to the prompt
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
	if bs.acpConn == nil {
		return &sessionError{"no ACP connection"}
	}

	bs.promptMu.Lock()
	if bs.isPrompting {
		// Check if the ACP connection is dead (process crashed)
		// We use a non-blocking check on the Done() channel to detect this.
		// This is more reliable than a timeout because legitimate operations
		// (like running tests) can take a very long time.
		acpDead := false
		if bs.acpConn != nil {
			select {
			case <-bs.acpConn.Done():
				acpDead = true
			default:
				// Connection still alive
			}
		} else {
			acpDead = true // No connection at all
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
				// Notify observers that we're restarting
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("The AI agent process stopped unexpectedly. Restarting...")
				})

				// Attempt to restart the ACP process
				if err := bs.restartACPProcess(); err != nil {
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError("Failed to restart the AI agent: " + err.Error() + ". Please switch to another conversation and back to retry.")
					})
					return &sessionError{"ACP process died and restart failed: " + err.Error()}
				}

				// Restart succeeded - notify user and retry the prompt
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted successfully. Please resend your message.")
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

	// Build the actual prompt to send to ACP
	// Apply message processors (prepend/append based on config)
	promptMessage := config.ApplyProcessors(message, bs.processors, isFirst)

	// Apply external command hooks
	var hookAttachmentBlocks []acp.ContentBlock
	if bs.hookManager != nil && len(bs.hookManager.Hooks()) > 0 {
		hookInput := &msghooks.HookInput{
			Message:        promptMessage,
			IsFirstMessage: isFirst,
			SessionID:      bs.persistedID,
			WorkingDir:     bs.workingDir,
		}
		hookResult, hookErr := bs.hookManager.Apply(bs.ctx, hookInput)
		if hookErr != nil {
			if bs.logger != nil {
				bs.logger.Error("Hook execution failed", "error", hookErr)
			}
			// Continue with original message on hook failure
		} else if hookResult != nil {
			promptMessage = hookResult.Message

			// Convert hook attachments to content blocks
			if len(hookResult.Attachments) > 0 {
				acpAttachments, err := hookResult.ToACPAttachments(bs.workingDir)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Error("Failed to resolve hook attachments", "error", err)
					}
				} else {
					for _, att := range acpAttachments {
						if att.Type == "image" {
							hookAttachmentBlocks = append(hookAttachmentBlocks, acp.ImageBlock(att.Data, att.MimeType))
						}
						// Note: Non-image attachments could be handled differently in the future
					}
				}
			}
		}
	}

	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(promptMessage)
	}

	// Build final content blocks: images first (from uploads and hooks), then text
	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+len(hookAttachmentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, hookAttachmentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	// Run prompt in background
	go func() {
		promptResp, err := bs.acpConn.Prompt(bs.ctx, acp.PromptRequest{
			SessionId: acp.SessionId(bs.acpID),
			Prompt:    finalBlocks,
		})

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
			userFriendlyErr := formatACPError(err)
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError(userFriendlyErr)
			})
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
	}()

	return nil
}

// analyzeFollowUpQuestions asynchronously analyzes an agent message for follow-up questions.
// It uses the auxiliary conversation to identify questions and sends suggested responses
// to observers via OnActionButtons. This is non-blocking and runs in a goroutine.
// userPrompt provides context about what the user asked.
func (bs *BackgroundSession) analyzeFollowUpQuestions(userPrompt, agentMessage string) {
	// Create a timeout context for the analysis
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if session is still valid before starting
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis skipped: session closed")
		return
	}

	bs.logger.Debug("follow-up analysis: starting",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Use the auxiliary conversation to analyze the message
	suggestions, err := auxiliary.AnalyzeFollowUpQuestions(ctx, userPrompt, agentMessage)
	if err != nil {
		bs.logger.Debug("follow-up analysis failed", "error", err)
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

	if bs.acpConn == nil {
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
		_, err := bs.acpConn.SetSessionMode(ctx, acp.SetSessionModeRequest{
			SessionId: acp.SessionId(bs.acpID),
			ModeId:    acp.SessionModeId(value),
		})
		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to set session mode",
					"config_id", configID,
					"value", value,
					"error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
	} else {
		// Future: Use SetConfigOption API when available in SDK
		// For now, only mode category is supported via legacy API
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

// formatACPError transforms ACP errors into user-friendly messages.
// It detects common error patterns and provides actionable guidance.
func formatACPError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// Timeout errors from ACP server (tool execution took too long)
	if strings.Contains(errMsg, "aborted due to timeout") {
		return "A tool operation timed out. The AI agent's tool call took too long to complete. " +
			"Try breaking your request into smaller steps, or ask for a more specific task."
	}

	// Connection/transport errors
	if strings.Contains(errMsg, "peer disconnected") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") {
		return "Lost connection to the AI agent. The agent process may have crashed or been restarted. " +
			"Please try sending your message again."
	}

	// Context cancelled (user cancelled or session closed)
	if strings.Contains(errMsg, "context canceled") ||
		strings.Contains(errMsg, "context deadline exceeded") {
		return "The request was cancelled. Please try again."
	}

	// Rate limiting
	if strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "too many requests") {
		return "Rate limit reached. Please wait a moment before sending another message."
	}

	// Generic JSON-RPC internal error with details
	if strings.Contains(errMsg, "-32603") && strings.Contains(errMsg, "Internal error") {
		// Extract any details if present
		if strings.Contains(errMsg, "details") {
			return "The AI agent encountered an internal error. Please try again, " +
				"or simplify your request if the problem persists."
		}
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
		bs.dismissActivePromptLocked("timeout")
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
		bs.dismissActivePromptLocked("cancelled")
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
func (bs *BackgroundSession) HandleUIPromptAnswer(requestID, optionID, label string) {
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
