package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gorilla/websocket"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// agentMessageBuffer accumulates agent message chunks for persistence.
// We buffer chunks and persist complete messages to avoid excessive disk writes.
type agentMessageBuffer struct {
	text      strings.Builder
	lastFlush time.Time
}

func (b *agentMessageBuffer) Write(text string) {
	b.text.WriteString(text)
}

func (b *agentMessageBuffer) Flush() string {
	text := b.text.String()
	b.text.Reset()
	b.lastFlush = time.Now()
	return text
}

// SessionContext holds all state for a single ACP session.
// It is immutable after creation (except for the closed flag) to prevent
// race conditions when switching sessions.
type SessionContext struct {
	// Immutable session identifiers - set at creation, never changed
	persistedID string // The session ID used for persistence and frontend routing
	acpID       string // The ACP protocol session ID

	// ACP connection state
	acpCmd    *exec.Cmd
	acpConn   *acp.ClientSideConnection
	acpClient *WebClient

	// Session persistence
	recorder        *session.Recorder
	agentMsgBuffer  *agentMessageBuffer
	agentThoughtBuf *agentMessageBuffer

	// closed is set to 1 when the session is closed.
	// Callbacks check this flag and discard messages if set.
	closed atomic.Int32
}

// IsClosed returns true if this session context has been closed.
func (sc *SessionContext) IsClosed() bool {
	return sc.closed.Load() != 0
}

// Close marks this session context as closed.
// After this, all callbacks will discard their messages.
func (sc *SessionContext) Close() {
	sc.closed.Store(1)
}

// GetSessionID returns the session ID for frontend routing.
func (sc *SessionContext) GetSessionID() string {
	return sc.persistedID
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// WSMessage represents a WebSocket message between frontend and backend.
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Message types (frontend -> backend)
const (
	WSMsgTypePrompt           = "prompt"
	WSMsgTypeCancel           = "cancel"
	WSMsgTypeNewSession       = "new_session"
	WSMsgTypeLoadSession      = "load_session"
	WSMsgTypeSwitchSession    = "switch_session"
	WSMsgTypePermissionAnswer = "permission_answer"
	WSMsgTypeRenameSession    = "rename_session"
	WSMsgTypeSyncSession      = "sync_session" // Request incremental sync from a sequence number
)

// Message types (backend -> frontend)
const (
	WSMsgTypeConnected       = "connected"
	WSMsgTypeSessionCreated  = "session_created"
	WSMsgTypeSessionSwitched = "session_switched"
	WSMsgTypeSessionRenamed  = "session_renamed"
	WSMsgTypeSessionDeleted  = "session_deleted"
	WSMsgTypeAgentMessage    = "agent_message"
	WSMsgTypeAgentThought    = "agent_thought"
	WSMsgTypeToolCall        = "tool_call"
	WSMsgTypeToolUpdate      = "tool_update"
	WSMsgTypePlan            = "plan"
	WSMsgTypePermission      = "permission"
	WSMsgTypeError           = "error"
	WSMsgTypeSessionLoaded   = "session_loaded"
	WSMsgTypePromptComplete  = "prompt_complete"
	WSMsgTypeFileWrite       = "file_write"
	WSMsgTypeFileRead        = "file_read"
	WSMsgTypeSessionSync     = "session_sync" // Incremental sync response with events since last seen
)

// WSClient represents a connected WebSocket client.
type WSClient struct {
	server *Server
	conn   *websocket.Conn
	send   chan []byte

	// Current session context - holds all session-specific state
	// This is replaced atomically when switching sessions
	session   *SessionContext
	sessionMu sync.RWMutex

	// Background session - the session that runs independently of this WebSocket
	// When set, this client is "attached" to the background session
	bgSession   *BackgroundSession
	bgSessionMu sync.RWMutex

	// WebSocket lifecycle context
	ctx    context.Context
	cancel context.CancelFunc

	// Session persistence store (shared across sessions)
	store *session.Store

	// Permission handling
	permissionChan chan acp.RequestPermissionResponse
	permissionMu   sync.Mutex

	// Prompt tracking for auto-title generation
	promptCount int
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("WebSocket upgrade failed", "error", err)
		}
		return
	}

	// Initialize session store for persistence
	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to create session store", "error", err)
		}
		// Continue without persistence - not fatal
		store = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &WSClient{
		server:         s,
		conn:           conn,
		send:           make(chan []byte, 256),
		ctx:            ctx,
		cancel:         cancel,
		permissionChan: make(chan acp.RequestPermissionResponse, 1),
		store:          store,
	}

	// Register client for broadcasts
	s.registerClient(client)

	go client.writePump()
	go client.readPump()

	client.sendMessage(WSMsgTypeConnected, map[string]string{
		"acp_server": s.config.ACPServer,
	})
}

func (c *WSClient) readPump() {
	defer func() {
		c.cancel()
		// Unregister client from broadcasts
		c.server.unregisterClient(c)
		if err := c.conn.Close(); err != nil && c.server.logger != nil {
			c.server.logger.Debug("WebSocket close error", "error", err)
		}
		// Suspend the current session (keeps it active for later resumption)
		c.suspendCurrentSession()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			c.sendError("Invalid message format")
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *WSClient) writePump() {
	defer c.conn.Close()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, message)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *WSClient) handleMessage(msg WSMessage) {
	switch msg.Type {
	case WSMsgTypeNewSession:
		var data struct {
			Name string `json:"name,omitempty"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handleNewSession(data.Name)

	case WSMsgTypePrompt:
		var data struct {
			Message string `json:"message"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handlePrompt(data.Message)

	case WSMsgTypeCancel:
		c.handleCancel()

	case WSMsgTypeLoadSession:
		var data struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handleLoadSession(data.SessionID)

	case WSMsgTypeSwitchSession:
		var data struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handleSwitchSession(data.SessionID)

	case WSMsgTypePermissionAnswer:
		var data struct {
			OptionID string `json:"option_id"`
			Cancel   bool   `json:"cancel"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handlePermissionAnswer(data.OptionID, data.Cancel)

	case WSMsgTypeRenameSession:
		var data struct {
			SessionID string `json:"session_id"`
			Name      string `json:"name"`
		}
		json.Unmarshal(msg.Data, &data)
		c.handleRenameSession(data.SessionID, data.Name)

	case WSMsgTypeSyncSession:
		var data struct {
			SessionID string `json:"session_id"`
			AfterSeq  int64  `json:"after_seq"` // Last sequence number the client has seen
		}
		json.Unmarshal(msg.Data, &data)
		c.handleSyncSession(data.SessionID, data.AfterSeq)
	}
}

func (c *WSClient) handleNewSession(name string) {
	// Detach from any existing background session and close legacy session
	c.detachFromBackgroundSession()
	c.closeCurrentSession("new_session")

	// Set default name if not provided
	sessionName := name
	if sessionName == "" {
		sessionName = "New conversation"
	}

	cwd, _ := os.Getwd()
	createdAt := time.Now()

	// Set the store on the session manager if we have one
	if c.store != nil {
		c.server.sessionManager.SetStore(c.store)
	}

	// Create a new background session via the session manager
	bs, err := c.server.sessionManager.CreateSession(sessionName, cwd)
	if err != nil {
		c.sendError("Failed to create session: " + err.Error())
		return
	}

	// Attach this client to the background session
	c.attachToBackgroundSession(bs)

	// Send session_created message with full session info
	c.sendMessage(WSMsgTypeSessionCreated, map[string]interface{}{
		"session_id":     bs.GetSessionID(),
		"acp_session_id": bs.GetACPID(),
		"name":           sessionName,
		"acp_server":     c.server.config.ACPServer,
		"created_at":     createdAt.Format(time.RFC3339),
		"status":         "active",
	})
}

func (c *WSClient) handlePrompt(message string) {
	// Try background session first
	bs := c.getBackgroundSession()
	if bs != nil {
		if err := bs.Prompt(message); err != nil {
			c.sendError("Failed to send prompt: " + err.Error())
		}
		return
	}

	// Fall back to legacy SessionContext
	sessCtx := c.getCurrentSession()
	if sessCtx == nil || sessCtx.acpConn == nil {
		c.sendError("No active session. Create a new session first.")
		return
	}

	// Track prompt count for auto-title generation
	c.promptCount++
	isFirstPrompt := c.promptCount == 1
	sessionID := sessCtx.GetSessionID()

	// Persist user prompt immediately
	c.persistUserPromptForSession(sessCtx, message)

	// Send prompt (this blocks until response is complete)
	// Capture sessCtx to ensure we use the correct session even if switched
	go func(ctx *SessionContext) {
		_, err := ctx.acpConn.Prompt(c.ctx, acp.PromptRequest{
			SessionId: acp.SessionId(ctx.acpID),
			Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
		})

		// Check if session was closed during prompt
		if ctx.IsClosed() {
			return
		}

		// Flush any remaining markdown
		if ctx.acpClient != nil {
			ctx.acpClient.FlushMarkdown()
		}

		// Flush and persist buffered agent messages
		c.flushAndPersistAgentMessagesForSession(ctx)

		if err != nil {
			if !ctx.IsClosed() {
				c.sendError("Prompt failed: " + err.Error())
				c.persistErrorForSession(ctx, err.Error())
			}
		} else {
			if !ctx.IsClosed() {
				// Signal that the response is complete
				c.sendMessage(WSMsgTypePromptComplete, map[string]interface{}{
					"session_id": ctx.GetSessionID(),
				})

				// Auto-generate title after first successful prompt
				if isFirstPrompt && sessionID != "" {
					c.generateAndSetTitle(message, sessionID)
				}
			}
		}
	}(sessCtx)
}

func (c *WSClient) handleCancel() {
	// Try background session first
	if bs := c.getBackgroundSession(); bs != nil {
		bs.Cancel()
		return
	}

	// Fall back to legacy SessionContext
	sessCtx := c.getCurrentSession()
	if sessCtx != nil && sessCtx.acpConn != nil && sessCtx.acpID != "" {
		sessCtx.acpConn.Cancel(c.ctx, acp.CancelNotification{
			SessionId: acp.SessionId(sessCtx.acpID),
		})
	}
}

func (c *WSClient) handleRenameSession(sessionID, name string) {
	if sessionID == "" || name == "" {
		c.sendError("Session ID and name are required")
		return
	}

	// Update session metadata in store
	if c.store == nil {
		c.sendError("Session store not available")
		return
	}

	err := c.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = name
	})
	if err != nil {
		if c.server.logger != nil {
			c.server.logger.Error("Failed to rename session", "error", err, "session_id", sessionID)
		}
		c.sendError("Failed to rename session: " + err.Error())
		return
	}

	// Broadcast the rename to all connected clients (including this one)
	c.server.BroadcastSessionRenamed(sessionID, name)

	if c.server.logger != nil {
		c.server.logger.Info("Session renamed", "session_id", sessionID, "name", name)
	}
}

func (c *WSClient) handleLoadSession(sessionID string) {
	// Load session events from store and send to frontend
	store, err := session.DefaultStore()
	if err != nil {
		c.sendError("Failed to access session store: " + err.Error())
		return
	}
	defer store.Close()

	// Get session metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			c.sendError("Session not found")
			return
		}
		c.sendError("Failed to get session metadata: " + err.Error())
		return
	}

	// Get session events
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		c.sendError("Failed to read session events: " + err.Error())
		return
	}

	// Send session loaded message with metadata and events
	c.sendMessage(WSMsgTypeSessionLoaded, map[string]interface{}{
		"session_id": sessionID,
		"name":       meta.Name,
		"acp_server": meta.ACPServer,
		"created_at": meta.CreatedAt.Format(time.RFC3339),
		"status":     meta.Status,
		"events":     events,
	})
}

// handleSyncSession handles incremental sync requests from the frontend.
// This is used when a mobile client reconnects after sleeping and needs to
// catch up on events that occurred while disconnected.
func (c *WSClient) handleSyncSession(sessionID string, afterSeq int64) {
	// Check if there's a running background session we can attach to
	if bs := c.server.sessionManager.GetSession(sessionID); bs != nil {
		// Attach to the running session
		c.attachToBackgroundSession(bs)

		if c.server.logger != nil {
			c.server.logger.Info("Reattached to running background session",
				"session_id", sessionID,
				"is_prompting", bs.IsPrompting())
		}
	}

	// Use the client's store if available
	var store *session.Store
	var err error
	if c.store != nil {
		store = c.store
	} else {
		store, err = session.DefaultStore()
		if err != nil {
			c.sendError("Failed to access session store: " + err.Error())
			return
		}
		defer store.Close()
	}

	// Get session metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			c.sendError("Session not found")
			return
		}
		c.sendError("Failed to get session metadata: " + err.Error())
		return
	}

	// Get events after the specified sequence number
	events, err := store.ReadEventsFrom(sessionID, afterSeq)
	if err != nil {
		c.sendError("Failed to read session events: " + err.Error())
		return
	}

	// Check if we're attached to a running session
	isRunning := c.getBackgroundSession() != nil

	// Send sync response with new events
	c.sendMessage(WSMsgTypeSessionSync, map[string]interface{}{
		"session_id":  sessionID,
		"after_seq":   afterSeq,
		"events":      events,
		"event_count": meta.EventCount,
		"status":      meta.Status,
		"name":        meta.Name,
		"is_running":  isRunning, // Tell frontend if session is still running
	})

	if c.server.logger != nil {
		c.server.logger.Debug("Session sync",
			"session_id", sessionID,
			"after_seq", afterSeq,
			"events_sent", len(events),
			"total_events", meta.EventCount,
			"is_running", isRunning)
	}
}

func (c *WSClient) handleSwitchSession(sessionID string) {
	// Close current session first
	c.closeCurrentSession("session_switch")

	// Load session from store
	var store *session.Store
	var err error
	if c.store != nil {
		store = c.store
	} else {
		store, err = session.DefaultStore()
		if err != nil {
			c.sendError("Failed to access session store: " + err.Error())
			return
		}
		defer store.Close()
	}

	// Get session metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			c.sendError("Session not found")
			return
		}
		c.sendError("Failed to get session metadata: " + err.Error())
		return
	}

	// Get session events
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		c.sendError("Failed to read session events: " + err.Error())
		return
	}

	// Start a new ACP process for this session
	args := strings.Fields(c.server.config.ACPCommand)
	if len(args) == 0 {
		c.sendError("Empty ACP command")
		return
	}

	cmd := exec.CommandContext(c.ctx, args[0], args[1:]...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.sendError("Failed to create stdin pipe: " + err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.sendError("Failed to create stdout pipe: " + err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		c.sendError("Failed to start ACP server: " + err.Error())
		return
	}

	// Resume recording to the existing session
	var recorder *session.Recorder
	if c.store != nil {
		recorder = session.NewRecorderWithID(c.store, sessionID)
		if err := recorder.Resume(); err != nil {
			if c.server.logger != nil {
				c.server.logger.Error("Failed to resume session recording", "error", err)
			}
			recorder = nil
		} else {
			// Update the metadata to mark it as active again
			if err := c.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
				m.Status = "active"
			}); err != nil && c.server.logger != nil {
				c.server.logger.Error("Failed to update session status", "error", err)
			}
		}
	}

	// Create the session context with the existing session ID
	sessCtx := &SessionContext{
		persistedID:     sessionID,
		acpCmd:          cmd,
		recorder:        recorder,
		agentMsgBuffer:  &agentMessageBuffer{},
		agentThoughtBuf: &agentMessageBuffer{},
	}

	// Create web client with streaming callbacks that capture the session context
	sessCtx.acpClient = NewWebClient(WebClientConfig{
		AutoApprove: c.server.config.AutoApprove,
		OnAgentMessage: func(html string) {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypeAgentMessage, map[string]interface{}{
				"html":       html,
				"format":     "html",
				"session_id": sessCtx.GetSessionID(),
			})
			if sessCtx.agentMsgBuffer != nil {
				sessCtx.agentMsgBuffer.Write(html)
			}
		},
		OnAgentThought: func(text string) {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypeAgentThought, map[string]interface{}{
				"text":       text,
				"session_id": sessCtx.GetSessionID(),
			})
			if sessCtx.agentThoughtBuf != nil {
				sessCtx.agentThoughtBuf.Write(text)
			}
		},
		OnToolCall: func(id, title, status string) {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypeToolCall, map[string]interface{}{
				"id":         id,
				"title":      title,
				"status":     status,
				"session_id": sessCtx.GetSessionID(),
			})
			c.persistToolCallForSession(sessCtx, id, title, status)
		},
		OnToolUpdate: func(id string, status *string) {
			if sessCtx.IsClosed() {
				return
			}
			data := map[string]interface{}{
				"id":         id,
				"session_id": sessCtx.GetSessionID(),
			}
			if status != nil {
				data["status"] = *status
			}
			c.sendMessage(WSMsgTypeToolUpdate, data)
			c.persistToolUpdateForSession(sessCtx, id, status)
		},
		OnPlan: func() {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypePlan, map[string]interface{}{
				"session_id": sessCtx.GetSessionID(),
			})
		},
		OnFileWrite: func(path string, size int) {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypeFileWrite, map[string]interface{}{
				"path":       path,
				"size":       size,
				"session_id": sessCtx.GetSessionID(),
			})
			c.persistFileWriteForSession(sessCtx, path, size)
		},
		OnFileRead: func(path string, size int) {
			if sessCtx.IsClosed() {
				return
			}
			c.sendMessage(WSMsgTypeFileRead, map[string]interface{}{
				"path":       path,
				"size":       size,
				"session_id": sessCtx.GetSessionID(),
			})
			c.persistFileReadForSession(sessCtx, path, size)
		},
		OnPermission: c.handlePermissionRequest,
	})

	// Create ACP connection
	sessCtx.acpConn = acp.NewClientSideConnection(sessCtx.acpClient, stdin, stdout)
	if c.server.config.Debug && c.server.logger != nil {
		sessCtx.acpConn.SetLogger(c.server.logger)
	}

	// Initialize
	_, err = sessCtx.acpConn.Initialize(c.ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		c.sendError("Failed to initialize: " + err.Error())
		sessCtx.Close()
		return
	}

	// Create a new ACP session (we can't resume the old one, but we can continue the conversation)
	cwd := meta.WorkingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessResp, err := sessCtx.acpConn.NewSession(c.ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		c.sendError("Failed to create session: " + err.Error())
		sessCtx.Close()
		return
	}

	sessCtx.acpID = string(sessResp.SessionId)

	// Set the new session as current
	c.setCurrentSession(sessCtx)

	// Send session switched message with events for UI to display
	c.sendMessage(WSMsgTypeSessionSwitched, map[string]interface{}{
		"session_id":         sessionID,
		"new_acp_session_id": sessCtx.acpID,
		"name":               meta.Name,
		"acp_server":         meta.ACPServer,
		"created_at":         meta.CreatedAt.Format(time.RFC3339),
		"status":             "active",
		"events":             events,
	})
}

func (c *WSClient) handlePermissionRequest(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Send permission request to frontend
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	options := make([]map[string]string, len(params.Options))
	for i, opt := range params.Options {
		options[i] = map[string]string{
			"id":   string(opt.OptionId),
			"name": opt.Name,
			"kind": string(opt.Kind),
		}
	}

	c.sendMessage(WSMsgTypePermission, map[string]interface{}{
		"title":   title,
		"options": options,
	})

	// Wait for response from frontend
	select {
	case resp := <-c.permissionChan:
		return resp, nil
	case <-ctx.Done():
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
		}, ctx.Err()
	}
}

func (c *WSClient) handlePermissionAnswer(optionID string, cancel bool) {
	// Try background session first
	if bs := c.getBackgroundSession(); bs != nil {
		bs.AnswerPermission(optionID, cancel)
		return
	}

	// Fall back to legacy handling
	c.permissionMu.Lock()
	defer c.permissionMu.Unlock()

	var resp acp.RequestPermissionResponse
	var outcome string
	if cancel {
		resp.Outcome.Cancelled = &acp.RequestPermissionOutcomeCancelled{}
		outcome = "cancelled"
	} else {
		resp.Outcome.Selected = &acp.RequestPermissionOutcomeSelected{
			OptionId: acp.PermissionOptionId(optionID),
		}
		outcome = "approved"
	}

	// Persist permission decision
	c.persistPermission("", optionID, outcome)

	select {
	case c.permissionChan <- resp:
	default:
	}
}

// --- Session Context Management ---

// getCurrentSession returns the current session context.
func (c *WSClient) getCurrentSession() *SessionContext {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.session
}

// setCurrentSession sets the current session context.
func (c *WSClient) setCurrentSession(sessCtx *SessionContext) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	c.session = sessCtx
}

// getBackgroundSession returns the attached background session.
func (c *WSClient) getBackgroundSession() *BackgroundSession {
	c.bgSessionMu.RLock()
	defer c.bgSessionMu.RUnlock()
	return c.bgSession
}

// attachToBackgroundSession attaches this client to a background session.
func (c *WSClient) attachToBackgroundSession(bs *BackgroundSession) {
	c.bgSessionMu.Lock()
	defer c.bgSessionMu.Unlock()

	// Detach from previous session if any
	if c.bgSession != nil {
		c.bgSession.DetachClient()
	}

	c.bgSession = bs
	if bs != nil {
		bs.AttachClient(c)
	}
}

// detachFromBackgroundSession detaches from the current background session.
func (c *WSClient) detachFromBackgroundSession() {
	c.bgSessionMu.Lock()
	defer c.bgSessionMu.Unlock()

	if c.bgSession != nil {
		c.bgSession.DetachClient()
		c.bgSession = nil
	}
}

// closeCurrentSession closes the current session and its ACP connection.
// The reason is recorded in the session metadata.
func (c *WSClient) closeCurrentSession(reason string) {
	c.sessionMu.Lock()
	sessCtx := c.session
	c.session = nil
	c.sessionMu.Unlock()

	if sessCtx == nil {
		return
	}

	// Mark the session as closed - this will cause all callbacks to discard messages
	sessCtx.Close()

	// Close ACP client and connection
	if sessCtx.acpClient != nil {
		sessCtx.acpClient.Close()
	}
	if sessCtx.acpCmd != nil && sessCtx.acpCmd.Process != nil {
		sessCtx.acpCmd.Process.Kill()
	}

	// End the recording session
	if sessCtx.recorder != nil {
		if err := sessCtx.recorder.End(reason); err != nil && c.server.logger != nil {
			c.server.logger.Error("Failed to end session recording", "error", err)
		}
	}

	// Flush message buffers
	if sessCtx.agentMsgBuffer != nil {
		sessCtx.agentMsgBuffer.Flush()
	}
	if sessCtx.agentThoughtBuf != nil {
		sessCtx.agentThoughtBuf.Flush()
	}
}

// suspendCurrentSession suspends the current session without ending it.
// This is used when the WebSocket connection is temporarily closed (e.g., browser refresh).
func (c *WSClient) suspendCurrentSession() {
	// First, detach from any background session (keeps it running)
	c.detachFromBackgroundSession()

	// Handle legacy SessionContext if present
	c.sessionMu.Lock()
	sessCtx := c.session
	c.session = nil
	c.sessionMu.Unlock()

	if sessCtx == nil {
		return
	}

	// Mark the session as closed
	sessCtx.Close()

	// Close ACP client and connection
	if sessCtx.acpClient != nil {
		sessCtx.acpClient.Close()
	}
	if sessCtx.acpCmd != nil && sessCtx.acpCmd.Process != nil {
		sessCtx.acpCmd.Process.Kill()
	}

	// Flush and persist buffered agent messages BEFORE suspending the recorder.
	// This ensures any in-flight agent response is saved when the connection drops
	// (e.g., phone locked, browser closed). Without this, messages would be lost.
	c.flushAndPersistAgentMessagesForSession(sessCtx)

	// Suspend the recording session (keeps it active for later resumption)
	if sessCtx.recorder != nil {
		if err := sessCtx.recorder.Suspend(); err != nil && c.server.logger != nil {
			c.server.logger.Error("Failed to suspend session recording", "error", err)
		}
	}
}

func (c *WSClient) sendMessage(msgType string, data interface{}) {
	var dataJSON json.RawMessage
	if data != nil {
		dataJSON, _ = json.Marshal(data)
	}
	msg := WSMessage{Type: msgType, Data: dataJSON}
	msgBytes, _ := json.Marshal(msg)

	select {
	case c.send <- msgBytes:
	default:
		// Channel full, client too slow
	}
}

func (c *WSClient) sendError(message string) {
	sessCtx := c.getCurrentSession()
	sessionID := ""
	if sessCtx != nil {
		sessionID = sessCtx.GetSessionID()
	}
	c.sendMessage(WSMsgTypeError, map[string]interface{}{
		"message":    message,
		"session_id": sessionID,
	})
}

// --- Session Persistence Methods (Session-Context-Aware) ---

// persistUserPromptForSession records a user prompt event to disk for a specific session.
func (c *WSClient) persistUserPromptForSession(sessCtx *SessionContext, message string) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordUserPrompt(message); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist user prompt", "error", err)
	}
}

// flushAndPersistAgentMessagesForSession flushes buffered agent messages and thoughts to disk.
// Thoughts are persisted before messages to preserve chronological order
// (thinking happens before the response in real-time streaming).
func (c *WSClient) flushAndPersistAgentMessagesForSession(sessCtx *SessionContext) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}

	// Flush and persist agent thought first (thinking comes before the response)
	if sessCtx.agentThoughtBuf != nil {
		text := sessCtx.agentThoughtBuf.Flush()
		if text != "" {
			if err := sessCtx.recorder.RecordAgentThought(text); err != nil && c.server.logger != nil {
				c.server.logger.Error("Failed to persist agent thought", "error", err)
			}
		}
	}

	// Flush and persist agent message
	if sessCtx.agentMsgBuffer != nil {
		text := sessCtx.agentMsgBuffer.Flush()
		if text != "" {
			if err := sessCtx.recorder.RecordAgentMessage(text); err != nil && c.server.logger != nil {
				c.server.logger.Error("Failed to persist agent message", "error", err)
			}
		}
	}
}

// persistToolCallForSession records a tool call event to disk for a specific session.
func (c *WSClient) persistToolCallForSession(sessCtx *SessionContext, id, title, status string) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordToolCall(id, title, status, "", nil, nil); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist tool call", "error", err)
	}
}

// persistToolUpdateForSession records a tool update event to disk for a specific session.
func (c *WSClient) persistToolUpdateForSession(sessCtx *SessionContext, id string, status *string) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordToolCallUpdate(id, status, nil); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist tool update", "error", err)
	}
}

// persistFileWriteForSession records a file write event to disk for a specific session.
func (c *WSClient) persistFileWriteForSession(sessCtx *SessionContext, path string, size int) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordFileWrite(path, size); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist file write", "error", err)
	}
}

// persistFileReadForSession records a file read event to disk for a specific session.
func (c *WSClient) persistFileReadForSession(sessCtx *SessionContext, path string, size int) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordFileRead(path, size); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist file read", "error", err)
	}
}

// persistErrorForSession records an error event to disk for a specific session.
func (c *WSClient) persistErrorForSession(sessCtx *SessionContext, message string) {
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordError(message, 0); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist error", "error", err)
	}
}

// persistPermission records a permission event to disk for the current session.
func (c *WSClient) persistPermission(title, selectedOption, outcome string) {
	sessCtx := c.getCurrentSession()
	if sessCtx == nil || sessCtx.recorder == nil {
		return
	}
	if err := sessCtx.recorder.RecordPermission(title, selectedOption, outcome); err != nil && c.server.logger != nil {
		c.server.logger.Error("Failed to persist permission", "error", err)
	}
}

// generateAndSetTitle uses the auxiliary session to generate a title for the conversation.
// This runs in the background and doesn't block the main conversation.
func (c *WSClient) generateAndSetTitle(initialMessage, sessionID string) {
	go func() {
		// Use a separate context with timeout for title generation
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		title, err := auxiliary.GenerateTitle(ctx, initialMessage)
		if err != nil {
			if c.server.logger != nil {
				c.server.logger.Error("Failed to generate title", "error", err, "session_id", sessionID)
			}
			return
		}

		if title == "" {
			return
		}

		// Update session metadata in store
		if c.store != nil {
			if err := c.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
				m.Name = title
			}); err != nil {
				if c.server.logger != nil {
					c.server.logger.Error("Failed to update session name", "error", err, "session_id", sessionID)
				}
				return
			}
		}

		// Notify frontend of the name change
		c.sendMessage(WSMsgTypeSessionRenamed, map[string]string{
			"session_id": sessionID,
			"name":       title,
		})

		if c.server.logger != nil {
			c.server.logger.Info("Auto-generated session title", "session_id", sessionID, "title", title)
		}
	}()
}
