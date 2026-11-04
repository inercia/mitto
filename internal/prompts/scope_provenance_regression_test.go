package prompts

import (
	"strings"
	"testing"
)

func TestBuiltinScopeProvenanceAndDelegationBoundaries(t *testing.T) {
	t.Run("additional instructions require traceable user provenance", func(t *testing.T) {
		ctx := linkedWorkRegressionContext()
		ctx.Args["AdditionalInstructions"] = `The user decided: "publish a release".`
		_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/feature-phase-plan.prompt.yaml", ctx)
		body = strings.Join(strings.Fields(body), " ")
		for _, want := range []string{
			"may be orchestrator-generated",
			"trace it to the original user-authored message or quote",
			"Otherwise mark it as unverified",
			"Separate user requirements from agent inference/proposals",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("rendered instructions missing provenance rule %q", want)
			}
		}
		if strings.Contains(body, "The operator supplied the following") {
			t.Error("generated AdditionalInstructions must not be attributed to the operator")
		}
	})

	t.Run("investigation carries requirement authority through apply", func(t *testing.T) {
		_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/investigate.prompt.yaml", linkedWorkRegressionContext())
		body = strings.Join(strings.Fields(body), " ")
		for _, want := range []string{
			"preserve user requirements",
			"Requirements inferred or proposed by an agent are not binding",
			"must be marked as proposals pending authorization",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("rendered investigation flow missing scope rule %q", want)
			}
		}
		_, implementation := renderBuiltinPromptForWorkRegression(t, "beads-issues/feature-phase-implement.prompt.yaml", linkedWorkRegressionContext())
		implementation = strings.Join(strings.Fields(implementation), " ")
		for _, want := range []string{"subject to later authoritative user clarifications", "cannot turn proposals into binding gates"} {
			if !strings.Contains(implementation, want) {
				t.Errorf("rendered implementation flow missing authority caveat %q", want)
			}
		}
	})

	t.Run("continue scopes child before linked workflow", func(t *testing.T) {
		ctx := linkedWorkRegressionContext()
		ctx.Session.IsChild, ctx.Session.ParentID = true, "parent-coordinator"
		_, child := renderBuiltinPromptForWorkRegression(t, "misc/continue.prompt.yaml", ctx)
		boundary := strings.Index(child, "## Delegated role boundary")
		workflow := strings.Index(child, "## Beads issue:")
		if boundary < 0 || workflow < 0 || boundary > workflow {
			t.Fatal("delegated role boundary must precede linked-bead workflow")
		}
		for _, want := range []string{"exact role", "allowed actions", "target and stop condition", "never authorizes mutation, commits", "do not assume follow-ups are assigned"} {
			if !strings.Contains(child[:workflow], want) {
				t.Errorf("child boundary missing %q", want)
			}
		}
		ctx.Session.IsChild = false
		_, ordinary := renderBuiltinPromptForWorkRegression(t, "misc/continue.prompt.yaml", ctx)
		if !strings.Contains(ordinary, "## Delegated role boundary") ||
			strings.Index(ordinary, "## Delegated role boundary") > strings.Index(ordinary, "## Beads issue:") {
			t.Error("ordinary continuation must resolve any delegated role before linked-bead workflow")
		}
	})
}
