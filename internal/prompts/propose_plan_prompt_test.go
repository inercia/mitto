package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestProposePlanPrompt_SimpleChoiceAndSharedTicketTail(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin/docs", "propose-a-plan.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("docs/propose-a-plan.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if prompt.Name != "Propose a plan" || prompt.EnabledWhen != `CommandExists("bd")` {
		t.Errorf("metadata = name %q, enabledWhen %q", prompt.Name, prompt.EnabledWhen)
	}

	plan := renderBuiltinPromptWithFragments(t, "Propose a plan", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "plan-session", HasMessages: true},
	})
	for _, want := range []string{
		"simple** only when every condition below is true",
		"modify one existing source, configuration, or documentation file",
		"adds no external dependency",
		"File beads tickets and stop",
		"Skip ticket creation and implement here",
		"For a **complex** task, do not offer direct implementation",
		"**Acceptance criteria**: concrete, testable",
		"bd dep add <blocked-id> <blocker-id>",
		"Retrospective coverage and coherence pass",
		"Final ticket handoff",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("Propose a plan render missing %q", want)
		}
	}
	for _, old := range []string{
		"Execute the plan in a new child conversation",
		"Optional: file this as a beads issue",
	} {
		if strings.Contains(plan, old) {
			t.Errorf("Propose a plan retained obsolete flow %q", old)
		}
	}

	spec := renderBuiltinPromptWithFragments(t, "Implement spec", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "spec-session"},
		Args:    map[string]string{"SpecFile": "specs/example.md"},
	})
	for _, shared := range []string{
		"## Ticket conventions and decomposition",
		"## Create beads and wire dependencies",
		"## Final ticket handoff",
	} {
		if !strings.Contains(plan, shared) || !strings.Contains(spec, shared) {
			t.Errorf("shared ticket behavior %q missing from one or both prompts", shared)
		}
	}
}
