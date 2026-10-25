package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

const reviewConclusionsBuiltin = "beads-issues/review-conclusions.prompt.yaml"

func TestBuiltinReviewConclusionsEligibilityAndRouting(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", reviewConclusionsBuiltin))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile(reviewConclusionsBuiltin, data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "Review conclusions" || prompt.Menus != "beadsIssues" {
		t.Fatalf("name/menus = %q/%q, want Review conclusions/beadsIssues", prompt.Name, prompt.Menus)
	}
	if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Smartest" {
		t.Errorf("preferred models = %+v, want only Smartest", prompt.PreferredModels)
	}
	if !promptTargetReuseIssue(prompt) || promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
		t.Error("Review conclusions must reuse/coalesce the bead conversation")
	}
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
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
		{"investigated bug", "bug", "open", []string{"implementation-refined"}, true},
		{"researched without reproduction", "bug", "open", []string{"researched"}, true},
		{"reproduced bug", "bug", "open", []string{"reproduced"}, true},
		{"in-progress unfixed bug", "bug", "in_progress", []string{"researched", "reproduced"}, true},
		{"uninvestigated bug", "bug", "open", nil, false},
		{"empty labels", "bug", "open", []string{}, false},
		{"feature", "feature", "open", []string{"implementation-refined"}, false},
		{"task", "task", "open", []string{"researched"}, false},
		{"closed bug", "bug", "closed", []string{"researched"}, false},
		{"fixed investigated bug", "bug", "open", []string{"implementation-refined", "fixed"}, false},
		{"fixed researched bug", "bug", "open", []string{"researched", "fixed"}, false},
		{"fixed reproduced bug", "bug", "open", []string{"reproduced", "fixed"}, false},
		{"support question", "bug", "open", []string{"researched", "support-question"}, false},
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

func TestBuiltinReviewConclusionsRendersReviewAndWriteBoundaries(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["IssueID"] = "mitto-other"
	_, body := renderBuiltinPromptForWorkRegression(t, reviewConclusionsBuiltin, ctx)
	body = strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"bd show mitto-baseline", "bd comments mitto-baseline",
		"conclusions-only review", "together as a whole", "Read all Investigation Reports",
		"If no investigation can be found, stop", "Reproduction is useful evidence, not a prerequisite",
		"plausible alternative causes", "Open the referenced code", "Assess every proposed fix",
		"If no fix has been proposed, propose an actionable fix direction",
		"If evidence is insufficient", "smallest discriminating checks",
		"Conclusions Review [tier: Smartest]", "Evidence synthesis", "regression test",
		"Implementation instructions", "Code not to touch",
		"read-only until the user approves tracker changes",
		"Apply the conclusion corrections", "Record review only", "Just the report",
		"reload the bead and comments", "stop and re-review", "do not assume consent",
		"append an audit note", "preserving unrelated content and historical reports",
		"Do not implement code", "or change its status, labels, dependencies, or priority",
		"in-flight", "active driver/claim", "Stop after the review",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered review missing %q", want)
		}
	}
	if strings.Contains(body, "bd show mitto-other") {
		t.Error("linked bead must take precedence over the IssueID argument")
	}
	t.Run("argument target", func(t *testing.T) {
		ctx := &cel.PromptEnabledContext{Args: map[string]string{"IssueID": "mitto-argument"}}
		_, body := renderBuiltinPromptForWorkRegression(t, reviewConclusionsBuiltin, ctx)
		if !strings.Contains(body, "bd show mitto-argument") {
			t.Error("IssueID must select the bead when no linked bead exists")
		}
	})
	t.Run("missing target", func(t *testing.T) {
		_, body := renderBuiltinPromptForWorkRegression(t, reviewConclusionsBuiltin, &cel.PromptEnabledContext{})
		if strings.Contains(body, "## Step 1") || strings.Contains(body, "bd show") {
			t.Error("missing target must not render the review/write workflow")
		}
	})
}
