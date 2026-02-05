// Package web provides WebSocket message types and constants for the Mitto web interface.
//
// # WebSocket Protocol Overview
//
// The web interface uses two WebSocket endpoints:
//   - /api/events: Global events (session lifecycle notifications)
//   - /api/sessions/{id}/ws: Per-session communication (prompts, responses, tools)
//
// All messages are JSON-encoded with the following structure:
//
//	{
//	    "type": "message_type",
//	    "data": { ... }  // Optional, type-specific payload
//	}
package web

import (
	"encoding/json"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// WSMessage represents a WebSocket message between frontend and backend.
// All WebSocket communication uses this envelope format.
type WSMessage struct {
	Type string          `json:"type"`           // Message type (see WSMsgType* constants)
	Data json.RawMessage `json:"data,omitempty"` // Type-specific payload
}

// ParseMessage parses raw message bytes into a WSMessage.
func ParseMessage(data []byte) (WSMessage, error) {
	var msg WSMessage
	err := json.Unmarshal(data, &msg)
	return msg, err
}

// =============================================================================
// Frontend → Backend Message Types
// =============================================================================
// These messages are sent from the browser to the server.

const (
	// WSMsgTypePrompt sends a user message to the AI agent.
	// Data: { "message": string, "image_ids": []string (optional) }
	WSMsgTypePrompt = "prompt"

	// WSMsgTypeCancel requests cancellation of the current agent operation.
	// Data: none
	WSMsgTypeCancel = "cancel"

	// WSMsgTypeForceReset forcefully resets a stuck session.
	// Used when the agent is unresponsive and Cancel doesn't work.
	// This resets the isPrompting flag so new prompts can be sent.
	// Data: none
	WSMsgTypeForceReset = "force_reset"

	// WSMsgTypePermissionAnswer responds to a permission request from the agent.
	// Data: { "request_id": string, "approved": bool }
	WSMsgTypePermissionAnswer = "permission_answer"

	// WSMsgTypeRenameSession requests renaming the current session.
	// Data: { "name": string }
	WSMsgTypeRenameSession = "rename_session"

	// WSMsgTypeSyncSession requests incremental sync of events missed while disconnected.
	// Used by mobile clients that may have been suspended.
	// Data: { "session_id": string, "after_seq": int }
	WSMsgTypeSyncSession = "sync_session"

	// WSMsgTypeKeepalive is an application-level keepalive with timestamp.
	// Used to detect stale connections and measure latency.
	// Data: { "timestamp": int64 (Unix ms) }
	WSMsgTypeKeepalive = "keepalive"
)

// =============================================================================
// Backend → Frontend Message Types
// =============================================================================
// These messages are sent from the server to the browser.

const (
	// WSMsgTypeConnected confirms WebSocket connection is established.
	// Sent immediately after connection on both /api/events and /api/sessions/{id}/ws.
	// Data: { "session_id": string, "client_id": string, "acp_server": string, ... }
	WSMsgTypeConnected = "connected"

	// WSMsgTypeSessionCreated notifies that a new session was created.
	// Sent on /api/events to all connected clients.
	// Data: { "session_id": string, "name": string, "working_dir": string }
	WSMsgTypeSessionCreated = "session_created"

	// WSMsgTypeSessionSwitched confirms session switch completed.
	// Data: { "session_id": string }
	WSMsgTypeSessionSwitched = "session_switched"

	// WSMsgTypeSessionRenamed notifies that a session was renamed.
	// Sent on both /api/events (broadcast) and session WebSocket.
	// Data: { "session_id": string, "name": string }
	WSMsgTypeSessionRenamed = "session_renamed"

	// WSMsgTypeSessionDeleted notifies that a session was deleted.
	// Sent on /api/events to all connected clients.
	// Data: { "session_id": string }
	WSMsgTypeSessionDeleted = "session_deleted"

	// WSMsgTypeAgentMessage contains HTML-rendered agent response content.
	// Sent incrementally as the agent generates output.
	// Data: { "html": string }
	WSMsgTypeAgentMessage = "agent_message"

	// WSMsgTypeAgentThought contains plain text agent thinking/reasoning.
	// Data: { "text": string }
	WSMsgTypeAgentThought = "agent_thought"

	// WSMsgTypeToolCall notifies that the agent is invoking a tool.
	// Data: { "id": string, "title": string, "status": string }
	WSMsgTypeToolCall = "tool_call"

	// WSMsgTypeToolUpdate updates the status of an in-progress tool call.
	// Data: { "id": string, "status": string (optional) }
	WSMsgTypeToolUpdate = "tool_update"

	// WSMsgTypePlan notifies that the agent has created/updated a plan.
	// Data: { "plan": object }
	WSMsgTypePlan = "plan"

	// WSMsgTypePermission requests user permission for a sensitive operation.
	// The frontend should display a dialog and respond with permission_answer.
	// Data: { "request_id": string, "title": string, "description": string, ... }
	WSMsgTypePermission = "permission"

	// WSMsgTypeError reports an error to the client.
	// Data: { "message": string, "code": string (optional) }
	WSMsgTypeError = "error"

	// WSMsgTypeSessionLoaded confirms a session was loaded successfully.
	// Data: { "session_id": string, "events": []object }
	WSMsgTypeSessionLoaded = "session_loaded"

	// WSMsgTypePromptReceived acknowledges that a prompt was received and persisted.
	// Sent immediately after receiving a prompt, before agent processing begins.
	// Data: { "prompt_id": string }
	WSMsgTypePromptReceived = "prompt_received"

	// WSMsgTypeUserPrompt broadcasts a user's prompt to all session observers.
	// Used for multi-client scenarios where other clients need to see the prompt.
	// Data: { "sender_id": string, "prompt_id": string, "message": string, "image_ids": []string }
	WSMsgTypeUserPrompt = "user_prompt"

	// WSMsgTypePromptComplete signals that the agent has finished responding.
	// Data: { "event_count": int }
	WSMsgTypePromptComplete = "prompt_complete"

	// WSMsgTypeFileWrite notifies that the agent wrote a file.
	// Data: { "path": string, "size": int }
	WSMsgTypeFileWrite = "file_write"

	// WSMsgTypeFileRead notifies that the agent read a file.
	// Data: { "path": string, "size": int }
	WSMsgTypeFileRead = "file_read"

	// WSMsgTypeSessionSync responds to a sync_session request with missed events.
	// Data: { "session_id": string, "events": []object }
	WSMsgTypeSessionSync = "session_sync"

	// WSMsgTypeKeepaliveAck responds to a keepalive with server timestamp.
	// Data: { "client_timestamp": int64, "server_timestamp": int64 }
	WSMsgTypeKeepaliveAck = "keepalive_ack"

	// WSMsgTypeQueueUpdated notifies that the message queue state changed.
	// Sent when messages are added, removed, or the queue is cleared.
	// Data: { "queue_length": int, "action": string, "message_id": string }
	// action is one of: "added", "removed", "cleared"
	WSMsgTypeQueueUpdated = "queue_updated"

	// WSMsgTypeQueueMessageSending notifies that a queued message is being sent to the agent.
	// Sent just before the message is delivered.
	// Data: { "message_id": string }
	WSMsgTypeQueueMessageSending = "queue_message_sending"

	// WSMsgTypeQueueMessageSent notifies that a queued message was delivered to the agent.
	// Sent after the message has been removed from the queue and sent.
	// Data: { "message_id": string }
	WSMsgTypeQueueMessageSent = "queue_message_sent"

	// WSMsgTypeQueueMessageTitled notifies that a queued message received an auto-generated title.
	// Sent after the auxiliary conversation generates a title for the message.
	// Data: { "session_id": string, "message_id": string, "title": string }
	WSMsgTypeQueueMessageTitled = "queue_message_titled"

	// WSMsgTypeQueueReordered notifies that the queue order has changed.
	// Sent when a message is moved up or down in the queue.
	// Data: { "session_id": string, "messages": []QueuedMessage }
	WSMsgTypeQueueReordered = "queue_reordered"

	// WSMsgTypeActionButtons sends suggested follow-up action buttons to the client.
	// Sent asynchronously after prompt_complete when follow-up suggestions are enabled.
	// The buttons are generated by the auxiliary conversation analyzing the agent's response.
	// Data: { "session_id": string, "buttons": []{ "label": string, "response": string } }
	WSMsgTypeActionButtons = "action_buttons"

	// WSMsgTypeSessionReset notifies that a session was forcefully reset.
	// Sent after a force_reset message is processed.
	// Data: { "session_id": string }
	WSMsgTypeSessionReset = "session_reset"
)

// =============================================================================
// Unified Event Buffer for Streaming Events
// =============================================================================

// BufferedEventType represents the type of buffered event.
type BufferedEventType string

const (
	BufferedEventAgentMessage   BufferedEventType = "agent_message"
	BufferedEventAgentThought   BufferedEventType = "agent_thought"
	BufferedEventToolCall       BufferedEventType = "tool_call"
	BufferedEventToolCallUpdate BufferedEventType = "tool_call_update"
	BufferedEventPlan           BufferedEventType = "plan"
	BufferedEventFileRead       BufferedEventType = "file_read"
	BufferedEventFileWrite      BufferedEventType = "file_write"
)

// BufferedEvent represents a single event in the streaming buffer.
// Events are stored in the order they arrive and persisted together when the prompt completes.
// Each event has a sequence number (Seq) assigned when it's created, which is used for
// ordering and deduplication across WebSocket reconnections.
type BufferedEvent struct {
	Type BufferedEventType
	Seq  int64 // Sequence number for ordering and deduplication
	Data interface{}
}

// EventPersister defines the interface for persisting buffered events.
// This is implemented by session.Recorder to allow decoupled persistence.
type EventPersister interface {
	RecordAgentMessage(html string) error
	RecordAgentThought(text string) error
	RecordToolCall(toolCallID, title, status, kind string, rawInput, rawOutput any) error
	RecordToolCallUpdate(toolCallID string, status, title *string) error
	RecordPlan(entries []session.PlanEntry) error
	RecordFileRead(path string, size int) error
	RecordFileWrite(path string, size int) error
}

// AgentMessageData holds data for an agent message event.
type AgentMessageData struct {
	HTML string
}

// AgentThoughtData holds data for an agent thought event.
type AgentThoughtData struct {
	Text string
}

// ToolCallData holds data for a tool call event.
type ToolCallData struct {
	ID     string
	Title  string
	Status string
}

// ToolCallUpdateData holds data for a tool call update event.
type ToolCallUpdateData struct {
	ID     string
	Status *string
}

// PlanData holds data for a plan event.
type PlanData struct {
	// Plan data is not persisted in detail, just the event occurrence
}

// FileOperationData holds data for file read/write events.
type FileOperationData struct {
	Path string
	Size int
}

// EventBuffer accumulates streaming events in order for later persistence.
// Events are buffered during a prompt and persisted together when the prompt completes.
// This ensures events are persisted in the correct streaming order, not the order
// they would be persisted if done immediately (which would put tool calls before
// agent messages due to buffering of agent message chunks).
type EventBuffer struct {
	events []BufferedEvent
}

// NewEventBuffer creates a new empty event buffer.
func NewEventBuffer() *EventBuffer {
	return &EventBuffer{
		events: make([]BufferedEvent, 0, 16),
	}
}

// Append adds an event to the buffer.
func (b *EventBuffer) Append(event BufferedEvent) {
	b.events = append(b.events, event)
}

// LastSeq returns the sequence number of the last event in the buffer, or 0 if empty.
func (b *EventBuffer) LastSeq() int64 {
	if len(b.events) == 0 {
		return 0
	}
	return b.events[len(b.events)-1].Seq
}

// AppendAgentMessage appends an agent message chunk to the buffer.
// If the last event is also an agent message, the text is concatenated and returns (lastSeq, false).
// If this creates a new event, it uses the provided seq and returns (seq, true).
func (b *EventBuffer) AppendAgentMessage(seq int64, html string) (int64, bool) {
	if len(b.events) > 0 {
		last := &b.events[len(b.events)-1]
		if last.Type == BufferedEventAgentMessage {
			if data, ok := last.Data.(*AgentMessageData); ok {
				data.HTML += html
				return last.Seq, false // Appended to existing event
			}
		}
	}
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventAgentMessage,
		Seq:  seq,
		Data: &AgentMessageData{HTML: html},
	})
	return seq, true // Created new event
}

// AppendAgentThought appends an agent thought chunk to the buffer.
// If the last event is also an agent thought, the text is concatenated and returns (lastSeq, false).
// If this creates a new event, it uses the provided seq and returns (seq, true).
func (b *EventBuffer) AppendAgentThought(seq int64, text string) (int64, bool) {
	if len(b.events) > 0 {
		last := &b.events[len(b.events)-1]
		if last.Type == BufferedEventAgentThought {
			if data, ok := last.Data.(*AgentThoughtData); ok {
				data.Text += text
				return last.Seq, false // Appended to existing event
			}
		}
	}
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventAgentThought,
		Seq:  seq,
		Data: &AgentThoughtData{Text: text},
	})
	return seq, true // Created new event
}

// AppendToolCall appends a tool call event to the buffer.
// Always creates a new event with the provided seq.
func (b *EventBuffer) AppendToolCall(seq int64, id, title, status string) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventToolCall,
		Seq:  seq,
		Data: &ToolCallData{ID: id, Title: title, Status: status},
	})
}

// AppendToolCallUpdate appends a tool call update event to the buffer.
// Always creates a new event with the provided seq.
func (b *EventBuffer) AppendToolCallUpdate(seq int64, id string, status *string) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventToolCallUpdate,
		Seq:  seq,
		Data: &ToolCallUpdateData{ID: id, Status: status},
	})
}

// AppendPlan appends a plan event to the buffer.
// Always creates a new event with the provided seq.
func (b *EventBuffer) AppendPlan(seq int64) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventPlan,
		Seq:  seq,
		Data: &PlanData{},
	})
}

// AppendFileRead appends a file read event to the buffer.
// Always creates a new event with the provided seq.
func (b *EventBuffer) AppendFileRead(seq int64, path string, size int) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventFileRead,
		Seq:  seq,
		Data: &FileOperationData{Path: path, Size: size},
	})
}

// AppendFileWrite appends a file write event to the buffer.
// Always creates a new event with the provided seq.
func (b *EventBuffer) AppendFileWrite(seq int64, path string, size int) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventFileWrite,
		Seq:  seq,
		Data: &FileOperationData{Path: path, Size: size},
	})
}

// Events returns a copy of all buffered events.
func (b *EventBuffer) Events() []BufferedEvent {
	result := make([]BufferedEvent, len(b.events))
	copy(result, b.events)
	return result
}

// Flush returns all buffered events and clears the buffer.
func (b *EventBuffer) Flush() []BufferedEvent {
	events := b.events
	b.events = make([]BufferedEvent, 0, 16)
	return events
}

// Len returns the number of buffered events.
func (b *EventBuffer) Len() int {
	return len(b.events)
}

// IsEmpty returns true if the buffer has no events.
func (b *EventBuffer) IsEmpty() bool {
	return len(b.events) == 0
}

// GetAgentMessage returns the accumulated agent message HTML from all buffered events.
// This is used to send buffered content to newly connected observers.
func (b *EventBuffer) GetAgentMessage() string {
	var result strings.Builder
	for _, e := range b.events {
		if e.Type == BufferedEventAgentMessage {
			if data, ok := e.Data.(*AgentMessageData); ok {
				result.WriteString(data.HTML)
			}
		}
	}
	return result.String()
}

// GetAgentThought returns the accumulated agent thought text from all buffered events.
// This is used to send buffered content to newly connected observers.
func (b *EventBuffer) GetAgentThought() string {
	var result strings.Builder
	for _, e := range b.events {
		if e.Type == BufferedEventAgentThought {
			if data, ok := e.Data.(*AgentThoughtData); ok {
				result.WriteString(data.Text)
			}
		}
	}
	return result.String()
}

// ReplayTo sends this buffered event to a SessionObserver.
// This is used to catch up newly connected observers on in-progress streaming.
// The event's Seq is passed to the observer for ordering and deduplication.
func (e BufferedEvent) ReplayTo(observer SessionObserver) {
	switch e.Type {
	case BufferedEventAgentThought:
		if data, ok := e.Data.(*AgentThoughtData); ok && data.Text != "" {
			observer.OnAgentThought(e.Seq, data.Text)
		}
	case BufferedEventAgentMessage:
		if data, ok := e.Data.(*AgentMessageData); ok && data.HTML != "" {
			observer.OnAgentMessage(e.Seq, data.HTML)
		}
	case BufferedEventToolCall:
		if data, ok := e.Data.(*ToolCallData); ok {
			observer.OnToolCall(e.Seq, data.ID, data.Title, data.Status)
		}
	case BufferedEventToolCallUpdate:
		if data, ok := e.Data.(*ToolCallUpdateData); ok {
			observer.OnToolUpdate(e.Seq, data.ID, data.Status)
		}
	case BufferedEventPlan:
		observer.OnPlan(e.Seq)
	case BufferedEventFileRead:
		if data, ok := e.Data.(*FileOperationData); ok {
			observer.OnFileRead(e.Seq, data.Path, data.Size)
		}
	case BufferedEventFileWrite:
		if data, ok := e.Data.(*FileOperationData); ok {
			observer.OnFileWrite(e.Seq, data.Path, data.Size)
		}
	}
}

// PersistTo persists this buffered event to an EventPersister (e.g., session.Recorder).
// Returns an error if persistence fails.
func (e BufferedEvent) PersistTo(persister EventPersister) error {
	switch e.Type {
	case BufferedEventAgentThought:
		if data, ok := e.Data.(*AgentThoughtData); ok && data.Text != "" {
			return persister.RecordAgentThought(data.Text)
		}
	case BufferedEventAgentMessage:
		if data, ok := e.Data.(*AgentMessageData); ok && data.HTML != "" {
			return persister.RecordAgentMessage(data.HTML)
		}
	case BufferedEventToolCall:
		if data, ok := e.Data.(*ToolCallData); ok {
			return persister.RecordToolCall(data.ID, data.Title, data.Status, "", nil, nil)
		}
	case BufferedEventToolCallUpdate:
		if data, ok := e.Data.(*ToolCallUpdateData); ok {
			return persister.RecordToolCallUpdate(data.ID, data.Status, nil)
		}
	case BufferedEventPlan:
		return persister.RecordPlan(nil)
	case BufferedEventFileRead:
		if data, ok := e.Data.(*FileOperationData); ok {
			return persister.RecordFileRead(data.Path, data.Size)
		}
	case BufferedEventFileWrite:
		if data, ok := e.Data.(*FileOperationData); ok {
			return persister.RecordFileWrite(data.Path, data.Size)
		}
	}
	return nil
}
