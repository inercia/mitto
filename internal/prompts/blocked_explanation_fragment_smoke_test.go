package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestBlockedExplanationFragmentRendersAcrossBlockedIssuePaths(t *testing.T) {
	consumers := []string{
		"Explain",
		"Start work",
		"Show status",
		"Reevaluate all issues",
		"Bug fix — reproduce phase",
		"Loop fixing bug",
		"Loop processing tasks",
		"Mention — driver",
		"Triage untriaged bugs",
		"Investigate ALL more",
		"Overview",
		"Status ALL in-progress",
		"Status ONE in-progress",
		"Recalculate issue dependencies",
		"Assess issue readiness",
		"Start working on ready",
	}
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "session-blocked-explanation",
			Name:          "Blocked explanation",
			HasMessages:   true,
			BeadsIssue:    "mitto-blocked",
			HasBeadsIssue: true,
		},
		Args: map[string]string{
			"IssueID": "mitto-blocked",
			"Commit":  "true",
		},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
	}

	for _, name := range consumers {
		t.Run(name, func(t *testing.T) {
			out := renderBuiltinPromptWithFragments(t, name, ctx)
			for _, hallmark := range []string{
				"**Required blocked explanation — make the cause understandable.**",
				"Confirmed blocker only:",
				"For missing evidence or scope ambiguity",
				"not a presumed external prerequisite",
				"We are trying to **<goal or outcome>**, **but** cannot continue until:",
				"**A — <concrete prerequisite>**",
				"Never report only “blocked”",
				"causal relationship to the intended goal",
			} {
				if !strings.Contains(out, hallmark) {
					t.Errorf("rendered output missing blocked-explanation hallmark %q", hallmark)
				}
			}
		})
	}
}

func TestBlockedExplanationExamplesDoNotUseOpaqueBlockerIDs(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "session-loop"},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
	})

	for _, hallmark := range []string{
		"`blocked-by <id>` alone is forbidden",
		"blocked-by <id> (<title>): need <prerequisite> before <goal can continue>",
		"Blocked summary: <confirmed blocker with owner/evidence/causal reason OR unknown evidence/scope",
	} {
		if !strings.Contains(out, hallmark) {
			t.Errorf("Loop processing tasks missing clear-blocker hallmark %q", hallmark)
		}
	}
}
