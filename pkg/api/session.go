package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SessionCallbacks defines callbacks for session events.
// All callbacks are optional; nil callbacks are ignored.
type SessionCallbacks struct {
	// OnConnected is called when the WebSocket connection is established.
	OnConnected func(sessionID, clientID, acpServer string)

	// OnAgentMessage is called when agent message content is received (HTML).
	OnAgentMessage func(html string)

	// OnAgentThought is called when agent thinking content is received.
	OnAgentThought func(text string)

	// OnToolCall is called when a tool invocation starts.
	OnToolCall func(id, title, status string)

	// OnToolUpdate is called when a tool's status is updated.
	OnToolUpdate func(id, status string)

	// OnFileRead is called when the agent reads a file.
	OnFileRead func(path string, size int)

	// OnFileWrite is called when the agent writes a file.
	OnFileWrite func(path string, size int)

	// OnPermission is called when the agent requests permission.
	OnPermission func(requestID, title, description string)

	// OnPromptReceived is called when a prompt is acknowledged.
	OnPromptReceived func(promptID string)

	// OnPromptComplete is called when the agent finishes responding.
	OnPromptComplete func(eventCount int)

	// OnUserPrompt is called when another client sends a prompt.
	// This is used for multi-client scenarios where clients need to see prompts from others.
	OnUserPrompt func(senderID, promptID, message string)

	// OnSessionSync is called when a sync response is received.
	// events contains the missed events, eventCount is the total event count.
	OnSessionSync func(events []SyncEvent, eventCount int)

	// OnEventsLoaded is called when events are loaded from storage.
	// events contains the loaded events, hasMore indicates if there are more events,
	// isPrompting indicates if the agent is currently responding.
	OnEventsLoaded func(events []SyncEvent, hasMore bool, isPrompting bool)

	// OnEventsLoadedWithMeta is called when events are loaded, with additional metadata.
	// totalCount is the total number of events in the session (useful for detecting stale sync).
	OnEventsLoadedWithMeta func(events []SyncEvent, hasMore bool, isPrompting bool, totalCount int)

	// OnQueueUpdated is called when the message queue state changes.
	// action is one of: "added", "removed", "cleared"
	OnQueueUpdated func(queueLength int, action, messageID string)

	// OnQueueMessageSending is called when a queued message is about to be sent.
	OnQueueMessageSending func(messageID string)

	// OnQueueMessageSent is called after a queued message was delivered.
	OnQueueMessageSent func(messageID string)

	// OnQueueMessageTitled is called when a queued message receives an auto-generated title.
	OnQueueMessageTitled func(messageID, title string)

	// OnError is called when an error occurs.
	OnError func(message string)

	// OnACPStopped is called when the ACP connection for this session is stopped.
	// This happens when the session is archived or explicitly closed.
	// reason indicates why the session was stopped (e.g., "archived", "archived_timeout").
	OnACPStopped func(reason string)

	// OnACPStarted is called when the ACP connection for this session becomes ready.
	// This is fired after successful ACP initialization (including after restarts).
	OnACPStarted func()

	// OnConnectedFull is called with the full connected message data (including acp_ready).
	// This is separate from OnConnected for backward compatibility.
	OnConnectedFull func(data map[string]interface{})

	// OnSessionGone is called when the session has been deleted from the server.
	OnSessionGone func(sessionID string)

	// OnDisconnected is called when the WebSocket connection is closed.
	OnDisconnected func(err error)

	// OnClosed is called when the server sends a WebSocket close frame.
	// code is the WebSocket close code (e.g., 1000 normal, 1001 going away, 1011 internal error).
	// reason is the human-readable close reason.
	OnClosed func(code int, reason string)

	// OnRawMessage is called for every WebSocket message received (for debugging).
	// If set, this is called before any other callback.
	OnRawMessage func(msgType string, data []byte)

	// OnReconnecting is called when the supervisor is about to attempt a
	// reconnect after an unexpected disconnect. attempt is 0-based (the
	// first retry is attempt 0); delay is how long the supervisor will wait
	// before dialing. Only fires when WithReconnect is enabled.
	OnReconnecting func(attempt int, delay time.Duration)

	// OnReconnected is called after a reconnect attempt successfully
	// re-establishes the WebSocket connection (a fresh "connected" message
	// has not necessarily arrived yet). Only fires when WithReconnect is
	// enabled.
	OnReconnected func()

	// OnKeepaliveAck is called when a keepalive_ack response is received.
	// maxSeq is the server's current highest sequence number for the
	// session, isPrompting reports whether the agent is currently
	// responding. Only fires when WithKeepalive is enabled.
	OnKeepaliveAck func(maxSeq int64, isPrompting bool)
}

// SyncEvent represents an event returned from a sync request.
type SyncEvent struct {
	Seq       int64       `json:"seq"`
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data"`
	HTML      string      `json:"html,omitempty"` // For agent_message events
}

// Session represents an active WebSocket connection to a Mitto session.
// It is safe for concurrent use.
type Session struct {
	client    *Client
	sessionID string
	clientID  string
	callbacks SessionCallbacks

	ctx    context.Context
	cancel context.CancelFunc

	// mu guards conn, closed and clientID. conn is mutable when the
	// reconnect supervisor is enabled (WithReconnect): a redial swaps it
	// out from under any concurrent sendMessage call.
	mu     sync.Mutex
	conn   *websocket.Conn
	closed bool

	// writeMu serializes writes to conn, independent of mu, so a redial
	// (which holds mu only briefly to swap the pointer) never blocks on a
	// slow in-flight write and vice versa.
	writeMu sync.Mutex

	// SentMessages records all outgoing WebSocket messages (for test assertions).
	// Only populated when recording is enabled via EnableMessageRecording().
	SentMessages []json.RawMessage

	// recordMessages controls whether outgoing messages are recorded.
	recordMessages bool

	// resilience holds the opt-in reconnect/keepalive/seq-sync configuration
	// built from the SessionOption values passed to Connect. Its zero value
	// disables every feature below, so a Session created with no options
	// behaves exactly as before this feature was added.
	resilience resilienceConfig

	// dial reconnects the underlying WebSocket using the same URL/headers
	// as the initial Connect. Only used when resilience.reconnect.Enabled.
	dial func(ctx context.Context) (*websocket.Conn, error)

	// seqMu guards lastSeenSeq and seenSeqs, used for the watermark and
	// dedup features.
	seqMu       sync.Mutex
	lastSeenSeq int64
	seenSeqs    map[int64]struct{}
	seenOrder   []int64

	// keepaliveMissed counts consecutive un-acked keepalives, reset on any
	// keepalive_ack. Guarded by seqMu for simplicity (low contention).
	keepaliveMissed int

	// streamMu guards stream, the at-most-one-active channel/iterator
	// adapter registered via Events/EventsChan (see stream.go).
	streamMu sync.Mutex
	stream   *eventStream
}

// maxSeenSeqs bounds the client-side dedup sliding window so a long-lived
// session cannot grow it unboundedly.
const maxSeenSeqs = 4096

// Connect establishes a WebSocket connection to a session.
//
// The handshake is decorated with the Client's configured authentication
// (see authProvider): a shared bearer token is sent as an Authorization
// header on the upgrade request, and a cookie-login session is sent as a
// Cookie header sourced from the Client's cookie jar. In no case is a token
// or session credential placed in the ws(s):// URL or query string.
//
// By default, a dropped connection is reported via OnDisconnected/OnClosed
// and not retried, matching historical behavior. Pass SessionOption values
// (WithReconnect, WithKeepalive, WithSeqStore, WithSeqDedup) to opt into
// automatic reconnection with exponential backoff, sequence-number resync,
// zombie-connection detection via keepalives, and client-side
// deduplication. See docs/devel/go-client-library.md §6 and
// docs/devel/websockets/{sequence-numbers,synchronization}.md for the
// contract this mirrors from the browser client.
func (c *Client) Connect(ctx context.Context, sessionID string, callbacks SessionCallbacks, opts ...SessionOption) (*Session, error) {
	var rc resilienceConfig
	for _, opt := range opts {
		opt(&rc)
	}
	if rc.seqStore == nil {
		rc.seqStore = NewMemorySeqStore()
	}

	dial := func(dialCtx context.Context) (*websocket.Conn, error) {
		u, err := url.Parse(c.baseURL)
		if err != nil {
			return nil, fmt.Errorf("parse base URL: %w", err)
		}
		switch u.Scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		}
		u.Path = c.apiPrefix + "/api/sessions/" + url.PathEscape(sessionID) + "/ws"

		handshakeHeader := http.Header{}
		if err := c.currentAuth().applyWS(handshakeHeader); err != nil {
			return nil, fmt.Errorf("websocket connect: %w", err)
		}

		dialer := websocket.Dialer{
			HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
			Jar:              c.httpClient.Jar,
		}

		conn, _, err := dialer.DialContext(dialCtx, u.String(), handshakeHeader)
		if err != nil {
			return nil, fmt.Errorf("websocket connect: %w", err)
		}
		return conn, nil
	}

	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	s := &Session{
		client:     c,
		sessionID:  sessionID,
		conn:       conn,
		callbacks:  callbacks,
		ctx:        sessCtx,
		cancel:     cancel,
		resilience: rc,
		dial:       dial,
	}
	if rc.dedup {
		s.seenSeqs = make(map[int64]struct{})
	}
	if seq, err := rc.seqStore.Load(sessionID); err == nil {
		s.lastSeenSeq = seq
	}

	// Start reading messages
	go s.readLoop()

	if rc.keepalive.Enabled {
		go s.keepaliveLoop()
	}

	return s, nil
}

// SessionID returns the session ID.
func (s *Session) SessionID() string {
	return s.sessionID
}

// ClientID returns the client ID assigned by the server.
func (s *Session) ClientID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientID
}

// SendPrompt sends a message to the agent.
func (s *Session) SendPrompt(message string) error {
	return s.sendMessage("prompt", map[string]interface{}{
		"message": message,
	})
}

// SendPromptWithImages sends a message with image attachments.
func (s *Session) SendPromptWithImages(message string, imageIDs []string) error {
	return s.sendMessage("prompt", map[string]interface{}{
		"message":   message,
		"image_ids": imageIDs,
	})
}

// Cancel requests cancellation of the current operation.
func (s *Session) Cancel() error {
	return s.sendMessage("cancel", nil)
}

// AnswerPermission responds to a permission request.
func (s *Session) AnswerPermission(requestID string, approved bool) error {
	return s.sendMessage("permission_answer", map[string]interface{}{
		"request_id": requestID,
		"approved":   approved,
	})
}

// Rename renames the session.
func (s *Session) Rename(name string) error {
	return s.sendMessage("rename_session", map[string]interface{}{
		"name": name,
	})
}

// Sync requests missed events after a sequence number.
func (s *Session) Sync(afterSeq int) error {
	return s.sendMessage("sync_session", map[string]interface{}{
		"session_id": s.sessionID,
		"after_seq":  afterSeq,
	})
}

// LoadEvents requests events from storage.
// limit is the maximum number of events to load.
// afterSeq loads events after this sequence number (for sync).
// beforeSeq loads events before this sequence number (for pagination).
func (s *Session) LoadEvents(limit, afterSeq, beforeSeq int64) error {
	data := map[string]interface{}{
		"limit": limit,
	}
	if afterSeq > 0 {
		data["after_seq"] = afterSeq
	}
	if beforeSeq > 0 {
		data["before_seq"] = beforeSeq
	}
	return s.sendMessage("load_events", data)
}

// Keepalive sends a keepalive message.
func (s *Session) Keepalive(timestamp int64) error {
	return s.sendMessage("keepalive", map[string]interface{}{
		"timestamp": timestamp,
	})
}

// SendKeepalive sends an application-level keepalive ping to the server.
// clientSeq is the highest sequence number the client has seen (last_seen_seq).
// The server responds with a keepalive_ack containing its current max_seq.
func (s *Session) SendKeepalive(clientSeq int64) error {
	return s.sendMessage("keepalive", map[string]interface{}{
		"client_time":   time.Now().UnixMilli(),
		"last_seen_seq": clientSeq,
	})
}

// Close closes the WebSocket connection. This is always terminal: even
// when WithReconnect is enabled, a Close() call never triggers a reconnect.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.mu.Unlock()

	s.cancel()
	return conn.Close()
}

// EnableMessageRecording enables recording of all outgoing WebSocket messages.
// Call this before sending any messages you want to record.
// Recorded messages are accessible via SentMessages and GetSentMessages().
func (s *Session) EnableMessageRecording() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordMessages = true
}

// GetSentMessages returns a copy of all recorded outgoing messages.
func (s *Session) GetSentMessages() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.SentMessages) == 0 {
		return nil
	}
	cp := make([]json.RawMessage, len(s.SentMessages))
	copy(cp, s.SentMessages)
	return cp
}

// LastLoadEventsAfterSeq returns the after_seq value from the most recently
// recorded load_events message, or -1 if no load_events was recorded.
// This is useful for asserting that the client synced from the correct position
// after a reconnect.
func (s *Session) LastLoadEventsAfterSeq() int64 {
	msgs := s.GetSentMessages()

	type outgoingMsg struct {
		Type string `json:"type"`
		Data struct {
			AfterSeq int64 `json:"after_seq"`
		} `json:"data"`
	}

	for i := len(msgs) - 1; i >= 0; i-- {
		var m outgoingMsg
		if err := json.Unmarshal(msgs[i], &m); err != nil {
			continue
		}
		if m.Type == "load_events" {
			return m.Data.AfterSeq
		}
	}
	return -1
}

// sendMessage sends a WebSocket message.
func (s *Session) sendMessage(msgType string, data map[string]interface{}) error {
	msg := map[string]interface{}{
		"type": msgType,
	}
	if data != nil {
		msg["data"] = data
	}

	s.mu.Lock()
	closed := s.closed
	conn := s.conn
	if !closed && s.recordMessages {
		if msgBytes, err := json.Marshal(msg); err == nil {
			s.SentMessages = append(s.SentMessages, json.RawMessage(msgBytes))
		}
	}
	s.mu.Unlock()

	if closed {
		return fmt.Errorf("session closed")
	}

	// writeMu serializes writes against conn independently of mu, so a
	// concurrent redial (which only briefly holds mu to swap the pointer)
	// cannot interleave with an in-flight WriteJSON on the old conn.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteJSON(msg)
}

// wsMessage represents a WebSocket message from the server. Seq and MaxSeq
// are top-level envelope fields on every streaming message (see
// internal/web/session_ws.go and docs/devel/websockets/sequence-numbers.md);
// they are parsed here regardless of whether resilience features are
// enabled, but are only acted upon (watermark tracking, dedup) when
// WithReconnect/WithSeqDedup are set.
type wsMessage struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	Seq    int64           `json:"seq"`
	MaxSeq int64           `json:"max_seq"`
}

// isSessionGone reports whether msg is the terminal session_gone circuit
// breaker (docs/devel/websockets/synchronization.md "Circuit Breaker:
// Terminal Session Errors"). It must never be retried by the reconnect
// supervisor.
func isSessionGone(msg wsMessage) bool {
	return msg.Type == "session_gone"
}

// readLoop supervises the WebSocket connection: it reads messages until an
// error occurs, then either terminates (Close(), ctx cancellation,
// session_gone, or reconnection disabled/exhausted) or, when WithReconnect
// is enabled, backs off and redials before resuming reads. This mirrors the
// browser client's ws.onclose reconnection flow
// (docs/devel/websockets/synchronization.md).
func (s *Session) readLoop() {
	attempt := 0
	for {
		gone := s.readUntilError()

		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()

		// Terminal: explicit Close(), ctx cancellation, session_gone, or
		// reconnect not enabled.
		if closed || gone || s.ctx.Err() != nil || !s.resilience.reconnect.Enabled {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			return
		}

		delay := reconnectDelay(attempt, s.resilience.reconnect)
		if s.callbacks.OnReconnecting != nil {
			s.callbacks.OnReconnecting(attempt, delay)
		}

		select {
		case <-s.ctx.Done():
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			return
		case <-time.After(delay):
		}

		conn, err := s.dial(s.ctx)
		if err != nil {
			attempt++
			continue
		}

		// Close() may have run while the dial was in flight. Discard the
		// fresh connection rather than installing one nobody will close.
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conn = conn
		s.mu.Unlock()
		attempt = 0

		if s.callbacks.OnReconnected != nil {
			s.callbacks.OnReconnected()
		}

		// Resync from the watermark, mirroring ws.onopen in
		// docs/devel/websockets/synchronization.md. A zero watermark means
		// no prior events were seen; the caller is responsible for the
		// initial load in that case, as before this feature existed.
		if watermark := s.Watermark(); watermark > 0 {
			_ = s.LoadEvents(0, watermark, 0)
		}
	}
}

// readUntilError reads messages from the current connection until an error
// occurs (returning false) or a terminal session_gone message is received
// (returning true). OnDisconnected/OnClosed fire on every drop, exactly as
// before reconnection support was added.
func (s *Session) readUntilError() (gone bool) {
	for {
		select {
		case <-s.ctx.Done():
			s.terminateActiveStream(ErrDisconnected)
			return false
		default:
		}

		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()

		var msg wsMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				if s.callbacks.OnClosed != nil {
					s.callbacks.OnClosed(closeErr.Code, closeErr.Text)
				}
			}
			if s.callbacks.OnDisconnected != nil {
				s.callbacks.OnDisconnected(err)
			}
			// Any disconnect terminates an active stream with a non-nil
			// error, regardless of whether WithReconnect will redial
			// afterward (docs/devel/go-client-library.md §6): a streaming
			// consumer must not block forever waiting on a dead connection.
			s.terminateActiveStream(fmt.Errorf("%w: %v", ErrDisconnected, err))
			return false
		}

		if isSessionGone(msg) {
			s.handleMessage(msg)
			s.terminateActiveStream(ErrDisconnected)
			return true
		}

		s.handleMessage(msg)
	}
}

// handleMessage processes a received WebSocket message.
func (s *Session) handleMessage(msg wsMessage) {
	// Call debug callback if set
	if s.callbacks.OnRawMessage != nil {
		s.callbacks.OnRawMessage(msg.Type, msg.Data)
	}

	// Track the reconnection watermark and, if enabled, drop duplicates.
	// msg.Seq is 0 for envelope types that don't carry a sequence number
	// (connected, error, session_gone, acp_started, queue_*), which always
	// pass through. See docs/devel/websockets/sequence-numbers.md for the
	// coalescing rule (same seq as the last delivered message is always
	// allowed through, for streaming continuation).
	if msg.Seq > 0 {
		if !s.observeSeq(msg.Seq) {
			return
		}
	}

	switch msg.Type {
	case "connected":
		var data struct {
			SessionID string `json:"session_id"`
			ClientID  string `json:"client_id"`
			ACPServer string `json:"acp_server"`
		}
		if json.Unmarshal(msg.Data, &data) == nil {
			s.mu.Lock()
			s.clientID = data.ClientID
			s.mu.Unlock()
			if s.callbacks.OnConnected != nil {
				s.callbacks.OnConnected(data.SessionID, data.ClientID, data.ACPServer)
			}
		}
		if s.callbacks.OnConnectedFull != nil {
			var rawData map[string]interface{}
			if json.Unmarshal(msg.Data, &rawData) == nil {
				s.callbacks.OnConnectedFull(rawData)
			}
		}

	case "agent_message":
		var data struct {
			HTML string `json:"html"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnAgentMessage != nil {
			s.callbacks.OnAgentMessage(data.HTML)
		}

	case "agent_thought":
		var data struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnAgentThought != nil {
			s.callbacks.OnAgentThought(data.Text)
		}

	case "tool_call":
		var data struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnToolCall != nil {
			s.callbacks.OnToolCall(data.ID, data.Title, data.Status)
		}

	case "tool_update":
		var data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnToolUpdate != nil {
			s.callbacks.OnToolUpdate(data.ID, data.Status)
		}

	case "file_read":
		var data struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnFileRead != nil {
			s.callbacks.OnFileRead(data.Path, data.Size)
		}

	case "file_write":
		var data struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnFileWrite != nil {
			s.callbacks.OnFileWrite(data.Path, data.Size)
		}

	case "permission":
		var data struct {
			RequestID   string `json:"request_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnPermission != nil {
			s.callbacks.OnPermission(data.RequestID, data.Title, data.Description)
		}

	case "prompt_received":
		var data struct {
			PromptID string `json:"prompt_id"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnPromptReceived != nil {
			s.callbacks.OnPromptReceived(data.PromptID)
		}

	case "prompt_complete":
		var data struct {
			EventCount int `json:"event_count"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnPromptComplete != nil {
			s.callbacks.OnPromptComplete(data.EventCount)
		}

	case "user_prompt":
		var data struct {
			SenderID string `json:"sender_id"`
			PromptID string `json:"prompt_id"`
			Message  string `json:"message"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnUserPrompt != nil {
			s.callbacks.OnUserPrompt(data.SenderID, data.PromptID, data.Message)
		}

	case "session_sync":
		var data struct {
			Events     []SyncEvent `json:"events"`
			EventCount int         `json:"event_count"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnSessionSync != nil {
			s.callbacks.OnSessionSync(data.Events, data.EventCount)
		}

	case "events_loaded":
		var data struct {
			Events      []SyncEvent `json:"events"`
			HasMore     bool        `json:"has_more"`
			IsPrompting bool        `json:"is_prompting"`
			TotalCount  int         `json:"total_count"`
		}
		if json.Unmarshal(msg.Data, &data) == nil {
			// Server authority: if our watermark exceeds what the server
			// reports it has, our state is stale. Reset the dedup window
			// and watermark before delivering, so the fresh events below
			// are never rejected as duplicates
			// (docs/devel/websockets/sequence-numbers.md "Stale Client
			// Reset"). Never attempt to correct the server.
			if s.resilience.reconnect.Enabled || s.resilience.dedup {
				if s.Watermark() > int64(data.TotalCount) {
					s.resetWatermark()
				}
			}
			for _, ev := range data.Events {
				if ev.Seq > 0 {
					s.observeSeq(ev.Seq)
				}
			}
			if s.callbacks.OnEventsLoaded != nil {
				s.callbacks.OnEventsLoaded(data.Events, data.HasMore, data.IsPrompting)
			}
			if s.callbacks.OnEventsLoadedWithMeta != nil {
				s.callbacks.OnEventsLoadedWithMeta(data.Events, data.HasMore, data.IsPrompting, data.TotalCount)
			}
		}

	case "queue_updated":
		var data struct {
			QueueLength int    `json:"queue_length"`
			Action      string `json:"action"`
			MessageID   string `json:"message_id"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnQueueUpdated != nil {
			s.callbacks.OnQueueUpdated(data.QueueLength, data.Action, data.MessageID)
		}

	case "queue_message_sending":
		var data struct {
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnQueueMessageSending != nil {
			s.callbacks.OnQueueMessageSending(data.MessageID)
		}

	case "queue_message_sent":
		var data struct {
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnQueueMessageSent != nil {
			s.callbacks.OnQueueMessageSent(data.MessageID)
		}

	case "queue_message_titled":
		var data struct {
			MessageID string `json:"message_id"`
			Title     string `json:"title"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnQueueMessageTitled != nil {
			s.callbacks.OnQueueMessageTitled(data.MessageID, data.Title)
		}

	case "error":
		var data struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnError != nil {
			s.callbacks.OnError(data.Message)
		}

	case "acp_stopped":
		var data struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnACPStopped != nil {
			s.callbacks.OnACPStopped(data.Reason)
		}

	case "acp_started":
		if s.callbacks.OnACPStarted != nil {
			s.callbacks.OnACPStarted()
		}

	case "session_gone":
		var data struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnSessionGone != nil {
			s.callbacks.OnSessionGone(data.SessionID)
		}

	case "keepalive_ack":
		var data struct {
			MaxSeq      int64 `json:"max_seq"`
			IsPrompting bool  `json:"is_prompting"`
		}
		if json.Unmarshal(msg.Data, &data) == nil {
			s.seqMu.Lock()
			s.keepaliveMissed = 0
			s.seqMu.Unlock()
			if s.callbacks.OnKeepaliveAck != nil {
				s.callbacks.OnKeepaliveAck(data.MaxSeq, data.IsPrompting)
			}
			// Immediate gap detection via max_seq piggybacking
			// (docs/devel/websockets/synchronization.md): if the server is
			// ahead of our watermark, request the missing events.
			if s.resilience.reconnect.Enabled || s.resilience.dedup {
				if watermark := s.Watermark(); data.MaxSeq > watermark {
					_ = s.LoadEvents(0, watermark, 0)
				}
			}
		}
	}

	// Feed the channel/iterator streaming adapter (stream.go), if a stream
	// is currently active. This runs after every callback dispatch above
	// and after the dedup gate at the top of this function, so the stream
	// inherits watermark/dedup semantics for free and never affects
	// SessionCallbacks delivery.
	s.emitStream(msg)
}

// observeSeq records seq as seen and advances the watermark. It returns
// false when seq is a duplicate that must be dropped (dedup enabled, seq
// already observed, and seq differs from the last-seen seq — same-seq is
// always allowed through for streaming coalescing per
// docs/devel/websockets/sequence-numbers.md). When dedup is disabled, it
// still advances the watermark (needed for resync after reconnect) and
// always returns true.
func (s *Session) observeSeq(seq int64) bool {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()

	isDuplicate := false
	if s.resilience.dedup {
		if _, seen := s.seenSeqs[seq]; seen && seq != s.lastSeenSeq {
			isDuplicate = true
		}
	}

	if seq > s.lastSeenSeq {
		s.lastSeenSeq = seq
		if s.resilience.seqStore != nil {
			_ = s.resilience.seqStore.Store(s.sessionID, seq)
		}
	}

	if s.resilience.dedup && !isDuplicate {
		if _, seen := s.seenSeqs[seq]; !seen {
			s.seenSeqs[seq] = struct{}{}
			s.seenOrder = append(s.seenOrder, seq)
			if len(s.seenOrder) > maxSeenSeqs {
				oldest := s.seenOrder[0]
				s.seenOrder = s.seenOrder[1:]
				delete(s.seenSeqs, oldest)
			}
		}
	}

	return !isDuplicate
}

// Watermark returns the highest sequence number seen so far on this
// Session, used to resume a reconnect via load_events{after_seq}.
func (s *Session) Watermark() int64 {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	return s.lastSeenSeq
}

// resetWatermark clears the watermark, the dedup window and the SeqStore
// entry for this session. Called when the server reports our state is
// stale (docs/devel/websockets/sequence-numbers.md "Stale Client Reset").
func (s *Session) resetWatermark() {
	s.seqMu.Lock()
	s.lastSeenSeq = 0
	if s.resilience.dedup {
		s.seenSeqs = make(map[int64]struct{})
		s.seenOrder = nil
	}
	s.seqMu.Unlock()
	if s.resilience.seqStore != nil {
		_ = s.resilience.seqStore.Store(s.sessionID, 0)
	}
}

// keepaliveLoop periodically sends application-level keepalives and forces
// a reconnect (by closing the connection, which readLoop's error path
// picks up) after MaxMissed consecutive un-acked sends. Only started when
// WithKeepalive is enabled. Mirrors the zombie-connection detection in
// docs/devel/websockets/synchronization.md.
func (s *Session) keepaliveLoop() {
	cfg := s.resilience.keepalive.withDefaults()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			closed := s.closed
			conn := s.conn
			s.mu.Unlock()
			if closed {
				return
			}

			s.seqMu.Lock()
			s.keepaliveMissed++
			missed := s.keepaliveMissed
			s.seqMu.Unlock()

			if missed >= cfg.MaxMissed {
				// Zombie connection: force-close so readLoop's error path
				// drives the normal reconnect flow.
				_ = conn.Close()
				s.seqMu.Lock()
				s.keepaliveMissed = 0
				s.seqMu.Unlock()
				continue
			}

			_ = s.SendKeepalive(s.Watermark())
		}
	}
}
