package web

import (
	"context"

	"github.com/coder/acp-go-sdk"
)

// SessionObserver defines the interface for receiving session events.
// This allows multiple clients (WebSocket connections) to observe a single session.
type SessionObserver interface {
	// OnAgentMessage is called when the agent sends a message chunk (HTML).
	OnAgentMessage(html string)

	// OnAgentThought is called when the agent sends a thought chunk (plain text).
	OnAgentThought(text string)

	// OnToolCall is called when a tool call starts.
	OnToolCall(id, title, status string)

	// OnToolUpdate is called when a tool call status is updated.
	OnToolUpdate(id string, status *string)

	// OnPlan is called when a plan update occurs.
	OnPlan()

	// OnFileWrite is called when a file is written.
	OnFileWrite(path string, size int)

	// OnFileRead is called when a file is read.
	OnFileRead(path string, size int)

	// OnPermission is called when a permission request needs user input.
	// Only the first observer to respond will have their answer used.
	OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)

	// OnPromptComplete is called when a prompt response is complete.
	// eventCount is the current total event count for the session (for sync tracking).
	OnPromptComplete(eventCount int)

	// OnUserPrompt is called when a user sends a prompt.
	// This is broadcast to all observers so they can update their UI.
	// senderID identifies which observer sent the prompt (for deduplication).
	// promptID is the client-generated ID for delivery confirmation.
	// imageIDs contains IDs of any attached images.
	OnUserPrompt(senderID, promptID, message string, imageIDs []string)

	// OnError is called when an error occurs.
	OnError(message string)

	// OnQueueUpdated is called when the message queue state changes.
	// action is one of: "added", "removed", "cleared"
	OnQueueUpdated(queueLength int, action string, messageID string)

	// OnQueueMessageSending is called when a queued message is about to be sent.
	OnQueueMessageSending(messageID string)

	// OnQueueMessageSent is called after a queued message was delivered.
	OnQueueMessageSent(messageID string)
}
