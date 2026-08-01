package conversation

import (
	"log/slog"
	"strconv"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

// LogSessionConfigOptions logs a per-response summary of the ConfigOptions
// slice returned by session/new, session/load, or session/resume. Emitted at
// INFO level unconditionally (even when opts is empty) so an agent-side model-
// catalog regression is diagnosable without enabling DEBUG or a raw-payload
// trace: prior to this, ModelStateFromConfigOptions returning nil was silent
// and the only visible symptom was "decision=skip_no_agent_models" much later
// at prompt time. Only non-secret structural metadata is logged (id/category
// /type + Select current value and option counts) — never a user-supplied
// _meta payload. No-op when logger is nil.
func LogSessionConfigOptions(logger *slog.Logger, source string, opts []acp.SessionConfigOption) {
	if logger == nil {
		return
	}
	hasModel := false
	summaries := make([]string, 0, len(opts))
	for _, opt := range opts {
		switch {
		case opt.Select != nil:
			sel := opt.Select
			cat := ""
			if sel.Category != nil {
				cat = string(*sel.Category)
				if cat == string(acp.SessionConfigOptionCategoryModel) {
					hasModel = true
				}
			}
			optCount := 0
			switch {
			case sel.Options.Ungrouped != nil:
				optCount = len(*sel.Options.Ungrouped)
			case sel.Options.Grouped != nil:
				for _, g := range *sel.Options.Grouped {
					optCount += len(g.Options)
				}
			}
			summaries = append(summaries, string(sel.Id)+"[select,cat="+cat+
				",current="+string(sel.CurrentValue)+
				",n="+strconv.Itoa(optCount)+"]")
		case opt.Boolean != nil:
			b := opt.Boolean
			cat := ""
			if b.Category != nil {
				cat = string(*b.Category)
			}
			summaries = append(summaries, string(b.Id)+"[boolean,cat="+cat+"]")
		default:
			summaries = append(summaries, "?[unknown-variant]")
		}
	}
	logger.Info("ACP session config options",
		"source", source,
		"config_option_count", len(opts),
		"has_model_option", hasModel,
		"config_options", summaries)
}

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

// ModelStateFromACP converts an SDK-native *acp.SessionModelState (populated from
// the top-level `models` field on session/new, session/load, or session/resume
// responses) into Mitto's local *SessionModelState. Returns nil when src is nil
// or advertises no available models. Used as the fallback path for agents (e.g.
// Auggie) that ship their model catalog under the out-of-spec top-level `models`
// field instead of inside configOptions[] with category="model" (mitto-i8n).
func ModelStateFromACP(src *acp.SessionModelState) *SessionModelState {
	if src == nil || len(src.AvailableModels) == 0 {
		return nil
	}
	state := &SessionModelState{
		CurrentModelId:  string(src.CurrentModelId),
		AvailableModels: make([]ModelInfo, 0, len(src.AvailableModels)),
	}
	for _, m := range src.AvailableModels {
		state.AvailableModels = append(state.AvailableModels, ModelInfo{
			ModelId:     string(m.ModelId),
			Name:        m.Name,
			Description: m.Description,
		})
	}
	return state
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
