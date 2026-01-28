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
	OnPromptComplete()

	// OnError is called when an error occurs.
	OnError(message string)
}
