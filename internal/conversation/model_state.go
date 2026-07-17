package conversation

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
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

// SynthesizeModelStateFromProfiles builds a synthetic SessionModelState from the
// caller's model profiles for agents that never advertise a model catalog via ACP
// ConfigOptions (mitto-ishl). One ModelInfo per profile is emitted with both
// ModelId and Name set to profile.Name — profile.Criteria (e.g. contains "Opus")
// matches the display Name, so downstream resolvers (SelectPreferredModel via
// ResolveProfileModel / MatchConstraintOption) resolve preferredModels against
// this synthetic state identically to a genuine agent-advertised catalog.
//
// CurrentModelId is left empty because the agent does not report a current model;
// callers must therefore treat any resolved id as an intended tier tag for
// display-only (session_change pill) — not as a target for a set_model RPC.
// Returns nil when profiles is empty so the caller can distinguish "no data to
// synthesize" from "synthesised state with zero available models".
func SynthesizeModelStateFromProfiles(profiles []config.ModelProfile) *SessionModelState {
	if len(profiles) == 0 {
		return nil
	}
	state := &SessionModelState{
		AvailableModels: make([]ModelInfo, 0, len(profiles)),
	}
	for _, p := range profiles {
		state.AvailableModels = append(state.AvailableModels, ModelInfo{
			ModelId: p.Name,
			Name:    p.Name,
		})
	}
	return state
}
