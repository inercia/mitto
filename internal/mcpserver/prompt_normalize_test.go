package mcpserver

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// helper: build a WebPrompt with a single picker parameter named pickerName.
func promptWithPicker(name, pickerName string) config.WebPrompt {
	return config.WebPrompt{
		Name: name,
		Parameters: []config.PromptParameter{
			{Name: pickerName, Type: "prompts"},
		},
	}
}

// helper: build a leaf prompt with a text parameter.
func promptWithTextParam(name, paramName string) config.WebPrompt {
	return config.WebPrompt{
		Name: name,
		Parameters: []config.PromptParameter{
			{Name: paramName, Type: "text"},
		},
	}
}

// v1 name-string values must pass through untouched (backward compatibility).
func TestNormalizeMCPArguments_V1PassThrough(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		"Prompt":      "inner-name",
		"Prompt_Args": `{"X":"1"}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	// v1 → same map (identity is acceptable since no rewrites occurred).
	if !reflect.DeepEqual(out, args) {
		t.Fatalf("v1 args should pass through unchanged. got=%v want=%v", out, args)
	}
}

// v2 JSON-encoded picker values are rewritten to v1 sibling-key shape.
func TestNormalizeMCPArguments_V2Rewritten(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	v2 := map[string]any{
		"name": "inner-name",
		"arguments": map[string]string{
			"X": "1",
			"Y": "2",
		},
	}
	raw, err := json.Marshal(v2)
	if err != nil {
		t.Fatalf("marshal v2: %v", err)
	}
	args := map[string]string{"Prompt": string(raw)}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != "inner-name" {
		t.Fatalf("outer picker key should be rewritten to inner name; got %q", out["Prompt"])
	}
	sibling, ok := out["Prompt_Args"]
	if !ok {
		t.Fatalf("expected v1 sibling key 'Prompt_Args' after normalization; got %v", out)
	}
	var inner map[string]string
	if err := json.Unmarshal([]byte(sibling), &inner); err != nil {
		t.Fatalf("sibling not valid JSON: %v (%q)", err, sibling)
	}
	if inner["X"] != "1" || inner["Y"] != "2" {
		t.Fatalf("inner args round-trip mismatch: %v", inner)
	}
}

// An existing v1 sibling wins if the caller supplies both shapes.
func TestNormalizeMCPArguments_V2DoesNotOverwriteExistingSibling(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	raw, _ := json.Marshal(map[string]any{
		"name":      "inner",
		"arguments": map[string]string{"X": "from-v2"},
	})
	args := map[string]string{
		"Prompt":      string(raw),
		"Prompt_Args": `{"X":"from-v1"}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt_Args"] != `{"X":"from-v1"}` {
		t.Fatalf("existing v1 sibling must not be overwritten. got=%q", out["Prompt_Args"])
	}
	// The outer key IS still rewritten (v2 name wins the picker slot).
	if out["Prompt"] != "inner" {
		t.Fatalf("outer picker key should be rewritten; got %q", out["Prompt"])
	}
}

// Non-picker params (text/beadsId/etc.) are never touched, even if their value
// happens to look like a JSON object.
func TestNormalizeMCPArguments_NonPickerValueUntouched(t *testing.T) {
	outer := config.WebPrompt{
		Name: "outer",
		Parameters: []config.PromptParameter{
			{Name: "Notes", Type: "text"},
		},
	}
	args := map[string]string{
		"Notes": `{"name":"looks-like-picker"}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Notes"] != args["Notes"] {
		t.Fatalf("non-picker value must be untouched; got %q want %q", out["Notes"], args["Notes"])
	}
}

// Malformed JSON in a picker slot must NOT be rewritten (treat as v1 name).
func TestNormalizeMCPArguments_MalformedJSONFallsThroughAsV1(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		// starts with '{' but isn't valid JSON
		"Prompt": `{"name": "unterminated`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != args["Prompt"] {
		t.Fatalf("malformed JSON must fall through as v1; got %q", out["Prompt"])
	}
	if _, ok := out["Prompt_Args"]; ok {
		t.Fatalf("no sibling should be synthesised on malformed JSON")
	}
}

// v2 object with an empty Name is treated as v1 (invalid v2, safest to not
// rewrite than to silently blank the picker slot).
func TestNormalizeMCPArguments_V2EmptyNameFallsThroughAsV1(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		"Prompt": `{"name": "", "arguments": {"X":"1"}}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != args["Prompt"] {
		t.Fatalf("v2 with empty name must fall through as v1; got %q", out["Prompt"])
	}
}

// nil / empty inputs are safe.
func TestNormalizeMCPArguments_NilAndEmpty(t *testing.T) {
	if out := normalizeMCPArguments("", nil, nil); out != nil {
		t.Fatalf("nil args should return nil; got %v", out)
	}
	if out := normalizeMCPArguments("outer", map[string]string{}, nil); len(out) != 0 {
		t.Fatalf("empty args should return empty; got %v", out)
	}
	// Unknown prompt name → pass through.
	orig := map[string]string{"X": `{"name":"y"}`}
	if out := normalizeMCPArguments("missing", orig, nil); !reflect.DeepEqual(out, orig) {
		t.Fatalf("unknown prompt should pass through untouched; got %v", out)
	}
}

// buildNestedPromptSchemas populates a catalog for each picker param.
func TestBuildNestedPromptSchemas_PopulatesForPickerParams(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	leaf1 := promptWithTextParam("leaf-a", "X")
	leaf2 := promptWithTextParam("leaf-b", "Y")
	all := []config.WebPrompt{outer, leaf1, leaf2}

	got := buildNestedPromptSchemas(outer.Parameters, all)
	if got == nil {
		t.Fatalf("expected non-nil result")
	}
	inner, ok := got["Prompt"]
	if !ok {
		t.Fatalf("expected catalog under picker param 'Prompt'; got %v", got)
	}
	if _, ok := inner["leaf-a"]; !ok {
		t.Fatalf("expected leaf-a in catalog; got %v", inner)
	}
	if _, ok := inner["leaf-b"]; !ok {
		t.Fatalf("expected leaf-b in catalog; got %v", inner)
	}
}

// Prompts without parameters are skipped in the catalog (noise reduction).
func TestBuildNestedPromptSchemas_SkipsParamlessPrompts(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	leaf := promptWithTextParam("leaf-a", "X")
	bare := config.WebPrompt{Name: "bare-prompt"} // no parameters
	all := []config.WebPrompt{outer, leaf, bare}

	got := buildNestedPromptSchemas(outer.Parameters, all)
	if got == nil {
		t.Fatalf("expected non-nil result")
	}
	inner := got["Prompt"]
	if _, ok := inner["bare-prompt"]; ok {
		t.Fatalf("param-less prompt should not appear in catalog; got %v", inner)
	}
}

// A prompt with no picker parameters yields a nil result (omitempty-friendly).
func TestBuildNestedPromptSchemas_NoPickerParamsReturnsNil(t *testing.T) {
	plain := promptWithTextParam("plain", "X")
	got := buildNestedPromptSchemas(plain.Parameters, []config.WebPrompt{plain})
	if got != nil {
		t.Fatalf("expected nil when no picker params; got %v", got)
	}
}
