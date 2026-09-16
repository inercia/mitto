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
			if !containsBoundedToken(nameLower, word) {
				return false
			}
		}
		return true
	}
	return false
}

// isASCIIAlnum reports whether b is an ASCII letter or digit.
func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// containsBoundedToken reports whether word occurs in name as a standalone
// token: flanked on both sides by either a non-alphanumeric byte or the
// start/end of the string. This prevents a short lookAlike pattern word such
// as "5" from matching inside an unrelated larger run of digits/letters such
// as "500k" (mitto-bx4), while still allowing tokens bounded by punctuation
// (hyphens, parentheses, periods between words) or whitespace to match.
func containsBoundedToken(name, word string) bool {
	if word == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(name[start:], word)
		if idx == -1 {
			return false
		}
		idx += start
		end := idx + len(word)
		leftOK := idx == 0 || !isASCIIAlnum(name[idx-1])
		rightOK := end == len(name) || !isASCIIAlnum(name[end])
		if leftOK && rightOK {
			return true
		}
		start = idx + 1
	}
}
