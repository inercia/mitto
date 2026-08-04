package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// This file pins the mitto-bz1 audit findings: the three builtin prompts that
// declare a `type: prompts` parameter must keep consuming the picked prompt's
// inner `<Picker>_Args` companion the way the audit classified them, so a
// future edit cannot silently regress a correct consumer back into the
// audit's classification-1 bug (bare PromptText, inner args dropped).

// TestPromptUntil_ForwardsPickedPromptInnerArgs pins loop/prompt-until's
// "already correct" classification: the picked prompt is rendered via
// PromptTextWithArgs + ArgsMap "Prompt_Args", so the picked prompt's OWN
// parameter values actually reach its body instead of being silently
// dropped (audit classification 1 would be a bare `PromptText .Args.Prompt`).
func TestPromptUntil_ForwardsPickedPromptInnerArgs(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("..", "..", "config", "prompts", "builtin", "loop", "prompt-until.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("loop/prompt-until.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	if !strings.Contains(body, `PromptTextWithArgs .Args.Prompt (ArgsMap "Prompt_Args")`) {
		t.Fatalf("prompt-until.prompt.yaml: expected `PromptTextWithArgs .Args.Prompt (ArgsMap \"Prompt_Args\")`; not found in body:\n%s", body)
	}

	resolver := func(name string) (string, error) {
		if name == "Picked hook" {
			return `picked-inner-value={{ .Args.Foo }}`, nil
		}
		return "", fmt.Errorf("resolver: unknown %q", name)
	}
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "loop-1"},
		Args: map[string]string{
			"Condition":   "tests pass",
			"Commit":      "false",
			"Prompt":      "Picked hook",
			"Prompt_Args": `{"Foo":"bar"}`,
		},
		PromptTextResolver: resolver,
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, rerr := RenderPromptTemplate("loop-prompt-until", body, ctx, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate: %v", rerr)
	}
	if !strings.Contains(out, "picked-inner-value=bar") {
		t.Errorf("rendered output missing the picked prompt's substituted inner arg (\"picked-inner-value=bar\") — the Prompt_Args companion was not forwarded. Got:\n%s", out)
	}
}

// TestLoopProcessingStep5H_ForwardsPickedPromptInnerArgs pins
// beads-issues/loop-processing's Step 5H "already correct" classification
// (spawn/delegate case): the picked post-task hook's decoded ArgsMap is
// forwarded as `arguments:` on the spawned mitto_conversation_new call.
func TestLoopProcessingStep5H_ForwardsPickedPromptInnerArgs(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("..", "..", "config", "prompts", "builtin", "beads-issues", "loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args: map[string]string{
			"FixBugs":                  "true",
			"WorkOnFeatures":           "true",
			"PostIterationPrompt":      "Run tests",
			"PostIterationPrompt_Args": `{"Foo":"bar"}`,
		},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctx, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate: %v", rerr)
	}

	i := strings.Index(out, "## Step 5H")
	if i < 0 {
		t.Fatal("Step 5H section not found in rendered output (expected since PostIterationPrompt is set)")
	}
	j := strings.Index(out[i:], "## Step 6")
	if j < 0 {
		t.Fatal("Step 6 boundary not found after Step 5H")
	}
	step5H := out[i : i+j]

	if !strings.Contains(step5H, `prompt_name: "Run tests"`) {
		t.Errorf("Step 5H: expected spawn call to carry prompt_name: \"Run tests\"; got:\n%s", step5H)
	}
	if !strings.Contains(step5H, `arguments: { "Foo": "bar" }`) {
		t.Errorf("Step 5H: expected the decoded PostIterationPrompt_Args to be forwarded as arguments: { \"Foo\": \"bar\" }; got:\n%s", step5H)
	}
}

// TestSpecializePrompts_PickerIsNameOnly pins misc/specialize-prompts'
// "name-only, intentional" classification: the picked prompt is only ever
// used as an edit SUBJECT (mitto_prompt_get / mitto_prompt_update) and its
// body is never rendered/executed, so it must never invoke PromptText or
// PromptTextWithArgs on the picker value. A future edit that starts
// rendering the picked prompt inline would silently change this prompt's
// semantics (from "edit this prompt" to "run this prompt").
func TestSpecializePrompts_PickerIsNameOnly(t *testing.T) {
	path := filepath.Join("..", "..", "config", "prompts", "builtin", "misc", "specialize-prompts.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("misc/specialize-prompts.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	if strings.Contains(body, "PromptText") {
		t.Errorf("specialize-prompts.prompt.yaml: expected no PromptText/PromptTextWithArgs call (picked prompt is a name-only edit subject); found one in body:\n%s", body)
	}
	if !strings.Contains(body, "mitto_prompt_get") || !strings.Contains(body, "mitto_prompt_update") {
		t.Errorf("specialize-prompts.prompt.yaml: expected mitto_prompt_get/mitto_prompt_update edit-subject usage; not found")
	}
}
