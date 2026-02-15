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
	// DEPRECATED: Use WSMsgTypeLoadEvents instead for unified event loading.
	WSMsgTypeSyncSession = "sync_session"

	// WSMsgTypeLoadEvents requests events from the session with pagination support.
	// This is the unified message type for initial load, pagination, and sync.
	// Data: { "limit": int (optional), "before_seq": int (optional), "after_seq": int (optional) }
	// - limit: Maximum events to return (default: 50)
	// - before_seq: Load events with seq < before_seq (for "load more" pagination)
	// - after_seq: Load events with seq > after_seq (for sync after reconnect)
	// Note: before_seq and after_seq are mutually exclusive.
	WSMsgTypeLoadEvents = "load_events"

	// WSMsgTypeKeepalive is an application-level keepalive with timestamp and sequence info.
	// Used to detect stale connections, measure latency, and detect out-of-sync clients.
	// Data: { "client_time": int64 (Unix ms), "last_seen_seq": int64 (optional, highest seq client has seen) }
	// The server responds with keepalive_ack containing server_max_seq so clients can detect if they're behind.
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

	// WSMsgTypeSessionPinned notifies that a session's pinned state changed.
	// Sent on /api/events to all connected clients.
	// Data: { "session_id": string, "pinned": bool }
	WSMsgTypeSessionPinned = "session_pinned"

	// WSMsgTypeSessionDeleted notifies that a session was deleted.
	// Sent on /api/events to all connected clients.
	// Data: { "session_id": string }
	WSMsgTypeSessionDeleted = "session_deleted"

	// WSMsgTypeSessionArchived notifies that a session's archived state changed.
	// Sent on /api/events to all connected clients.
	// Data: { "session_id": string, "archived": bool }
	WSMsgTypeSessionArchived = "session_archived"

	// WSMsgTypeSessionStreaming notifies that a session's streaming state changed.
	// Sent on /api/events when a session starts or stops streaming.
	// Data: { "session_id": string, "is_streaming": bool }
	WSMsgTypeSessionStreaming = "session_streaming"

	// WSMsgTypePeriodicUpdated notifies that a session's periodic prompt state changed.
	// Sent on /api/events to all connected clients when periodic is enabled/disabled.
	// Data: { "session_id": string, "periodic_enabled": bool }
	WSMsgTypePeriodicUpdated = "periodic_updated"

	// WSMsgTypePeriodicStarted notifies that a periodic prompt was delivered.
	// Sent on /api/events to all connected clients when a scheduled periodic run starts.
	// Data: { "session_id": string, "session_name": string }
	WSMsgTypePeriodicStarted = "periodic_started"

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
	// DEPRECATED: Use WSMsgTypeEventsLoaded instead for unified event loading.
	WSMsgTypeSessionSync = "session_sync"

	// WSMsgTypeEventsLoaded responds to a load_events request with events.
	// Data: { "events": []object, "has_more": bool, "first_seq": int, "last_seq": int, "total_count": int }
	// - events: Array of event objects
	// - has_more: True if more older events exist (for pagination)
	// - first_seq: Lowest seq in returned events
	// - last_seq: Highest seq in returned events
	// - total_count: Total events in session (from metadata)
	// - prepend: True if these are older events to prepend (for "load more")
	WSMsgTypeEventsLoaded = "events_loaded"

	// WSMsgTypeKeepaliveAck responds to a keepalive with server timestamp, sequence info, and session state.
	// Data: {
	//   "client_time": int64,      // Echo back client time for RTT calculation
	//   "server_time": int64,      // Server's current time (Unix ms)
	//   "server_max_seq": int64,   // Highest seq server has for this session
	//   "is_prompting": bool,      // Whether agent is currently responding
	//   "is_running": bool,        // Whether background session is active
	//   "queue_length": int,       // Number of messages waiting in queue
	//   "status": string           // Session status (active, completed, error)
	// }
	// The server_max_seq field allows clients to detect if they're behind and need to sync.
	// If client's last_seen_seq < server_max_seq, client should request events with after_seq.
	// Additional fields (is_running, queue_length, status) allow the UI to stay in sync
	// without separate API calls, useful for multi-tab scenarios and mobile wake recovery.
	WSMsgTypeKeepaliveAck = "keepalive_ack"

	// WSMsgTypeRunnerFallback notifies that a configured runner is not supported and fell back to exec.
	// Data: { "session_id": string, "requested_type": string, "fallback_type": string, "reason": string }
	WSMsgTypeRunnerFallback = "runner_fallback"

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

	// WSMsgTypeACPStopped notifies that the ACP connection for a session was stopped.
	// Sent when a session is archived and the ACP process is gracefully terminated.
	// Data: { "session_id": string, "reason": string }
	WSMsgTypeACPStopped = "acp_stopped"

	// WSMsgTypeACPStarted notifies that the ACP connection for a session was started.
	// Sent when a session is unarchived and the ACP process is restarted.
	// Data: { "session_id": string }
	WSMsgTypeACPStarted = "acp_started"

	// WSMsgTypeACPStartFailed notifies that the ACP connection for a session failed to start.
	// Sent when session creation or resumption fails due to ACP startup error.
	// Data: { "session_id": string, "error": string }
	WSMsgTypeACPStartFailed = "acp_start_failed"

	// WSMsgTypeAvailableCommandsUpdated notifies that the agent has sent available slash commands.
	// Sent when the agent provides its list of supported slash commands.
	// These commands can be used for autocomplete in the chat input.
	// Data: { "session_id": string, "commands": []{ "name": string, "description": string, "input_hint": string (optional) } }
	WSMsgTypeAvailableCommandsUpdated = "available_commands_updated"

	// WSMsgTypeConfigOptionChanged notifies that a session config option has changed.
	// Sent when any config option is changed either by the client or by the agent.
	// For backward compatibility with legacy modes, config_id will be "mode" for mode changes.
	// Data: { "session_id": string, "config_id": string, "value": string }
	WSMsgTypeConfigOptionChanged = "config_option_changed"

	// WSMsgTypeSetConfigOption is sent from the frontend to change a config option value.
	// For backward compatibility with legacy modes, use config_id "mode" for mode changes.
	// Data: { "config_id": string, "value": string }
	WSMsgTypeSetConfigOption = "set_config_option"
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
	Entries []PlanEntry `json:"entries"`
}

// FileOperationData holds data for file read/write events.
type FileOperationData struct {
	Path string
	Size int
}

// EventBuffer accumulates streaming events for coalescing and replay.
// This is primarily used for coalescing agent message chunks that arrive
// in rapid succession. With immediate persistence, events are persisted
// as they arrive, but this buffer can still be useful for observer replay
// and testing purposes.
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
func (b *EventBuffer) AppendPlan(seq int64, entries []PlanEntry) {
	b.events = append(b.events, BufferedEvent{
		Type: BufferedEventPlan,
		Seq:  seq,
		Data: &PlanData{Entries: entries},
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
		if data, ok := e.Data.(*PlanData); ok {
			observer.OnPlan(e.Seq, data.Entries)
		}
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
		if data, ok := e.Data.(*PlanData); ok {
			// Convert web.PlanEntry to session.PlanEntry
			sessionEntries := make([]session.PlanEntry, len(data.Entries))
			for i, entry := range data.Entries {
				sessionEntries[i] = session.PlanEntry{
					Content:  entry.Content,
					Priority: entry.Priority,
					Status:   entry.Status,
				}
			}
			return persister.RecordPlan(sessionEntries)
		}
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
