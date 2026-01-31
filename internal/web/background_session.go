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
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
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
	recorder        *session.Recorder
	agentMsgBuffer  *agentMessageBuffer
	agentThoughtBuf *agentMessageBuffer

	// Session lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Int32

	// Observers (multiple clients can observe this session)
	observersMu sync.RWMutex
	observers   map[SessionObserver]struct{}

	// Prompt state
	promptMu    sync.Mutex
	isPrompting bool
	promptCount int

	// Configuration
	autoApprove bool
	logger      *slog.Logger

	// Resume state - for injecting conversation history
	isResumed       bool           // True if this session was resumed from storage
	store           *session.Store // Store reference for reading history
	historyInjected bool           // True after history has been injected

	// Conversation processing
	processors    []config.MessageProcessor // Merged processors from global and workspace config
	isFirstPrompt bool                      // True until first prompt is sent (for processor conditions)
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
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
func NewBackgroundSession(config BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:             ctx,
		cancel:          cancel,
		autoApprove:     config.AutoApprove,
		logger:          config.Logger,
		agentMsgBuffer:  &agentMessageBuffer{},
		agentThoughtBuf: &agentMessageBuffer{},
		observers:       make(map[SessionObserver]struct{}),
		processors:      config.Processors,
		isFirstPrompt:   true, // New session starts with first prompt pending
	}

	// Create recorder for persistence
	if config.Store != nil {
		bs.recorder = session.NewRecorder(config.Store)
		bs.persistedID = bs.recorder.SessionID()
		// Use StartWithCommand to store the ACP command for session resume
		if err := bs.recorder.StartWithCommand(config.ACPServer, config.ACPCommand, config.WorkingDir); err != nil {
			cancel()
			return nil, err
		}
		// Update session name
		if config.SessionName != "" {
			config.Store.UpdateMetadata(bs.persistedID, func(meta *session.Metadata) {
				meta.Name = config.SessionName
			})
		}

		// Create session-scoped logger with context
		if bs.logger != nil {
			bs.logger = logging.WithSessionContext(bs.logger, bs.persistedID, config.WorkingDir, config.ACPServer)
		}
	}

	// Start ACP process (no ACP session ID for new sessions)
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir, ""); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
	}

	// Store the ACP session ID in metadata for future resumption
	if config.Store != nil && bs.acpID != "" {
		if err := config.Store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
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
		persistedID:     config.PersistedID,
		ctx:             ctx,
		cancel:          cancel,
		autoApprove:     config.AutoApprove,
		logger:          sessionLogger,
		agentMsgBuffer:  &agentMessageBuffer{},
		agentThoughtBuf: &agentMessageBuffer{},
		observers:       make(map[SessionObserver]struct{}),
		isResumed:       true, // Mark as resumed session
		store:           config.Store,
		processors:      config.Processors,
		isFirstPrompt:   false, // Resumed session = first prompt already sent
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

// GetEventCount returns the current event count for the session.
// Returns 0 if the recorder is not available or there's an error.
func (bs *BackgroundSession) GetEventCount() int {
	if bs.recorder == nil {
		return 0
	}
	return bs.recorder.EventCount()
}

// CreatedAt returns when the session was created.
func (bs *BackgroundSession) CreatedAt() time.Time {
	if bs.recorder != nil {
		// This would need to be stored; for now return zero
		return time.Time{}
	}
	return time.Time{}
}

// --- Observer Management ---

// AddObserver adds an observer to receive session events.
// Multiple observers can be added to the same session.
func (bs *BackgroundSession) AddObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	defer bs.observersMu.Unlock()
	if bs.observers == nil {
		bs.observers = make(map[SessionObserver]struct{})
	}
	bs.observers[observer] = struct{}{}
	if bs.logger != nil {
		bs.logger.Debug("Observer added", "observer_count", len(bs.observers))
	}
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

// flushAndPersistMessages persists any buffered agent messages.
// Note: Thoughts are persisted BEFORE messages to maintain correct display order
// (the agent thinks before responding).
func (bs *BackgroundSession) flushAndPersistMessages() {
	if bs.recorder == nil {
		return
	}

	// Persist thoughts FIRST (agent thinks before responding)
	if bs.agentThoughtBuf != nil {
		text := bs.agentThoughtBuf.Flush()
		if text != "" {
			if err := bs.recorder.RecordAgentThought(text); err != nil && bs.logger != nil {
				bs.logger.Error("Failed to persist agent thought", "error", err)
			}
		}
	}

	// Then persist the agent message
	if bs.agentMsgBuffer != nil {
		text := bs.agentMsgBuffer.Flush()
		if text != "" {
			if err := bs.recorder.RecordAgentMessage(text); err != nil && bs.logger != nil {
				bs.logger.Error("Failed to persist agent message", "error", err)
			}
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
	bs.acpClient = NewWebClient(WebClientConfig{
		AutoApprove:    bs.autoApprove,
		OnAgentMessage: bs.onAgentMessage,
		OnAgentThought: bs.onAgentThought,
		OnToolCall:     bs.onToolCall,
		OnToolUpdate:   bs.onToolUpdate,
		OnPlan:         bs.onPlan,
		OnFileWrite:    bs.onFileWrite,
		OnFileRead:     bs.onFileRead,
		OnPermission:   bs.onPermission,
	})

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
		bs.promptMu.Unlock()
		return &sessionError{"prompt already in progress"}
	}
	bs.isPrompting = true
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

	// Persist user prompt with image references
	if bs.recorder != nil {
		if err := bs.recorder.RecordUserPromptWithImages(message, imageRefs); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(meta.SenderID, meta.PromptID, message, imageIDs)
	})

	// Build the actual prompt to send to ACP
	// Apply message processors (prepend/append based on config)
	promptMessage := config.ApplyProcessors(message, bs.processors, isFirst)
	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(promptMessage)
	}

	// Build final content blocks: images first, then text
	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	// Run prompt in background
	go func() {
		defer func() {
			bs.promptMu.Lock()
			bs.isPrompting = false
			bs.promptMu.Unlock()
		}()

		_, err := bs.acpConn.Prompt(bs.ctx, acp.PromptRequest{
			SessionId: acp.SessionId(bs.acpID),
			Prompt:    finalBlocks,
		})

		if bs.IsClosed() {
			return
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
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
		}
	}()

	return nil
}

// Cancel cancels the current prompt.
func (bs *BackgroundSession) Cancel() error {
	if bs.acpConn == nil {
		return nil
	}
	return bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
		SessionId: acp.SessionId(bs.acpID),
	})
}

// --- Callback methods for WebClient ---

func (bs *BackgroundSession) onAgentMessage(html string) {
	if bs.IsClosed() {
		return
	}

	// Buffer for persistence
	if bs.agentMsgBuffer != nil {
		bs.agentMsgBuffer.Write(html)
	}

	// Notify all observers
	observerCount := bs.ObserverCount()
	if bs.logger != nil && observerCount > 1 {
		bs.logger.Debug("Notifying multiple observers of agent message",
			"observer_count", observerCount,
			"html_len", len(html))
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(html)
	})
}

func (bs *BackgroundSession) onAgentThought(text string) {
	if bs.IsClosed() {
		return
	}

	// Buffer for persistence
	if bs.agentThoughtBuf != nil {
		bs.agentThoughtBuf.Write(text)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentThought(text)
	})
}

func (bs *BackgroundSession) onToolCall(id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Persist
	if bs.recorder != nil {
		bs.recorder.RecordToolCall(id, title, status, "", nil, nil)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(id, title, status)
	})
}

func (bs *BackgroundSession) onToolUpdate(id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Persist
	if bs.recorder != nil {
		bs.recorder.RecordToolCallUpdate(id, status, nil)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(id, status)
	})
}

func (bs *BackgroundSession) onPlan() {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan()
	})
}

func (bs *BackgroundSession) onFileWrite(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(path, size)
	})
}

func (bs *BackgroundSession) onFileRead(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(path, size)
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
