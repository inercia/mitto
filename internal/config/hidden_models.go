package config

import (
	"fmt"
	"regexp"
	"strings"
)

// HiddenModelPolicy is a compiled per-server set of hidden-model rules.
// Rules are RE2 patterns matched case-insensitively against a model ID or name.
type HiddenModelPolicy struct {
	server   string
	patterns []*regexp.Regexp
}

// HiddenModelValidationError identifies the first invalid rule.
type HiddenModelValidationError struct {
	Server  string
	Index   int
	Rule    string
	Field   string
	Message string
}

func (e *HiddenModelValidationError) Error() string {
	return fmt.Sprintf("server %q: %s: %s", e.Server, e.Field, e.Message)
}

// NormalizeHiddenModels drops blank rows, preserving order and the content of
// every nonblank rule (no trimming). Nil stays nil; empty stays empty.
func NormalizeHiddenModels(rules []string) []string {
	if rules == nil {
		return nil
	}
	result := make([]string, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			continue
		}
		result = append(result, rule)
	}
	return result
}

// ValidateHiddenModels compiles each nonblank rule, reporting the first failure
// with an index into the submitted slice.
func ValidateHiddenModels(server string, rules []string) error {
	for i, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			continue
		}
		if _, err := regexp.Compile("(?i)" + rule); err != nil {
			return &HiddenModelValidationError{
				Server:  server,
				Index:   i,
				Rule:    rule,
				Field:   fmt.Sprintf("hidden_models[%d]", i),
				Message: fmt.Sprintf("invalid regular expression: %v", err),
			}
		}
	}
	return nil
}

// CompileHiddenModels validates and compiles the rules for a configured server.
func CompileHiddenModels(server string, rules []string) (*HiddenModelPolicy, error) {
	if err := ValidateHiddenModels(server, rules); err != nil {
		return nil, err
	}
	p := &HiddenModelPolicy{server: server}
	for _, rule := range NormalizeHiddenModels(rules) {
		p.patterns = append(p.patterns, regexp.MustCompile("(?i)"+rule))
	}
	return p, nil
}

// MergeHiddenModelPolicies returns a policy that hides a model when any non-nil
// input hides it. Nil inputs are skipped; when every input is nil the result is
// nil, meaning no rules. Inputs are never mutated.
func MergeHiddenModelPolicies(policies ...*HiddenModelPolicy) *HiddenModelPolicy {
	var merged *HiddenModelPolicy
	for _, p := range policies {
		if p == nil {
			continue
		}
		if merged == nil {
			merged = &HiddenModelPolicy{server: p.server}
		}
		merged.patterns = append(merged.patterns, p.patterns...)
	}
	return merged
}

// Hides reports whether any rule matches the model ID or display name.
// A nil or empty policy hides nothing.
func (p *HiddenModelPolicy) Hides(id, name string) bool {
	if p == nil {
		return false
	}
	for _, re := range p.patterns {
		if re.MatchString(id) || re.MatchString(name) {
			return true
		}
	}
	return false
}

// FilterHiddenModels returns a new slice of the visible items, preserving order
// and elements; the input is never mutated. A nil policy returns a copy.
func FilterHiddenModels[T any](p *HiddenModelPolicy, items []T, idName func(T) (string, string)) []T {
	if items == nil {
		return nil
	}
	result := make([]T, 0, len(items))
	for _, item := range items {
		id, name := idName(item)
		if p.Hides(id, name) {
			continue
		}
		result = append(result, item)
	}
	return result
}
