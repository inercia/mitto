package processors

import (
	"github.com/inercia/mitto/internal/config"
)

// ResolveProcessorArgs builds the effective argument map for a prompt-mode processor.
//
// Resolution rule: start with each declared parameter's Default value, then
// overlay any per-workspace override from the caller-supplied overrides map
// (non-empty values only; empty values are treated as "not set" and fall back
// to the declared default).
//
// Returns nil when both params and overrides are empty (fast path: nothing to
// do). A non-nil map is always safe to feed into the template .Args context.
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
