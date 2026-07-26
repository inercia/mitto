package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessingStep1_PromptsPredicate verifies the mitto-s1w refactor of
// loop-processing.prompt.yaml Step 1: with `Loop fixing bug` and `Loop
// implementing feature` registered in the workspace prompt view (via
// `.Prompts.EnabledNames`), Step 1 renders no disable-messages; with those
// names absent from the view, Step 1 emits the per-class disable-messages.
// Pins the .Prompts.Enabled predicate wiring at the consumer level.
func TestLoopProcessingStep1_PromptsPredicate(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	step1Excerpt := func(out string) string {
		i := strings.Index(out, "## Step 1")
		if i < 0 {
			return ""
		}
		rel := strings.Index(out[i:], "## Step 2P")
		if rel < 0 {
			return out[i:]
		}
		return out[i : i+rel]
	}

	// Case A: both driver prompts registered + enabled — Step 1 should NOT
	// emit a disable-message for either class.
	ctxOK := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
	}
	funcs := cel.BuildTemplateFuncMap(ctxOK)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxOK, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both enabled): %v", rerr)
	}
	step1 := step1Excerpt(out)
	if strings.Contains(step1, "§B disabled this pass") {
		t.Errorf("both drivers enabled: Step 1 should not disable §B; got:\n%s", step1)
	}
	if strings.Contains(step1, "§C disabled this pass") {
		t.Errorf("both drivers enabled: Step 1 should not disable §C; got:\n%s", step1)
	}
	// The mitto_prompt_get(...) tool-call form must be gone entirely (the
	// bare word "mitto_prompt_get" may still appear in narrative text).
	if strings.Contains(step1, "mitto_prompt_get(") {
		t.Errorf("Step 1 still invokes mitto_prompt_get(...) after refactor:\n%s", step1)
	}

	// Case B: neither driver prompt is registered (zero-value .Prompts) —
	// Step 1 should emit both disable-messages.
	ctxMissing := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		// Prompts left zero-valued -> .Prompts.Enabled returns false.
	}
	funcs2 := cel.BuildTemplateFuncMap(ctxMissing)
	out2, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxMissing, funcs2)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both missing): %v", rerr)
	}
	step1Missing := step1Excerpt(out2)
	if !strings.Contains(step1Missing, "§B disabled this pass") {
		t.Errorf("missing drivers: Step 1 should disable §B; got:\n%s", step1Missing)
	}
	if !strings.Contains(step1Missing, "§C disabled this pass") {
		t.Errorf("missing drivers: Step 1 should disable §C; got:\n%s", step1Missing)
	}
}
