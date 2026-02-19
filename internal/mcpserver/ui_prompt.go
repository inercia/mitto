// Package mcpserver provides MCP (Model Context Protocol) servers for Mitto.
// This file defines the UI prompt types and interfaces for interactive user prompts.
package mcpserver

import (
	"context"
)

// UIPromptType identifies the kind of interactive prompt.
type UIPromptType string

const (
	// UIPromptTypeYesNo displays two buttons for a yes/no decision.
	UIPromptTypeYesNo UIPromptType = "yes_no"

	// UIPromptTypeSelect displays a dropdown/combo box for selecting one option.
	// Reserved for future use.
	UIPromptTypeSelect UIPromptType = "select"

	// UIPromptTypeOptions displays multiple buttons for selecting one option.
	// Unlike yes_no which has exactly two buttons, this supports any number of options.
	UIPromptTypeOptions UIPromptType = "options_buttons"
)

// UIPromptOption represents a selectable option in a UI prompt.
type UIPromptOption struct {
	// ID is the machine-readable identifier returned when this option is selected.
	ID string `json:"id"`
	// Label is the human-readable text displayed to the user.
	Label string `json:"label"`
}

// UIPromptRequest is sent to the UI to display an interactive prompt.
type UIPromptRequest struct {
	// RequestID is a unique identifier for correlating the response.
	RequestID string `json:"request_id"`
	// Type determines how the prompt is rendered (yes_no, select, etc.).
	Type UIPromptType `json:"type"`
	// Question is the text displayed to the user.
	Question string `json:"question"`
	// Options are the available choices for the user.
	Options []UIPromptOption `json:"options"`
	// TimeoutSeconds is how long to wait for a response before timing out.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// UIPromptResponse contains the user's response to a UI prompt.
type UIPromptResponse struct {
	// RequestID correlates this response to the original request.
	RequestID string `json:"request_id"`
	// OptionID is the ID of the option the user selected (empty if timed out).
	OptionID string `json:"option_id,omitempty"`
	// Label is the display text of the selected option (empty if timed out).
	Label string `json:"label,omitempty"`
	// TimedOut is true if the prompt timed out without a response.
	TimedOut bool `json:"timed_out,omitempty"`
}

// UIPrompter allows MCP tools to display interactive prompts in the UI.
// The BackgroundSession implements this interface to bridge MCP tools
// with WebSocket-connected UI clients.
type UIPrompter interface {
	// UIPrompt displays an interactive prompt and blocks until the user responds
	// or the timeout expires. Returns an error if the session is closed or
	// the context is cancelled.
	//
	// If a new prompt is sent while one is pending, the previous prompt is
	// dismissed and replaced by the new one.
	UIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error)

	// DismissPrompt cancels any active prompt with the given request ID.
	// This is called when the prompt should be dismissed (e.g., session activity).
	DismissPrompt(requestID string)
}
