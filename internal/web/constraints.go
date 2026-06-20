package web

import (
	"path"
	"regexp"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

// modelsToConfigOptions converts an agent model state into config option values
// (Value=ModelId, Name, Description). Returns nil for a nil/empty model state.
func modelsToConfigOptions(models *acp.UnstableSessionModelState) []SessionConfigOptionValue {
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

// matchConstraintOption finds the best matching option value for a constraint.
// It iterates through all options and returns the last match, so that the latest version wins
// when models are ordered by version. Returns empty string if no match.
func matchConstraintOption(constraint *config.ACPServerConstraint, options []SessionConfigOptionValue) string {
	patternLower := strings.ToLower(constraint.Pattern)
	var matchedValue string
	for _, opt := range options {
		nameLower := strings.ToLower(opt.Name)
		switch constraint.MatchMode {
		case "contains":
			if strings.Contains(nameLower, patternLower) {
				matchedValue = opt.Value
			}
		case "exact":
			if nameLower == patternLower {
				matchedValue = opt.Value
			}
		case "startsWith":
			if strings.HasPrefix(nameLower, patternLower) {
				matchedValue = opt.Value
			}
		case "regex":
			if matched, _ := regexp.MatchString("(?i)"+constraint.Pattern, opt.Name); matched {
				matchedValue = opt.Value
			}
		case "lookAlike":
			// Split pattern into words and check all words appear in the name
			words := strings.Fields(patternLower)
			if len(words) > 0 {
				allFound := true
				for _, word := range words {
					if !strings.Contains(nameLower, word) {
						allFound = false
						break
					}
				}
				if allFound {
					matchedValue = opt.Value
				}
			}
		}
	}
	return matchedValue
}

// resolveAuxModelSwitch decides which model a freshly-created auxiliary session should run
// and whether a SetSessionModel RPC is actually required to get there. It returns the matched
// model id and shouldSet=true only when a switch is genuinely needed.
//
// shouldSet is false when the constraint is unset/empty, when no available model matches the
// constraint (caller keeps the server default), OR when the session's current model already
// satisfies the constraint. The last case lets the caller skip a needless set_model RPC; this
// mirrors selectPreferredModel's prompt-path behaviour and removes calls from the per-process
// set_model serialisation queue — the main source of the 8s deadline cascade at server wakeup
// when many auxiliary sessions resume at once (mitto-ykb).
func resolveAuxModelSwitch(constraint *config.ACPServerConstraint, models *acp.UnstableSessionModelState) (modelID string, shouldSet bool) {
	if constraint == nil || constraint.Pattern == "" {
		return "", false
	}
	matched := matchConstraintOption(constraint, modelsToConfigOptions(models))
	if matched == "" {
		return "", false
	}
	if models != nil && string(models.CurrentModelId) == matched {
		return matched, false
	}
	return matched, true
}

// selectPreferredModel resolves an ordered list of case-insensitive glob patterns to the
// model id the session should run with. Patterns are walked in preference order and, for
// each pattern, the currently active model is checked FIRST: when it already matches the
// pattern it is kept as-is (returning the current id) so no needless SetSessionModel RPC is
// issued. Only when the active model does not match does the function fall back to the first
// other available model matching that pattern. Patterns that match no available model are
// skipped, so resolution continues with the next preference. Matching is glob against both
// ModelId and Name. Returns "" when nothing matches, signalling the caller to fall back to
// the session baseline.
func selectPreferredModel(patterns []string, models *acp.UnstableSessionModelState) string {
	if len(patterns) == 0 || models == nil {
		return ""
	}
	current := string(models.CurrentModelId)
	// Resolve the current model's display name for name-based matching.
	var currentName string
	for _, m := range models.AvailableModels {
		if string(m.ModelId) == current {
			currentName = m.Name
			break
		}
	}
	for _, pattern := range patterns {
		patternLower := strings.ToLower(pattern)
		// Prefer keeping the active model when it already matches this preference.
		if current != "" && (globMatchCI(patternLower, current) ||
			(currentName != "" && globMatchCI(patternLower, currentName))) {
			return current
		}
		// Otherwise switch to the first other available model matching this preference.
		for _, m := range models.AvailableModels {
			if globMatchCI(patternLower, string(m.ModelId)) || globMatchCI(patternLower, m.Name) {
				return string(m.ModelId)
			}
		}
		// Pattern matched nothing → fall through to the next preference.
	}
	return ""
}

// globMatchCI reports whether the already-lowercased pattern matches s (case-insensitive).
// Uses path.Match semantics: '*' matches any non-'/' sequence, '?' matches one character.
// Model IDs and display names never contain '/', so '*' effectively matches anything.
func globMatchCI(patternLower, s string) bool {
	matched, _ := path.Match(patternLower, strings.ToLower(s))
	return matched
}
