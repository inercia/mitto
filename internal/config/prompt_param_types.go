package config

import (
	"fmt"
	"strings"
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
var KnownPromptParameterTypes = []string{
	"beadsId",
	"beadsTitle",
	"sessionId",
	"childSessionId",
	"workspaceId",
	"workspaceFolder",
	"acpServer",
	"text",
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

// ValidatePromptParameters validates a prompt's declared parameters against the
// known type registry and any type-specific menu constraints.
//   - menus is the prompt's raw comma-separated menus string ("" => treated as "prompts").
//   - childSessionId parameters are only valid in prompts targeting the
//     "prompts" and/or "conversation" menus.
func ValidatePromptParameters(menus string, params []PromptParameter) error {
	for i, param := range params {
		if param.Name == "" {
			return fmt.Errorf("parameter #%d: name must not be empty", i+1)
		}
		if param.Type == "" || !IsKnownPromptParameterType(param.Type) {
			return fmt.Errorf("parameter %q has unknown type %q (must be one of: %s)", param.Name, param.Type, strings.Join(KnownPromptParameterTypes, ", "))
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
			if m = strings.TrimSpace(m); m != "" {
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
