package agentbackend

// ModelDescriptor describes a single selectable model, a neutral mirror of
// conversation.ModelInfo that does not depend on it.
type ModelDescriptor struct {
	ID          string
	Name        string
	Description string
}

// ModelState is a neutral view of a session's available models and the
// currently selected one, mirroring conversation.SessionModelState.
type ModelState struct {
	CurrentModelID string
	Available      []ModelDescriptor
}

// ModeDescriptor describes a single selectable session mode.
type ModeDescriptor struct {
	ID          string
	Name        string
	Description string
}

// ModeState is a neutral view of a session's available modes and the
// currently selected one.
type ModeState struct {
	CurrentModeID string
	Available     []ModeDescriptor
}

// ConfigOptionValue is one selectable value of a ConfigOption.
type ConfigOptionValue struct {
	Value       string
	Name        string
	Description string
}

// ConfigOption is a neutral mirror of a session-level configuration knob
// (e.g. model or mode selection exposed as a generic option), a neutral
// counterpart to conversation.SessionConfigOption.
type ConfigOption struct {
	ID       string
	Category string
	Current  string
	Values   []ConfigOptionValue
}
