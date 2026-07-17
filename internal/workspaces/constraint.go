package workspaces

import (
	"regexp"
	"strings"
)

// ACPServerConstraint defines a pattern-matching rule for auto-selecting config option values.
// Used by the constraints system to automatically configure sessions on startup.
type ACPServerConstraint struct {
	// MatchMode determines how Pattern is matched against option names.
	// Valid values: "contains", "exact", "startsWith", "regex", "lookAlike"
	MatchMode string `json:"matchMode"`
	// Pattern is the text to match against option names (e.g., "Opus 4.6").
	Pattern string `json:"pattern"`
}

// ConstraintMatchesName reports whether name matches the constraint's Pattern under
// its MatchMode. It is the single-string core of the constraint match engine, shared by
// MatchConstraintOption (which applies it across a list of option names) and by model-tag
// resolution, so the contains/exact/startsWith/regex/lookAlike semantics never drift.
// Matching is case-insensitive (regex uses the (?i) flag). A nil constraint never matches.
func ConstraintMatchesName(c *ACPServerConstraint, name string) bool {
	if c == nil {
		return false
	}
	patternLower := strings.ToLower(c.Pattern)
	nameLower := strings.ToLower(name)
	switch c.MatchMode {
	case "contains":
		return strings.Contains(nameLower, patternLower)
	case "exact":
		return nameLower == patternLower
	case "startsWith":
		return strings.HasPrefix(nameLower, patternLower)
	case "regex":
		matched, _ := regexp.MatchString("(?i)"+c.Pattern, name)
		return matched
	case "lookAlike":
		words := strings.Fields(patternLower)
		if len(words) == 0 {
			return false
		}
		for _, word := range words {
			if !strings.Contains(nameLower, word) {
				return false
			}
		}
		return true
	}
	return false
}
