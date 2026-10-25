package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestImplementSpecRetainsSharedTicketTail(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	spec := renderBuiltinPromptWithFragments(t, "Implement spec", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "spec-session"},
		Args:    map[string]string{"SpecFile": "specs/example.md"},
	})
	for _, shared := range []string{
		"## Ticket conventions and decomposition",
		"## Create beads and wire dependencies",
		"## Final ticket handoff",
	} {
		if !strings.Contains(spec, shared) {
			t.Errorf("shared ticket behavior %q missing from Implement spec", shared)
		}
	}
}
