package conversation

import (
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

// PlanEntry represents a single task in the agent's execution plan.
// This mirrors the ACP protocol's PlanEntry structure.
type PlanEntry struct {
	// Content is a human-readable description of what this task aims to accomplish.
	Content string `json:"content"`
	// Priority indicates the relative importance of this task (high, medium, low).
	Priority string `json:"priority"`
	// Status is the current execution status (pending, in_progress, completed).
	Status string `json:"status"`
}

// Type aliases for UI prompt types from mcpserver package.
// This avoids duplication while keeping the types accessible in the web package.
type (
	UIPromptType        = mcpserver.UIPromptType
	UIPromptOption      = mcpserver.UIPromptOption
	UIPromptOptionStyle = mcpserver.UIPromptOptionStyle
	UIPromptRequest     = mcpserver.UIPromptRequest
	UIPromptResponse    = mcpserver.UIPromptResponse
	UINotifyRequest     = mcpserver.UINotifyRequest
)

// Re-export UI prompt type constants for convenience.
const (
	UIPromptTypeYesNo         = mcpserver.UIPromptTypeYesNo
	UIPromptTypeSelect        = mcpserver.UIPromptTypeSelect
	UIPromptTypeOptions       = mcpserver.UIPromptTypeOptions
	UIPromptTypeActionButtons = mcpserver.UIPromptTypeActionButtons
	UIPromptTypePermission    = mcpserver.UIPromptTypePermission
	UIPromptTypeTextbox       = mcpserver.UIPromptTypeTextbox
	UIPromptTypeForm          = mcpserver.UIPromptTypeForm
)

// Re-export UI prompt option style constants for convenience.
const (
	UIPromptOptionStylePrimary   = mcpserver.UIPromptOptionStylePrimary
	UIPromptOptionStyleSecondary = mcpserver.UIPromptOptionStyleSecondary
	UIPromptOptionStyleDanger    = mcpserver.UIPromptOptionStyleDanger
	UIPromptOptionStyleSuccess   = mcpserver.UIPromptOptionStyleSuccess
)

// EventMetaObserver is an optional sibling of SessionObserver. Observers that
// implement it receive generic per-event metadata alongside the typed OnXxx
// notification, so new low-traffic annotations can flow through without
// requiring new per-event-type methods.
//
// Implementations of OnEventMeta must be safe to call concurrently from the
// same goroutine that invokes notifyObservers.
type EventMetaObserver interface {
	// OnEventMeta is called with the seq of a persisted event and its generic
	// metadata bag. It is called only when len(meta) > 0. Observers should
	// store meta keyed by seq and attach it to the matching typed notification
	// when it arrives.
	OnEventMeta(seq int64, meta map[string]any)
}

// SessionChangeObserver is an optional sibling of SessionObserver. Observers that
// implement it receive generic, first-class session-change timeline events
// (model changes today; other kinds later) for live push. Generic by design: the
// payload discriminates on Kind, so new kinds need no new observer method.
type SessionChangeObserver interface {
	// OnSessionChange is called with the seq of a persisted session_change event
	// and its generic payload.
	OnSessionChange(seq int64, data session.SessionChangeData)
}

// SessionObserver defines the interface for receiving session events.
// This allows multiple clients (WebSocket connections) to observe a single session.
//
// Events that are persisted include a sequence number (seq) for ordering and deduplication.
// The seq is assigned when the event is received from the ACP, ensuring streaming and
// persisted events have the same seq. Clients can use seq to:
// - Deduplicate events (same session_id + seq = same event)
// - Order events correctly after reconnection
// - Track which events they've seen for sync requests
type SessionObserver interface {
	// OnAgentMessage is called when the agent sends a message chunk (HTML).
	// seq is the sequence number for this logical message (chunks of the same message share the same seq).
	OnAgentMessage(seq int64, html string)

	// OnAgentThought is called when the agent sends a thought chunk (plain text).
	// seq is the sequence number for this logical thought (chunks share the same seq).
	OnAgentThought(seq int64, text string)

	// OnToolCall is called when a tool call starts.
	// seq is the sequence number for this tool call event.
	OnToolCall(seq int64, id, title, status string)

	// OnToolUpdate is called when a tool call status is updated.
	// seq is the sequence number for this tool update event.
	OnToolUpdate(seq int64, id string, status *string)

	// OnPlan is called when a plan update occurs.
	// seq is the sequence number for this plan event.
	// entries contains the list of plan tasks with their status.
	OnPlan(seq int64, entries []PlanEntry)

	// OnFileWrite is called when a file is written.
	// seq is the sequence number for this file write event.
	OnFileWrite(seq int64, path string, size int)

	// OnFileRead is called when a file is read.
	// seq is the sequence number for this file read event.
	OnFileRead(seq int64, path string, size int)

	// OnPromptComplete is called when a prompt response is complete.
	// eventCount is the current total event count for the session (for sync tracking).
	OnPromptComplete(eventCount int)

	// OnActionButtons is called when follow-up suggestions have been generated.
	// This is called asynchronously after OnPromptComplete, generated by the auxiliary
	// conversation analyzing the agent's response for questions or follow-up prompts.
	OnActionButtons(buttons []ActionButton)

	// OnUserPrompt is called when a user sends a prompt.
	// This is broadcast to all observers so they can update their UI.
	// senderID identifies which observer sent the prompt (for deduplication).
	// promptID is the client-generated ID for delivery confirmation.
	// imageIDs contains IDs of any attached images.
	// fileIDs contains IDs of any attached files.
	// promptName is the name of the workspace prompt used (empty string for ad-hoc prompts).
	// seq is the sequence number for this user prompt event.
	// argumentCount is the number of Go-template .Args arguments supplied (0 for ad-hoc or no-arg named prompts).
	OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string, promptName string, argumentCount int)

	// OnError is called when an error occurs.
	OnError(message string)

	// OnQueueUpdated is called when the message queue state changes.
	// action is one of: "added", "removed", "cleared"
	OnQueueUpdated(queueLength int, action string, messageID string)

	// OnQueueReordered is called when the queue order changes.
	// messages contains the full updated list of queued messages in their new order.
	OnQueueReordered(messages []session.QueuedMessage)

	// OnQueueMessageSending is called when a queued message is about to be sent.
	OnQueueMessageSending(messageID string)

	// OnQueueMessageSent is called after a queued message was delivered.
	OnQueueMessageSent(messageID string)

	// OnAvailableCommandsUpdated is called when the agent sends available slash commands.
	// Commands are sent shortly after session creation and may be updated during the session.
	OnAvailableCommandsUpdated(commands []AvailableCommand)

	// OnACPStopped is called when the ACP connection for this session is stopped.
	// This happens when the session is archived or explicitly closed.
	// reason indicates why the session was stopped (e.g., "archived", "archived_timeout").
	// Observers should update their state to prevent further prompts.
	OnACPStopped(reason string)

	// OnACPStarted is called when the ACP connection for this session becomes ready.
	// This is fired after successful ACP initialization (including after restarts).
	// Observers can use this to update their UI state and enable prompt input.
	OnACPStarted()

	// OnUIPrompt is called when an MCP tool requests user input via the UI.
	// The observer should display the prompt with the specified options and
	// respond by calling HandleUIPromptAnswer on the BackgroundSession.
	OnUIPrompt(req UIPromptRequest)

	// OnUIPromptDismiss is called when a UI prompt should be dismissed.
	// This happens when the prompt times out, is cancelled, or is replaced.
	// reason can be: "timeout", "cancelled", "replaced"
	OnUIPromptDismiss(requestID string, reason string)

	// OnNotification is called when an MCP tool sends a fire-and-forget notification.
	// The observer should display the notification as a toast and optionally
	// play a sound or show a native OS notification.
	// Unlike OnUIPrompt, no response is expected from the observer.
	OnNotification(req UINotifyRequest)

	// OnContextUsageUpdate is called when the agent sends a context window usage update.
	// size is the total context window size in tokens, used is how many tokens are currently in context.
	OnContextUsageUpdate(size, used int)
}
