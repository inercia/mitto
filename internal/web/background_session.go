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

	// Legacy client support (for backward compatibility during migration)
	clientMu sync.RWMutex
	client   *WSClient

	// Prompt state
	promptMu    sync.Mutex
	isPrompting bool
	promptCount int

	// Permission handling when no client is attached
	permissionMu   sync.Mutex
	permissionChan chan acp.RequestPermissionResponse

	// Configuration
	autoApprove bool
	logger      *slog.Logger

	// Resume state - for injecting conversation history
	isResumed       bool           // True if this session was resumed from storage
	store           *session.Store // Store reference for reading history
	historyInjected bool           // True after history has been injected
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID string
	ACPCommand  string
	ACPServer   string
	WorkingDir  string
	AutoApprove bool
	Logger      *slog.Logger
	Store       *session.Store
	SessionName string
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
		permissionChan:  make(chan acp.RequestPermissionResponse, 1),
		observers:       make(map[SessionObserver]struct{}),
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
	}

	// Start ACP process
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
	}

	return bs, nil
}

// ResumeBackgroundSession creates a background session for an existing persisted session.
// This is used when switching to an old conversation - we create a new ACP connection
// but continue recording to the existing session.
func ResumeBackgroundSession(config BackgroundSessionConfig) (*BackgroundSession, error) {
	if config.PersistedID == "" {
		return nil, &sessionError{"persisted session ID is required for resume"}
	}

	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		persistedID:     config.PersistedID,
		ctx:             ctx,
		cancel:          cancel,
		autoApprove:     config.AutoApprove,
		logger:          config.Logger,
		agentMsgBuffer:  &agentMessageBuffer{},
		agentThoughtBuf: &agentMessageBuffer{},
		permissionChan:  make(chan acp.RequestPermissionResponse, 1),
		observers:       make(map[SessionObserver]struct{}),
		isResumed:       true, // Mark as resumed session
		store:           config.Store,
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

	// Start ACP process
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
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

// AttachClient attaches a WebSocket client to receive real-time updates.
// Only one client can be attached at a time.
func (bs *BackgroundSession) AttachClient(client *WSClient) {
	bs.clientMu.Lock()
	defer bs.clientMu.Unlock()
	bs.client = client
}

// DetachClient detaches the current client.
func (bs *BackgroundSession) DetachClient() {
	bs.clientMu.Lock()
	defer bs.clientMu.Unlock()
	bs.client = nil
}

// GetAttachedClient returns the currently attached client, or nil.
func (bs *BackgroundSession) GetAttachedClient() *WSClient {
	bs.clientMu.RLock()
	defer bs.clientMu.RUnlock()
	return bs.client
}

// HasAttachedClient returns true if a client is currently attached.
func (bs *BackgroundSession) HasAttachedClient() bool {
	return bs.GetAttachedClient() != nil
}

// --- Observer Management (new architecture) ---

// AddObserver adds an observer to receive session events.
// Multiple observers can be added to the same session.
func (bs *BackgroundSession) AddObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	defer bs.observersMu.Unlock()
	if bs.observers == nil {
		bs.observers = make(map[SessionObserver]struct{})
	}
	bs.observers[observer] = struct{}{}
}

// RemoveObserver removes an observer from the session.
func (bs *BackgroundSession) RemoveObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	defer bs.observersMu.Unlock()
	delete(bs.observers, observer)
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
			"session_id", bs.persistedID,
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
func (bs *BackgroundSession) startACPProcess(acpCommand, workingDir string) error {
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

	// Initialize
	_, err = bs.acpConn.Initialize(bs.ctx, acp.InitializeRequest{
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

	// Create session
	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}
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

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	return bs.PromptWithImages(message, nil)
}

// PromptWithImages sends a message with optional images to the agent. This runs asynchronously.
// The imageIDs should be IDs of images previously uploaded to this session.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithImages(message string, imageIDs []string) error {
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
	bs.promptMu.Unlock()

	// Check if session needs auto-title generation (before any state changes)
	// Only generate title if metadata.Name is empty
	shouldGenerateTitle := bs.NeedsTitle()

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

	// Build the actual prompt to send to ACP
	promptMessage := message
	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(message)
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

		// Notify legacy attached client
		client := bs.GetAttachedClient()
		if client != nil {
			if err != nil {
				client.sendError("Prompt failed: " + err.Error())
			} else {
				// Include event_count so frontend can update lastSeq for sync
				client.sendMessage(WSMsgTypePromptComplete, map[string]interface{}{
					"session_id":  bs.persistedID,
					"event_count": bs.GetEventCount(),
				})

				// Auto-generate title if session has no title yet
				if shouldGenerateTitle && bs.persistedID != "" {
					client.generateAndSetTitle(message, bs.persistedID)
				}
			}
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
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(html)
	})

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeAgentMessage, map[string]interface{}{
			"html":       html,
			"format":     "html",
			"session_id": bs.persistedID,
		})
	}
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

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeAgentThought, map[string]interface{}{
			"text":       text,
			"session_id": bs.persistedID,
		})
	}
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

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeToolCall, map[string]interface{}{
			"id":         id,
			"title":      title,
			"status":     status,
			"session_id": bs.persistedID,
		})
	}
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

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		data := map[string]interface{}{
			"id":         id,
			"session_id": bs.persistedID,
		}
		if status != nil {
			data["status"] = *status
		}
		client.sendMessage(WSMsgTypeToolUpdate, data)
	}
}

func (bs *BackgroundSession) onPlan() {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan()
	})

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypePlan, map[string]interface{}{
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onFileWrite(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(path, size)
	})

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeFileWrite, map[string]interface{}{
			"path":       path,
			"size":       size,
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onFileRead(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(path, size)
	})

	// Forward to legacy attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeFileRead, map[string]interface{}{
			"path":       path,
			"size":       size,
			"session_id": bs.persistedID,
		})
	}
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

	// Check if we have any observers or legacy clients
	hasObservers := bs.HasObservers()
	client := bs.GetAttachedClient()

	// If no observers and no client, auto-approve if configured
	if !hasObservers && client == nil {
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

	// Try to get response from observers first
	// The first observer to respond wins
	if hasObservers {
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
	}

	// Fallback to legacy client if no observers handled it
	if client != nil {
		// Format options for frontend
		options := make([]map[string]string, len(params.Options))
		for i, opt := range params.Options {
			options[i] = map[string]string{
				"id":   string(opt.OptionId),
				"name": opt.Name,
				"kind": string(opt.Kind),
			}
		}

		client.sendMessage(WSMsgTypePermission, map[string]interface{}{
			"title":      title,
			"options":    options,
			"session_id": bs.persistedID,
		})

		// Wait for response from client
		bs.permissionMu.Lock()
		// Clear any stale response
		select {
		case <-bs.permissionChan:
		default:
		}
		bs.permissionMu.Unlock()

		select {
		case <-ctx.Done():
			return acp.RequestPermissionResponse{}, ctx.Err()
		case <-bs.ctx.Done():
			return acp.RequestPermissionResponse{}, bs.ctx.Err()
		case resp := <-bs.permissionChan:
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

	// Shouldn't reach here, but cancel if we do
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// AnswerPermission provides a response to a pending permission request.
func (bs *BackgroundSession) AnswerPermission(optionID string, cancel bool) {
	var resp acp.RequestPermissionResponse
	if cancel {
		resp.Outcome.Cancelled = &acp.RequestPermissionOutcomeCancelled{}
	} else {
		resp.Outcome.Selected = &acp.RequestPermissionOutcomeSelected{
			OptionId: acp.PermissionOptionId(optionID),
		}
	}

	select {
	case bs.permissionChan <- resp:
	default:
	}
}
