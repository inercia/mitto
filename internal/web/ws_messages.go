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
type BufferedEvent struct {
	Type BufferedEventType
	Data interface{}
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

// AppendAgentMessage appends an agent message chunk to the buffer.
// If the last event is also an agent message, the text is concatenated.
func (b *EventBuffer) AppendAgentMessage(html string) {
	if len(b.events) > 0 {
		last := &b.events[len(b.events)-1]
		if last.Type == BufferedEventAgentMessage {
			if data, ok := last.Data.(*AgentMessageData); ok {
				data.HTML += html
				return
			}
		}
	}
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventAgentMessage,
		Data: &AgentMessageData{HTML: html},
	})
}

// AppendAgentThought appends an agent thought chunk to the buffer.
// If the last event is also an agent thought, the text is concatenated.
func (b *EventBuffer) AppendAgentThought(text string) {
	if len(b.events) > 0 {
		last := &b.events[len(b.events)-1]
		if last.Type == BufferedEventAgentThought {
			if data, ok := last.Data.(*AgentThoughtData); ok {
				data.Text += text
				return
			}
		}
	}
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventAgentThought,
		Data: &AgentThoughtData{Text: text},
	})
}

// AppendToolCall appends a tool call event to the buffer.
func (b *EventBuffer) AppendToolCall(id, title, status string) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventToolCall,
		Data: &ToolCallData{ID: id, Title: title, Status: status},
	})
}

// AppendToolCallUpdate appends a tool call update event to the buffer.
func (b *EventBuffer) AppendToolCallUpdate(id string, status *string) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventToolCallUpdate,
		Data: &ToolCallUpdateData{ID: id, Status: status},
	})
}

// AppendPlan appends a plan event to the buffer.
func (b *EventBuffer) AppendPlan() {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventPlan,
		Data: &PlanData{},
	})
}

// AppendFileRead appends a file read event to the buffer.
func (b *EventBuffer) AppendFileRead(path string, size int) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventFileRead,
		Data: &FileOperationData{Path: path, Size: size},
	})
}

// AppendFileWrite appends a file write event to the buffer.
func (b *EventBuffer) AppendFileWrite(path string, size int) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventFileWrite,
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
