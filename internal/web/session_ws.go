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

	"github.com/inercia/mitto/internal/auxiliary"
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

	// Guard against concurrent handleLoadEvents goroutines. TryLock is used
	// so that a second load_events arriving while one is in-flight is silently
	// dropped (the client will get the results from the first one).
	loadEventsMu sync.Mutex
}

func hasRenderableConversationEvent(events []session.Event) bool {
	for _, event := range events {
		switch event.Type {
		case session.EventTypeUserPrompt,
			session.EventTypeAgentMessage,
			session.EventTypeAgentThought,
			session.EventTypeToolCall,
			session.EventTypeError:
			return true
		}
	}
	return false
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

	// Use secure upgrader with compression for external connections
	secureUpgrader := s.getSecureUpgraderForRequest(r)
	conn, err := secureUpgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Session WebSocket upgrade failed", "error", err, "session_id", sessionID)
		}
		return
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()

	ctx, cancel := context.WithCancel(context.Background())
	clientID := generateClientID()

	// Create client-scoped logger with session and client context
	clientLogger := logging.WithClient(s.logger, clientID, sessionID)

	// Apply security settings, using a higher message size limit for localhost connections.
	// The macOS app sends large prompts over localhost; external connections keep the
	// smaller limit (default: 64KB) to bound the attack surface for remote callers.
	wsConfig := s.wsSecurityConfig
	if !IsExternalConnection(r) && wsConfig.LocalMaxMessageSize > 0 {
		wsConfig.MaxMessageSize = wsConfig.LocalMaxMessageSize
	}

	// Create shared WebSocket connection wrapper.
	// Use a larger send buffer (1024) for session connections because high-traffic
	// sessions can generate thousands of events. Combined with graceful backpressure
	// in WSConn (which waits briefly then closes slow connections instead of dropping
	// messages), this prevents sequence gaps that confuse the frontend.
	wsConn := NewWSConn(WSConnConfig{
		Conn:     conn,
		Config:   wsConfig,
		Logger:   clientLogger,
		ClientIP: clientIP,
		SendSize: 1024,
	})

	client := &SessionWSClient{
		server:    s,
		wsConn:    wsConn,
		sessionID: sessionID,
		clientID:  clientID,
		logger:    clientLogger,
		ctx:       ctx,
		cancel:    cancel,
		store:     store,
	}

	// Try to get existing background session first
	bs := s.sessionManager.GetSession(sessionID)

	// Circuit breaker fast path: if session is known to not exist (negative cache hit),
	// send session_gone immediately without hitting the filesystem.
	if bs == nil && s.negativeSessionCache != nil && s.negativeSessionCache.IsNotFound(sessionID) {
		if clientLogger != nil {
			clientLogger.Debug("Session in negative cache, sending session_gone",
				"session_id", sessionID)
		}
		go client.writePump()
		go client.readPump()
		client.sendMessage(WSMsgTypeSessionGone, map[string]interface{}{
			"session_id": sessionID,
			"reason":     "session not found (cached)",
		})
		go func() {
			time.Sleep(100 * time.Millisecond)
			client.wsConn.Close()
		}()
		return
	}

	// staleSessionThreshold is the age beyond which a WebSocket connection to a
	// session is flagged in the log for investigation.  A very old session whose
	// WebSocket upgrade is unusually slow can exhaust file descriptors or consume
	// disproportionate memory when its event history is replayed.
	const staleSessionThreshold = 30 * 24 * time.Hour // 30 days

	// If no running session, try to resume it (unless archived)
	if bs == nil && store != nil {
		// Check if session exists in store
		meta, err := store.GetMetadata(sessionID)
		switch err {
		case nil:
			// Stale-session detection: warn when a WebSocket connects to a session
			// that has not been active for a long time.  245-second upgrade times
			// observed in the access log were all associated with sessions many
			// months old whose large event histories caused slow disk I/O during
			// the subsequent load_events replay.
			if age := time.Since(meta.CreatedAt); age > staleSessionThreshold {
				if clientLogger != nil {
					clientLogger.Warn("WebSocket connecting to stale session",
						"session_id", sessionID,
						"age_days", int(age.Hours()/24),
						"event_count", meta.EventCount,
					)
				}
			}

			// Don't resume archived sessions - they should remain read-only
			// without an ACP connection. Users can still view history.
			if meta.Archived {
				if clientLogger != nil {
					clientLogger.Debug("Session is archived, not resuming ACP",
						"session_id", sessionID)
				}
			} else {
				// Session exists in store and is not archived.
				// Resume ACP asynchronously to avoid blocking the WebSocket handler.
				// The frontend will receive `connected` immediately with is_running=false,
				// and `acp_started` when the ACP process is ready. Events from events.jsonl
				// can be served via load_events before ACP is up.
				cwd := meta.WorkingDir
				if cwd == "" {
					cwd, _ = os.Getwd()
				}
				sessionName := meta.Name
				go func() {
					resumedBS, err := s.sessionManager.ResumeSession(sessionID, sessionName, cwd)
					if err != nil {
						if clientLogger != nil {
							clientLogger.Error("Failed to resume session (async)", "error", err)
						}
						s.BroadcastACPStartFailed(sessionID, err, "")
						return
					}
					// Invalidate negative cache
					if s.negativeSessionCache != nil {
						s.negativeSessionCache.Remove(sessionID)
					}
					if clientLogger != nil {
						clientLogger.Debug("Resumed session for WebSocket client (async)",
							"acp_id", resumedBS.GetACPID())
					}
					// Attach to all WebSocket clients watching this session.
					// Use the same pattern as unarchive: tryAttachToSession handles
					// observer registration and sends acp_started notification.
					client.tryAttachToSession()
					// Broadcast to global events so all clients (including other browsers) learn about it
					s.BroadcastACPStarted(sessionID)
					// Trigger follow-up suggestions for resumed sessions
					resumedBS.TriggerFollowUpSuggestions()
				}()
			}
		case session.ErrSessionNotFound:
			// Session truly doesn't exist in memory or store — send session_gone and close.
			// Also cache this result to prevent repeated filesystem lookups for the same
			// deleted session (circuit breaker for "Session not found" error storms).
			if s.negativeSessionCache != nil {
				s.negativeSessionCache.MarkNotFound(sessionID)
			}
			if clientLogger != nil {
				clientLogger.Info("Session not found, sending session_gone",
					"session_id", sessionID)
			}
			go client.writePump()
			go client.readPump()
			client.sendMessage(WSMsgTypeSessionGone, map[string]interface{}{
				"session_id": sessionID,
				"reason":     "session not found",
			})
			go func() {
				time.Sleep(100 * time.Millisecond)
				client.wsConn.Close()
			}()
			return
		default:
			// other store errors — log and continue (let client see what's possible)
		}
	}

	// Store reference to background session if available.
	// Note: We do NOT add the client as an observer yet. The observer is added
	// after the initial load_events request is processed to prevent race conditions
	// where events are sent via observer callbacks before the client has loaded
	// historical events from storage.
	if bs != nil {
		client.bgSession = bs
		bs.AddConnectedClient()
		// Observer will be added in handleLoadEvents after initial load
		if clientLogger != nil {
			clientLogger.Debug("SessionWSClient has background session, observer will be added after initial load",
				"acp_id", bs.GetACPID())
		}
	}

	go client.writePump()

	// Send the `connected` message BEFORE starting readPump.
	//
	// The frontend sends `load_events` immediately in ws.onopen. On multi-core
	// systems (GOMAXPROCS > 1), if readPump were started first, it could read
	// the client's `load_events` request and dispatch handleLoadEventsAsync on
	// another CPU core, which would enqueue `events_loaded` into the send channel
	// *before* sendSessionConnected has a chance to enqueue `connected`. The
	// writePump drains in FIFO order, so the client would receive `events_loaded`
	// before `connected` — a protocol ordering violation.
	//
	// By enqueuing `connected` while readPump is not yet running, we guarantee
	// it is the first message in the send channel and therefore the first message
	// the client receives, regardless of scheduling.
	client.sendSessionConnected(bs)

	// Reset the read deadline before starting readPump. The initial deadline was set
	// at WebSocket upgrade time (in configureWebSocketConn). If ResumeSession blocked
	// for longer than PongWait (60s), the deadline has already expired and ReadMessage()
	// would fail immediately. This refresh ensures readPump starts with a fresh deadline.
	client.wsConn.ResetReadDeadline()

	go client.readPump()
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
			// Override acp_server with session-specific value from metadata
			// This fixes grouping for multiple workspaces with the same folder but different ACP servers
			if meta.ACPServer != "" {
				data["acp_server"] = meta.ACPServer
			}
			if c.logger != nil {
				c.logger.Debug("Sending connected message", "working_dir", meta.WorkingDir, "acp_server", data["acp_server"])
			}
		} else if c.logger != nil {
			c.logger.Warn("Failed to get metadata for connected message", "error", err)
		}

		// Get periodic prompts state
		// periodic_enabled = true means a periodic config exists (session is in periodic mode)
		// This determines UI mode (shows frequency panel and lock/unlock buttons)
		periodicStore := c.store.Periodic(c.sessionID)
		if periodic, err := periodicStore.Get(); err == nil && periodic != nil {
			data["periodic_enabled"] = true
		} else {
			data["periodic_enabled"] = false
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

	// Include session config options from the agent
	// This includes modes (converted to config option format) and any other config options
	if bs != nil {
		if configOptions := bs.ConfigOptions(); len(configOptions) > 0 {
			data["config_options"] = configOptions
		}
	}

	// Include agent capability flags so the frontend can adapt the UI
	if bs != nil {
		data["agent_supports_images"] = bs.AgentSupportsImages()
		data["workspace_uuid"] = bs.GetWorkspaceUUID()
		data["acp_ready"] = bs.IsACPReady()
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
			c.bgSession.RemoveConnectedClient()
			c.bgSession.RemoveObserver(c)
		}
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
		go c.handleLoadEventsAsync(data.Limit, data.BeforeSeq, data.AfterSeq)

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

	case WSMsgTypeSetConfigOption:
		var data struct {
			ConfigID string `json:"config_id"`
			Value    string `json:"value"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleSetConfigOption(data.ConfigID, data.Value)

	case WSMsgTypeUIPromptAnswer:
		var data struct {
			RequestID string `json:"request_id"`
			OptionID  string `json:"option_id"`
			Label     string `json:"label"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.sendError("Invalid message data")
			return
		}
		c.handleUIPromptAnswer(data.RequestID, data.OptionID, data.Label)
	}
}

func (c *SessionWSClient) handlePromptWithMeta(message string, promptID string, imageIDs, fileIDs []string) {
	// If bgSession is nil, try to attach to a running session.
	// This handles the case where the session was unarchived after this client connected.
	if c.bgSession == nil {
		c.tryAttachToSession()
	}

	if c.bgSession == nil {
		c.sendPromptError("The AI agent for this conversation is not connected. It may still be starting up — please wait a moment and try again.", promptID)
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

// handlePermissionAnswer handles legacy permission_answer messages from older frontends.
// Permissions now use the unified UIPrompt system, so this forwards to handleUIPromptAnswer.
func (c *SessionWSClient) handlePermissionAnswer(optionID string, cancel bool) {
	// For backwards compatibility, forward to the UIPrompt answer handler.
	// The request_id for permission prompts is the tool_call_id.
	if cancel {
		// No easy way to get the request_id for cancelled permissions in legacy flow.
		// Log and ignore - new frontends should use ui_prompt_answer.
		if c.logger != nil {
			c.logger.Debug("legacy_permission_answer_cancelled",
				"note", "legacy permission_answer cancel not fully supported, use ui_prompt_answer")
		}
		return
	}

	// For legacy clients, we don't have the request_id readily available.
	// The new UIPrompt system uses tool_call_id as request_id, but legacy clients
	// don't send it. Log a warning and do nothing - this is a deprecation path.
	if c.logger != nil {
		c.logger.Warn("legacy_permission_answer_received",
			"option_id", optionID,
			"note", "please upgrade to ui_prompt_answer message type")
	}
}

func (c *SessionWSClient) handleUIPromptAnswer(requestID, optionID, label string) {
	// Try to attach to session if unarchived after client connected
	if c.bgSession == nil {
		c.tryAttachToSession()
	}

	if c.bgSession == nil {
		c.sendError("Session not running")
		return
	}

	if c.logger != nil {
		c.logger.Debug("UI prompt answer received",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"request_id", requestID,
			"option_id", optionID,
			"label", label)
	}

	// Forward the answer to the background session
	c.bgSession.HandleUIPromptAnswer(requestID, optionID, label)
}

func (c *SessionWSClient) handleSync(afterSeq int64) {
	// DEPRECATED: Use handleLoadEvents instead
	if c.store == nil {
		c.sendError("Session store not available")
		return
	}

	events, err := c.store.ReadEventsFrom(c.sessionID, afterSeq, 0)
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

// handleLoadEventsAsync wraps handleLoadEvents with a TryLock guard to prevent
// concurrent executions. If another load is already in progress, this call is
// silently dropped — the client will receive the results from the in-flight load.
// This is safe because:
// - c.store has its own sync.RWMutex for concurrent reads
// - c.seqMu protects lastSentSeq
// - c.initialLoadMu protects initialLoadDone and AddObserver
// - c.sendMessage writes to a buffered channel (thread-safe)
func (c *SessionWSClient) handleLoadEventsAsync(limit int, beforeSeq, afterSeq int64) {
	if !c.loadEventsMu.TryLock() {
		if c.logger != nil {
			c.logger.Debug("load_events_skipped_concurrent",
				"session_id", c.sessionID,
				"client_id", c.clientID,
				"reason", "another load_events is already in progress")
		}
		return
	}
	defer c.loadEventsMu.Unlock()
	c.handleLoadEvents(limit, beforeSeq, afterSeq)
}

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

		// Use the higher of MaxSeq (highest persisted streaming seq) and EventCount
		// (total events, including user prompts recorded via AppendEvent).
		// AppendEvent assigns seq = EventCount (sequential), so EventCount is always a
		// valid lower-bound on the highest persisted seq. MaxSeq tracks coalesced
		// streaming seq which can exceed EventCount. Taking the max of both gives the
		// true highest persisted seq regardless of which persistence path was used.
		serverMaxSeq := meta.MaxSeq
		if int64(meta.EventCount) > serverMaxSeq {
			serverMaxSeq = int64(meta.EventCount)
		}

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
			// NOTE: We intentionally do NOT reset lastSentSeq here.
			// The observer path may have already delivered events with higher seq numbers.
			// The lastSentSeq will be updated below based on the loaded events,
			// but only if the loaded events have higher seq than what was already sent.
		} else {
			// Normal sync: update lastSentSeq to afterSeq before loading to prevent duplicates
			c.seqMu.Lock()
			if afterSeq > c.lastSentSeq {
				c.lastSentSeq = afterSeq
			}
			c.seqMu.Unlock()

			events, err = c.store.ReadEventsFrom(c.sessionID, afterSeq, 0)
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
		// This happens on:
		// 1. Fresh page load (client has no stored state)
		// 2. After stale client detection (see keepalive "stale_detected" message)
		// 3. After mismatch fallback (client_after_seq > server_max_seq)
		if c.logger != nil {
			c.logger.Debug("load_events_initial_load",
				"limit", limit,
				"session_id", c.sessionID,
				"client_id", c.clientID,
				"reason", "no_after_seq_or_before_seq")
		}
		events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
		isPrepend = false
	}

	if err != nil {
		if err == session.ErrSessionNotFound {
			// Send terminal session_gone instead of generic error.
			// This tells the client to stop reconnecting for this session.
			c.sendMessage(WSMsgTypeSessionGone, map[string]interface{}{
				"session_id": c.sessionID,
				"reason":     "session not found in store",
			})
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

	if hasMore && len(events) > 0 && !hasRenderableConversationEvent(events) {
		searchBefore := firstSeq
		extraLoaded := 0
		for searchBefore > 1 && !hasRenderableConversationEvent(events) && extraLoaded < 2000 {
			remaining := int(searchBefore - 1)
			if remaining <= 0 {
				break
			}

			batchSize := limit
			if batchSize > remaining {
				batchSize = remaining
			}

			olderEvents, readErr := c.store.ReadEventsLast(c.sessionID, batchSize, searchBefore)
			if readErr != nil || len(olderEvents) == 0 {
				break
			}

			events = append(olderEvents, events...)
			extraLoaded += len(olderEvents)
			firstSeq = olderEvents[0].Seq
			searchBefore = firstSeq
			hasMore = firstSeq > 1
		}
	}

	// Guard against WebSocket 1009 "Message Too Large" errors.
	//
	// WKWebView (macOS / iOS) enforces a hard WebSocket receive-message size limit
	// (~1 MB by default). Sessions with many large agent responses (code blocks,
	// long explanations) can produce events_loaded payloads of several megabytes,
	// which causes the client to close the connection with close code 1009.
	// This creates a permanent failure loop: every reconnect triggers another
	// initial load → another 1009 → the user's pending prompt is permanently lost.
	//
	// Fix: for non-prepend loads (initial load and sync fallback), marshal the
	// events slice and, if the payload exceeds the limit, drop the oldest events
	// one at a time until it fits. Dropped events remain accessible through the
	// standard "load more" (before_seq) pagination mechanism; has_more is set to
	// true so the client knows to offer that control.
	//
	// 512 KB keeps us comfortably below the 1 MB WKWebView limit even after the
	// JSON envelope overhead (has_more, first_seq, last_seq, max_seq, …) is added.
	const maxEventsLoadedBytes = 512 * 1024
	if !isPrepend && len(events) > 1 {
		payloadJSON, payloadErr := json.Marshal(events)
		if payloadErr == nil && len(payloadJSON) > maxEventsLoadedBytes {
			originalCount := len(events)
			originalBytes := len(payloadJSON)
			for len(events) > 1 {
				events = events[1:] // drop oldest — still reachable via before_seq
				hasMore = true
				payloadJSON, payloadErr = json.Marshal(events)
				if payloadErr != nil || len(payloadJSON) <= maxEventsLoadedBytes {
					break
				}
			}
			// If we are left with a single event (or small slice) that is still too
			// large, avoid sending an oversized payload by dropping it from this
			// response and logging the condition. The event remains reachable via
			// pagination, but will not be sent in this batch.
			if payloadErr == nil && len(payloadJSON) > maxEventsLoadedBytes {
				droppedCount := len(events)
				events = nil
				hasMore = true
				payloadJSON = []byte("[]")
				if c.logger != nil {
					c.logger.Error("events_loaded_single_event_too_large",
						"session_id", c.sessionID,
						"client_id", c.clientID,
						"dropped_event_count", droppedCount,
						"max_payload_bytes", maxEventsLoadedBytes)
				}
				_ = droppedCount // used only in the log above
			}
			// Recompute firstSeq after trimming (lastSeq is unchanged).
			if len(events) > 0 {
				firstSeq = events[0].Seq
			}
			if c.logger != nil {
				c.logger.Warn("events_loaded_payload_trimmed",
					"session_id", c.sessionID,
					"client_id", c.clientID,
					"original_event_count", originalCount,
					"original_payload_bytes", originalBytes,
					"trimmed_event_count", len(events),
					"trimmed_payload_bytes", len(payloadJSON),
					"max_payload_bytes", maxEventsLoadedBytes,
					"new_first_seq", firstSeq,
					"last_seq", lastSeq,
					"has_more", hasMore)
			}
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
		"max_seq":      c.getServerMaxSeq(),
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

		// Trigger MCP availability check (once per workspace per server lifetime).
		// This runs when a client focuses/switches to a conversation (load_events).
		// The check is skipped if already done for this workspace (IsMCPChecked).
		if c.bgSession != nil && c.server != nil && c.server.sessionManager != nil {
			if workspaceUUID := c.bgSession.GetWorkspaceUUID(); workspaceUUID != "" {
				if !c.server.sessionManager.IsMCPChecked(workspaceUUID) {
					// Mark immediately to prevent concurrent checks for the same workspace.
					c.server.sessionManager.MarkMCPChecked(workspaceUUID)
					go c.triggerMCPAvailabilityCheck(workspaceUUID)
				}
				// Check if MCP tools have been fetched for this workspace
				if !c.server.sessionManager.IsMCPToolsFetched(workspaceUUID) {
					c.server.sessionManager.MarkMCPToolsFetched(workspaceUUID)
					go c.triggerMCPToolsFetch(workspaceUUID)
				} else if c.server.auxiliaryManager != nil && c.server.eventsManager != nil {
					// Tools already fetched for this workspace — broadcast cached result
					// via the global events WebSocket (where the frontend handler lives).
					if cached, ok := c.server.auxiliaryManager.GetCachedMCPTools(workspaceUUID); ok && len(cached) > 0 {
						c.server.eventsManager.Broadcast(WSMsgTypeMCPToolsAvailable, map[string]interface{}{
							"workspace_uuid": workspaceUUID,
							"tools":          cached,
						})
					}
				}
			}
		}
	}

	// If session has buffered events, we need to replay any that haven't been
	// persisted yet. This handles multiple cases:
	//
	// 1. Initial load (afterSeq == 0): Client connects mid-stream while agent messages
	//    are still being coalesced in the buffer.
	//
	// Note: With immediate persistence, all events are persisted as soon as they're
	// received from ACP. There's no buffer to replay from. Events are available in
	// storage immediately after they're assigned a sequence number.
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
	events, err := c.store.ReadEventsFrom(c.sessionID, lastLoadedSeq, 0)
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
		"max_seq":      c.getServerMaxSeq(),
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
	if c.bgSession != nil {
		c.bgSession.TouchActivity()
	}
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
		"max_seq":        serverMaxSeq,
		"server_max_seq": serverMaxSeq, // Deprecated: use max_seq. Kept for backward compatibility.
		"is_prompting":   isPrompting,
		"is_running":     isRunning,
		"queue_length":   queueLength,
		"status":         status,
	})
}

// handleSetConfigOption handles a request to change a session config option value.
// This sends the request to the ACP agent and broadcasts the change.
// For legacy modes, use configID "mode" with the desired mode value.
func (c *SessionWSClient) handleSetConfigOption(configID, value string) {
	if configID == "" {
		c.sendError("config_id is required")
		return
	}
	if value == "" {
		c.sendError("value is required")
		return
	}

	// Try to attach to session if unarchived after client connected
	if c.bgSession == nil {
		c.tryAttachToSession()
	}

	if c.bgSession == nil {
		c.sendError("No active session")
		return
	}

	// Call the background session to set the config option
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.bgSession.SetConfigOption(ctx, configID, value); err != nil {
		if c.logger != nil {
			c.logger.Error("Failed to set config option",
				"config_id", configID,
				"value", value,
				"error", err)
		}
		c.sendError("Failed to set config: " + err.Error())
		return
	}

	// The config change will be broadcast to all clients via the onConfigChanged callback
	// which is set up in SessionManager
}

// getServerMaxSeq returns the highest sequence number for this session.
// This considers both persisted events (from storage) and in-flight events
// (from the BackgroundSession's sequence counter if active).
func (c *SessionWSClient) getServerMaxSeq() int64 {
	var maxSeq int64

	// First, check persisted events from storage
	// Use MaxSeq (highest persisted seq) not EventCount (number of events)
	// because seq numbers can be sparse due to coalescing
	if c.store != nil {
		meta, err := c.store.GetMetadata(c.sessionID)
		if err == nil {
			maxSeq = meta.MaxSeq
			if maxSeq == 0 {
				// Fallback for sessions created before MaxSeq was tracked
				maxSeq = int64(meta.EventCount)
			}
		}
	}

	// If there's an active background session, check its assigned seq counter.
	// This includes events that have been assigned but may not yet be reflected
	// in the store metadata (small window between assign and persist).
	// This prevents false "stale client" detection during active streaming.
	if c.bgSession != nil {
		assignedSeq := c.bgSession.GetMaxAssignedSeq()
		if assignedSeq > maxSeq {
			maxSeq = assignedSeq
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
		Store:            c.store,
		SessionID:        c.sessionID,
		Message:          initialMessage,
		Logger:           c.server.logger,
		WorkspaceUUID:    c.bgSession.GetWorkspaceUUID(),
		AuxiliaryManager: c.bgSession.GetAuxiliaryManager(),
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

// triggerMCPAvailabilityCheck asynchronously checks if Mitto MCP tools are available
// in the ACP server for the given workspace. Should be called in a goroutine.
// Called at most once per workspace per server lifetime (enforced by IsMCPChecked/MarkMCPChecked).
// If tools are not available, sends mcp_tools_unavailable to this client.
func (c *SessionWSClient) triggerMCPAvailabilityCheck(workspaceUUID string) {
	// Get MCP server URL from the running MCP server instance.
	var mcpServerURL string
	if c.server != nil && c.server.mcpServer != nil {
		mcpServerURL = fmt.Sprintf("http://%s:%d/mcp",
			c.server.mcpServer.Host(), c.server.mcpServer.Port())
	}

	// Get auxiliary manager for performing the check.
	if c.server == nil || c.server.auxiliaryManager == nil {
		if c.logger != nil {
			c.logger.Debug("mcp availability check: no auxiliary manager available",
				"workspace_uuid", workspaceUUID)
		}
		return
	}
	auxMgr := c.server.auxiliaryManager

	// Use a long timeout — auxiliary session creation and the availability-check prompt
	// can take several minutes depending on agent load. 30 minutes covers worst-case scenarios.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if c.logger != nil {
		c.logger.Debug("mcp availability check: starting",
			"workspace_uuid", workspaceUUID,
			"mcp_server_url", mcpServerURL)
	}

	result, err := auxMgr.CheckMCPAvailability(ctx, workspaceUUID, mcpServerURL)
	if err != nil {
		if c.logger != nil {
			c.logger.Debug("mcp availability check: failed",
				"workspace_uuid", workspaceUUID,
				"error", err)
		}
		// Clear the checked flag so the next client connection will retry.
		if c.server != nil && c.server.sessionManager != nil {
			c.server.sessionManager.ClearMCPChecked(workspaceUUID)
		}
		return
	}

	if c.logger != nil {
		c.logger.Debug("mcp availability check: completed",
			"workspace_uuid", workspaceUUID,
			"available", result.Available)
	}

	if !result.Available {
		data := map[string]interface{}{
			"workspace_uuid": workspaceUUID,
		}
		if result.SuggestedRun != "" {
			data["suggested_run"] = result.SuggestedRun
		}
		if result.SuggestedInstructions != "" {
			data["suggested_instructions"] = result.SuggestedInstructions
		}
		// Broadcast via global events WebSocket (where the frontend handler lives).
		if c.server != nil && c.server.eventsManager != nil {
			c.server.eventsManager.Broadcast(WSMsgTypeMCPToolsUnavailable, data)
		}
	}
}

// triggerMCPToolsFetch asynchronously fetches the list of available MCP tools
// for the given workspace. Should be called in a goroutine.
// Called at most once per workspace per server lifetime (enforced by IsMCPToolsFetched/MarkMCPToolsFetched).
// On success, sends mcp_tools_available to this client.
func (c *SessionWSClient) triggerMCPToolsFetch(workspaceUUID string) {
	// Get auxiliary manager
	if c.server == nil || c.server.auxiliaryManager == nil {
		if c.logger != nil {
			c.logger.Debug("mcp tools fetch: no auxiliary manager available",
				"workspace_uuid", workspaceUUID)
		}
		return
	}
	auxMgr := c.server.auxiliaryManager

	// Use a long timeout — auxiliary session creation and the tool-fetch prompt
	// can take several minutes depending on agent load. 30 minutes covers worst-case scenarios.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if c.logger != nil {
		c.logger.Debug("mcp tools fetch: starting",
			"workspace_uuid", workspaceUUID)
	}

	tools, err := auxMgr.FetchMCPTools(ctx, workspaceUUID)
	if err != nil {
		if c.logger != nil {
			c.logger.Debug("mcp tools fetch: failed",
				"workspace_uuid", workspaceUUID,
				"error", err)
		}
		// Clear the fetched flag so the next client connection will retry.
		if c.server != nil && c.server.sessionManager != nil {
			c.server.sessionManager.ClearMCPToolsFetched(workspaceUUID)
		}
		return
	}

	if c.logger != nil {
		c.logger.Debug("mcp tools fetch: completed",
			"workspace_uuid", workspaceUUID,
			"tool_count", len(tools))
	}

	// If the agent returned an empty list, clear the fetched flag so the
	// next client connection will retry (the agent may have misunderstood).
	if len(tools) == 0 {
		if c.server != nil && c.server.sessionManager != nil {
			c.server.sessionManager.ClearMCPToolsFetched(workspaceUUID)
		}
	}

	// Broadcast the tools list to ALL clients via the global events WebSocket.
	// The frontend handler for mcp_tools_available is in the global events handler,
	// not the per-session handler.
	if c.server != nil && c.server.eventsManager != nil {
		c.server.eventsManager.Broadcast(WSMsgTypeMCPToolsAvailable, map[string]interface{}{
			"workspace_uuid": workspaceUUID,
			"tools":          tools,
		})
	}

	// After broadcasting tools, check required tool patterns from prompts.
	go c.checkRequiredToolPatterns(workspaceUUID, tools)
}

// checkRequiredToolPatterns collects enabledWhenMCP patterns from all prompts,
// checks them against the initial tools list, and retries unsatisfied patterns
// with exponential backoff. MCP tools from external servers can take time to appear
// (e.g., external Python programs), so retries are essential.
//
// Flow:
//  1. Collect all enabledWhenMCP patterns from global + workspace prompts
//  2. Local match: check patterns against the initial fetched tools list
//  3. For unsatisfied patterns: query the auxiliary session (targeted check)
//  4. Broadcast results
//  5. Retry unsatisfied patterns with backoff: 30s, 60s, 120s
func (c *SessionWSClient) checkRequiredToolPatterns(workspaceUUID string, initialTools []auxiliary.MCPToolInfo) {
	// Collect all enabledWhenMCP patterns from prompts
	patterns := c.collectRequiredToolPatterns()
	if len(patterns) == 0 {
		if c.logger != nil {
			c.logger.Debug("required tools check: no patterns found in prompts",
				"workspace_uuid", workspaceUUID)
		}
		return
	}

	if c.logger != nil {
		c.logger.Debug("required tools check: starting",
			"workspace_uuid", workspaceUUID,
			"pattern_count", len(patterns),
			"patterns", patterns)
	}

	// Phase 1: Local match against initially fetched tools
	satisfied := make(map[string]bool)
	for _, pattern := range patterns {
		for _, tool := range initialTools {
			if config.MatchToolPattern(pattern, tool.Name) {
				satisfied[pattern] = true
				break
			}
		}
		if !satisfied[pattern] {
			satisfied[pattern] = false
		}
	}

	// Broadcast initial status
	c.broadcastEnabledWhenMCPStatus(workspaceUUID, satisfied)

	// Check if all patterns are already satisfied
	unsatisfied := c.getUnsatisfiedPatterns(satisfied)
	if len(unsatisfied) == 0 {
		if c.logger != nil {
			c.logger.Debug("required tools check: all patterns satisfied from initial tools",
				"workspace_uuid", workspaceUUID)
		}
		return
	}

	// Phase 2: Query auxiliary session for unsatisfied patterns, with retry
	retryDelays := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}

	for attempt, delay := range retryDelays {
		if c.logger != nil {
			c.logger.Debug("required tools check: waiting before retry",
				"workspace_uuid", workspaceUUID,
				"attempt", attempt+1,
				"delay", delay,
				"unsatisfied_count", len(unsatisfied))
		}

		// Wait before retry; use NewTimer so we can stop it promptly on disconnect.
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			// delay elapsed, proceed to retry
		case <-c.ctx.Done():
			// Client disconnected; stop and drain timer to avoid leaking it.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}

		// Check auxiliary manager is available
		if c.server == nil || c.server.auxiliaryManager == nil {
			return
		}

		// Query the agent about unsatisfied patterns.
		// Derive from c.ctx so the query is cancelled if the client disconnects.
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Minute)
		result, err := c.server.auxiliaryManager.CheckRequiredToolPatterns(ctx, workspaceUUID, unsatisfied)
		cancel()

		if err != nil {
			if c.logger != nil {
				c.logger.Debug("required tools check: query failed",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt+1,
					"error", err)
			}
			continue
		}

		// Merge results (only upgrade to true)
		for pattern, available := range result {
			if available {
				satisfied[pattern] = true
			}
		}

		// Broadcast updated status
		c.broadcastEnabledWhenMCPStatus(workspaceUUID, satisfied)

		// Check if all satisfied now
		unsatisfied = c.getUnsatisfiedPatterns(satisfied)
		if len(unsatisfied) == 0 {
			if c.logger != nil {
				c.logger.Info("required tools check: all patterns satisfied",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt+1)
			}
			return
		}
	}

	if c.logger != nil {
		c.logger.Info("required tools check: some patterns still unsatisfied after all retries",
			"workspace_uuid", workspaceUUID,
			"unsatisfied", unsatisfied)
	}
}

// collectRequiredToolPatterns collects all unique enabledWhenMCP patterns from all prompt sources.
func (c *SessionWSClient) collectRequiredToolPatterns() []string {
	if c.server == nil {
		return nil
	}

	var allPatterns []string
	seen := make(map[string]bool)
	addPatterns := func(patterns []string) {
		for _, p := range patterns {
			if !seen[p] {
				seen[p] = true
				allPatterns = append(allPatterns, p)
			}
		}
	}

	// Global prompts from cache
	if c.server.config.PromptsCache != nil {
		if prompts, err := c.server.config.PromptsCache.Get(); err == nil {
			addPatterns(config.CollectRequiredToolPatterns(prompts))
		}
	}

	// Workspace prompts (if we have a background session with working dir)
	if c.bgSession != nil {
		workingDir := c.bgSession.GetWorkingDir()
		if workingDir != "" && c.server.sessionManager != nil {
			wsPrompts := c.server.sessionManager.GetWorkspacePrompts(workingDir)
			addPatterns(config.CollectRequiredToolPatternsFromWebPrompts(wsPrompts))
		}
	}

	return allPatterns
}

// getUnsatisfiedPatterns returns patterns that are not yet satisfied (false or missing).
func (c *SessionWSClient) getUnsatisfiedPatterns(satisfied map[string]bool) []string {
	var unsatisfied []string
	for pattern, available := range satisfied {
		if !available {
			unsatisfied = append(unsatisfied, pattern)
		}
	}
	return unsatisfied
}

// broadcastEnabledWhenMCPStatus broadcasts the required tools pattern status to all clients.
func (c *SessionWSClient) broadcastEnabledWhenMCPStatus(workspaceUUID string, patterns map[string]bool) {
	if c.server != nil && c.server.eventsManager != nil {
		c.server.eventsManager.Broadcast(WSMsgTypeEnabledWhenMCPStatus, map[string]interface{}{
			"workspace_uuid": workspaceUUID,
			"patterns":       patterns,
		})
	}
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

// tryAttachToSession attempts to attach to a running BackgroundSession.
// This is called when bgSession is nil but the session may have been resumed
// (e.g., after unarchiving). If successful, the client is added as an observer.
func (c *SessionWSClient) tryAttachToSession() {
	if c.server.sessionManager == nil {
		return
	}

	bs := c.server.sessionManager.GetSession(c.sessionID)
	if bs == nil {
		return
	}

	// Attach to the session
	c.bgSession = bs

	// Add as observer if initial load is done
	c.initialLoadMu.Lock()
	shouldAddObserver := c.initialLoadDone
	c.initialLoadMu.Unlock()

	if shouldAddObserver {
		bs.AddObserver(c)
		if c.logger != nil {
			c.logger.Debug("Attached to session after unarchive",
				"session_id", c.sessionID,
				"acp_id", bs.GetACPID(),
				"observer_count", bs.ObserverCount())
		}
	} else {
		if c.logger != nil {
			c.logger.Debug("Attached to session after unarchive (observer will be added after load)",
				"session_id", c.sessionID,
				"acp_id", bs.GetACPID())
		}
	}

	// Send a notification to the client that the session is now running
	c.sendMessage(WSMsgTypeACPStarted, map[string]interface{}{
		"session_id": c.sessionID,
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
		"max_seq":      c.getServerMaxSeq(),
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
		"max_seq":      c.getServerMaxSeq(),
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
		"max_seq":      c.getServerMaxSeq(),
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
		"max_seq":      c.getServerMaxSeq(),
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
		"max_seq":    c.getServerMaxSeq(),
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
		"max_seq":    c.getServerMaxSeq(),
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
		"max_seq":    c.getServerMaxSeq(),
		"path":       path,
		"size":       size,
		"session_id": c.sessionID,
	})
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
		"max_seq":     c.getServerMaxSeq(),
	})
}

// OnActionButtons is called when action buttons are extracted from the agent's response.
// An empty slice is a valid "clear" signal and must be forwarded to all clients.
func (c *SessionWSClient) OnActionButtons(buttons []ActionButton) {
	c.logger.Debug("action_buttons: OnActionButtons called", "button_count", len(buttons))
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
		"max_seq":      c.getServerMaxSeq(),
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

// OnConfigOptionChanged is called when a session config option changes.
// This is used to notify clients of mode changes and other config option updates.
func (c *SessionWSClient) OnConfigOptionChanged(configID, value string) {
	c.sendMessage(WSMsgTypeConfigOptionChanged, map[string]interface{}{
		"session_id": c.sessionID,
		"config_id":  configID,
		"value":      value,
	})
}

// OnACPStarted is called when the ACP connection for this session becomes ready.
// This notifies the WebSocket client that the session is now running and ready for prompts.
func (c *SessionWSClient) OnACPStarted() {
	if c.logger != nil {
		c.logger.Debug("ACP started notification sent to client",
			"session_id", c.sessionID,
			"client_id", c.clientID)
	}
	c.sendMessage(WSMsgTypeACPStarted, map[string]interface{}{
		"session_id": c.sessionID,
	})
}

// OnACPStopped is called when the ACP connection for this session is stopped.
// This notifies the WebSocket client that the session is no longer running,
// preventing further prompts and allowing the UI to update accordingly.
func (c *SessionWSClient) OnACPStopped(reason string) {
	c.bgSession = nil

	if c.logger != nil {
		c.logger.Debug("ACP stopped notification sent to client",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"reason", reason)
	}
	c.sendMessage(WSMsgTypeACPStopped, map[string]interface{}{
		"session_id": c.sessionID,
		"reason":     reason,
	})
}

// OnUIPrompt is called when an MCP tool requests user input via the UI.
// The client should display the prompt with the specified options.
func (c *SessionWSClient) OnUIPrompt(req UIPromptRequest) {
	if c.logger != nil {
		c.logger.Debug("UI prompt sent to client",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"request_id", req.RequestID,
			"prompt_type", req.Type,
			"question", req.Question,
			"option_count", len(req.Options))
	}
	c.sendMessage(WSMsgTypeUIPrompt, map[string]interface{}{
		"session_id":      c.sessionID,
		"request_id":      req.RequestID,
		"prompt_type":     req.Type,
		"question":        req.Question,
		"options":         req.Options,
		"timeout_seconds": req.TimeoutSeconds,
	})
}

// OnUIPromptDismiss is called when a UI prompt should be dismissed.
func (c *SessionWSClient) OnUIPromptDismiss(requestID string, reason string) {
	if c.logger != nil {
		c.logger.Debug("UI prompt dismiss sent to client",
			"session_id", c.sessionID,
			"client_id", c.clientID,
			"request_id", requestID,
			"reason", reason)
	}
	c.sendMessage(WSMsgTypeUIPromptDismiss, map[string]interface{}{
		"session_id": c.sessionID,
		"request_id": requestID,
		"reason":     reason,
	})
}
