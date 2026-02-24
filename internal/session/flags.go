package session

// FlagDefinition describes an available advanced setting flag.
// These flags are defined at compile time and exposed to the frontend
// via the /api/advanced-flags endpoint.
type FlagDefinition struct {
	// Name is the unique identifier for this flag (used as key in AdvancedSettings map)
	Name string `json:"name"`
	// Label is a short human-readable name for UI display
	Label string `json:"label"`
	// Description provides detailed explanation of what this flag controls
	Description string `json:"description"`
	// Default is the default value for this flag when not explicitly set
	Default bool `json:"default"`
}

// AvailableFlags is the compile-time registry of all supported advanced settings flags.
// The frontend fetches this list to know what flags are available and their defaults.
// When adding a new flag:
//  1. Add a FlagName constant below
//  2. Add a FlagDefinition to this slice
//  3. Implement the flag's behavior in the relevant package
var AvailableFlags = []FlagDefinition{
	{
		Name:        FlagCanDoIntrospection,
		Label:       "Can do introspection",
		Description: "Allow this conversation to access Mitto's MCP server for introspection (view conversations, session data, etc.)",
		Default:     false,
	},
	{
		Name:        FlagCanSendPrompt,
		Label:       "Can Send Prompt",
		Description: "Allow this conversation to send prompts to other conversations via MCP tool",
		Default:     false,
	},
	{
		Name:        FlagCanPromptUser,
		Label:       "Can prompt user",
		Description: "Allow MCP tools to display interactive prompts (yes/no questions, etc.) in the UI and wait for user response",
		Default:     true,
	},
	{
		Name:        FlagCanStartConversation,
		Label:       "Can start conversation",
		Description: "Allow this conversation to create new conversations via MCP tool. Child conversations cannot start further conversations.",
		Default:     false,
	},
}

// Flag name constants for type-safe access to flag values.
// Use these constants when checking flag values in code.
const (
	// FlagCanDoIntrospection controls whether the conversation can access
	// Mitto's MCP server for introspection capabilities.
	FlagCanDoIntrospection = "can_do_introspection"

	// FlagCanSendPrompt controls whether the conversation can send prompts
	// to other conversations via the send_prompt_to_conversation MCP tool.
	FlagCanSendPrompt = "can_send_prompt"

	// FlagCanPromptUser controls whether MCP tools can display interactive
	// prompts (yes/no questions, etc.) in the UI and wait for user response.
	FlagCanPromptUser = "can_prompt_user"

	// FlagCanStartConversation controls whether the conversation can create
	// new conversations via the mitto_conversation_start MCP tool.
	// Child conversations (those with a ParentSessionID) cannot start further conversations.
	FlagCanStartConversation = "can_start_conversation"
)

// GetFlagDefault returns the default value for a flag by name.
// Returns false if the flag is not found.
func GetFlagDefault(flagName string) bool {
	for _, flag := range AvailableFlags {
		if flag.Name == flagName {
			return flag.Default
		}
	}
	return false
}

// GetFlagValue returns the value of a flag from the given settings map.
// If the flag is not present in the map, returns the flag's default value.
// If the flag is not in AvailableFlags, returns false.
func GetFlagValue(settings map[string]bool, flagName string) bool {
	if settings != nil {
		if value, exists := settings[flagName]; exists {
			return value
		}
	}
	return GetFlagDefault(flagName)
}
