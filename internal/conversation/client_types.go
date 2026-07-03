package conversation

// SeqProvider provides sequence numbers for event ordering.
// Sequence numbers are assigned when events are received from ACP,
// ensuring correct ordering even when content is buffered (e.g., in MarkdownBuffer).
type SeqProvider interface {
	// GetNextSeq returns the next sequence number and increments the counter.
	GetNextSeq() int64
}

// SessionConfigOption represents a configurable session option.
// This type mirrors the ACP configOptions structure and supports both:
// - Legacy "modes" API (converted to configOptions with category "mode")
// - Newer "configOptions" API (used directly when available)
// See https://agentclientprotocol.com/protocol/session-config-options
type SessionConfigOption struct {
	// ID is the unique identifier for this option (e.g., "mode", "model").
	ID string `json:"id"`
	// Name is the human-readable label for the option (e.g., "Session Mode", "Model").
	Name string `json:"name"`
	// Description provides more details about what this option controls.
	Description string `json:"description,omitempty"`
	// Category is semantic metadata for UX (e.g., "mode", "model", "thought_level").
	// For legacy modes, this is always "mode".
	Category string `json:"category,omitempty"`
	// Type is the input control type. Currently only "select" is supported.
	Type string `json:"type"`
	// CurrentValue is the currently selected value for this option.
	CurrentValue string `json:"current_value"`
	// Options are the available values for this option.
	Options []SessionConfigOptionValue `json:"options"`
}

// SessionConfigOptionValue represents a selectable value for a config option.
type SessionConfigOptionValue struct {
	// Value is the identifier used when setting this option.
	Value string `json:"value"`
	// Name is the human-readable name to display.
	Name string `json:"name"`
	// Description explains what this value does.
	Description string `json:"description,omitempty"`
}

// ConfigOptionCategory constants for well-known categories.
const (
	ConfigOptionCategoryMode         = "mode"
	ConfigOptionCategoryModel        = "model"
	ConfigOptionCategoryThoughtLevel = "thought_level"
	// ConfigOptionCategoryModelOverride marks a transient, per-prompt model
	// switch (driven by a prompt's preferredModels) that leaves the conversation
	// baseline unchanged. Rendered as a distinct timeline pill, not a config change.
	ConfigOptionCategoryModelOverride = "model_override"
)

// ConfigOptionType constants for option types.
const (
	// ConfigOptionTypeSelect is a dropdown/select control.
	ConfigOptionTypeSelect = "select"
	// ConfigOptionTypeToggle is a boolean toggle control (future).
	ConfigOptionTypeToggle = "toggle"
)
