package prompts

// PromptSource indicates where a prompt originated from.
type PromptSource string

const (
	// PromptSourceFile indicates the prompt was loaded from a .md file in MITTO_DIR/prompts/
	PromptSourceFile PromptSource = "file"
	// PromptSourceSettings indicates the prompt was defined in settings.json
	PromptSourceSettings PromptSource = "settings"
	// PromptSourceWorkspace indicates the prompt was defined in a workspace .mittorc file
	PromptSourceWorkspace PromptSource = "workspace"
	// PromptSourceBuiltin indicates the prompt was loaded from the builtin prompts directory
	// (MITTO_DIR/prompts/builtin/). These prompts are read-only and can only be disabled.
	PromptSourceBuiltin PromptSource = "builtin"
)

// WebPrompt represents a predefined prompt for the web interface.
type WebPrompt struct {
	// Name is the display name for the prompt button
	Name string `json:"name"`
	// Prompt is the actual prompt text to send
	Prompt string `json:"prompt"`
	// BackgroundColor is an optional hex color string for the prompt button (e.g., "#E8F5E9")
	BackgroundColor string `json:"backgroundColor,omitempty"`
	// Icon is an optional icon name (from the frontend icon registry) shown next to
	// the prompt in menus, e.g. "beads", "search", "settings".
	Icon string `json:"icon,omitempty"`
	// Description is an optional description shown as tooltip in the UI
	Description string `json:"description,omitempty"`
	// Group is an optional group name for organizing prompts in the UI.
	// Prompts with the same group will be displayed together under a group header.
	// If empty, the prompt will appear in an "Other" section.
	Group string `json:"group,omitempty"`
	// Menus is a comma-separated list of UI menus this prompt should appear in
	// (beyond the default ChatInput "Insert predefined prompt" dropup). For
	// example, "conversation" makes the prompt available in the per-conversation
	// context menu. Multiple values may be combined, e.g. "conversation,group".
	Menus string `json:"menus,omitempty"`
	// Singleton, when true, declares that this prompt must not have multiple
	// concurrent conversation instances (subject to find-or-route logic).
	Singleton bool `json:"singleton,omitempty"`
	// Tags is an optional list of categorization tags for this prompt.
	Tags []string `json:"tags,omitempty"`
	// Source indicates where this prompt originated from (file, settings, workspace).
	// This is used by the frontend to determine which prompts should be saved back to settings.
	// Only prompts with Source="settings" or empty Source should be saved.
	Source PromptSource `json:"source,omitempty"`
	// EnabledWhen is an optional CEL expression for conditional visibility.
	// Actual filtering happens server-side via filterPromptsByEnabled; not serialized to JSON.
	EnabledWhen string `json:"-"`
	// Enabled controls whether the prompt is active after merging.
	// A nil value means enabled (default true). Only explicit false disables.
	// This is used during merge to allow higher-priority sources to disable prompts.
	Enabled *bool `json:"enabled,omitempty"`
	// Loop, if non-nil, declares that selecting this prompt in a menu creates
	// a loop (recurring) conversation instead of a one-time seed. The fields
	// provide default schedule values for the schedule dialog.
	Loop *PromptLoop `json:"loop,omitempty"`
	// PreferredModels is an ordered list of references to global model profiles
	// (Settings → Models), by profile name or capability tag. The first entry that
	// resolves to an available model wins. Empty/absent means use the session's
	// baseline model. This field is carried through PromptMeta to enable
	// per-prompt model selection without mutating the user's model preference.
	PreferredModels []PromptPreferredModel `json:"preferredModels,omitempty"`
	// Parameters declares the named, typed inputs this prompt expects.
	// Populated from the `parameters:` block in .prompt.yaml or inline config prompts.
	Parameters []PromptParameter `json:"parameters,omitempty"`
}

// ============================================================================
// Prompt Merging
//
// Prompts can come from multiple sources with different priorities.
// MergePrompts combines them, with later sources overriding earlier ones by name.
//
// Priority order (lowest to highest):
//   1. Global file prompts (MITTO_DIR/prompts/*.prompt.yaml)
//   2. Settings file prompts (config.Prompts)
//   3. Workspace prompts (.mittorc)
// ============================================================================

// MergePrompts combines prompts from multiple sources with proper priority.
// Later sources override earlier ones when prompts have the same name.
// The order of prompts is preserved, with higher-priority prompts appearing first.
//
// Each prompt's Source field is set to indicate its origin:
//   - PromptSourceFile for globalFilePrompts (already set by ToWebPrompt)
//   - PromptSourceSettings for settingsPrompts
//   - PromptSourceWorkspace for workspacePrompts
//
// Parameters:
//   - globalFilePrompts: prompts from MITTO_DIR/prompts/*.prompt.yaml (lowest priority)
//   - settingsPrompts: prompts from settings file (medium priority)
//   - workspacePrompts: prompts from workspace .mittorc (highest priority)
//
// Returns a merged list with duplicates removed (by name).
func MergePrompts(globalFilePrompts, settingsPrompts, workspacePrompts []WebPrompt) []WebPrompt {
	seen := make(map[string]bool)
	var result []WebPrompt

	// Add workspace prompts first (highest priority)
	for _, p := range workspacePrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceWorkspace
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add settings prompts (medium priority)
	for _, p := range settingsPrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceSettings
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add global file prompts (lowest priority)
	// Note: Source is already set to PromptSourceFile by ToWebPrompt()
	for _, p := range globalFilePrompts {
		if p.Name != "" && !seen[p.Name] {
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Filter out disabled prompts after merge.
	// Higher-priority sources can set Enabled=false to suppress same-named lower-priority prompts.
	var filtered []WebPrompt
	for _, p := range result {
		if p.Enabled == nil || *p.Enabled {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// MergePromptsKeepDisabled combines prompts from multiple sources with proper priority,
// but unlike MergePrompts, it does NOT filter out disabled prompts.
// This is needed when returning workspace prompts to the frontend, because
// disabled entries (enabled: false) must reach the frontend so it can use them
// to suppress same-named global/builtin prompts in the prompts menu.
func MergePromptsKeepDisabled(globalFilePrompts, settingsPrompts, workspacePrompts []WebPrompt) []WebPrompt {
	seen := make(map[string]bool)
	var result []WebPrompt

	// Add workspace prompts first (highest priority)
	for _, p := range workspacePrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceWorkspace
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add settings prompts (medium priority)
	for _, p := range settingsPrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceSettings
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add global file prompts (lowest priority)
	for _, p := range globalFilePrompts {
		if p.Name != "" && !seen[p.Name] {
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	return result
}
