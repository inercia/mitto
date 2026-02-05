package web

import (
	"context"
	"log/slog"
	"os/exec"
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
	"github.com/inercia/mitto/internal/msghooks"
	"github.com/inercia/mitto/internal/session"
)

// BackgroundSession manages an ACP session that runs independently of WebSocket connections.
// It continues running even when no client is connected, persisting all events to disk.
// Multiple observers can subscribe to receive real-time updates.
type BackgroundSession struct {
	// Immutable identifiers
	persistedID string // Session ID for persistence and routing
	acpID       string // ACP protocol session ID

	// ACP connection state
	acpCmd    *exec.Cmd
	acpConn   *acp.ClientSideConnection
	acpClient *WebClient

	// Session persistence
	recorder    *session.Recorder
	eventBuffer *EventBuffer // Unified buffer for all streaming events
	bufferMu    sync.Mutex   // Protects eventBuffer

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
	isPrompting          bool
	promptCount          int
	promptStartTime      time.Time // When the current prompt started (for stuck detection)
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
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID  string
	ACPCommand   string
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

	ActionButtonsConfig *config.ActionButtonsConfig // Action buttons configuration
	FileLinksConfig     *config.FileLinksConfig     // File path linking configuration
	APIPrefix           string                      // URL prefix for API endpoints (for HTTP file links)
	WorkspaceUUID       string                      // Workspace UUID for secure file links
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
func NewBackgroundSession(cfg BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:                 ctx,
		cancel:              cancel,
		autoApprove:         cfg.AutoApprove,
		logger:              cfg.Logger,
		eventBuffer:         NewEventBuffer(),
		observers:           make(map[SessionObserver]struct{}),
		processors:          cfg.Processors,
		hookManager:         cfg.HookManager,
		workingDir:          cfg.WorkingDir,
		isFirstPrompt:       true, // New session starts with first prompt pending
		queueConfig:         cfg.QueueConfig,
		actionButtonsConfig: cfg.ActionButtonsConfig,
		fileLinksConfig:     cfg.FileLinksConfig,
		apiPrefix:           cfg.APIPrefix,
		workspaceUUID:       cfg.WorkspaceUUID,
	}

	// Create recorder for persistence
	if cfg.Store != nil {
		bs.recorder = session.NewRecorder(cfg.Store)
		bs.persistedID = bs.recorder.SessionID()
		bs.store = cfg.Store
		// Use StartWithCommand to store the ACP command for session resume
		if err := bs.recorder.StartWithCommand(cfg.ACPServer, cfg.ACPCommand, cfg.WorkingDir); err != nil {
			cancel()
			return nil, err
		}
		// Update session name
		if cfg.SessionName != "" {
			cfg.Store.UpdateMetadata(bs.persistedID, func(meta *session.Metadata) {
				meta.Name = cfg.SessionName
			})
		}

		// Initialize nextSeq from current event count (new session starts at 1)
		bs.nextSeq = int64(bs.recorder.EventCount()) + 1

		// Create session-scoped logger with context
		if bs.logger != nil {
			bs.logger = logging.WithSessionContext(bs.logger, bs.persistedID, cfg.WorkingDir, cfg.ACPServer)
		}
	}

	// Start ACP process (no ACP session ID for new sessions)
	if err := bs.startACPProcess(cfg.ACPCommand, cfg.WorkingDir, ""); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
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
		persistedID:         config.PersistedID,
		ctx:                 ctx,
		cancel:              cancel,
		autoApprove:         config.AutoApprove,
		logger:              sessionLogger,
		eventBuffer:         NewEventBuffer(),
		observers:           make(map[SessionObserver]struct{}),
		isResumed:           true, // Mark as resumed session
		store:               config.Store,
		processors:          config.Processors,
		hookManager:         config.HookManager,
		workingDir:          config.WorkingDir,
		isFirstPrompt:       false, // Resumed session = first prompt already sent
		queueConfig:         config.QueueConfig,
		actionButtonsConfig: config.ActionButtonsConfig,
		fileLinksConfig:     config.FileLinksConfig,
		apiPrefix:           config.APIPrefix,
		workspaceUUID:       config.WorkspaceUUID,
	}

	// Resume recorder for the existing session
	if config.Store != nil {
		bs.recorder = session.NewRecorderWithID(config.Store, config.PersistedID)
		if err := bs.recorder.Resume(); err != nil {
			cancel()
			return nil, &sessionError{"failed to resume session recording: " + err.Error()}
		}
		// Update the metadata to mark it as active again
		if err := config.Store.UpdateMetadata(config.PersistedID, func(m *session.Metadata) {
			m.Status = "active"
		}); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to update session status", "error", err)
		}

		// Initialize nextSeq from current event count
		bs.nextSeq = int64(bs.recorder.EventCount()) + 1
	}

	// Start ACP process, passing the ACP session ID for potential resumption
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir, config.ACPSessionID); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
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
	seq := bs.nextSeq
	bs.nextSeq++
	return seq
}

// refreshNextSeq updates nextSeq from the current event count.
// This should be called after events are persisted outside the normal buffer flow
// (e.g., after user prompts are persisted directly).
func (bs *BackgroundSession) refreshNextSeq() {
	if bs.recorder == nil {
		return
	}
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()
	bs.nextSeq = int64(bs.recorder.EventCount()) + 1
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

	if isPrompting {
		bs.replayBufferedEventsTo(observer)
	} else {
		// If not prompting, send cached action buttons to the new observer
		// This ensures new clients see the follow-up suggestions
		bs.sendCachedActionButtonsTo(observer)
	}
}

// replayBufferedEventsTo sends all buffered events to a single observer in order.
// This is used to catch up newly connected observers on in-progress streaming.
func (bs *BackgroundSession) replayBufferedEventsTo(observer SessionObserver) {
	bs.bufferMu.Lock()
	var events []BufferedEvent
	if bs.eventBuffer != nil {
		events = bs.eventBuffer.Events() // Get a copy, don't flush
	}
	bs.bufferMu.Unlock()

	if len(events) == 0 {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Replaying buffered events to new observer", "event_count", len(events))
	}

	for _, event := range events {
		event.ReplayTo(observer)
	}
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

	// Cancel context to stop any ongoing operations
	bs.cancel()

	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Close ACP client
	if bs.acpClient != nil {
		bs.acpClient.Close()
	}

	// Kill ACP process
	if bs.acpCmd != nil && bs.acpCmd.Process != nil {
		bs.acpCmd.Process.Kill()
	}

	// End recording
	if bs.recorder != nil {
		bs.recorder.End(reason)
	}
}

// Suspend suspends the session without ending it (for temporary disconnects).
func (bs *BackgroundSession) Suspend() {
	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Suspend recording (keeps status as "active")
	if bs.recorder != nil {
		bs.recorder.Suspend()
	}
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
		bs.logger.Info("Injecting conversation history into resumed session",
			"history_length", len(history))
	}

	return history + message
}

// flushAndPersistMessages persists all buffered events in streaming order.
// Events are persisted in the order they were received, ensuring correct
// interleaving of agent messages, thoughts, and tool calls.
func (bs *BackgroundSession) flushAndPersistMessages() {
	if bs.recorder == nil {
		return
	}

	// Lock buffer access and flush all events
	bs.bufferMu.Lock()
	var events []BufferedEvent
	if bs.eventBuffer != nil {
		events = bs.eventBuffer.Flush()
	}
	bs.bufferMu.Unlock()

	// Persist each event in order
	for _, event := range events {
		if err := event.PersistTo(bs.recorder); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist event", "type", event.Type, "error", err)
		}
	}
}

// startACPProcess starts the ACP server process and initializes the connection.
// If acpSessionID is provided and the agent supports session loading, it attempts
// to resume that session. Otherwise, it creates a new session.
func (bs *BackgroundSession) startACPProcess(acpCommand, workingDir, acpSessionID string) error {
	args := strings.Fields(acpCommand)
	if len(args) == 0 {
		return &sessionError{"empty ACP command"}
	}

	cmd := exec.CommandContext(bs.ctx, args[0], args[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return &sessionError{"failed to create stdin pipe: " + err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &sessionError{"failed to create stdout pipe: " + err.Error()}
	}

	if err := cmd.Start(); err != nil {
		return &sessionError{"failed to start ACP server: " + err.Error()}
	}

	bs.acpCmd = cmd

	// Create web client with callbacks that route to attached client or persist
	webClientConfig := WebClientConfig{
		AutoApprove:    bs.autoApprove,
		OnAgentMessage: bs.onAgentMessage,
		OnAgentThought: bs.onAgentThought,
		OnToolCall:     bs.onToolCall,
		OnToolUpdate:   bs.onToolUpdate,
		OnPlan:         bs.onPlan,
		OnFileWrite:    bs.onFileWrite,
		OnFileRead:     bs.onFileRead,
		OnPermission:   bs.onPermission,
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

	// Create ACP connection
	bs.acpConn = acp.NewClientSideConnection(bs.acpClient, stdin, stdout)
	if bs.logger != nil {
		bs.acpConn.SetLogger(bs.logger)
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
		bs.acpCmd.Process.Kill()
		return &sessionError{"failed to initialize: " + err.Error()}
	}

	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}

	// Try to load existing session if we have an ACP session ID and the agent supports it
	if acpSessionID != "" && initResp.AgentCapabilities.LoadSession {
		loadResp, err := bs.acpConn.LoadSession(bs.ctx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(acpSessionID),
			Cwd:        cwd,
			McpServers: []acp.McpServer{},
		})
		if err == nil {
			bs.acpID = acpSessionID
			if bs.logger != nil {
				bs.logger.Info("Resumed ACP session",
					"acp_session_id", acpSessionID,
					"modes", loadResp.Modes,
					"models", loadResp.Models)
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
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		bs.acpCmd.Process.Kill()
		return &sessionError{"failed to create session: " + err.Error()}
	}

	bs.acpID = string(sessResp.SessionId)
	return nil
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

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	imageIDs := meta.ImageIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil {
		return &sessionError{"no ACP connection"}
	}

	bs.promptMu.Lock()
	if bs.isPrompting {
		// Check if the current prompt is stuck (running for too long without activity)
		// If it's been more than 5 minutes, auto-recover by resetting the state
		const stuckPromptTimeout = 5 * time.Minute
		if !bs.promptStartTime.IsZero() && time.Since(bs.promptStartTime) > stuckPromptTimeout {
			if bs.logger != nil {
				bs.logger.Warn("Auto-recovering stuck prompt",
					"prompt_start_time", bs.promptStartTime,
					"elapsed", time.Since(bs.promptStartTime))
			}
			bs.isPrompting = false
			bs.lastResponseComplete = time.Now()
			// Fall through to process the new prompt
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

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Persist user prompt with image references
	// User prompts are persisted immediately (not buffered), so we need to
	// refresh nextSeq after persistence to get the correct seq for the prompt
	var userPromptSeq int64
	if bs.recorder != nil {
		if err := bs.recorder.RecordUserPromptWithImages(message, imageRefs); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
		// Get the seq that was assigned to the user prompt (it's the current event count)
		userPromptSeq = int64(bs.recorder.EventCount())
		// Update nextSeq for subsequent agent events
		bs.refreshNextSeq()
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs)
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
		bs.promptMu.Unlock()

		if bs.IsClosed() {
			return
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
		}

		// Get the agent message for async analysis (before flushing)
		var agentMessage string
		isEndTurn := err == nil && promptResp.StopReason == acp.StopReasonEndTurn
		if bs.actionButtonsConfig.IsEnabled() && isEndTurn {
			bs.bufferMu.Lock()
			if bs.eventBuffer != nil {
				agentMessage = bs.eventBuffer.GetAgentMessage()
			}
			bs.bufferMu.Unlock()
		}

		// Persist buffered messages
		bs.flushAndPersistMessages()

		// Notify all observers
		eventCount := bs.GetEventCount()
		if err != nil {
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("Prompt failed: " + err.Error())
			})
		} else {
			bs.notifyObservers(func(o SessionObserver) {
				o.OnPromptComplete(eventCount)
			})

			// Process next queued message if queue processing is enabled
			bs.processNextQueuedMessage()

			// Async follow-up analysis (non-blocking)
			// This runs after prompt_complete so the user sees the response immediately
			// Note: 'message' is captured from the outer function scope (the user's prompt)
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

	bs.logger.Info("follow-up analysis: sending buttons to observers", "count", len(buttons))
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

	bs.logger.Info("follow-up suggestions: triggering analysis for resumed session",
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
	// Reset prompting state regardless of whether cancel succeeds
	// This ensures the session can accept new prompts even if the agent is unresponsive
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptMu.Unlock()

	if wasPrompting {
		// Flush any buffered content before notifying completion
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
		}
		bs.flushAndPersistMessages()

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
	bs.promptMu.Unlock()

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
	bs.flushAndPersistMessages()

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

func (bs *BackgroundSession) onAgentMessage(html string) {
	if bs.IsClosed() {
		return
	}

	// Get next seq for potential new event
	candidateSeq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	// AppendAgentMessage returns the actual seq used (may be existing if appending)
	// and whether a new event was created (if not, we need to "return" the seq)
	bs.bufferMu.Lock()
	var seq int64
	var isNew bool
	if bs.eventBuffer != nil {
		seq, isNew = bs.eventBuffer.AppendAgentMessage(candidateSeq, html)
	} else {
		seq = candidateSeq
		isNew = true
	}
	bs.bufferMu.Unlock()

	// If we didn't create a new event, we need to "return" the seq we got
	// by decrementing nextSeq (since we incremented it but didn't use it)
	if !isNew {
		bs.seqMu.Lock()
		bs.nextSeq--
		bs.seqMu.Unlock()
	}

	// Notify all observers
	observerCount := bs.ObserverCount()
	if bs.logger != nil && observerCount > 1 {
		bs.logger.Debug("Notifying multiple observers of agent message",
			"observer_count", observerCount,
			"html_len", len(html),
			"seq", seq)
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(seq, html)
	})
}

func (bs *BackgroundSession) onAgentThought(text string) {
	if bs.IsClosed() {
		return
	}

	// Get next seq for potential new event
	candidateSeq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	var seq int64
	var isNew bool
	if bs.eventBuffer != nil {
		seq, isNew = bs.eventBuffer.AppendAgentThought(candidateSeq, text)
	} else {
		seq = candidateSeq
		isNew = true
	}
	bs.bufferMu.Unlock()

	// If we didn't create a new event, return the unused seq
	if !isNew {
		bs.seqMu.Lock()
		bs.nextSeq--
		bs.seqMu.Unlock()
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentThought(seq, text)
	})
}

func (bs *BackgroundSession) onToolCall(id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Get next seq - tool calls always create new events
	seq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendToolCall(seq, id, title, status)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(seq, id, title, status)
	})
}

func (bs *BackgroundSession) onToolUpdate(id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Get next seq - tool updates always create new events
	seq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendToolCallUpdate(seq, id, status)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(seq, id, status)
	})
}

func (bs *BackgroundSession) onPlan() {
	if bs.IsClosed() {
		return
	}

	// Get next seq - plan events always create new events
	seq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendPlan(seq)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan(seq)
	})
}

func (bs *BackgroundSession) onFileWrite(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Get next seq - file write events always create new events
	seq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendFileWrite(seq, path, size)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(seq, path, size)
	})
}

func (bs *BackgroundSession) onFileRead(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Get next seq - file read events always create new events
	seq := bs.getNextSeq()

	// Buffer for persistence (protected by mutex)
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendFileRead(seq, path, size)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(seq, path, size)
	})
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if bs.IsClosed() {
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	// Check if we have any observers
	hasObservers := bs.HasObservers()

	// If no observers, auto-approve if configured
	if !hasObservers {
		if bs.autoApprove {
			resp := mittoAcp.AutoApprovePermission(params.Options)
			// Record the permission decision
			if bs.recorder != nil && resp.Outcome.Selected != nil {
				bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "auto_approved_no_client")
			}
			return resp, nil
		}
		// No observers and no auto-approve - cancel
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Get a snapshot of observers
	bs.observersMu.RLock()
	observers := make([]SessionObserver, 0, len(bs.observers))
	for o := range bs.observers {
		observers = append(observers, o)
	}
	bs.observersMu.RUnlock()

	// Ask first observer for permission (others just observe)
	if len(observers) > 0 {
		resp, err := observers[0].OnPermission(ctx, params)
		if err == nil {
			// Persist the permission decision
			if bs.recorder != nil {
				if resp.Outcome.Selected != nil {
					bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "user_selected")
				} else {
					bs.recorder.RecordPermission(title, "", "cancelled")
				}
			}
			return resp, nil
		}
	}

	// No observer handled it - cancel
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}
