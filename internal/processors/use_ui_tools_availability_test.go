package processors

import (
	"strings"
	"testing"
)

// TestUseUIToolsProcessor_GuardsAgainstPreemptiveUnavailabilityClaims is the
// reproduction test for mitto-qwm: agents were observed announcing "The UI
// tools aren't available in this session" and falling back to a plain-text
// plan WITHOUT ever calling a mitto_ui_* tool. Investigation confirmed the
// tools were registered unconditionally (internal/mcpserver/tool_registration.go),
// the CanPromptUser flag was on, and no backend guard ever fired (the "no UI
// connected" / permission-error strings appear zero times in logs) — the
// unavailability claim was purely hallucinated by the model.
//
// Root cause: the builtin `use-ui-tools` processor
// (config/processors/builtin/use-ui-tools.yaml) tells the agent to ALWAYS use
// the UI tools, but never tells it HOW to determine whether they are
// actually available — so in long sessions the model rationalizes skipping
// them by inventing an availability excuse instead of just calling the tool
// and observing whether it errors.
//
// This test currently FAILS because the processor text has no guard against
// preemptive unavailability claims. The fix phase must add guidance
// instructing the agent to call the tool first and only report unavailability
// if the tool call itself returns an error.
func TestUseUIToolsProcessor_GuardsAgainstPreemptiveUnavailabilityClaims(t *testing.T) {
	const path = "../../config/processors/builtin/use-ui-tools.yaml"

	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if proc == nil {
		t.Fatalf("LoadFile(%q): nil processor (file may be empty)", path)
	}

	if proc.Name != "use-ui-tools" {
		t.Errorf("processor name = %q, want %q", proc.Name, "use-ui-tools")
	}
	if !proc.IsTextMode() {
		t.Errorf("processor must be text-mode (Text set, no Command/Prompt); got Text=%q Command=%q Prompt=%q",
			proc.Text, proc.Command, proc.Prompt)
	}

	// Hallmarks the guidance MUST contain (mitto-qwm fix): an explicit
	// instruction to probe by actually calling the tool, and that the ONLY
	// valid signal of unavailability is the tool call itself returning an
	// error — never a preemptive assumption.
	hallmarks := []string{
		"call the tool first",
		"only report unavailability if the tool call itself returns an error",
	}
	for _, h := range hallmarks {
		if !strings.Contains(proc.Text, h) {
			t.Errorf("use-ui-tools guidance missing hallmark %q (mitto-qwm)\n---\ntext:\n%s\n---", h, proc.Text)
		}
	}

	// Anti-regression: guard against reintroducing an unqualified directive
	// that a model could misread as license to assume unavailability. Look
	// for an explicit "never assume" / "do not assume" phrase pinning the
	// anti-hallucination guard.
	lower := strings.ToLower(proc.Text)
	if !strings.Contains(lower, "never assume") && !strings.Contains(lower, "do not assume") {
		t.Errorf("use-ui-tools guidance missing an explicit 'never/do not assume' unavailability guard (mitto-qwm)\n---\ntext:\n%s\n---", proc.Text)
	}
}
