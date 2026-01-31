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
	"time"
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

	// WSMsgTypeNewSession requests creation of a new session.
	// Data: { "working_dir": string, "acp_server": string (optional) }
	// Note: Deprecated - use POST /api/sessions instead
	WSMsgTypeNewSession = "new_session"

	// WSMsgTypeLoadSession requests loading an existing session.
	// Data: { "session_id": string }
	// Note: Deprecated - connect to /api/sessions/{id}/ws instead
	WSMsgTypeLoadSession = "load_session"

	// WSMsgTypeSwitchSession requests switching to a different session.
	// Data: { "session_id": string }
	// Note: Deprecated - connect to /api/sessions/{id}/ws instead
	WSMsgTypeSwitchSession = "switch_session"

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
)

// =============================================================================
// Message Buffer for Agent Output
// =============================================================================

// agentMessageBuffer accumulates agent message chunks for persistence.
// We buffer chunks and persist complete messages to avoid excessive disk writes.
type agentMessageBuffer struct {
	text      strings.Builder
	lastFlush time.Time
}

// Write appends text to the buffer.
func (b *agentMessageBuffer) Write(text string) {
	b.text.WriteString(text)
}

// Flush returns the accumulated text and resets the buffer.
func (b *agentMessageBuffer) Flush() string {
	text := b.text.String()
	b.text.Reset()
	b.lastFlush = time.Now()
	return text
}
