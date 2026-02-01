package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

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

	// OnError is called when an error occurs.
	OnError func(message string)

	// OnDisconnected is called when the WebSocket connection is closed.
	OnDisconnected func(err error)
}

// SyncEvent represents an event returned from a sync request.
type SyncEvent struct {
	Seq       int64       `json:"seq"`
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// Session represents an active WebSocket connection to a Mitto session.
// It is safe for concurrent use.
type Session struct {
	client    *Client
	sessionID string
	clientID  string
	conn      *websocket.Conn
	callbacks SessionCallbacks

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// Connect establishes a WebSocket connection to a session.
func (c *Client) Connect(ctx context.Context, sessionID string, callbacks SessionCallbacks) (*Session, error) {
	// Build WebSocket URL
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	// Convert http(s) to ws(s)
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = c.apiPrefix + "/api/sessions/" + url.PathEscape(sessionID) + "/ws"

	// Connect
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("websocket connect: %w", err)
	}

	sessCtx, cancel := context.WithCancel(ctx)
	s := &Session{
		client:    c,
		sessionID: sessionID,
		conn:      conn,
		callbacks: callbacks,
		ctx:       sessCtx,
		cancel:    cancel,
	}

	// Start reading messages
	go s.readLoop()

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

// Keepalive sends a keepalive message.
func (s *Session) Keepalive(timestamp int64) error {
	return s.sendMessage("keepalive", map[string]interface{}{
		"timestamp": timestamp,
	})
}

// Close closes the WebSocket connection.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	return s.conn.Close()
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
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}
	return s.conn.WriteJSON(msg)
}

// wsMessage represents a WebSocket message from the server.
type wsMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// readLoop reads messages from the WebSocket connection.
func (s *Session) readLoop() {
	defer func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		var msg wsMessage
		err := s.conn.ReadJSON(&msg)
		if err != nil {
			if s.callbacks.OnDisconnected != nil {
				s.callbacks.OnDisconnected(err)
			}
			return
		}

		s.handleMessage(msg)
	}
}

// handleMessage processes a received WebSocket message.
func (s *Session) handleMessage(msg wsMessage) {
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

	case "error":
		var data struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg.Data, &data) == nil && s.callbacks.OnError != nil {
			s.callbacks.OnError(data.Message)
		}
	}
}
