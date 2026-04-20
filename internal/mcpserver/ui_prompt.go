// Package mcpserver provides MCP (Model Context Protocol) servers for Mitto.
// This file defines the UI prompt types and interfaces for interactive user prompts.
//
// The unified prompt system handles three categories of user interactions:
//
//  1. MCP Tool Questions - Yes/no, select, or multi-option prompts from MCP tools
//  2. Follow-up Suggestions - Action buttons suggesting responses after agent completes
//  3. Permission Requests - Allow/deny prompts for ACP permission requests
//
// All three use the same UIPromptRequest/UIPromptResponse types and are rendered
// by the same frontend component, with type-specific styling and behavior.
package mcpserver

import (
	"context"
)

// UIPromptType identifies the kind of interactive prompt.
type UIPromptType string

const (
	// UIPromptTypeYesNo displays two buttons for a yes/no decision.
	// Used by: MCP tools asking simple yes/no questions.
	// Blocking: Yes (caller waits for response).
	UIPromptTypeYesNo UIPromptType = "yes_no"

	// UIPromptTypeSelect displays a dropdown/combo box for selecting one option.
	// Used by: MCP tools with many options where buttons would be overwhelming.
	// Blocking: Yes (caller waits for response).
	UIPromptTypeSelect UIPromptType = "select"

	// UIPromptTypeOptions displays multiple buttons for selecting one option.
	// Unlike yes_no which has exactly two buttons, this supports any number of options.
	// Used by: MCP tools with multiple choice questions.
	// Blocking: Yes (caller waits for response).
	UIPromptTypeOptions UIPromptType = "options_buttons"

	// UIPromptTypeActionButtons displays follow-up suggestion buttons.
	// Clicking a button sends the option's Response field as a new prompt.
	// Used by: Follow-up suggestions after agent completes a response.
	// Blocking: No (non-blocking, purely UI suggestions).
	// Persistent: Yes (survives reconnects, stored on disk).
	UIPromptTypeActionButtons UIPromptType = "action_buttons"

	// UIPromptTypePermission displays a permission request dialog.
	// Shows command details and allow/deny options from the ACP agent.
	// Used by: ACP permission requests for file writes, command execution, etc.
	// Blocking: Yes (ACP waits for permission decision).
	UIPromptTypePermission UIPromptType = "permission"

	// UIPromptTypeTextbox displays a large text editing area.
	// Used by: MCP tools that need the user to review/edit text.
	// Blocking: Yes (caller waits for response).
	UIPromptTypeTextbox UIPromptType = "textbox"

	// UIPromptTypeForm displays a sanitized HTML form for user input.
	// Used by: MCP tools that need structured user input via form fields.
	// The HTML is strictly sanitized to allow only form-related elements.
	// Blocking: Yes (caller waits for response).
	UIPromptTypeForm UIPromptType = "form"
)

// UIPromptOptionStyle defines the visual style for an option button.
type UIPromptOptionStyle string

const (
	// UIPromptOptionStylePrimary is a prominent button (blue).
	UIPromptOptionStylePrimary UIPromptOptionStyle = "primary"
	// UIPromptOptionStyleSecondary is a less prominent button (gray).
	UIPromptOptionStyleSecondary UIPromptOptionStyle = "secondary"
	// UIPromptOptionStyleDanger is a destructive/warning button (red).
	UIPromptOptionStyleDanger UIPromptOptionStyle = "danger"
	// UIPromptOptionStyleSuccess is a positive/approve button (green).
	UIPromptOptionStyleSuccess UIPromptOptionStyle = "success"
)

// UIPromptOption represents a selectable option in a UI prompt.
type UIPromptOption struct {
	// ID is the machine-readable identifier returned when this option is selected.
	ID string `json:"id"`
	// Label is the human-readable text displayed to the user.
	Label string `json:"label"`
	// Description is an optional longer description shown below the label.
	// Used by the unified options menu to provide additional context.
	Description string `json:"description,omitempty"`

	// Response is the text payload to send as a new prompt when this option is selected.
	// Used by action_buttons type - clicking sends this text as a user message.
	// For other types, this is empty and only ID/Label are returned.
	Response string `json:"response,omitempty"`

	// Kind indicates the semantic meaning of this option.
	// Used by permission type: "allow", "deny", "always_allow", "session_allow".
	// Helps the backend understand what action to take.
	Kind string `json:"kind,omitempty"`

	// Style determines the visual appearance of the button.
	// Options: "primary", "secondary", "danger", "success".
	// If empty, defaults based on type and position.
	Style UIPromptOptionStyle `json:"style,omitempty"`
}

// UIPromptRequest is sent to the UI to display an interactive prompt.
type UIPromptRequest struct {
	// RequestID is a unique identifier for correlating the response.
	RequestID string `json:"request_id"`
	// Type determines how the prompt is rendered (yes_no, select, permission, etc.).
	Type UIPromptType `json:"type"`
	// Question is the text displayed to the user.
	Question string `json:"question"`
	// Options are the available choices for the user.
	Options []UIPromptOption `json:"options"`
	// TimeoutSeconds is how long to wait for a response before timing out.
	// Zero means use default timeout. Ignored for non-blocking prompts.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// Blocking indicates whether the caller is waiting for a response.
	// True for: yes_no, select, options_buttons, permission.
	// False for: action_buttons (purely UI suggestions).
	Blocking bool `json:"blocking"`

	// Persistent indicates whether this prompt survives client reconnects.
	// True for: action_buttons (stored on disk).
	// False for: yes_no, select, options_buttons, permission (in-memory only).
	Persistent bool `json:"persistent,omitempty"`

	// ToolCallID is the ACP tool call ID for permission requests.
	// Used to correlate the permission response back to the waiting agent.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// Title provides additional context for the prompt.
	// For permission requests: the command or operation being requested.
	// For other types: optional subtitle or context.
	Title string `json:"title,omitempty"`

	// Description provides detailed information about the prompt.
	// For permission requests: command details, file paths, etc.
	Description string `json:"description,omitempty"`

	// AllowFreeText indicates whether the user can type a custom response
	// in addition to selecting from the predefined options.
	AllowFreeText bool `json:"allow_free_text,omitempty"`

	// FreeTextPlaceholder is the placeholder text for the free text input.
	// Only relevant when AllowFreeText is true.
	FreeTextPlaceholder string `json:"free_text_placeholder,omitempty"`

	// Text is the initial text content for textbox prompts.
	// Only relevant when Type is UIPromptTypeTextbox.
	Text string `json:"text,omitempty"`

	// ResultMode controls how textbox results are returned: "text" (full text) or "diff" (unified diff).
	// Only relevant when Type is UIPromptTypeTextbox.
	ResultMode string `json:"result_mode,omitempty"`

	// AllowAbort indicates whether the user can abort the textbox editing.
	// Only relevant when Type is UIPromptTypeTextbox.
	AllowAbort bool `json:"allow_abort,omitempty"`

	// FormHTML is sanitized HTML content for form prompts.
	// Only relevant when Type is UIPromptTypeForm.
	// Contains only whitelisted form elements (input, select, textarea, label, etc.).
	FormHTML string `json:"form_html,omitempty"`
}

// UIPromptResponse contains the user's response to a UI prompt.
type UIPromptResponse struct {
	// RequestID correlates this response to the original request.
	RequestID string `json:"request_id"`
	// OptionID is the ID of the option the user selected (empty if timed out).
	OptionID string `json:"option_id,omitempty"`
	// Label is the display text of the selected option (empty if timed out).
	Label string `json:"label,omitempty"`
	// Response is the response payload for action_buttons type.
	// When user clicks an action button, this contains the button's Response field.
	Response string `json:"response,omitempty"`
	// FreeText is the custom text typed by the user when AllowFreeText is enabled.
	FreeText string `json:"free_text,omitempty"`
	// TimedOut is true if the prompt timed out without a response.
	TimedOut bool `json:"timed_out,omitempty"`
	// Aborted is true if the user clicked the abort button.
	Aborted bool `json:"aborted,omitempty"`
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
