package processors

import (
	"regexp"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// argPlaceholderRe matches bash-like ${VAR} and ${VAR:-default} placeholders.
//
//	Group 1: variable name (must start with a letter or underscore).
//	Group 2: the optional ":-default" segment (present only when a default given).
//	Group 3: the default value (the text after ":-").
var argPlaceholderRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// SubstituteArguments replaces bash-like ${VAR} and ${VAR:-default} placeholders
// in text with values from the args map.
//
// Rules:
//   - ${VAR}          — replaced with args["VAR"], or "" if VAR is absent.
//   - ${VAR:-default} — replaced with args["VAR"] when present AND non-empty,
//     otherwise the default. This matches bash ":-" semantics, where the default
//     is used when the variable is unset OR empty.
//   - Surrounding single or double quotes around a default are stripped, so
//     ${VAR:-"a value"} (with VAR unset) yields: a value
//   - A literal ${...} can be emitted by escaping the dollar with a backslash:
//     \${VAR} → ${VAR} (the backslash is stripped and no substitution occurs).
//
// Substitution is applied ONLY by callers that hold an arguments map (named /
// scenario prompts). Ad-hoc user messages are never passed through this
// function, so pasted shell or code containing ${...} is left untouched.
func SubstituteArguments(text string, args map[string]string) string {
	if !strings.Contains(text, "${") {
		return text // Fast path: nothing to substitute
	}

	// Escape handling: a backslash-escaped \${ must be emitted literally as ${
	// with no substitution. Replace \${ with a sentinel (containing a NUL byte,
	// which cannot appear in source text or argument values) before substitution,
	// then restore it to a literal ${ afterwards.
	const sentinelDollarBrace = "\x00MITTO_ARG_ESCAPED\x00"
	text = strings.ReplaceAll(text, `\${`, sentinelDollarBrace)

	result := argPlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		m := argPlaceholderRe.FindStringSubmatch(match)
		// m[1] = name, m[2] = ":-default" (optional), m[3] = default value.
		name := m[1]
		if val, ok := args[name]; ok && val != "" {
			return val
		}
		if m[2] != "" { // A default was provided via ":-".
			return stripSurroundingQuotes(m[3])
		}
		// No value and no default → empty string.
		return ""
	})

	result = strings.ReplaceAll(result, sentinelDollarBrace, "${")
	return result
}

// ResolveProcessorArgs builds the effective argument map for a prompt-mode processor.
//
// Resolution rule: start with each declared parameter's Default value, then
// overlay any per-workspace override from the caller-supplied overrides map
// (non-empty values only; empty values are treated as "not set" and fall back
// to the declared default).
//
// Returns nil when both params and overrides are empty (fast path: nothing to
// substitute). A non-nil map is always safe to pass to SubstituteArguments.
func ResolveProcessorArgs(params []config.PromptParameter, overrides map[string]string) map[string]string {
	if len(params) == 0 && len(overrides) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(params)+len(overrides))
	// Seed from declared defaults.
	for _, p := range params {
		if p.Default != "" {
			resolved[p.Name] = p.Default
		}
	}
	// Overlay workspace overrides (non-empty values win over the declared default).
	for k, v := range overrides {
		if v != "" {
			resolved[k] = v
		}
	}
	return resolved
}

// stripSurroundingQuotes removes a single pair of matching surrounding double
// or single quotes from s, if present.
func stripSurroundingQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
