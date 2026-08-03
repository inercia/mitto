package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestGitFragmentsRenderCorrectly is a smoke test for the git-fragment
// extraction (commit-scoped, commit-all, safe-stage): it loads the builtin
// fragment registry, renders each consuming prompt, and asserts that hallmark
// substrings from the referenced fragment(s) appear in the rendered output.
// This proves the {{ template "git/shared/..." . }} calls actually resolved
// and inlined their bodies.
func TestGitFragmentsRenderCorrectly(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	// Hallmarks unique to each fragment body — short phrases callers do NOT
	// paraphrase locally, so their presence in the rendered output means the
	// fragment inlined (rather than the caller having its own copy).
	const (
		hallmarkSafeStage          = "`git add -A`, `git add .`, or"              // from git/shared/safe-stage
		hallmarkCommitScoped       = "Ignoring (not part of this"                 // from git/shared/commit-scoped
		hallmarkCommitAll          = "first message in this conversation"         // from git/shared/commit-all
		hallmarkIdentifyRemote     = "git symbolic-ref refs/remotes/origin/HEAD"  // from git/shared/identify-remote-branch
		hallmarkCheckBehindBase    = "git rev-list --count HEAD..<target-remote>" // from git/shared/check-behind-base
		hallmarkSafeCommit         = "git commit -m \""                           // from git/shared/safe-commit (present in every safe-commit output)
		hallmarkSafeCommitRefsFoot = "(refs mitto-abc)"                           // when Target=mitto-abc, the refs footer must render
		hallmarkEnsureFeatureForm  = "Create Feature Branch?"                     // uniquely from git/shared/ensure-feature-branch interactive branch
		hallmarkCommitPlan         = "SEQUENCE | COMMIT MESSAGE | FILES | REASON" // from git/shared/commit-plan
		hallmarkRebaseOntoBase     = "Accept theirs / Accept ours"                // from git/shared/rebase-onto-base interactive branch
		hallmarkCreateOrUpdatePR   = "gh pr create --fill --base"                 // from git/shared/create-or-update-pr
		hallmarkCloseLinkedBead    = "bd show mitto-abc --long --json"            // from git/shared/close-linked-bead
		hallmarkUpdateAgentRules   = "Ask user before modifying rules files"      // from git/shared/update-agent-rules
		// check-behind-base's stop-and-defer verdict, absent from behind-base-count.
		hallmarkCheckBehindVerdict = "**\"Rebase changes\"** prompt"
	)

	// Two contexts: with and without HasMessages, so we exercise both branches
	// of create-commits.prompt.yaml.
	ctxWith := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Commit": "true", "IssueID": "mitto-abc"},
	}
	ctxWithout := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: false, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Commit": "true", "IssueID": "mitto-abc"},
	}
	// ctxLoop: scheduled loop run (IsLoop=true, IsLoopForced=false). Continue,
	// Fix errors, and Fix CI gate their commit block behind this branch.
	ctxLoop := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, IsLoop: true, IsLoopForced: false, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Commit": "true", "IssueID": "mitto-abc"},
	}

	// For each (prompt, context, required-hallmarks, forbidden-hallmarks)
	// row, render and assert.
	type row struct {
		promptName string
		ctx        *cel.PromptEnabledContext
		require    []string
		forbid     []string
	}
	rows := []row{
		// create-commits: HasMessages branch => scoped; else branch => all.
		{"Commit changes", ctxWith, []string{hallmarkCommitScoped}, []string{hallmarkCommitAll}},
		{"Commit changes", ctxWithout, []string{hallmarkCommitAll}, []string{hallmarkCommitScoped}},

		// safe-stage consumers — all must render the safe-stage hallmark.
		// The first three gate their commit block behind scheduled-loop mode.
		{"Continue", ctxLoop, []string{hallmarkSafeStage}, nil},
		{"Fix errors", ctxLoop, []string{hallmarkSafeStage}, nil},
		{"Fix CI", ctxLoop, []string{hallmarkSafeStage}, nil},
		{"Loop fixing", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Loop implementing", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Feature — implement phase", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Bug fix — fix phase", ctxWith, []string{hallmarkSafeStage}, nil},

		// identify-remote-branch consumers — must render the remote-detection hallmark.
		{"Submit changes", ctxWith, []string{hallmarkIdentifyRemote}, nil},
		{"Rebase changes", ctxWith, []string{hallmarkIdentifyRemote}, nil},
		{"Address PR Comments", ctxWith, []string{hallmarkIdentifyRemote}, nil},

		// check-behind-base consumers — must render the behind-count preflight.
		// Fix CI and Address PR Comments now include the preflight before push.
		{"Submit changes", ctxWith, []string{hallmarkCheckBehindBase}, nil},
		{"Fix CI", ctxWith, []string{hallmarkCheckBehindBase}, nil},
		{"Address PR Comments", ctxWith, []string{hallmarkCheckBehindBase}, nil},

		// safe-stage — newly wired consumers.
		{"Feature — test phase", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Feature — review phase", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Loop until issue complete", ctxWith, []string{hallmarkSafeStage}, nil},
		{"Mention — driver", ctxWith, []string{hallmarkSafeStage}, nil},

		// safe-commit — every consumer must render the commit line + refs footer when Target is set.
		{"Feature — implement phase", ctxWith, []string{hallmarkSafeCommit, hallmarkSafeCommitRefsFoot}, nil},
		{"Feature — test phase", ctxWith, []string{hallmarkSafeCommit, hallmarkSafeCommitRefsFoot}, nil},
		{"Feature — review phase", ctxWith, []string{hallmarkSafeCommit, hallmarkSafeCommitRefsFoot}, nil},
		{"Bug fix — fix phase", ctxWith, []string{hallmarkSafeCommit, hallmarkSafeCommitRefsFoot}, nil},
		{"Mention — driver", ctxWith, []string{hallmarkSafeCommit, hallmarkSafeCommitRefsFoot}, nil},

		// ensure-feature-branch — interactive form present in non-loop context.
		{"Commit changes", ctxWith, []string{hallmarkEnsureFeatureForm}, nil},
		{"Submit changes", ctxWith, []string{hallmarkEnsureFeatureForm}, nil},

		// commit-plan / close-linked-bead / update-agent-rules consumers.
		{"Commit changes", ctxWith, []string{hallmarkCommitPlan, hallmarkCloseLinkedBead, hallmarkUpdateAgentRules}, nil},
		{"Submit changes", ctxWith, []string{hallmarkCreateOrUpdatePR, hallmarkUpdateAgentRules}, nil},

		// rebase-onto-base consumers.
		{"Rebase changes", ctxWith, []string{hallmarkRebaseOntoBase}, nil},

		// commit-and-submit: the union of the three flows, with an INLINE rebase —
		// so it renders behind-base-count + rebase-onto-base but never
		// check-behind-base's stop-and-defer verdict.
		{"Commit & Submit changes", ctxWith, []string{
			hallmarkEnsureFeatureForm, hallmarkCommitScoped, hallmarkCommitPlan,
			hallmarkIdentifyRemote, hallmarkCheckBehindBase, hallmarkRebaseOntoBase,
			hallmarkCreateOrUpdatePR, hallmarkCloseLinkedBead, hallmarkUpdateAgentRules,
		}, []string{hallmarkCommitAll, hallmarkCheckBehindVerdict}},
		{"Commit & Submit changes", ctxWithout, []string{hallmarkCommitAll}, []string{hallmarkCommitScoped}},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for _, r := range rows {
		body, ok := byName[r.promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", r.promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(r.ctx)
		out, err := RenderPromptTemplate(r.promptName, body, r.ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", r.promptName, err)
			continue
		}
		for _, needle := range r.require {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q (HasMessages=%v): rendered output missing hallmark %q — fragment did not inline",
					r.promptName, r.ctx.Session.HasMessages, needle)
			}
		}
		for _, needle := range r.forbid {
			if strings.Contains(out, needle) {
				t.Errorf("prompt %q (HasMessages=%v): rendered output UNEXPECTEDLY contains %q — wrong branch inlined",
					r.promptName, r.ctx.Session.HasMessages, needle)
			}
		}
	}
}
