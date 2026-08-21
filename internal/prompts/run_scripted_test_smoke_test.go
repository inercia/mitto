package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestRunScriptedTest_InstructionsBlock verifies that the optional
// Instructions parameter added to testing/run-scripted-test.prompt.yaml
// renders its dedicated section iff .Args.Instructions is non-empty and
// stays fully collapsed otherwise. Guards against a regression where the
// gate is flipped or the section leaks into every run.
func TestRunScriptedTest_InstructionsBlock(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir: %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("fragment load errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir: %v", err)
	}
	var body string
	for _, p := range list {
		if p.Name == "Run scripted test" {
			body = p.Content
			break
		}
	}
	if body == "" {
		t.Fatalf("prompt %q not found", "Run scripted test")
	}

	const header = "### Additional instructions for this run"

	// Absent → section must not render.
	ctxAbsent := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s1", Name: "N", HasMessages: true},
		Args:    map[string]string{"Test": "test_foo.md", "CreateIssues": "false"},
	}
	outAbsent, err := RenderPromptTemplate("Run scripted test", body,
		ctxAbsent, cel.BuildTemplateFuncMap(ctxAbsent))
	if err != nil {
		t.Fatalf("render (absent): %v", err)
	}
	if strings.Contains(outAbsent, header) {
		t.Errorf("absent branch leaked %q into output", header)
	}

	// Present → section + verbatim body must render.
	const instr = "- Use staging.\n- Skip step 3.\n- Report timings."
	ctxPresent := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s1", Name: "N", HasMessages: true},
		Args: map[string]string{
			"Test":         "test_foo.md",
			"Instructions": instr,
			"CreateIssues": "false",
		},
	}
	outPresent, err := RenderPromptTemplate("Run scripted test", body,
		ctxPresent, cel.BuildTemplateFuncMap(ctxPresent))
	if err != nil {
		t.Fatalf("render (present): %v", err)
	}
	if !strings.Contains(outPresent, header) {
		t.Errorf("present branch missing header %q", header)
	}
	// The verbatim user text must appear (line-by-line — the template
	// interpolates it inside a fenced dash-rule block).
	for _, line := range strings.Split(instr, "\n") {
		if !strings.Contains(outPresent, line) {
			t.Errorf("present branch missing user line %q", line)
		}
	}
}
