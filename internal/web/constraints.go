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

// matchPreferredModels finds the first model that matches any pattern in patterns.
// Matching is case-insensitive glob against both ModelId and Name; first pattern in
// preference order wins. Returns the matching ModelId, or "" if nothing matches.
func matchPreferredModels(patterns []string, models *acp.UnstableSessionModelState) string {
	if len(patterns) == 0 || models == nil {
		return ""
	}
	for _, pattern := range patterns {
		patternLower := strings.ToLower(pattern)
		for _, m := range models.AvailableModels {
			if globMatchCI(patternLower, string(m.ModelId)) || globMatchCI(patternLower, m.Name) {
				return string(m.ModelId)
			}
		}
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
