package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/session"
)

// generateClientID creates a unique client identifier for sender tracking.
func generateClientID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}

// SessionWSClient represents a WebSocket client connected to a specific session.
type SessionWSClient struct {
	server    *Server
	wsConn    *WSConn // Shared WebSocket connection wrapper
	sessionID string
	clientID  string       // Unique identifier for this client (for sender identification)
	logger    *slog.Logger // Client-scoped logger with session_id and client_id context

	// The background session this client is observing
	bgSession *BackgroundSession

	// WebSocket lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Session store for persistence operations
	store *session.Store

	// Permission handling
	permissionChan chan acp.RequestPermissionResponse
	permissionMu   sync.Mutex
}

// handleSessionWS handles WebSocket connections for a specific session.
// Route: {prefix}/api/sessions/{id}/ws
func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from URL path: {prefix}/api/sessions/{id}/ws
	// First strip the API prefix, then strip /api/sessions/
	path := r.URL.Path
	path = strings.TrimPrefix(path, s.apiPrefix)
	path = strings.TrimPrefix(path, "/api/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "ws" {
		http.Error(w, "Invalid session WebSocket path", http.StatusBadRequest)
		return
	}
	sessionID := parts[0]
	clientIP := getClientIPWithProxyCheck(r)

	// Check connection limit per IP
	if s.connectionTracker != nil && !s.connectionTracker.TryAdd(clientIP) {
		if s.logger != nil {
			s.logger.Warn("Session WebSocket rejected: too many connections",
				"client_ip", clientIP, "session_id", sessionID)
		}
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	}

	// Use secure upgrader
	secureUpgrader := s.getSecureUpgrader()
	conn, err := secureUpgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.connectionTracker != nil {
			s.connectionTracker.Remove(clientIP)
		}
		if s.logger != nil {
			s.logger.Error("Session WebSocket upgrade failed", "error", err, "session_id", sessionID)
		}
		return
	}

	// Apply security settings
	configureWebSocketConn(conn, s.wsSecurityConfig)

	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()

	ctx, cancel := context.WithCancel(context.Background())
	clientID := generateClientID()

	// Create client-scoped logger with session and client context
	clientLogger := logging.WithClient(s.logger, clientID, sessionID)

	// Create shared WebSocket connection wrapper
	wsConn := NewWSConn(WSConnConfig{
		Conn:     conn,
		Config:   s.wsSecurityConfig,
		Logger:   clientLogger,
		ClientIP: clientIP,
		Tracker:  s.connectionTracker,
	})

	client := &SessionWSClient{
		server:         s,
		wsConn:         wsConn,
		sessionID:      sessionID,
		clientID:       clientID,
		logger:         clientLogger,
		ctx:            ctx,
		cancel:         cancel,
		store:          store,
		permissionChan: make(chan acp.RequestPermissionResponse, 1),
	}

	// Try to get existing background session first
	bs := s.sessionManager.GetSession(sessionID)

	// If no running session, try to resume it
	if bs == nil && store != nil {
		// Check if session exists in store
		meta, err := store.GetMetadata(sessionID)
		if err == nil {
			// Session exists in store, resume it
			cwd := meta.WorkingDir
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
			if err != nil {
				if clientLogger != nil {
					clientLogger.Error("Failed to resume session", "error", err)
				}
				// Continue without a running session - client can still view history
			} else if clientLogger != nil {
				clientLogger.Debug("Resumed session for WebSocket client",
					"acp_id", bs.GetACPID())
			}
		}
	}

	// Attach to background session if available
	if bs != nil {
		client.bgSession = bs
		bs.AddObserver(client)
		if clientLogger != nil {
			clientLogger.Debug("SessionWSClient attached to session",
				"observer_count", bs.ObserverCount())
		}
	}

	go client.writePump()
	go client.readPump()

	// Send connection confirmation with session info
	client.sendSessionConnected(bs)
}

func (c *SessionWSClient) sendSessionConnected(bs *BackgroundSession) {
	data := map[string]interface{}{
		"session_id":  c.sessionID,
		"client_id":   c.clientID, // Unique ID for this client (for multi-browser sync identification)
		"acp_server":  c.server.config.ACPServer,
		"is_running":  bs != nil && !bs.IsClosed(),
		"is_attached": bs != nil,
	}

	if bs != nil {
		data["acp_session_id"] = bs.GetACPID()
		data["is_prompting"] = bs.IsPrompting()
	}

	// Get session metadata if available
	if c.store != nil {
		if meta, err := c.store.GetMetadata(c.sessionID); err == nil {
			data["name"] = meta.Name
			data["working_dir"] = meta.WorkingDir
			data["created_at"] = meta.CreatedAt.Format(time.RFC3339)
			data["status"] = meta.Status
			if c.logger != nil {
				c.logger.Debug("Sending connected message", "working_dir", meta.WorkingDir)
			}
		} else if c.logger != nil {
			c.logger.Warn("Failed to get metadata for connected message", "error", err)
		}

		// Get queue length for the session
		queue := c.store.Queue(c.sessionID)
		if queueLen, err := queue.Len(); err == nil {
			data["queue_length"] = queueLen
		}
	} else if c.logger != nil {
		c.logger.Warn("No store for connected message")
	}

	// Get queue configuration for the session
	// Use the background session's config if available, otherwise use defaults
	var queueConfig *config.QueueConfig
	if bs != nil {
		queueConfig = bs.GetQueueConfig()
	}
	data["queue_config"] = NewQueueConfigResponse(queueConfig)

	c.sendMessage(WSMsgTypeConnected, data)
}

func (c *SessionWSClient) readPump() {
	defer func() {
		if c.logger != nil {
			observerCount := 0
			if c.bgSession != nil {
				observerCount = c.bgSession.ObserverCount()
			}
			c.logger.Debug("SessionWSClient readPump exiting",
				"observer_count_before", observerCount)
		}
		c.cancel()
		if c.bgSession != nil {
			c.bgSession.RemoveObserver(c)
		}
		// Release connection slot via WSConn
		c.wsConn.ReleaseConnectionSlot()
		// Note: Don't close c.store - it's owned by the server and shared across handlers
		c.wsConn.Close()
		if c.logger != nil {
			c.logger.Debug("SessionWSClient readPump cleanup complete")
		}
	}()

	for {
		message, err := c.wsConn.ReadMessage()
		if err != nil {
			return
		}

		msg, err := ParseMessage(message)
		if err != nil {
			c.sendError("Invalid message format")
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *SessionWSClient) writePump() {
	// Use WSConn's WritePump with our context
	c.wsConn.WritePump(c.ctx, nil)
}

func (c *SessionWSClient) handleMessage(msg WSMessage) {
	switch msg.Type {
	case WSMsgTypePrompt:
		var data struct {
			Message  string   `json:"message"`
			ImageIDs []string `json:"image_ids,omitempty"`
			PromptID string   `json:"prompt_id,omitempty"` // Client-generated ID for delivery confirmation
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handlePromptWithMeta(data.Message, data.PromptID, data.ImageIDs)

	case WSMsgTypeCancel:
		c.handleCancel()

	case WSMsgTypePermissionAnswer:
		var data struct {
			OptionID string `json:"option_id"`
			Cancel   bool   `json:"cancel"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handlePermissionAnswer(data.OptionID, data.Cancel)

	case WSMsgTypeSyncSession:
		var data struct {
			AfterSeq int64 `json:"after_seq"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleSync(data.AfterSeq)

	case WSMsgTypeKeepalive:
		var data struct {
			ClientTime int64 `json:"client_time"` // Client's timestamp in milliseconds
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleKeepalive(data.ClientTime)
	}
}

func (c *SessionWSClient) handlePromptWithMeta(message string, promptID string, imageIDs []string) {
	if c.bgSession == nil {
		c.sendError("Session not running. Create or resume the session first.")
		return
	}

	// Check if session needs auto-title generation (before any state changes)
	// Only generate title if metadata.Name is empty
	shouldGenerateTitle := c.sessionNeedsTitle()

	// Send prompt to background session with sender info for multi-client broadcast
	meta := PromptMeta{
		SenderID: c.clientID,
		PromptID: promptID,
		ImageIDs: imageIDs,
	}
	if err := c.bgSession.PromptWithMeta(message, meta); err != nil {
		c.sendError("Failed to send prompt: " + err.Error())
		return
	}

	// Note: prompt_received ACK is now sent via the OnUserPrompt observer callback
	// which is called by BackgroundSession.PromptWithMeta after persisting the prompt.
	// This ensures all observers (including the sender) receive the same broadcast.

	// Auto-generate title if session has no title yet
	if shouldGenerateTitle {
		go c.generateAndSetTitle(message)
	}
}

func (c *SessionWSClient) handleCancel() {
	if c.bgSession != nil {
		c.bgSession.Cancel()
	}
}

func (c *SessionWSClient) handlePermissionAnswer(optionID string, cancel bool) {
	var resp acp.RequestPermissionResponse
	if cancel {
		resp.Outcome.Cancelled = &acp.RequestPermissionOutcomeCancelled{}
	} else {
		resp.Outcome.Selected = &acp.RequestPermissionOutcomeSelected{
			OptionId: acp.PermissionOptionId(optionID),
		}
	}

	// Send to our own permission channel (non-blocking)
	select {
	case c.permissionChan <- resp:
	default:
	}
}

func (c *SessionWSClient) handleSync(afterSeq int64) {
	if c.store == nil {
		c.sendError("Session store not available")
		return
	}

	events, err := c.store.ReadEventsFrom(c.sessionID, afterSeq)
	if err != nil {
		c.sendError("Failed to read session events: " + err.Error())
		return
	}

	meta, _ := c.store.GetMetadata(c.sessionID)
	isRunning := c.bgSession != nil && !c.bgSession.IsClosed()
	isPrompting := isRunning && c.bgSession.IsPrompting()

	c.sendMessage(WSMsgTypeSessionSync, map[string]interface{}{
		"session_id":   c.sessionID,
		"after_seq":    afterSeq,
		"events":       events,
		"event_count":  meta.EventCount,
		"status":       meta.Status,
		"name":         meta.Name,
		"is_running":   isRunning,
		"is_prompting": isPrompting,
	})
}

// handleKeepalive responds to application-level keepalive messages.
// This allows the frontend to detect time gaps (e.g., phone sleep) by comparing
// the client timestamp with the server timestamp.
func (c *SessionWSClient) handleKeepalive(clientTime int64) {
	serverTime := time.Now().UnixMilli()

	// Send keepalive acknowledgment with both timestamps
	c.sendMessage(WSMsgTypeKeepaliveAck, map[string]interface{}{
		"client_time": clientTime,
		"server_time": serverTime,
	})
}

// sessionNeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func (c *SessionWSClient) sessionNeedsTitle() bool {
	return SessionNeedsTitle(c.store, c.sessionID)
}

func (c *SessionWSClient) generateAndSetTitle(initialMessage string) {
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:     c.store,
		SessionID: c.sessionID,
		Message:   initialMessage,
		Logger:    c.server.logger,
		OnTitleGenerated: func(sessionID, title string) {
			// Notify this client
			c.sendMessage(WSMsgTypeSessionRenamed, map[string]string{
				"session_id": sessionID,
				"name":       title,
			})

			// Broadcast to global events
			c.server.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
				"session_id": sessionID,
				"name":       title,
			})
		},
	})
}

func (c *SessionWSClient) sendMessage(msgType string, data interface{}) {
	c.wsConn.SendMessage(msgType, data)
}

func (c *SessionWSClient) sendError(message string) {
	c.wsConn.SendMessage(WSMsgTypeError, map[string]interface{}{
		"message":    message,
		"session_id": c.sessionID,
	})
}

// --- SessionObserver interface implementation ---

// OnAgentMessage is called when the agent sends a message chunk.
func (c *SessionWSClient) OnAgentMessage(html string) {
	c.sendMessage(WSMsgTypeAgentMessage, map[string]interface{}{
		"html":       html,
		"format":     "html",
		"session_id": c.sessionID,
	})
}

// OnAgentThought is called when the agent sends a thought chunk.
func (c *SessionWSClient) OnAgentThought(text string) {
	c.sendMessage(WSMsgTypeAgentThought, map[string]interface{}{
		"text":       text,
		"session_id": c.sessionID,
	})
}

// OnToolCall is called when a tool call starts.
func (c *SessionWSClient) OnToolCall(id, title, status string) {
	c.sendMessage(WSMsgTypeToolCall, map[string]interface{}{
		"id":         id,
		"title":      title,
		"status":     status,
		"session_id": c.sessionID,
	})
}

// OnToolUpdate is called when a tool call status is updated.
func (c *SessionWSClient) OnToolUpdate(id string, status *string) {
	data := map[string]interface{}{
		"id":         id,
		"session_id": c.sessionID,
	}
	if status != nil {
		data["status"] = *status
	}
	c.sendMessage(WSMsgTypeToolUpdate, data)
}

// OnPlan is called when a plan update occurs.
func (c *SessionWSClient) OnPlan() {
	c.sendMessage(WSMsgTypePlan, map[string]interface{}{
		"session_id": c.sessionID,
	})
}

// OnFileWrite is called when a file is written.
func (c *SessionWSClient) OnFileWrite(path string, size int) {
	c.sendMessage(WSMsgTypeFileWrite, map[string]interface{}{
		"path":       path,
		"size":       size,
		"session_id": c.sessionID,
	})
}

// OnFileRead is called when a file is read.
func (c *SessionWSClient) OnFileRead(path string, size int) {
	c.sendMessage(WSMsgTypeFileRead, map[string]interface{}{
		"path":       path,
		"size":       size,
		"session_id": c.sessionID,
	})
}

// OnPermission is called when a permission request needs user input.
func (c *SessionWSClient) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
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
		"title":      title,
		"options":    options,
		"session_id": c.sessionID,
	})

	// Clear any stale response
	c.permissionMu.Lock()
	select {
	case <-c.permissionChan:
	default:
	}
	c.permissionMu.Unlock()

	select {
	case <-ctx.Done():
		return acp.RequestPermissionResponse{}, ctx.Err()
	case <-c.ctx.Done():
		return acp.RequestPermissionResponse{}, c.ctx.Err()
	case resp := <-c.permissionChan:
		return resp, nil
	}
}

// OnPromptComplete is called when a prompt response is complete.
// eventCount is the current total event count for the session (for sync tracking).
func (c *SessionWSClient) OnPromptComplete(eventCount int) {
	c.sendMessage(WSMsgTypePromptComplete, map[string]interface{}{
		"session_id":  c.sessionID,
		"event_count": eventCount,
	})
}

// OnUserPrompt is called when any observer sends a prompt.
// This allows all connected clients to see user messages from other clients.
// senderID identifies which client sent the prompt (for deduplication).
func (c *SessionWSClient) OnUserPrompt(senderID, promptID, message string, imageIDs []string) {
	c.sendMessage(WSMsgTypeUserPrompt, map[string]interface{}{
		"session_id": c.sessionID,
		"sender_id":  senderID,
		"prompt_id":  promptID,
		"message":    message,
		"image_ids":  imageIDs,
		"is_mine":    senderID == c.clientID,
	})
}

// GetClientID returns the unique identifier for this client.
func (c *SessionWSClient) GetClientID() string {
	return c.clientID
}

// OnError is called when an error occurs.
func (c *SessionWSClient) OnError(message string) {
	c.sendError(message)
}

// OnQueueUpdated is called when the message queue state changes.
func (c *SessionWSClient) OnQueueUpdated(queueLength int, action string, messageID string) {
	c.sendMessage(WSMsgTypeQueueUpdated, map[string]interface{}{
		"session_id":   c.sessionID,
		"queue_length": queueLength,
		"action":       action,
		"message_id":   messageID,
	})
}

// OnQueueReordered is called when the queue order changes.
func (c *SessionWSClient) OnQueueReordered(messages []session.QueuedMessage) {
	c.sendMessage(WSMsgTypeQueueReordered, map[string]interface{}{
		"session_id": c.sessionID,
		"messages":   messages,
	})
}

// OnQueueMessageSending is called when a queued message is about to be sent.
func (c *SessionWSClient) OnQueueMessageSending(messageID string) {
	c.sendMessage(WSMsgTypeQueueMessageSending, map[string]interface{}{
		"session_id": c.sessionID,
		"message_id": messageID,
	})
}

// OnQueueMessageSent is called after a queued message was delivered.
func (c *SessionWSClient) OnQueueMessageSent(messageID string) {
	c.sendMessage(WSMsgTypeQueueMessageSent, map[string]interface{}{
		"session_id": c.sessionID,
		"message_id": messageID,
	})
}
