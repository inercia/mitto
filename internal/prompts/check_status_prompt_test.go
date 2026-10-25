package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

const checkStatusBuiltin = "beads-issues/status.prompt.yaml"

func TestBuiltinCheckStatusEligibilityAndRouting(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", checkStatusBuiltin))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile(checkStatusBuiltin, data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "Check status" || prompt.Menus != "beadsIssues, conversation" {
		t.Fatalf("name/menus = %q/%q", prompt.Name, prompt.Menus)
	}
	if !promptTargetReuseIssue(prompt) || promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
		t.Error("Check status must reuse/coalesce the bead conversation")
	}
	// Unification does not change the existing status prompt's model preference.
	if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Coding" {
		t.Errorf("preferred models = %+v, want only Coding", prompt.PreferredModels)
	}
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	installPromptMatrixCommands(t, "bd")
	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := evaluator.Compile(prompt.EnabledWhen)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"feature", "bug", "task", "epic", ""} {
		for _, status := range []string{"open", "in_progress", "blocked", "deferred", "closed", ""} {
			for _, labels := range [][]string{nil, {}, {"implemented", "fixed"}, {"support-question"}} {
				ctx := &cel.PromptEnabledContext{
					Workspace: cel.WorkspaceContext{Folder: workspace},
					Item:      cel.ItemContext{Type: kind, Status: status, Labels: labels},
				}
				got, err := evaluator.Evaluate(compiled, ctx)
				if err != nil {
					t.Fatal(err)
				}
				want := len(labels) == 0 || labels[0] != "support-question"
				if got != want {
					t.Errorf("type=%q status=%q labels=%v: enabled=%v, want %v", kind, status, labels, got, want)
				}
			}
		}
	}
	got, err := evaluator.Evaluate(compiled, &cel.PromptEnabledContext{
		Workspace: cel.WorkspaceContext{Folder: t.TempDir()},
	})
	if err != nil || got {
		t.Errorf("without .beads: enabled=%v err=%v, want false", got, err)
	}
}

func TestBuiltinCheckStatusAssessmentAndActionBoundaries(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["IssueID"] = "mitto-wrong-target"
	_, body := renderBuiltinPromptForWorkRegression(t, checkStatusBuiltin, ctx)
	body = strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"bd show mitto-baseline --long --json", "bd comments mitto-baseline",
		"bd dep tree mitto-baseline", "bd show mitto-baseline --children --json",
		"Check Status", "**readiness**", "**resolution**", "Status Report",
		"**Open and closed tickets**", "partial overlap is not enough to close",
		"Ambiguous or incomplete evidence", "not eligible for closure",
		"do not attribute unrelated dirty work", "tests not run without claiming they passed",
		"Investigate", "Start work", "Review plan", "Review conclusions", "Review implementation", "Review fix",
		"Just the report", "Record status report", "Close resolved ticket",
		"read-only until explicit approval", "do not assume consent",
		"**Before any write**, reload", "recheck the relevant code/test evidence",
		"get fresh approval", "in-flight", "claimed_by", "claim_heartbeat_at",
		"**do not close actively owned tickets**", "including duplicates",
		"**individual approval for each child closure**", "separate approval for the epic itself",
		"**Close an epic only once all its children are closed**", "Already-closed tickets need no closure",
		"bd dep relate mitto-baseline <canonical-id>", "if linking fails, do not close",
		"bd close mitto-baseline --reason", "bd comment mitto-baseline",
		"Do not add an extra comment unless it too was approved", "partially completed actions",
		"recommendation only; do not execute it here", "Do not implement code",
		"rewrite requirements or plans", "or create follow-up issues", "Stop after the status check",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered Check status missing %q", want)
		}
	}
	if strings.Contains(body, "mitto-wrong-target") {
		t.Error("linked bead must take precedence over IssueID")
	}
	_, unlinked := renderBuiltinPromptForWorkRegression(t, checkStatusBuiltin, &cel.PromptEnabledContext{})
	for _, forbidden := range []string{"bd show", "bd comments", "bd dep", "bd close", "bd create", "bd update", "## Step 3", "## Step 4"} {
		if strings.Contains(unlinked, forbidden) {
			t.Errorf("no-target mode leaked tracker workflow %q", forbidden)
		}
	}
}
