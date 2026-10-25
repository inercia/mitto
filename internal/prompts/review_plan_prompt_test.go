package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

const reviewPlanBuiltin = "beads-issues/review-plan.prompt.yaml"

func TestBuiltinReviewPlanEligibilityAndRouting(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", reviewPlanBuiltin))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile(reviewPlanBuiltin, data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "Review plan" || prompt.Menus != "beadsIssues" {
		t.Fatalf("name/menus = %q/%q, want Review plan/beadsIssues", prompt.Name, prompt.Menus)
	}
	if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Smartest" {
		t.Errorf("preferred models = %+v, want only Smartest", prompt.PreferredModels)
	}
	if !promptTargetReuseIssue(prompt) || promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
		t.Error("Review plan must reuse/coalesce the bead conversation")
	}

	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	// Keep CommandExists deterministic without invoking the real tracker.
	if err := os.WriteFile(filepath.Join(workspace, "bd"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", workspace)
	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := evaluator.Compile(prompt.EnabledWhen)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		kind   string
		status string
		labels []string
		want   bool
	}{
		{"investigated feature", "feature", "open", []string{"implementation-refined"}, true},
		{"planned feature", "feature", "open", []string{"planned"}, true},
		{"in-progress feature not implemented", "feature", "in_progress", []string{"planned", "implementation-refined"}, true},
		{"uninvestigated feature", "feature", "open", nil, false},
		{"empty labels", "feature", "open", []string{}, false},
		{"bug", "bug", "open", []string{"implementation-refined"}, false},
		{"task", "task", "open", []string{"planned"}, false},
		{"closed feature", "feature", "closed", []string{"planned"}, false},
		{"implemented feature", "feature", "open", []string{"planned", "implemented"}, false},
		{"tested feature", "feature", "open", []string{"implementation-refined", "tested"}, false},
		{"verified feature", "feature", "open", []string{"planned", "verified"}, false},
		{"support question", "feature", "open", []string{"planned", "support-question"}, false},
		{"no row", "", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cel.PromptEnabledContext{
				Workspace: cel.WorkspaceContext{Folder: workspace},
				Item:      cel.ItemContext{Type: tc.kind, Status: tc.status, Labels: tc.labels},
			}
			got, err := evaluator.Evaluate(compiled, ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuiltinReviewPlanRendersReviewAndWriteBoundaries(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, body := renderBuiltinPromptForWorkRegression(t, reviewPlanBuiltin, ctx)
	body = strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"bd show mitto-baseline", "bd comments mitto-baseline",
		"plan-only review", "If no existing plan can be found, stop",
		"each requirement and acceptance criterion", "Open the referenced code",
		"Plan Review [tier: Smartest]", "Problem decomposition",
		"Implementation instructions", "Code not to touch",
		"read-only until the user approves tracker changes",
		"Apply the plan corrections", "Record review only", "Just the report",
		"reload the bead and comments", "stop and re-review",
		"do not assume consent", "append an audit note",
		"Do not implement code", "or change its status, labels, dependencies, or priority",
		"in-flight", "active driver/claim", "Stop after the review",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered review missing %q", want)
		}
	}

	t.Run("argument target", func(t *testing.T) {
		ctx := &cel.PromptEnabledContext{Args: map[string]string{"IssueID": "mitto-argument"}}
		_, body := renderBuiltinPromptForWorkRegression(t, reviewPlanBuiltin, ctx)
		if !strings.Contains(body, "bd show mitto-argument") {
			t.Error("IssueID must select the bead when no linked bead exists")
		}
	})
	t.Run("missing target", func(t *testing.T) {
		_, body := renderBuiltinPromptForWorkRegression(t, reviewPlanBuiltin, &cel.PromptEnabledContext{})
		if strings.Contains(body, "## Step 1") || strings.Contains(body, "bd show") {
			t.Error("missing target must not render the review/write workflow")
		}
	})
}
