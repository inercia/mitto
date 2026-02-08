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

	// Seq tracking for deduplication - prevents sending the same event twice
	// This is the core of the WebSocket-only architecture: the server guarantees
	// no duplicates by tracking what has been sent to each client.
	lastSentSeq int64      // Highest seq sent to this client
	seqMu       sync.Mutex // Protects lastSentSeq

	// Track the seq of the message currently being streamed (for coalescing chunks)
	// This allows multiple chunks with the same seq to be sent (they're continuations)
	currentStreamingSeq int64
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
	wasResumed := false // Track if we resumed the session (vs already running)

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
			} else {
				wasResumed = true
				if clientLogger != nil {
					clientLogger.Debug("Resumed session for WebSocket client",
						"acp_id", bs.GetACPID())
				}
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

	// Trigger follow-up suggestions for resumed sessions with message history
	// This analyzes the last agent message and sends suggested responses asynchronously
	if wasResumed && bs != nil {
		bs.TriggerFollowUpSuggestions()
	}
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
			data["runner_type"] = meta.RunnerType
			data["runner_restricted"] = meta.RunnerRestricted
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

	case WSMsgTypeForceReset:
		c.handleForceReset()

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
		// DEPRECATED: Use load_events instead
		var data struct {
			AfterSeq int64 `json:"after_seq"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleSync(data.AfterSeq)

	case WSMsgTypeLoadEvents:
		var data struct {
			Limit     int   `json:"limit,omitempty"`
			BeforeSeq int64 `json:"before_seq,omitempty"`
			AfterSeq  int64 `json:"after_seq,omitempty"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleLoadEvents(data.Limit, data.BeforeSeq, data.AfterSeq)

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

func (c *SessionWSClient) handleForceReset() {
	if c.bgSession != nil {
		c.bgSession.ForceReset()
		// Notify the client that the session was reset
		c.sendMessage(WSMsgTypeSessionReset, map[string]interface{}{
			"session_id": c.sessionID,
		})
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
	// DEPRECATED: Use handleLoadEvents instead
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

// handleLoadEvents handles the unified load_events message for initial load, pagination, and sync.
// This is the core of the WebSocket-only architecture for event loading.
//
// Parameters:
//   - limit: Maximum events to return (default: 50)
//   - beforeSeq: Load events with seq < beforeSeq (for "load more" pagination)
//   - afterSeq: Load events with seq > afterSeq (for sync after reconnect)
//
// The server tracks lastSentSeq to prevent sending duplicates. After loading events,
// lastSentSeq is updated to the highest seq in the response.
func (c *SessionWSClient) handleLoadEvents(limit int, beforeSeq, afterSeq int64) {
	if c.store == nil {
		c.sendError("Session store not available")
		return
	}

	// Default limit
	if limit <= 0 {
		limit = 50
	}
	// Cap limit to prevent abuse
	if limit > 500 {
		limit = 500
	}

	// Validate mutually exclusive parameters
	if beforeSeq > 0 && afterSeq > 0 {
		c.sendError("before_seq and after_seq are mutually exclusive")
		return
	}

	var events []session.Event
	var err error
	var isPrepend bool // True if loading older events (for "load more")

	if afterSeq > 0 {
		// Sync mode: load events after a specific seq
		// Update lastSentSeq to afterSeq before loading to prevent duplicates
		c.seqMu.Lock()
		if afterSeq > c.lastSentSeq {
			c.lastSentSeq = afterSeq
		}
		c.seqMu.Unlock()

		events, err = c.store.ReadEventsFrom(c.sessionID, afterSeq)
		isPrepend = false
	} else if beforeSeq > 0 {
		// Pagination mode: load older events before a specific seq
		events, err = c.store.ReadEventsLast(c.sessionID, limit, beforeSeq)
		isPrepend = true
	} else {
		// Initial load: load last N events
		events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
		isPrepend = false
	}

	if err != nil {
		if err == session.ErrSessionNotFound {
			c.sendError("Session not found")
			return
		}
		c.sendError("Failed to read session events: " + err.Error())
		return
	}

	// Get metadata for total count
	meta, _ := c.store.GetMetadata(c.sessionID)

	// Calculate first_seq, last_seq, and has_more
	var firstSeq, lastSeq int64
	hasMore := false

	if len(events) > 0 {
		firstSeq = events[0].Seq
		lastSeq = events[len(events)-1].Seq

		// Check if there are more older events
		if firstSeq > 1 {
			hasMore = true
		}
	}

	// Update lastSentSeq for non-prepend loads (initial load and sync)
	// For prepend (load more), we don't update lastSentSeq because these are
	// historical events that don't affect streaming deduplication
	if !isPrepend && lastSeq > 0 {
		c.seqMu.Lock()
		if lastSeq > c.lastSentSeq {
			c.lastSentSeq = lastSeq
		}
		c.seqMu.Unlock()
	}

	// Get session status
	isRunning := c.bgSession != nil && !c.bgSession.IsClosed()
	isPrompting := isRunning && c.bgSession.IsPrompting()

	c.sendMessage(WSMsgTypeEventsLoaded, map[string]interface{}{
		"events":       events,
		"has_more":     hasMore,
		"first_seq":    firstSeq,
		"last_seq":     lastSeq,
		"total_count":  meta.EventCount,
		"prepend":      isPrepend,
		"is_running":   isRunning,
		"is_prompting": isPrompting,
	})

	// If this is an initial load (not prepend, not sync) and session is prompting,
	// we need to replay any buffered events that haven't been persisted yet.
	// This handles the case where a client connects mid-stream while agent messages
	// are still being coalesced in the buffer.
	//
	// Note: With immediate persistence for discrete events, only agent messages and
	// thoughts remain in the buffer (they need coalescing). These are the only events
	// that might not be in storage yet.
	if !isPrepend && afterSeq == 0 && isPrompting && c.bgSession != nil {
		c.replayBufferedEventsWithDedup()
	}
}

// replayBufferedEventsWithDedup replays buffered events to this client,
// but only events with seq > lastSentSeq to prevent duplicates.
//
// This is only needed for agent messages and thoughts that are still being
// coalesced in the buffer. Discrete events (tool calls, file ops) are persisted
// immediately and will be in storage.
func (c *SessionWSClient) replayBufferedEventsWithDedup() {
	if c.bgSession == nil {
		return
	}

	events := c.bgSession.GetBufferedEvents()
	if len(events) == 0 {
		return
	}

	c.seqMu.Lock()
	lastSent := c.lastSentSeq
	c.seqMu.Unlock()

	if c.logger != nil {
		c.logger.Debug("Replaying buffered events with dedup",
			"event_count", len(events),
			"last_sent_seq", lastSent)
	}

	for _, event := range events {
		// Only replay events with seq > lastSentSeq
		if event.Seq > lastSent {
			event.ReplayTo(c)
			// Update lastSentSeq after each event
			c.seqMu.Lock()
			if event.Seq > c.lastSentSeq {
				c.lastSentSeq = event.Seq
			}
			c.seqMu.Unlock()
		}
	}
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
// seq is the sequence number for this logical message (chunks of the same message share the same seq).
func (c *SessionWSClient) OnAgentMessage(seq int64, html string) {
	// Check if this is a new message or continuation of current streaming message
	c.seqMu.Lock()
	if seq < c.lastSentSeq {
		// Old message - skip entirely (already sent)
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		// New message - update tracking
		c.lastSentSeq = seq
		c.currentStreamingSeq = seq
	}
	// seq == lastSentSeq means continuation of current message - always send
	c.seqMu.Unlock()

	// Include is_prompting so the frontend knows if this is part of a user prompt response
	// or an unsolicited agent message (e.g., "indexing workspace" notifications).
	isPrompting := false
	if c.bgSession != nil {
		isPrompting = c.bgSession.IsPrompting()
	}
	c.sendMessage(WSMsgTypeAgentMessage, map[string]interface{}{
		"seq":          seq,
		"html":         html,
		"format":       "html",
		"session_id":   c.sessionID,
		"is_prompting": isPrompting,
	})
}

// OnAgentThought is called when the agent sends a thought chunk.
// seq is the sequence number for this logical thought (chunks share the same seq).
func (c *SessionWSClient) OnAgentThought(seq int64, text string) {
	// Check if this is a new thought or continuation of current streaming thought
	c.seqMu.Lock()
	if seq < c.lastSentSeq {
		// Old thought - skip entirely (already sent)
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		// New thought - update tracking
		c.lastSentSeq = seq
		c.currentStreamingSeq = seq
	}
	// seq == lastSentSeq means continuation of current thought - always send
	c.seqMu.Unlock()

	// Include is_prompting so the frontend knows if this is part of a user prompt response
	isPrompting := false
	if c.bgSession != nil {
		isPrompting = c.bgSession.IsPrompting()
	}
	c.sendMessage(WSMsgTypeAgentThought, map[string]interface{}{
		"seq":          seq,
		"text":         text,
		"session_id":   c.sessionID,
		"is_prompting": isPrompting,
	})
}

// OnToolCall is called when a tool call starts.
// seq is the sequence number for this tool call event.
func (c *SessionWSClient) OnToolCall(seq int64, id, title, status string) {
	// Check seq tracking - tool calls are discrete events, not streamed
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		// Already sent this tool call - skip
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// Include is_prompting so the frontend knows if this is part of a user prompt response
	isPrompting := false
	if c.bgSession != nil {
		isPrompting = c.bgSession.IsPrompting()
	}
	c.sendMessage(WSMsgTypeToolCall, map[string]interface{}{
		"seq":          seq,
		"id":           id,
		"title":        title,
		"status":       status,
		"session_id":   c.sessionID,
		"is_prompting": isPrompting,
	})
}

// OnToolUpdate is called when a tool call status is updated.
// seq is the sequence number for this tool update event.
func (c *SessionWSClient) OnToolUpdate(seq int64, id string, status *string) {
	// Check seq tracking - tool updates are discrete events
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		// Already sent this update - skip
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// Include is_prompting so the frontend knows if this is part of a user prompt response
	isPrompting := false
	if c.bgSession != nil {
		isPrompting = c.bgSession.IsPrompting()
	}
	data := map[string]interface{}{
		"seq":          seq,
		"id":           id,
		"session_id":   c.sessionID,
		"is_prompting": isPrompting,
	}
	if status != nil {
		data["status"] = *status
	}
	c.sendMessage(WSMsgTypeToolUpdate, data)
}

// OnPlan is called when a plan update occurs.
// seq is the sequence number for this plan event.
func (c *SessionWSClient) OnPlan(seq int64) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	c.sendMessage(WSMsgTypePlan, map[string]interface{}{
		"seq":        seq,
		"session_id": c.sessionID,
	})
}

// OnFileWrite is called when a file is written.
// seq is the sequence number for this file write event.
func (c *SessionWSClient) OnFileWrite(seq int64, path string, size int) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	c.sendMessage(WSMsgTypeFileWrite, map[string]interface{}{
		"seq":        seq,
		"path":       path,
		"size":       size,
		"session_id": c.sessionID,
	})
}

// OnFileRead is called when a file is read.
// seq is the sequence number for this file read event.
func (c *SessionWSClient) OnFileRead(seq int64, path string, size int) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	c.sendMessage(WSMsgTypeFileRead, map[string]interface{}{
		"seq":        seq,
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

// OnActionButtons is called when action buttons are extracted from the agent's response.
func (c *SessionWSClient) OnActionButtons(buttons []ActionButton) {
	c.logger.Debug("action_buttons: OnActionButtons called", "button_count", len(buttons))
	if len(buttons) == 0 {
		return
	}
	c.logger.Info("action_buttons: sending to WebSocket",
		"session_id", c.sessionID,
		"button_count", len(buttons))
	for i, btn := range buttons {
		c.logger.Debug("action_buttons: button detail", "index", i, "label", btn.Label, "response_len", len(btn.Response))
	}
	c.sendMessage(WSMsgTypeActionButtons, map[string]interface{}{
		"session_id": c.sessionID,
		"buttons":    buttons,
	})
}

// OnUserPrompt is called when any observer sends a prompt.
// This allows all connected clients to see user messages from other clients.
// senderID identifies which client sent the prompt (for deduplication).
// seq is the sequence number for this user prompt event.
func (c *SessionWSClient) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs []string) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	c.sendMessage(WSMsgTypeUserPrompt, map[string]interface{}{
		"seq":          seq,
		"session_id":   c.sessionID,
		"sender_id":    senderID,
		"prompt_id":    promptID,
		"message":      message,
		"image_ids":    imageIDs,
		"is_mine":      senderID == c.clientID,
		"is_prompting": true, // Signal frontend to show Stop button immediately
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
