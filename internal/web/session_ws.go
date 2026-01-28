package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gorilla/websocket"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// SessionWSClient represents a WebSocket client connected to a specific session.
type SessionWSClient struct {
	server    *Server
	conn      *websocket.Conn
	send      chan []byte
	sessionID string
	clientIP  string // For connection tracking cleanup

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

	// Prompt tracking for auto-title
	promptCount int
}

// handleSessionWS handles WebSocket connections for a specific session.
// Route: /api/sessions/{id}/ws
func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
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

	// Get or create session store
	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to create session store", "error", err)
		}
		store = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &SessionWSClient{
		server:         s,
		conn:           conn,
		send:           make(chan []byte, 256),
		sessionID:      sessionID,
		ctx:            ctx,
		cancel:         cancel,
		store:          store,
		permissionChan: make(chan acp.RequestPermissionResponse, 1),
		clientIP:       clientIP,
	}

	// Try to get existing background session first
	bs := s.sessionManager.GetSession(sessionID)

	// If no running session, try to resume it
	if bs == nil && store != nil {
		// Check if session exists in store
		meta, err := store.GetMetadata(sessionID)
		if err == nil {
			// Session exists in store, resume it
			s.sessionManager.SetStore(store)
			cwd := meta.WorkingDir
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
			if err != nil {
				if s.logger != nil {
					s.logger.Error("Failed to resume session", "error", err, "session_id", sessionID)
				}
				// Continue without a running session - client can still view history
			} else if s.logger != nil {
				s.logger.Info("Resumed session for WebSocket client",
					"session_id", sessionID,
					"acp_id", bs.GetACPID())
			}
		}
	}

	// Attach to background session if available
	if bs != nil {
		client.bgSession = bs
		bs.AddObserver(client)
	}

	go client.writePump()
	go client.readPump()

	// Send connection confirmation with session info
	client.sendSessionConnected(bs)
}

func (c *SessionWSClient) sendSessionConnected(bs *BackgroundSession) {
	data := map[string]interface{}{
		"session_id":  c.sessionID,
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
			c.server.logger.Info("Sending connected message with working_dir",
				"session_id", c.sessionID,
				"working_dir", meta.WorkingDir)
		} else {
			c.server.logger.Warn("Failed to get metadata for connected message",
				"session_id", c.sessionID,
				"error", err)
		}
	} else {
		c.server.logger.Warn("No store for connected message",
			"session_id", c.sessionID)
	}

	c.sendMessage(WSMsgTypeConnected, data)
}

func (c *SessionWSClient) readPump() {
	defer func() {
		c.cancel()
		if c.bgSession != nil {
			c.bgSession.RemoveObserver(c)
		}
		// Release connection slot
		if c.server.connectionTracker != nil && c.clientIP != "" {
			c.server.connectionTracker.Remove(c.clientIP)
		}
		if c.store != nil {
			c.store.Close()
		}
		c.conn.Close()
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

func (c *SessionWSClient) writePump() {
	// Create ping ticker using server's WebSocket security config
	pingPeriod := c.server.wsSecurityConfig.PingPeriod
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(c.server.wsSecurityConfig.WriteWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(c.server.wsSecurityConfig.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *SessionWSClient) handleMessage(msg WSMessage) {
	switch msg.Type {
	case WSMsgTypePrompt:
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handlePrompt(data.Message)

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
	}
}

func (c *SessionWSClient) handlePrompt(message string) {
	if c.bgSession == nil {
		c.sendError("Session not running. Create or resume the session first.")
		return
	}

	c.promptCount++
	isFirstPrompt := c.promptCount == 1

	if err := c.bgSession.Prompt(message); err != nil {
		c.sendError("Failed to send prompt: " + err.Error())
		return
	}

	// Auto-generate title after first prompt
	if isFirstPrompt {
		go c.generateAndSetTitle(message)
	}
}

func (c *SessionWSClient) handleCancel() {
	if c.bgSession != nil {
		c.bgSession.Cancel()
	}
}

func (c *SessionWSClient) handlePermissionAnswer(optionID string, cancel bool) {
	if c.bgSession != nil {
		c.bgSession.AnswerPermission(optionID, cancel)
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

	c.sendMessage(WSMsgTypeSessionSync, map[string]interface{}{
		"session_id":  c.sessionID,
		"after_seq":   afterSeq,
		"events":      events,
		"event_count": meta.EventCount,
		"status":      meta.Status,
		"name":        meta.Name,
		"is_running":  isRunning,
	})
}

func (c *SessionWSClient) generateAndSetTitle(initialMessage string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	title, err := auxiliary.GenerateTitle(ctx, initialMessage)
	if err != nil || title == "" {
		return
	}

	if c.store != nil {
		if err := c.store.UpdateMetadata(c.sessionID, func(m *session.Metadata) {
			m.Name = title
		}); err != nil {
			return
		}
	}

	// Notify this client
	c.sendMessage(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": c.sessionID,
		"name":       title,
	})

	// Broadcast to global events
	c.server.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": c.sessionID,
		"name":       title,
	})
}

func (c *SessionWSClient) sendMessage(msgType string, data interface{}) {
	var dataJSON json.RawMessage
	if data != nil {
		dataJSON, _ = json.Marshal(data)
	}
	msg := WSMessage{Type: msgType, Data: dataJSON}
	msgBytes, _ := json.Marshal(msg)

	select {
	case c.send <- msgBytes:
	default:
	}
}

func (c *SessionWSClient) sendError(message string) {
	c.sendMessage(WSMsgTypeError, map[string]interface{}{
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
func (c *SessionWSClient) OnPromptComplete() {
	c.sendMessage(WSMsgTypePromptComplete, map[string]interface{}{
		"session_id": c.sessionID,
	})
}

// OnError is called when an error occurs.
func (c *SessionWSClient) OnError(message string) {
	c.sendError(message)
}
