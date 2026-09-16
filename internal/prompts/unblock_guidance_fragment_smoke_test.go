package prompts

import (
	"os"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestUnblockGuidanceFragmentRendersInInteractiveConsumers(t *testing.T) {
	const fragmentCall = `template "beads-issues/shared/unblock-guidance"`
	consumers := map[string]string{
		"Explain":               "../../config/prompts/builtin/beads-issues/explain.prompt.yaml",
		"Start work":            "../../config/prompts/builtin/beads-issues/work.prompt.yaml",
		"Show status":           "../../config/prompts/builtin/beads-issues/status.prompt.yaml",
		"Reevaluate all issues": "../../config/prompts/builtin/beads/reevaluate.prompt.yaml",
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "session-unblock",
			Name:          "Unblocking",
			HasMessages:   true,
			BeadsIssue:    "mitto-blocked",
			HasBeadsIssue: true,
		},
		Args: map[string]string{"IssueID": "mitto-blocked"},
	}

	for name, path := range consumers {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if got := strings.Count(string(source), fragmentCall); got != 1 {
				t.Fatalf("%s fragment-call count = %d, want 1", name, got)
			}

			out := renderBuiltinPromptWithFragments(t, name, ctx)
			for _, hallmark := range []string{
				"**Verify before declaring it blocked.**",
				"**Required blocked explanation — make the cause understandable.**",
				"We are trying to **<goal or outcome>**, **but** cannot continue until:",
				"**Propose the smallest concrete next step.**",
				"**one direct, specific question or action request**",
				`mitto_ui_options(self_id: "session-unblock"`,
				`mitto_ui_textbox(self_id: "session-unblock")`,
				"**Be honest when the user cannot unblock it.**",
			} {
				if !strings.Contains(out, hallmark) {
					t.Errorf("%s rendered output missing unblock hallmark %q", name, hallmark)
				}
			}
			if name == "Show status" && !strings.Contains(out, "bd comments mitto-blocked") {
				t.Error("Show status must load comments for blocked as well as deferred beads")
			}
		})
	}
}

func TestReevaluateAllIssuesOffersAndAppliesApprovedUnblocking(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Reevaluate all issues", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "session-reevaluate", Name: "Reevaluate", HasMessages: true},
	})

	for _, hallmark := range []string{
		"### Unblocking opportunities",
		"bd comments <bead-id>",
		`mitto_ui_form(self_id: "session-reevaluate")`,
		"labelled field per bead instead of opening a sequence of dialogs",
		`bd update <bead-id> --acceptance "<full approved acceptance criteria>"`,
		"bd update <bead-id> --remove-label needs-human --defer ''",
		`printf '%s' "$decision_note" | bd comment <bead-id> --stdin`,
		"Only report the bead as",
		"unblocked when the actual tracker state confirms it",
	} {
		if !strings.Contains(out, hallmark) {
			t.Errorf("Reevaluate all issues missing unblocking hallmark %q", hallmark)
		}
	}
}
