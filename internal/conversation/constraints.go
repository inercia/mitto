package conversation

import (
	"path"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// ModelOverrideLookup resolves a workspace-scoped model override for a tag.
// The returned model id is still validated against the agent's available models.
type ModelOverrideLookup func(tag string) (modelID string, ok bool)

// ModelsToConfigOptions converts an agent model state into config option values
// (Value=ModelId, Name, Description). Duplicate IDs are represented once, using
// the first advertised entry. Returns nil for a nil/empty model state.
func ModelsToConfigOptions(models *SessionModelState) []SessionConfigOptionValue {
	if models == nil || len(models.AvailableModels) == 0 {
		return nil
	}
	options := make([]SessionConfigOptionValue, 0, len(models.AvailableModels))
	seen := make(map[string]bool, len(models.AvailableModels))
	for _, m := range models.AvailableModels {
		if seen[m.ModelId] {
			continue
		}
		seen[m.ModelId] = true
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		options = append(options, SessionConfigOptionValue{
			Value:       m.ModelId,
			Name:        m.Name,
			Description: desc,
		})
	}
	return options
}

// MatchConstraintOption finds the best matching option value for a constraint.
// It iterates through all options and returns the last match, so that the latest version wins
// when models are ordered by version. Returns empty string if no match.
//
// The per-name match semantics (contains/exact/startsWith/regex/lookAlike) live in
// config.ConstraintMatchesName, which is shared with model-tag resolution so the engine
// stays DRY and cannot drift between callers.
func MatchConstraintOption(constraint *config.ACPServerConstraint, options []SessionConfigOptionValue) string {
	var matchedValue string
	for _, opt := range options {
		if config.ConstraintMatchesName(constraint, opt.Name) {
			matchedValue = opt.Value
		}
	}
	return matchedValue
}

// ResolveProfileModel resolves a model profile's Criteria against the available models,
// returning the matched model id ("" when the profile/criteria is nil or nothing matches).
// It reuses MatchConstraintOption (and thus config.ConstraintMatchesName) so profile-based
// model resolution shares the exact same match engine as ACP server constraints.
func ResolveProfileModel(profile *config.ModelProfile, models *SessionModelState) string {
	if profile == nil || profile.Criteria == nil {
		return ""
	}
	return MatchConstraintOption(profile.Criteria, ModelsToConfigOptions(models))
}

// ResolveAuxModelSwitch decides which model a freshly-created auxiliary session should run
// and whether a SetSessionModel RPC is actually required to get there. It returns the matched
// model id and shouldSet=true only when a switch is genuinely needed.
//
// shouldSet is false when the constraint is unset/empty, when no available model matches the
// constraint (caller keeps the server default), OR when the session's current model already
// satisfies the constraint. The last case lets the caller skip a needless set_model RPC; this
// mirrors SelectPreferredModel's prompt-path behaviour and removes calls from the per-process
// set_model serialisation queue — the main source of the 8s deadline cascade at server wakeup
// when many auxiliary sessions resume at once (mitto-ykb).
func ResolveAuxModelSwitch(constraint *config.ACPServerConstraint, models *SessionModelState) (modelID string, shouldSet bool) {
	if constraint == nil || constraint.Pattern == "" {
		return "", false
	}
	matched := MatchConstraintOption(constraint, ModelsToConfigOptions(models))
	if matched == "" {
		return "", false
	}
	if models != nil && models.CurrentModelId == matched {
		return matched, false
	}
	return matched, true
}

// SelectPreferredModel resolves an ordered list of preferred-model profile references
// (each naming a global Model profile by ModelName or ModelTag) to the model id the
// session should run with. Entries are walked in preference order. For ModelName, the
// active model is kept when it satisfies the profile criteria; otherwise the profile
// resolves through ResolveProfileModel. For ModelTag, an optional workspace override
// is checked first and used only when its id is present in the agent's advertised
// catalog. Without a valid override, the active model is kept when it satisfies any
// tagged profile; otherwise the first tagged profile that resolves to an available
// model wins (deterministic first-match-wins). Unknown names/tags or entries that
// resolve to no available model are skipped, so resolution continues with the next
// preference.
// Returns "" when nothing matches, signalling the caller to fall back to the baseline.
func SelectPreferredModel(prefs []config.PromptPreferredModel, profiles []config.ModelProfile, models *SessionModelState, overrideLookups ...ModelOverrideLookup) string {
	return selectPreferredModel(prefs, profiles, models, true, firstModelOverrideLookup(overrideLookups))
}

// SelectHighestPriorityModel resolves prefs like SelectPreferredModel but never keeps
// the current model just because it satisfies a preference: each entry resolves to the
// highest-priority profile (profiles-slice order) that matches an available model. Used
// for the initial model of a new conversation, where the agent's default must not
// shadow a higher-priority profile carrying the requested tag. For ModelTag
// preferences, a valid workspace override takes priority over profile matching.
func SelectHighestPriorityModel(prefs []config.PromptPreferredModel, profiles []config.ModelProfile, models *SessionModelState, overrideLookups ...ModelOverrideLookup) string {
	return selectPreferredModel(prefs, profiles, models, false, firstModelOverrideLookup(overrideLookups))
}

func firstModelOverrideLookup(lookups []ModelOverrideLookup) ModelOverrideLookup {
	if len(lookups) == 0 {
		return nil
	}
	return lookups[0]
}

func selectPreferredModel(prefs []config.PromptPreferredModel, profiles []config.ModelProfile, models *SessionModelState, keepCurrent bool, overrideForTag ModelOverrideLookup) string {
	if len(prefs) == 0 || models == nil {
		return ""
	}
	current := ""
	if keepCurrent {
		current = models.CurrentModelId
	}
	var currentName string
	for _, m := range models.AvailableModels {
		if current != "" && m.ModelId == current {
			currentName = m.Name
			break
		}
	}

	for _, pref := range prefs {
		switch {
		case pref.ModelName != "":
			profile := config.ProfileByName(profiles, pref.ModelName)
			if profile == nil {
				continue
			}
			if current != "" && currentSatisfiesProfile(profile, current, currentName) {
				return current
			}
			if resolved := ResolveProfileModel(profile, models); resolved != "" {
				return resolved
			}
		case pref.ModelTag != "":
			// A synthesized catalog contains profile labels, not ids verified by
			// the ACP agent. Never use workspace overrides against it.
			if overrideForTag != nil && !models.Synthesized {
				if override, ok := overrideForTag(pref.ModelTag); ok && override != "" {
					for _, available := range models.AvailableModels {
						if available.ModelId == override {
							if keepCurrent && current == override {
								return current
							}
							return override
						}
					}
				}
			}
			tagged := config.ProfilesByTag(profiles, pref.ModelTag)
			if len(tagged) == 0 {
				continue
			}
			if current != "" {
				for i := range tagged {
					if currentSatisfiesProfile(&tagged[i], current, currentName) {
						return current
					}
				}
			}
			for i := range tagged {
				if resolved := ResolveProfileModel(&tagged[i], models); resolved != "" {
					return resolved
				}
			}
		}
		// Unset/unknown entry, or one that resolved to nothing → try the next preference.
	}
	return ""
}

// currentSatisfiesProfile reports whether the current model (matched by id or display
// name) already satisfies profile's Criteria, letting SelectPreferredModel skip a
// needless SetSessionModel RPC when the active model already belongs to the preferred
// profile.
func currentSatisfiesProfile(profile *config.ModelProfile, currentID, currentName string) bool {
	if profile == nil || profile.Criteria == nil {
		return false
	}
	if config.ConstraintMatchesName(profile.Criteria, currentID) {
		return true
	}
	return currentName != "" && config.ConstraintMatchesName(profile.Criteria, currentName)
}

// ModelDisplayName returns the human-readable Name for modelID from the available
// models, falling back to the raw modelID when no match is found (or models is nil).
func ModelDisplayName(models *SessionModelState, modelID string) string {
	if models == nil || modelID == "" {
		return modelID
	}
	for _, m := range models.AvailableModels {
		if m.ModelId == modelID {
			if m.Name != "" {
				return m.Name
			}
			return modelID
		}
	}
	return modelID
}

// GlobMatchCI reports whether the already-lowercased pattern matches s (case-insensitive).
// Uses path.Match semantics: '*' matches any non-'/' sequence, '?' matches one character.
// Model IDs and display names never contain '/', so '*' effectively matches anything.
func GlobMatchCI(patternLower, s string) bool {
	matched, _ := path.Match(patternLower, strings.ToLower(s))
	return matched
}
