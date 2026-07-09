package conversation

import (
	acp "github.com/coder/acp-go-sdk"
)

// SessionModelState is a Mitto-owned view of an agent's available models and the
// currently selected one. It replaces the ACP SDK's removed UnstableSessionModelState
// (v0.13.5) and is populated from the config-option framework's model category.
type SessionModelState struct {
	// CurrentModelId is the id of the currently selected model.
	CurrentModelId string
	// AvailableModels is the set of models the agent advertises for selection.
	AvailableModels []ModelInfo
}

// ModelInfo describes a single selectable model.
type ModelInfo struct {
	// ModelId is the id used to select this model via session/set_config_option.
	ModelId string
	// Name is the human-readable label.
	Name string
	// Description is an optional longer description of the model.
	Description *string
}

// ModelConfigId is the conventional SessionConfigId Mitto uses when the agent
// hasn't advertised its own id for the model config option. Prefer the id
// returned by ModelStateFromConfigOptions when available.
const ModelConfigId acp.SessionConfigId = "model"

// ModelStateFromConfigOptions extracts the model selection state (if any) from
// the ConfigOptions slice returned by NewSession / LoadSession / ResumeSession
// (v0.13.5+). It returns nil when no option with Category == "model" is present
// or the option is not a Select variant. The returned SessionConfigId (from the
// option's Id field) should be used when issuing session/set_config_option so
// we match the agent-declared identifier.
func ModelStateFromConfigOptions(opts []acp.SessionConfigOption) (*SessionModelState, acp.SessionConfigId) {
	for _, opt := range opts {
		sel := opt.Select
		if sel == nil {
			continue
		}
		if sel.Category == nil || *sel.Category != acp.SessionConfigOptionCategoryModel {
			continue
		}

		state := &SessionModelState{
			CurrentModelId: string(sel.CurrentValue),
		}

		switch {
		case sel.Options.Ungrouped != nil:
			for _, o := range *sel.Options.Ungrouped {
				state.AvailableModels = append(state.AvailableModels, ModelInfo{
					ModelId:     string(o.Value),
					Name:        o.Name,
					Description: o.Description,
				})
			}
		case sel.Options.Grouped != nil:
			for _, g := range *sel.Options.Grouped {
				for _, o := range g.Options {
					state.AvailableModels = append(state.AvailableModels, ModelInfo{
						ModelId:     string(o.Value),
						Name:        o.Name,
						Description: o.Description,
					})
				}
			}
		}
		return state, sel.Id
	}
	return nil, ""
}
