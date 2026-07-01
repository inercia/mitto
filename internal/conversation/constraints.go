package conversation

import (
	"path"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

// ModelsToConfigOptions converts an agent model state into config option values
// (Value=ModelId, Name, Description). Returns nil for a nil/empty model state.
func ModelsToConfigOptions(models *acp.UnstableSessionModelState) []SessionConfigOptionValue {
	if models == nil || len(models.AvailableModels) == 0 {
		return nil
	}
	options := make([]SessionConfigOptionValue, len(models.AvailableModels))
	for i, m := range models.AvailableModels {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		options[i] = SessionConfigOptionValue{
			Value:       string(m.ModelId),
			Name:        m.Name,
			Description: desc,
		}
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
func ResolveProfileModel(profile *config.ModelProfile, models *acp.UnstableSessionModelState) string {
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
func ResolveAuxModelSwitch(constraint *config.ACPServerConstraint, models *acp.UnstableSessionModelState) (modelID string, shouldSet bool) {
	if constraint == nil || constraint.Pattern == "" {
		return "", false
	}
	matched := MatchConstraintOption(constraint, ModelsToConfigOptions(models))
	if matched == "" {
		return "", false
	}
	if models != nil && string(models.CurrentModelId) == matched {
		return matched, false
	}
	return matched, true
}

// SelectPreferredModel resolves an ordered list of preferred-model profile references
// (each naming a global Model profile by ModelName or ModelTag) to the model id the
// session should run with. Entries are walked in preference order and, for each entry,
// the currently active model is checked FIRST: when it already satisfies the entry's
// profile criteria it is kept as-is (returning the current id) so no needless
// SetSessionModel RPC is issued. Only when the active model does not satisfy the entry
// does the function fall back to the model resolved from the entry's profile(s) via
// ResolveProfileModel. A ModelTag entry considers every profile carrying that tag, in
// profiles-slice order, and uses the first one that resolves to an available model
// (deterministic first-match-wins). Unknown names/tags or entries that resolve to no
// available model are skipped, so resolution continues with the next preference.
// Returns "" when nothing matches, signalling the caller to fall back to the baseline.
func SelectPreferredModel(prefs []config.PromptPreferredModel, profiles []config.ModelProfile, models *acp.UnstableSessionModelState) string {
	if len(prefs) == 0 || models == nil {
		return ""
	}
	current := string(models.CurrentModelId)
	var currentName string
	for _, m := range models.AvailableModels {
		if string(m.ModelId) == current {
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
func ModelDisplayName(models *acp.UnstableSessionModelState, modelID string) string {
	if models == nil || modelID == "" {
		return modelID
	}
	for _, m := range models.AvailableModels {
		if string(m.ModelId) == modelID {
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
