package config

import (
	"fmt"
	"strings"
	"time"
)

// KnownPromptParameterTypes is the canonical registry of supported parameter types
// for the structured `parameters:` field in .prompt.yaml files.
//
// This slice is the SINGLE SOURCE OF TRUTH for backend type validation.
// It is mirrored by KNOWN_PARAM_TYPES in web/static/utils/prompts.js (frontend)
// and surfaced via MCP tool schemas (sibling bead .2). When adding a new type,
// update BOTH this slice AND the frontend mirror — they must stay in sync.
//
// Type semantics:
//   - beadsId        — a beads issue ID (e.g. "mitto-42")
//   - beadsTitle     — a beads issue title (free text, typically auto-filled)
//   - sessionId      — a Mitto conversation/session UUID
//   - childSessionId — a child conversation/session UUID (relative to the host conversation)
//   - workspaceId    — a Mitto workspace UUID
//   - workspaceFolder — an absolute path to the workspace root directory
//   - acpServer      — an ACP server (agent) name
//   - text           — generic free-form text (the catch-all type)
//   - boolean        — a yes/no flag, rendered as a checkbox; supplied as the
//     string "true" or "false". Boolean parameters never gate
//     menu visibility and are always collected via the dialog
//     (a checkbox always has a definite answer; default false).
var KnownPromptParameterTypes = []string{
	"beadsId",
	"beadsTitle",
	"sessionId",
	"childSessionId",
	"workspaceId",
	"workspaceFolder",
	"acpServer",
	"text",
	"boolean",
}

// IsKnownPromptParameterType reports whether t is a recognised parameter type.
func IsKnownPromptParameterType(t string) bool {
	for _, known := range KnownPromptParameterTypes {
		if t == known {
			return true
		}
	}
	return false
}

// KnownPromptCacheDestinations is the registry of valid cache destination values
// for the PromptParameterCache.Destination field. Only "memory" is valid in v1;
// additional destinations (e.g. "disk") may be added in future versions.
var KnownPromptCacheDestinations = map[string]bool{
	"memory": true,
}

// ParsedTTL parses the TTL field of a PromptParameterCache.
// An empty TTL means "no expiry / conversation lifetime" and returns (0, nil).
// A non-empty TTL must be a valid Go duration string with a positive value;
// otherwise an error is returned.
func (c *PromptParameterCache) ParsedTTL() (time.Duration, error) {
	if c.TTL == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.TTL)
	if err != nil {
		return 0, fmt.Errorf("invalid cache ttl %q: %w", c.TTL, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid cache ttl %q: must be a positive duration", c.TTL)
	}
	return d, nil
}

// ValidatePromptParameters validates a prompt's declared parameters against the
// known type registry and any type-specific menu constraints.
//   - menus is the prompt's raw comma-separated menus string ("" => treated as "prompts").
//   - childSessionId parameters are only valid in prompts targeting the
//     "prompts" and/or "conversation" menus.
//   - Cache blocks (when present) must have a known destination and a valid TTL.
func ValidatePromptParameters(menus string, params []PromptParameter) error {
	for i, param := range params {
		if param.Name == "" {
			return fmt.Errorf("parameter #%d: name must not be empty", i+1)
		}
		if param.Type == "" || !IsKnownPromptParameterType(param.Type) {
			return fmt.Errorf("parameter %q has unknown type %q (must be one of: %s)", param.Name, param.Type, strings.Join(KnownPromptParameterTypes, ", "))
		}
		// multiLine only controls how a free-text field is rendered, so it is
		// only meaningful for the "text" type. Reject it elsewhere to catch
		// misconfiguration early.
		if param.MultiLine && param.Type != "text" {
			return fmt.Errorf("parameter %q: multiLine is only valid for type \"text\", not %q", param.Name, param.Type)
		}
		// Validate the optional cache block.
		if param.Cache != nil {
			if !KnownPromptCacheDestinations[param.Cache.Destination] {
				known := make([]string, 0, len(KnownPromptCacheDestinations))
				for k := range KnownPromptCacheDestinations {
					known = append(known, k)
				}
				return fmt.Errorf("parameter %q: cache destination %q is not valid (must be one of: %s)", param.Name, param.Cache.Destination, strings.Join(known, ", "))
			}
			if _, err := param.Cache.ParsedTTL(); err != nil {
				return fmt.Errorf("parameter %q: %w", param.Name, err)
			}
		}
	}
	// childSessionId menu rule: only valid in "prompts" and/or "conversation" menus.
	for _, param := range params {
		if param.Type != "childSessionId" {
			continue
		}
		parts := strings.Split(menus, ",")
		var menuList []string
		for _, m := range parts {
			if m = strings.TrimSpace(m); m != "" && !strings.HasPrefix(m, "!") {
				menuList = append(menuList, m)
			}
		}
		if len(menuList) == 0 {
			// Empty menus treated as "prompts" — allowed.
			return nil
		}
		for _, m := range menuList {
			if m != "prompts" && m != "conversation" {
				return fmt.Errorf("parameter %q of type childSessionId is only valid in prompts targeting the 'prompts' or 'conversation' menus, but this prompt targets '%s'", param.Name, m)
			}
		}
		return nil // valid menu set; no need to re-check for additional childSessionId params
	}
	return nil
}
