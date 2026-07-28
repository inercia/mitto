// prompt_normalize.go: normalization layer for MCP prompt-dispatch inputs
// (mitto-47y.6.3). Converts the v2 wire shape for `type: prompts` picker
// values — a JSON-encoded object {"name": "X", "arguments": {...}} — into the
// canonical v1 sibling-key shape (Picker=X, Picker_Args=<inner-args-json>)
// before the existing dispatcher and template engine see it. v1 string values
// pass through unchanged so old callers keep working.
package mcpserver

import (
	"encoding/json"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// pickerParamType is the parameter type marker for prompt-picker parameters.
// Mirrors internal/prompts.KnownPromptParameterTypes "prompts" entry.
const pickerParamType = "prompts"

// v2PickerValue is the shape a picker argument may take on the MCP wire in
// addition to a bare inner-prompt-name string. When present, the normalizer
// rewrites it to the v1 sibling-key shape used internally by the dispatcher.
type v2PickerValue struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// normalizeMCPArguments detects v2 JSON-encoded picker values in args and
// rewrites them to the v1 sibling-key shape. It never returns nil; a nil or
// empty input map is returned as-is. The prompt's declared parameters (from
// the resolved WebPrompt) are consulted so only picker (`type: prompts`)
// values are considered — every other value passes through verbatim.
//
// When promptName is empty or not found in prompts, the input is returned
// unchanged (no schema, no normalization). This is intentional: free-text
// dispatches (no prompt_name) never carry v2 pickers by construction.
//
// The v2 shape is recognised by two conjunctive tests to avoid mangling
// legitimate JSON-looking prompt names:
//  1. The parameter's declared type is "prompts".
//  2. The value parses cleanly as {"name": "X", ...} with a non-empty Name.
//
// On success the outer key is set to Name and a sibling "<key>_Args" key is
// populated with a JSON-encoded map of the inner arguments (matching the
// dispatcher's existing ArgsMap semantics — see rules/07-prompts.md).
// A sibling key that already exists in args is NOT overwritten (v1 callers
// win in the unlikely case they mix shapes).
func normalizeMCPArguments(promptName string, args map[string]string, prompts []config.WebPrompt) map[string]string {
	if len(args) == 0 || promptName == "" {
		return args
	}
	params := lookupPromptParameters(promptName, prompts)
	if len(params) == 0 {
		return args
	}
	// Copy-on-write: only allocate a new map if we actually rewrite anything.
	var out map[string]string
	for _, p := range params {
		if p.Type != pickerParamType {
			continue
		}
		raw, ok := args[p.Name]
		if !ok || raw == "" {
			continue
		}
		v, decoded := decodeV2Picker(raw)
		if !decoded {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(args)+1)
			for k, val := range args {
				out[k] = val
			}
		}
		out[p.Name] = v.Name
		sibling := p.Name + "_Args"
		if _, existing := out[sibling]; !existing {
			innerJSON, err := json.Marshal(v.Arguments)
			if err == nil {
				out[sibling] = string(innerJSON)
			} else {
				out[sibling] = "{}"
			}
		}
	}
	if out == nil {
		return args
	}
	return out
}

// decodeV2Picker attempts to parse raw as a v2 picker value. Returns the
// decoded value and true only when the JSON is a well-formed object with a
// non-empty Name field. Any other input (bare strings, malformed JSON, JSON
// arrays, objects without Name) is treated as v1 and returns false.
func decodeV2Picker(raw string) (v2PickerValue, bool) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return v2PickerValue{}, false
	}
	var v v2PickerValue
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return v2PickerValue{}, false
	}
	if strings.TrimSpace(v.Name) == "" {
		return v2PickerValue{}, false
	}
	return v, true
}

// lookupPromptParameters resolves promptName (case-insensitive) against the
// merged prompt list and returns its declared parameters, or nil when the
// prompt is not found.
func lookupPromptParameters(promptName string, prompts []config.WebPrompt) []config.PromptParameter {
	for _, p := range prompts {
		if strings.EqualFold(p.Name, promptName) {
			return p.Parameters
		}
	}
	return nil
}

// buildNestedPromptSchemas returns a catalog of inner parameter schemas for
// every `type: prompts` picker parameter declared on promptParams. The outer
// key is the picker parameter's Name; the inner map keys are candidate
// inner-prompt names, and values are the inner prompt's own Parameters slice.
//
// The catalog is populated from allPrompts (the same merged list used by
// mitto_prompt_list/get). Only inner prompts that themselves declare
// parameters are included — prompts with no parameters do not need a
// nested-args map at dispatch time so surfacing them here would be noise.
//
// Returns nil (not an empty map) when there are no picker params or no
// inner prompts with parameters, so JSON `omitempty` drops the field on
// unrelated prompt entries.
func buildNestedPromptSchemas(promptParams []config.PromptParameter, allPrompts []config.WebPrompt) map[string]map[string][]config.PromptParameter {
	var result map[string]map[string][]config.PromptParameter
	for _, p := range promptParams {
		if p.Type != pickerParamType {
			continue
		}
		catalog := make(map[string][]config.PromptParameter)
		for _, inner := range allPrompts {
			if len(inner.Parameters) == 0 {
				continue
			}
			catalog[inner.Name] = inner.Parameters
		}
		if len(catalog) == 0 {
			continue
		}
		if result == nil {
			result = make(map[string]map[string][]config.PromptParameter)
		}
		result[p.Name] = catalog
	}
	return result
}
