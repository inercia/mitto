package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	// Track whether the initial load has been done. The client is not added as an
	// observer until after the initial load to prevent race conditions where events
	// are sent via observer callbacks before the client has loaded historical events.
	initialLoadDone bool
	initialLoadMu   sync.Mutex
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
				// Broadcast ACP start failure to all clients
				s.BroadcastACPStartFailed(sessionID, err.Error())
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

	// Store reference to background session if available.
	// Note: We do NOT add the client as an observer yet. The observer is added
	// after the initial load_events request is processed to prevent race conditions
	// where events are sent via observer callbacks before the client has loaded
	// historical events from storage.
	if bs != nil {
		client.bgSession = bs
		// Observer will be added in handleLoadEvents after initial load
		if clientLogger != nil {
			clientLogger.Debug("SessionWSClient has background session, observer will be added after initial load",
				"acp_id", bs.GetACPID())
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

		// Include last user prompt info for delivery confirmation on reconnect
		// This helps clients determine if their pending prompt was actually delivered
		// when reconnecting after a zombie WebSocket connection timeout
		if events, err := c.store.ReadEventsLast(c.sessionID, 50, 0); err == nil {
			lastPromptInfo := session.GetLastUserPromptInfo(events)
			if lastPromptInfo.Found {
				data["last_user_prompt_id"] = lastPromptInfo.PromptID
				data["last_user_prompt_seq"] = lastPromptInfo.Seq
			}
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

	// Include available slash commands from the agent
	if bs != nil {
		if commands := bs.AvailableCommands(); len(commands) > 0 {
			data["available_commands"] = commands
		}
	}

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
			FileIDs  []string `json:"file_ids,omitempty"`
			PromptID string   `json:"prompt_id,omitempty"` // Client-generated ID for delivery confirmation
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handlePromptWithMeta(data.Message, data.PromptID, data.ImageIDs, data.FileIDs)

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
			ClientTime  int64 `json:"client_time"`   // Client's timestamp in milliseconds
			LastSeenSeq int64 `json:"last_seen_seq"` // Highest seq the client has seen (optional, 0 if not provided)
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleKeepalive(data.ClientTime, data.LastSeenSeq)
	}
}

func (c *SessionWSClient) handlePromptWithMeta(message string, promptID string, imageIDs, fileIDs []string) {
	if c.bgSession == nil {
		c.sendPromptError("Session not running. Create or resume the session first.", promptID)
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
		FileIDs:  fileIDs,
	}
	if err := c.bgSession.PromptWithMeta(message, meta); err != nil {
		c.sendPromptError("Failed to send prompt: "+err.Error(), promptID)
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
		// Get current metadata to validate afterSeq against server state
		meta, metaErr := c.store.GetMetadata(c.sessionID)
		if metaErr != nil && metaErr != session.ErrSessionNotFound {
			c.sendError("Failed to read session metadata: " + metaErr.Error())
			return
		}

		serverMaxSeq := int64(meta.EventCount)

		// SAFETY CHECK: If client's afterSeq is higher than server's max seq,
		// the client has stale/corrupt state from a previous streaming session.
		// This can happen when:
		// 1. Streaming seq numbers diverged from persistence seq numbers
		// 2. Client reconnects with stale in-memory message state
		// In this case, fall back to initial load instead of sync.
		if afterSeq > serverMaxSeq {
			if c.logger != nil {
				c.logger.Warn("load_events_client_seq_mismatch",
					"client_after_seq", afterSeq,
					"server_max_seq", serverMaxSeq,
					"session_id", c.sessionID,
					"client_id", c.clientID,
					"action", "falling_back_to_initial_load")
			}
			// Fall back to initial load - load last N events
			events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
			isPrepend = false
			// Reset lastSentSeq since we're doing a fresh load
			c.seqMu.Lock()
			c.lastSentSeq = 0
			c.seqMu.Unlock()
		} else {
			// Normal sync: update lastSentSeq to afterSeq before loading to prevent duplicates
			c.seqMu.Lock()
			if afterSeq > c.lastSentSeq {
				c.lastSentSeq = afterSeq
			}
			c.seqMu.Unlock()

			events, err = c.store.ReadEventsFrom(c.sessionID, afterSeq)
			isPrepend = false

			// Log sync request for debugging keepalive sync issues
			if c.logger != nil {
				c.logger.Debug("load_events_sync",
					"after_seq", afterSeq,
					"events_from_storage", len(events),
					"session_id", c.sessionID)
			}
		}
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

	// DEBUG: Log event order being sent to client
	if c.logger != nil && len(events) > 0 {
		eventSummary := make([]string, 0, len(events))
		for _, e := range events {
			eventSummary = append(eventSummary, fmt.Sprintf("seq=%d type=%s", e.Seq, e.Type))
		}
		c.logger.Debug("events_loaded_sending",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"event_count", len(events),
			"first_seq", firstSeq,
			"last_seq", lastSeq,
			"is_prepend", isPrepend,
			"event_order", eventSummary)
	}

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

	// Send cached plan state if available (for conversation switch restoration).
	// This is done after events_loaded so the frontend can restore the agent plan panel.
	// Only send for initial load or sync (not prepend/pagination).
	if !isPrepend && c.server != nil && c.server.sessionManager != nil {
		if planEntries := c.server.sessionManager.GetCachedPlanState(c.sessionID); len(planEntries) > 0 {
			if c.logger != nil {
				c.logger.Debug("Sending cached plan state",
					"session_id", c.sessionID,
					"entry_count", len(planEntries))
			}
			c.sendMessage(WSMsgTypePlan, map[string]interface{}{
				"entries":    planEntries,
				"session_id": c.sessionID,
			})
		}
	}

	// For initial load or sync (not prepend), add the client as an observer now.
	// This is done AFTER sending events_loaded to prevent race conditions where
	// events are sent via observer callbacks before the client has loaded historical
	// events from storage.
	//
	// IMPORTANT: We add the observer for BOTH initial load (afterSeq == 0) AND
	// sync requests (afterSeq > 0). Previously, sync requests didn't add the observer,
	// which caused reconnecting clients to miss all new events after syncing.
	if !isPrepend {
		c.initialLoadMu.Lock()
		if !c.initialLoadDone && c.bgSession != nil {
			c.bgSession.AddObserver(c)
			c.initialLoadDone = true
			if c.logger != nil {
				c.logger.Debug("Added client as observer after load_events",
					"session_id", c.sessionID,
					"after_seq", afterSeq,
					"observer_count", c.bgSession.ObserverCount())
			}
		}
		c.initialLoadMu.Unlock()

		// H2 fix: Check for events that were persisted between the initial load
		// and observer registration. This handles the race window where events
		// arrive after we read from storage but before we're registered as an observer.
		if lastSeq > 0 {
			c.syncMissedEventsDuringRegistration(lastSeq)
		}
	}

	// If session has buffered events, we need to replay any that haven't been
	// persisted yet. This handles multiple cases:
	//
	// 1. Initial load (afterSeq == 0): Client connects mid-stream while agent messages
	//    are still being coalesced in the buffer.
	//
	// 2. Sync after keepalive (afterSeq > 0): Keepalive detected client is behind,
	//    but the missing events might still be in the buffer (not persisted yet).
	//    The server's getServerMaxSeq() includes buffer events, so load_events must too.
	//
	// 3. Just after prompting ends: The agent completed, isPrompting is false, but
	//    events might still be in the buffer briefly before they're fully persisted.
	//
	// Note: With immediate persistence for discrete events, only agent messages and
	// thoughts remain in the buffer (they need coalescing). These are the only events
	// that might not be in storage yet.
	//
	// We check the buffer regardless of isPrompting because there's a race window
	// after prompting ends but before the buffer is fully flushed/cleared.
	if !isPrepend && c.bgSession != nil {
		c.replayBufferedEventsWithDedup()
	}
}

// syncMissedEventsDuringRegistration checks for events that were persisted
// between the initial load and observer registration, and sends them to the client.
// This handles the H2 race window where events arrive after we read from storage
// but before we're registered as an observer.
func (c *SessionWSClient) syncMissedEventsDuringRegistration(lastLoadedSeq int64) {
	if c.store == nil {
		return
	}

	// Read any events that were persisted after our initial load
	events, err := c.store.ReadEventsFrom(c.sessionID, lastLoadedSeq)
	if err != nil {
		if c.logger != nil {
			c.logger.Debug("Failed to read missed events during registration",
				"error", err,
				"last_loaded_seq", lastLoadedSeq)
		}
		return
	}

	if len(events) == 0 {
		return
	}

	firstSeq := events[0].Seq
	lastSeq := events[len(events)-1].Seq

	// L1: Structured logging for missed events sync
	if c.logger != nil {
		c.logger.Debug("seq_sync_missed_events",
			"event_count", len(events),
			"last_loaded_seq", lastLoadedSeq,
			"first_seq", firstSeq,
			"last_seq", lastSeq,
			"client_id", c.clientID,
			"session_id", c.sessionID)
	}

	// Send these events to the client
	// The client's deduplication will handle any overlap
	c.sendMessage(WSMsgTypeEventsLoaded, map[string]interface{}{
		"events":       events,
		"has_more":     false,
		"first_seq":    firstSeq,
		"last_seq":     lastSeq,
		"prepend":      false,
		"is_running":   c.bgSession != nil && !c.bgSession.IsClosed(),
		"is_prompting": c.bgSession != nil && c.bgSession.IsPrompting(),
	})

	// Update lastSentSeq
	c.seqMu.Lock()
	if lastSeq > c.lastSentSeq {
		c.lastSentSeq = lastSeq
	}
	c.seqMu.Unlock()
}

// replayBufferedEventsWithDedup replays buffered events to this client,
// but only events with seq > lastSentSeq to prevent duplicates.
//
// This is only needed for agent messages and thoughts that are still being
// coalesced in the buffer. Discrete events (tool calls, file ops) are persisted
// immediately and will be in storage.
//
// This is called on every load_events request (not just when isPrompting) because
// there's a race window after the agent completes where events may still be in
// the buffer waiting to be flushed.
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

	// Count how many events we'll actually send
	sentCount := 0
	var maxBufferedSeq int64
	for _, event := range events {
		if event.Seq > maxBufferedSeq {
			maxBufferedSeq = event.Seq
		}
		if event.Seq > lastSent {
			sentCount++
		}
	}

	if c.logger != nil {
		c.logger.Debug("replayBufferedEventsWithDedup",
			"buffer_event_count", len(events),
			"events_to_send", sentCount,
			"last_sent_seq", lastSent,
			"max_buffered_seq", maxBufferedSeq)
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
//
// The response includes server_max_seq which allows the client to detect if it's
// behind and needs to sync. The client sends last_seen_seq to help the server
// understand the client's state (useful for debugging).
//
// Additional state is piggybacked on the keepalive_ack to keep the UI in sync:
// - queue_length: Number of messages waiting in the queue
// - status: Session status (active, completed, error)
// - is_running: Whether the background session is active
func (c *SessionWSClient) handleKeepalive(clientTime int64, clientLastSeenSeq int64) {
	serverTime := time.Now().UnixMilli()

	// Get the server's current max sequence number for this session
	// This is the highest seq that the server has (either in storage or streaming buffer)
	serverMaxSeq := c.getServerMaxSeq()

	// Check if the session is currently prompting (agent is responding)
	isPrompting := c.bgSession != nil && c.bgSession.IsPrompting()

	// Check if the background session is running (ACP connection active)
	isRunning := c.bgSession != nil && !c.bgSession.IsClosed()

	// Get queue length for the session
	var queueLength int
	if c.store != nil {
		queue := c.store.Queue(c.sessionID)
		if qLen, err := queue.Len(); err == nil {
			queueLength = qLen
		}
	}

	// Get session status from metadata
	var status string
	if c.store != nil {
		if meta, err := c.store.GetMetadata(c.sessionID); err == nil {
			status = string(meta.Status)
		}
	}

	// Log the keepalive with sequence info for debugging sync issues
	if c.logger != nil {
		c.logger.Debug("keepalive_received",
			"client_last_seen_seq", clientLastSeenSeq,
			"server_max_seq", serverMaxSeq,
			"client_behind", serverMaxSeq > clientLastSeenSeq,
			"is_prompting", isPrompting,
			"is_running", isRunning,
			"queue_length", queueLength,
			"status", status)
	}

	// Send keepalive acknowledgment with timestamps, sequence info, and session state
	// The is_prompting flag allows the UI to sync its streaming state with the server
	// Additional fields allow the UI to stay in sync without separate API calls
	c.sendMessage(WSMsgTypeKeepaliveAck, map[string]interface{}{
		"client_time":    clientTime,
		"server_time":    serverTime,
		"server_max_seq": serverMaxSeq,
		"is_prompting":   isPrompting,
		"is_running":     isRunning,
		"queue_length":   queueLength,
		"status":         status,
	})
}

// getServerMaxSeq returns the highest sequence number for this session.
// This considers both persisted events (from storage) and in-flight events
// (from the streaming buffer if a BackgroundSession is active).
func (c *SessionWSClient) getServerMaxSeq() int64 {
	var maxSeq int64

	// First, check persisted events from storage
	if c.store != nil {
		meta, err := c.store.GetMetadata(c.sessionID)
		if err == nil {
			maxSeq = int64(meta.EventCount)
		}
	}

	// If there's an active background session, check its buffer for higher seq
	// (events in buffer might not be persisted yet)
	if c.bgSession != nil {
		bufferSeq := c.bgSession.GetMaxBufferedSeq()
		if bufferSeq > maxSeq {
			maxSeq = bufferSeq
		}
	}

	return maxSeq
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

// sendPromptError sends an error message that includes the prompt_id for proper tracking.
// This allows the frontend to cancel pending send timeouts for this specific prompt.
func (c *SessionWSClient) sendPromptError(message string, promptID string) {
	c.wsConn.SendMessage(WSMsgTypeError, map[string]interface{}{
		"message":    message,
		"session_id": c.sessionID,
		"prompt_id":  promptID,
	})
}

// --- SessionObserver interface implementation ---

// OnAgentMessage is called when the agent sends a message chunk.
// seq is the sequence number for this logical message (chunks of the same message share the same seq).
func (c *SessionWSClient) OnAgentMessage(seq int64, html string) {
	// Check if this is a new message or continuation of current streaming message
	c.seqMu.Lock()
	// Note: seq=0 is a special case that indicates "no sequence number assigned"
	// (e.g., when chunks arrive after prompt completes). We should still send these
	// messages to the client, but we don't update the lastSentSeq tracking.
	if seq > 0 && seq < c.lastSentSeq {
		// Old message - skip entirely (already sent)
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "agent_message",
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		// New message - update tracking
		c.lastSentSeq = seq
		c.currentStreamingSeq = seq
	}
	// seq == lastSentSeq means continuation of current message - always send
	// seq == 0 means no sequence assigned - still send but don't update tracking
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "agent_message",
			"client_id", c.clientID,
			"html_len", len(html))
	}

	// Include is_prompting so the frontend knows if this is part of a user prompt response
	// or an unsolicited agent message (e.g., "indexing workspace" notifications).
	isPrompting := false
	if c.bgSession != nil {
		isPrompting = c.bgSession.IsPrompting()
	}

	// DEBUG: Log WebSocket send with is_prompting state
	if c.logger != nil {
		htmlPreview := html
		if len(htmlPreview) > 100 {
			htmlPreview = html[:100] + "..."
		}
		c.logger.Debug("ws_send_agent_message",
			"seq", seq,
			"html_len", len(html),
			"html_preview", htmlPreview,
			"is_prompting", isPrompting,
			"client_id", c.clientID,
			"session_id", c.sessionID)
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
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "agent_thought",
				"client_id", c.clientID)
		}
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

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "agent_thought",
			"client_id", c.clientID,
			"text_len", len(text))
	}

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
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "tool_call",
				"tool_id", id,
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "tool_call",
			"tool_id", id,
			"tool_title", title,
			"client_id", c.clientID)
	}

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
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "tool_update",
				"tool_id", id,
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	statusStr := ""
	if status != nil {
		statusStr = *status
	}
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "tool_update",
			"tool_id", id,
			"status", statusStr,
			"client_id", c.clientID)
	}

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
// entries contains the list of plan tasks with their status.
func (c *SessionWSClient) OnPlan(seq int64, entries []PlanEntry) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "plan",
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "plan",
			"client_id", c.clientID,
			"entries_count", len(entries))
	}

	c.sendMessage(WSMsgTypePlan, map[string]interface{}{
		"seq":        seq,
		"session_id": c.sessionID,
		"entries":    entries,
	})
}

// OnFileWrite is called when a file is written.
// seq is the sequence number for this file write event.
func (c *SessionWSClient) OnFileWrite(seq int64, path string, size int) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "file_write",
				"path", path,
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "file_write",
			"path", path,
			"size", size,
			"client_id", c.clientID)
	}

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
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "file_read",
				"path", path,
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "file_read",
			"path", path,
			"size", size,
			"client_id", c.clientID)
	}

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
	// DEBUG: Log prompt_complete send
	if c.logger != nil {
		c.logger.Debug("ws_send_prompt_complete",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"event_count", eventCount,
			"last_sent_seq", c.lastSentSeq)
	}
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
	c.logger.Debug("action_buttons: sending to WebSocket",
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
func (c *SessionWSClient) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string) {
	// Check seq tracking
	c.seqMu.Lock()
	if seq > 0 && seq <= c.lastSentSeq {
		// L1: Log skipped duplicate
		if c.logger != nil {
			c.logger.Debug("seq_skipped_duplicate",
				"seq", seq,
				"last_sent_seq", c.lastSentSeq,
				"event_type", "user_prompt",
				"prompt_id", promptID,
				"client_id", c.clientID)
		}
		c.seqMu.Unlock()
		return
	}
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	c.seqMu.Unlock()

	// L1: Log event delivery
	if c.logger != nil {
		c.logger.Debug("seq_delivered",
			"seq", seq,
			"event_type", "user_prompt",
			"prompt_id", promptID,
			"sender_id", senderID,
			"is_mine", senderID == c.clientID,
			"client_id", c.clientID)
	}

	c.sendMessage(WSMsgTypeUserPrompt, map[string]interface{}{
		"seq":          seq,
		"session_id":   c.sessionID,
		"sender_id":    senderID,
		"prompt_id":    promptID,
		"message":      message,
		"image_ids":    imageIDs,
		"file_ids":     fileIDs,
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

// OnAvailableCommandsUpdated is called when the agent sends available slash commands.
func (c *SessionWSClient) OnAvailableCommandsUpdated(commands []AvailableCommand) {
	c.sendMessage(WSMsgTypeAvailableCommandsUpdated, map[string]interface{}{
		"session_id": c.sessionID,
		"commands":   commands,
	})
}

// OnACPStopped is called when the ACP connection for this session is stopped.
// This notifies the WebSocket client that the session is no longer running,
// preventing further prompts and allowing the UI to update accordingly.
func (c *SessionWSClient) OnACPStopped(reason string) {
	if c.logger != nil {
		c.logger.Info("ACP stopped notification sent to client",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"reason", reason)
	}
	c.sendMessage(WSMsgTypeACPStopped, map[string]interface{}{
		"session_id": c.sessionID,
		"reason":     reason,
	})
}
