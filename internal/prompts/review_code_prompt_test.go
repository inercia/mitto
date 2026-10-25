package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestBuiltinCodeReviewsEligibilityAndRouting(t *testing.T) {
	installBuiltinFragmentsForTest(t)
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
	for _, review := range []struct {
		file string
		name string
		kind string
	}{
		{"review-implementation", "Review implementation", "feature"},
		{"review-fix", "Review fix", "bug"},
	} {
		t.Run(review.name, func(t *testing.T) {
			path := "beads-issues/" + review.file + ".prompt.yaml"
			data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", path))
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := ParsePromptFile(path, data, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if prompt.Name != review.name || prompt.Menus != "beadsIssues" {
				t.Fatalf("name/menus = %q/%q", prompt.Name, prompt.Menus)
			}
			if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Smartest" {
				t.Errorf("preferred models = %+v, want only Smartest", prompt.PreferredModels)
			}
			if !promptTargetReuseIssue(prompt) || promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
				t.Error("code review must reuse/coalesce the bead conversation")
			}
			compiled, err := evaluator.Compile(prompt.EnabledWhen)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the cross-product so wrong ticket types and premature phase labels
			// cannot accidentally expose either review, including on closed tickets.
			for _, kind := range []string{"feature", "bug", "task", "epic", ""} {
				for _, status := range []string{"open", "in_progress", "closed"} {
					for _, labels := range [][]string{
						nil, {}, {"planned"}, {"implementation-refined"}, {"researched"}, {"reproduced"},
						{"implemented"}, {"tested"}, {"verified"}, {"fixed"},
						{"planned", "implemented", "tested", "verified"}, {"researched", "reproduced", "fixed"},
						{"implemented", "support-question"}, {"fixed", "support-question"},
					} {
						want := false
						support := false
						for _, label := range labels {
							support = support || label == "support-question"
							if review.kind == "feature" {
								want = want || label == "implemented" || label == "tested" || label == "verified"
							} else {
								want = want || label == "fixed"
							}
						}
						want = want && kind == review.kind && !support
						ctx := &cel.PromptEnabledContext{
							Workspace: cel.WorkspaceContext{Folder: workspace},
							Item:      cel.ItemContext{Type: kind, Status: status, Labels: labels},
						}
						got, err := evaluator.Evaluate(compiled, ctx)
						if err != nil {
							t.Fatal(err)
						}
						if got != want {
							t.Errorf("kind=%q status=%q labels=%v: enabled=%v want=%v", kind, status, labels, got, want)
						}
					}
				}
			}
		})
	}
}

func TestBuiltinCodeReviewsRenderCriticalReviewAndWriteBoundaries(t *testing.T) {
	for _, tc := range []struct {
		file  string
		title string
		want  string
		other string
	}{
		{"review-implementation", "Implementation Review", "feature's user-visible behavior end to end", "changed code address the supported root cause"},
		{"review-fix", "Fix Review", "changed code address the supported root cause", "feature's user-visible behavior end to end"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			file := "beads-issues/" + tc.file + ".prompt.yaml"
			ctx := linkedWorkRegressionContext()
			ctx.Args["IssueID"] = "mitto-other"
			_, body := renderBuiltinPromptForWorkRegression(t, file, ctx)
			body = strings.Join(strings.Fields(body), " ")
			for _, want := range []string{
				"bd show mitto-baseline", "bd comments mitto-baseline", "code review only",
				"Both open and closed tickets are eligible", "active driver/claim", "in-flight",
				"Locate the actual changes", "full changed functions", "instead of guessing",
				"each requirement and acceptance criterion", "a flawed plan is not justification",
				tc.title + " [tier: Smartest]", tc.want,
				"Proposed solutions", "regression test or check", "but do not apply them",
				"commands actually run", "do not manufacture findings",
				"read-only until the user approves tracker changes", "Record review only", "Just the report",
				"do not assume consent", "reload the bead, comments, and reviewed code state",
				"stop and re-review", "post only the approved " + tc.title,
				"Do not implement code", "or change its status, labels, dependencies, or priority",
				"Do not edit existing fields or historical reports", "No corrections have been applied",
				"Stop after the review",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("rendered review missing %q", want)
				}
			}
			if strings.Contains(body, tc.other) || strings.Contains(body, "bd show mitto-other") {
				t.Error("rendered review includes the wrong review kind or target")
			}
			t.Run("argument target", func(t *testing.T) {
				ctx := &cel.PromptEnabledContext{Args: map[string]string{"IssueID": "mitto-argument"}}
				_, body := renderBuiltinPromptForWorkRegression(t, file, ctx)
				if !strings.Contains(body, "bd show mitto-argument") {
					t.Error("IssueID must select the bead when no linked bead exists")
				}
			})
			t.Run("missing target", func(t *testing.T) {
				_, body := renderBuiltinPromptForWorkRegression(t, file, &cel.PromptEnabledContext{})
				if strings.Contains(body, "## Step 1") || strings.Contains(body, "bd show") {
					t.Error("missing target must not render the review/write workflow")
				}
			})
		})
	}
}

func TestBuiltinReviewsPreserveRecordingPolicies(t *testing.T) {
	for _, tc := range []struct {
		file       string
		correction string
	}{
		{"review-plan", "Apply the plan corrections"},
		{"review-conclusions", "Apply the conclusion corrections"},
		{"review-implementation", ""},
		{"review-fix", ""},
	} {
		t.Run(tc.file, func(t *testing.T) {
			_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/"+tc.file+".prompt.yaml", linkedWorkRegressionContext())
			body = strings.Join(strings.Fields(body), " ")
			for _, want := range []string{
				`mitto_ui_options(self_id: "session-baseline"`,
				"Do not edit existing fields or historical reports for this choice",
				"For **Just the report**, do not run any mutating `bd` commands",
				"Report failed writes; do not claim they succeeded",
				"claim/close/reopen the bead", "Do not add or remove workflow labels",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("rendered review missing shared safety rule %q", want)
				}
			}
			if strings.Count(body, "read-only until the user approves tracker changes") != 1 ||
				strings.Count(body, "Do not implement code") != 1 {
				t.Error("shared approval and boundary sections must each render exactly once")
			}
			for _, option := range []string{"Apply the plan corrections", "Apply the conclusion corrections"} {
				if strings.Contains(body, option) != (tc.correction == option) {
					t.Errorf("correction option %q must only appear for its owning review", option)
				}
			}
			if tc.correction != "" {
				for _, want := range []string{"Offer corrections only when there are actual edits", "append an audit note", "self-contained and consistent"} {
					if !strings.Contains(body, want) {
						t.Errorf("field-correction review missing %q", want)
					}
				}
			} else if strings.Contains(body, "Offer corrections") || strings.Contains(body, "append an audit note") {
				t.Error("comment-only code review must not offer field corrections")
			}
		})
	}
}
