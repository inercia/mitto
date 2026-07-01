package conversation

// AvailableCommand represents a slash command that the agent can execute.
// This mirrors the ACP protocol's AvailableCommand structure.
type AvailableCommand struct {
	// Name is the command name (e.g., "web", "test", "plan").
	Name string `json:"name"`
	// Description is a human-readable description of what the command does.
	Description string `json:"description"`
	// InputHint is an optional hint to display when the input hasn't been provided yet.
	InputHint string `json:"input_hint,omitempty"`
}
