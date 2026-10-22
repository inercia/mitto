package prompts

import (
	"strings"
	"testing"
)

func TestBuiltinPushAuthorizationScope(t *testing.T) {
	for _, strategy := range []string{"Commit", "None", "Pull Request"} {
		t.Run("phase worker "+strategy, func(t *testing.T) {
			ctx := linkedWorkRegressionContext()
			ctx.Args["SubmitStrategy"] = strategy
			_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/feature-phase-implement.prompt.yaml", ctx)
			normalized := strings.Join(strings.Fields(body), " ")
			for _, contract := range []string{
				"Phase workers never push",
				"does not authorize a push",
				"the driver's separate submission step",
			} {
				if !strings.Contains(normalized, contract) {
					t.Errorf("missing phase push boundary %q", contract)
				}
			}
			if strings.Contains(body, "git push ") {
				t.Error("phase worker must not render a push command")
			}
		})
	}

	for _, strategy := range []string{"Commit", "None"} {
		ctx := linkedWorkRegressionContext()
		ctx.Args["SubmitStrategy"] = strategy
		_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
		if strings.Contains(body, "gh pr create") || strings.Contains(body, "glab mr create") {
			t.Errorf("default Start work strategy %q must not create a PR", strategy)
		}
	}

	_, submission := renderBuiltinPromptForWorkRegression(t, "git/submit-changes.prompt.yaml", linkedWorkRegressionContext())
	normalizedSubmission := strings.Join(strings.Fields(submission), " ")
	prExistsStart := strings.Index(submission, "#### PR exists")
	if prExistsStart < 0 {
		t.Fatal("rendered submission is missing the PR-update section")
	}
	prExistsEnd := strings.Index(submission[prExistsStart:], "#### PR description constraints")
	if prExistsEnd < 0 {
		t.Fatal("rendered submission is missing the PR description section")
	}
	prExists := submission[prExistsStart : prExistsStart+prExistsEnd]
	normalizedPRExists := strings.Join(strings.Fields(prExists), " ")
	if !strings.Contains(normalizedPRExists, "git push <push-remote> <branch-name>") {
		t.Error("existing PR must use a normal scoped push by default")
	}
	if !strings.Contains(normalizedPRExists, "only if an authorized rebase rewrote this PR branch") ||
		!strings.Contains(normalizedPRExists, "git push --force-with-lease <push-remote> <branch-name>") {
		t.Error("leased force push must be conditional on an authorized rebase rewriting the PR branch")
	}
	if strings.Contains(prExists, "git push --force-with-lease <push-remote> <branch-name>\n```") {
		t.Error("leased force push must not be the unconditional PR-update command")
	}
	for _, contract := range []string{
		"authorizes pushing **only** `<branch-name>`",
		"git push -u <push-remote> <branch-name>",
		"does not authorize pushing tags, releases, other branches, protected/base branches",
		"if pushing is forbidden, leave submission unperformed",
	} {
		if !strings.Contains(normalizedSubmission, contract) {
			t.Errorf("explicit PR submission missing scoped-push contract %q", contract)
		}
	}
	if strings.Contains(submission, "git push --all") || strings.Contains(submission, "git push --mirror") {
		t.Error("explicit PR submission must not authorize broad pushes")
	}
}
