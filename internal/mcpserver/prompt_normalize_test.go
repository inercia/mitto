package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/inercia/mitto/internal/cel"
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

// Prompt-name lookup is case-insensitive (matches lookupPromptParameters and
// the rest of the prompt-resolution pipeline).
func TestNormalizeMCPArguments_PromptNameIsCaseInsensitive(t *testing.T) {
	outer := promptWithPicker("MyOuter", "Prompt")
	raw, _ := json.Marshal(map[string]any{
		"name":      "inner",
		"arguments": map[string]string{"X": "1"},
	})
	args := map[string]string{"Prompt": string(raw)}
	// caller uses lower-case; lookup must still resolve the picker parameter.
	out := normalizeMCPArguments("myouter", args, []config.WebPrompt{outer})
	if out["Prompt"] != "inner" {
		t.Fatalf("case-insensitive lookup should rewrite picker slot; got %q", out["Prompt"])
	}
	if _, ok := out["Prompt_Args"]; !ok {
		t.Fatalf("case-insensitive lookup should synthesise sibling; got %v", out)
	}
}

// A prompt with two picker params must have both normalized independently and
// each get its own sibling _Args key.
func TestNormalizeMCPArguments_MultiplePickerParams(t *testing.T) {
	outer := config.WebPrompt{
		Name: "outer",
		Parameters: []config.PromptParameter{
			{Name: "First", Type: "prompts"},
			{Name: "Second", Type: "prompts"},
			{Name: "Notes", Type: "text"},
		},
	}
	firstV2, _ := json.Marshal(map[string]any{"name": "a", "arguments": map[string]string{"X": "1"}})
	secondV2, _ := json.Marshal(map[string]any{"name": "b", "arguments": map[string]string{"Y": "2"}})
	args := map[string]string{
		"First":  string(firstV2),
		"Second": string(secondV2),
		"Notes":  "unchanged",
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["First"] != "a" || out["Second"] != "b" {
		t.Fatalf("both picker slots should be rewritten; got First=%q Second=%q", out["First"], out["Second"])
	}
	var f, s map[string]string
	if err := json.Unmarshal([]byte(out["First_Args"]), &f); err != nil || f["X"] != "1" {
		t.Fatalf("First_Args sibling wrong; got %q err=%v", out["First_Args"], err)
	}
	if err := json.Unmarshal([]byte(out["Second_Args"]), &s); err != nil || s["Y"] != "2" {
		t.Fatalf("Second_Args sibling wrong; got %q err=%v", out["Second_Args"], err)
	}
	if out["Notes"] != "unchanged" {
		t.Fatalf("non-picker value must be untouched; got %q", out["Notes"])
	}
}

// A v2 payload with an empty arguments map is still valid — the outer key is
// rewritten to Name and the sibling is populated with "{}" (renderer-safe).
func TestNormalizeMCPArguments_V2EmptyArgumentsMapProducesEmptySibling(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		"Prompt": `{"name": "inner", "arguments": {}}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != "inner" {
		t.Fatalf("outer picker key should be rewritten; got %q", out["Prompt"])
	}
	sibling, ok := out["Prompt_Args"]
	if !ok {
		t.Fatalf("expected sibling key even when arguments is empty; got %v", out)
	}
	var inner map[string]string
	if err := json.Unmarshal([]byte(sibling), &inner); err != nil {
		t.Fatalf("sibling must be valid JSON even when empty; got %q err=%v", sibling, err)
	}
	if len(inner) != 0 {
		t.Fatalf("expected empty inner map; got %v", inner)
	}
}

// A v2 payload where arguments is entirely omitted (only "name" set) is also
// accepted; the sibling is JSON "null" or "{}"-shape from the marshal of a nil
// map — both render safely through ArgsMap.
func TestNormalizeMCPArguments_V2MissingArgumentsField(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		"Prompt": `{"name": "inner"}`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != "inner" {
		t.Fatalf("outer picker key should be rewritten; got %q", out["Prompt"])
	}
	sibling, ok := out["Prompt_Args"]
	if !ok {
		t.Fatalf("expected sibling key even when arguments field is absent; got %v", out)
	}
	// json.Marshal of a nil map[string]string emits "null" — ArgsMap treats an
	// empty string as an empty map, and "null" unmarshals to a nil map too.
	// Either way the render side must not blow up.
	if sibling != "null" && sibling != "{}" {
		t.Fatalf("unexpected sibling shape for missing arguments; got %q", sibling)
	}
}

// A JSON array value in a picker slot is rejected as v2 (only object shape is
// recognised) and passes through unchanged.
func TestNormalizeMCPArguments_JSONArrayFallsThroughAsV1(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	args := map[string]string{
		"Prompt": `["not", "a", "picker"]`,
	}
	out := normalizeMCPArguments("outer", args, []config.WebPrompt{outer})
	if out["Prompt"] != args["Prompt"] {
		t.Fatalf("JSON array must fall through as v1; got %q", out["Prompt"])
	}
	if _, ok := out["Prompt_Args"]; ok {
		t.Fatalf("no sibling should be synthesised for a JSON array")
	}
}

// buildNestedPromptSchemas: when the outer prompt itself carries parameters,
// it MAY appear as an "inner" candidate in the catalog. This is intentional —
// the catalog does not know which prompts the user considers valid picks; it
// just reports every parametered prompt in the workspace. This test pins that
// behavior so a later "exclude self" refactor is noticed.
func TestBuildNestedPromptSchemas_OuterPromptWithParamsAppearsInCatalog(t *testing.T) {
	outer := config.WebPrompt{
		Name: "outer",
		Parameters: []config.PromptParameter{
			{Name: "Prompt", Type: "prompts"},
			{Name: "Notes", Type: "text"}, // gives the outer prompt >0 params
		},
	}
	leaf := promptWithTextParam("leaf-a", "X")
	got := buildNestedPromptSchemas(outer.Parameters, []config.WebPrompt{outer, leaf})
	if got == nil {
		t.Fatalf("expected non-nil result")
	}
	inner := got["Prompt"]
	if _, ok := inner["outer"]; !ok {
		t.Fatalf("outer prompt with params should appear in its own picker catalog (pinned behavior); got %v", inner)
	}
	if _, ok := inner["leaf-a"]; !ok {
		t.Fatalf("expected leaf-a in catalog; got %v", inner)
	}
}

// buildNestedPromptSchemas: empty allPrompts → nil (nothing to catalog).
func TestBuildNestedPromptSchemas_EmptyAllPromptsReturnsNil(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")
	got := buildNestedPromptSchemas(outer.Parameters, nil)
	if got != nil {
		t.Fatalf("expected nil when allPrompts is empty; got %v", got)
	}
}

// -----------------------------------------------------------------------------
// End-to-end round-trip test (mitto-47y.6.3 acceptance criterion #3):
// A v2 dispatch, after passing through normalizeMCPArguments, must render
// through the CEL template FuncMap identically to a v1 dispatch with the same
// inner name + inner arguments.
// -----------------------------------------------------------------------------

// renderOuter renders the canonical nested-args body:
//
//	{{ PromptTextWithArgs .Args.Prompt (ArgsMap "Prompt_Args") }}
//
// using the args map as the outer .Args scope, and a resolver that maps any
// inner name to a leaf body that echoes {{ .Args.X }} and {{ .Args.Y }}.
func renderOuter(t *testing.T, args map[string]string) string {
	t.Helper()
	ctx := &cel.PromptEnabledContext{
		Args: args,
		PromptTextResolver: func(name string) (string, error) {
			return fmt.Sprintf("leaf(%s) X=%s Y=%s", name, "{{ .Args.X }}", "{{ .Args.Y }}"), nil
		},
	}
	fm := cel.BuildTemplateFuncMap(ctx)
	tmpl, err := template.New("outer").Option("missingkey=zero").Funcs(fm).Parse(
		`{{ PromptTextWithArgs .Args.Prompt (ArgsMap "Prompt_Args") }}`,
	)
	if err != nil {
		t.Fatalf("parse outer template: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, map[string]any{"Args": args}); err != nil {
		t.Fatalf("execute outer template: %v", err)
	}
	return buf.String()
}

// v2 input, once normalized, must produce byte-identical output to v1.
func TestNormalizeMCPArguments_RoundTrip_V2RendersIdenticallyToV1(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")

	// v1 baseline: caller already supplied the sibling.
	v1Args := map[string]string{
		"Prompt":      "inner-prompt",
		"Prompt_Args": `{"X":"one","Y":"two"}`,
	}
	v1Rendered := renderOuter(t, v1Args)
	if !strings.Contains(v1Rendered, "leaf(inner-prompt)") {
		t.Fatalf("v1 baseline did not render leaf; got %q", v1Rendered)
	}
	if !strings.Contains(v1Rendered, "X=one") || !strings.Contains(v1Rendered, "Y=two") {
		t.Fatalf("v1 baseline did not interpolate inner args; got %q", v1Rendered)
	}

	// v2 input: single JSON-encoded picker value.
	v2Raw, _ := json.Marshal(map[string]any{
		"name": "inner-prompt",
		"arguments": map[string]string{
			"X": "one",
			"Y": "two",
		},
	})
	v2Args := map[string]string{"Prompt": string(v2Raw)}
	normalized := normalizeMCPArguments("outer", v2Args, []config.WebPrompt{outer})
	v2Rendered := renderOuter(t, normalized)

	if v2Rendered != v1Rendered {
		t.Fatalf("v2-normalized render must match v1 render byte-for-byte.\n  v1: %q\n  v2: %q", v1Rendered, v2Rendered)
	}
}

// A v2 dispatch whose inner arguments map is empty must render the same as a
// v1 dispatch with an empty (or missing) _Args sibling — no residual "X=one"
// values from a prior render leak into the leaf.
func TestNormalizeMCPArguments_RoundTrip_V2EmptyInnerArgsMatchesV1(t *testing.T) {
	outer := promptWithPicker("outer", "Prompt")

	// v1 baseline with explicit empty sibling.
	v1Args := map[string]string{
		"Prompt":      "inner-prompt",
		"Prompt_Args": `{}`,
	}
	v1Rendered := renderOuter(t, v1Args)

	// v2 with empty arguments field.
	v2Raw, _ := json.Marshal(map[string]any{
		"name":      "inner-prompt",
		"arguments": map[string]string{},
	})
	v2Args := map[string]string{"Prompt": string(v2Raw)}
	normalized := normalizeMCPArguments("outer", v2Args, []config.WebPrompt{outer})
	v2Rendered := renderOuter(t, normalized)

	if v2Rendered != v1Rendered {
		t.Fatalf("v2-empty-args render must match v1-empty-sibling render.\n  v1: %q\n  v2: %q", v1Rendered, v2Rendered)
	}
	// Sanity: with missingkey=zero + fail-open, both should render the leaf
	// with empty inner values, not error out.
	if !strings.Contains(v1Rendered, "leaf(inner-prompt)") {
		t.Fatalf("expected leaf render even with empty inner args; got %q", v1Rendered)
	}
}
