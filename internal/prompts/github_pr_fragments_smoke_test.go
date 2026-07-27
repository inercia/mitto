package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// renderBuiltinPromptWithFragments loads the builtin prompts + fragment
// registry, renders `promptName`, and returns the rendered output. Used by
// the github/shared fragment smoke tests below. The prior fragment registry
// is restored via t.Cleanup so tests remain isolated.
func renderBuiltinPromptWithFragments(t *testing.T, promptName string, ctx *cel.PromptEnabledContext) string {
	t.Helper()
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
	for _, p := range list {
		if p.Name == promptName {
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, err := RenderPromptTemplate(promptName, p.Content, ctx, funcs)
			if err != nil {
				t.Fatalf("render %q: %v", promptName, err)
			}
			return out
		}
	}
	t.Fatalf("prompt %q not found in builtin corpus", promptName)
	return ""
}

// TestGitHubPRFragmentsRenderCorrectly is a smoke test for the github/shared
// PR-babysit fragment extraction: `pr-working-copy-decision`,
// `pr-fetch-review-threads`, `pr-rebase-if-behind`, and `pr-fix-ci`. It loads
// the builtin fragment registry, renders each consuming prompt, and asserts
// that hallmark substrings from the referenced fragment bodies appear in the
// rendered output — proving the {{ template "github/shared/..." . }} calls
// actually resolved and inlined their bodies.
func TestGitHubPRFragmentsRenderCorrectly(t *testing.T) {
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
		hallmarkWorkingCopy    = `WORKING_MODE="local"`          // from github/shared/pr-working-copy-decision
		hallmarkFetchThreads   = "reviewThreads(first:100)"      // from github/shared/pr-fetch-review-threads
		hallmarkRebaseIfBehind = `git worktree add "$TMPDIR"`    // from github/shared/pr-rebase-if-behind
		hallmarkFixCI          = "Failing checks: <check names>" // from github/shared/pr-fix-ci
	)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N"},
		Args:    map[string]string{},
	}

	type row struct {
		promptName string
		require    []string
	}
	rows := []row{
		{"GitHub: babysit this PR", []string{
			hallmarkWorkingCopy,
			hallmarkFetchThreads,
			hallmarkRebaseIfBehind,
			hallmarkFixCI,
		}},
		{"GitHub: babysit my PRs", []string{
			hallmarkWorkingCopy,
			hallmarkFetchThreads,
			hallmarkRebaseIfBehind,
			hallmarkFixCI,
		}},
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
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(r.promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", r.promptName, err)
			continue
		}
		for _, needle := range r.require {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline",
					r.promptName, needle)
			}
		}
	}
}

// TestIdentifyPRByBranchFragmentInlines verifies that the
// github/shared/identify-pr-by-branch fragment resolves and inlines into
// both consuming prompts (check-pr-comments, address-pr-comments) — the
// shared PR/MR identification block extracted from §1 of each.
func TestIdentifyPRByBranchFragmentInlines(t *testing.T) {
	// Hallmarks unique to the fragment body (short phrases callers don't
	// paraphrase locally).
	const (
		hallmarkGitBranch = "git branch --show-current"
		hallmarkGlabMR    = "glab mr view          # GitLab"
		hallmarkAskUser   = "If multiple or none found, ask the user to specify."
	)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N"},
		Args:    map[string]string{},
	}

	for _, promptName := range []string{"Check PR Comments", "Address PR Comments"} {
		out := renderBuiltinPromptWithFragments(t, promptName, ctx)
		for _, needle := range []string{hallmarkGitBranch, hallmarkGlabMR, hallmarkAskUser} {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline",
					promptName, needle)
			}
		}
	}
}

// TestPRFetchCommentsFragmentInlines verifies that the
// github/shared/pr-fetch-comments fragment resolves and inlines into both
// consuming prompts (check-pr-comments, address-pr-comments) — the retrieval
// + categorize block extracted from §2 of each, replacing the mitto-emcc
// broken `gh pr view --json reviews,comments,reviewThreads` form.
func TestPRFetchCommentsFragmentInlines(t *testing.T) {
	// Hallmarks unique to the fragment body (short phrases callers don't
	// paraphrase locally).
	const (
		hallmarkReviewsComments = "gh pr view <number> --json reviews,comments"
		hallmarkGraphQL         = "gh api graphql -f query="
		hallmarkReviewThreads   = "reviewThreads(first:100)"
		hallmarkGlab            = "glab mr view <number> --comments"
		hallmarkCategorize      = "Categorize the merged set"
	)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N"},
		Args:    map[string]string{},
	}

	for _, promptName := range []string{"Check PR Comments", "Address PR Comments"} {
		out := renderBuiltinPromptWithFragments(t, promptName, ctx)
		for _, needle := range []string{
			hallmarkReviewsComments,
			hallmarkGraphQL,
			hallmarkReviewThreads,
			hallmarkGlab,
			hallmarkCategorize,
		} {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline",
					promptName, needle)
			}
		}
	}
}
