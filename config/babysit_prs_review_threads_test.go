package config

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestBabysitPRs_ReviewThreadsQuery_mittoEMCC is the regression guard for
// mitto-emcc: the `Babysit my PRs` prompt (Step 3d) used
//
//	gh pr view <n> --json reviewThreads --jq '...'
//
// but `gh pr view` does NOT support `reviewThreads` as a `--json` field —
// that field is only exposed via GraphQL. On `gh` 2.96.0 the command exits
// nonzero with `Unknown JSON field: "reviewThreads"`; the agent then read
// the count as 0 and treated every PR as having no unresolved threads,
// silently skipping (and never spawning) review-comment-fix children.
//
// This test locks in the fix: Step 3d must use `gh api graphql` with a
// `reviewThreads` selection instead of the broken `gh pr view --json
// reviewThreads` form.
func TestBabysitPRs_ReviewThreadsQuery_mittoEMCC(t *testing.T) {
	const promptFile = "github/babysit-my-prs.prompt.yaml"

	body, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+promptFile)
	if err != nil {
		t.Fatalf("read embedded prompt %s: %v", promptFile, err)
	}
	src := string(body)

	// Defect vector — `gh pr view ... --json reviewThreads` as an actual
	// executable command line (only leading whitespace before `gh`, i.e.
	// inside a ```bash fence), NOT as inline-code prose warning of the
	// form `` `gh pr view --json reviewThreads` is not supported ``. The
	// executable form is what caused the silent-skip regression; the prose
	// warning is now part of the fix and must be tolerated.
	brokenGhPrView := regexp.MustCompile(`(?m)^\s*gh pr view[^\n]*--json[^\n]*reviewThreads`)
	if loc := brokenGhPrView.FindStringIndex(src); loc != nil {
		t.Errorf("[mitto-emcc] %s still contains the broken `gh pr view ... --json reviewThreads` invocation at offset %d — `gh pr view` does not expose `reviewThreads` as a --json field (GraphQL only); Step 3d must use `gh api graphql` instead. Matching snippet: %q",
			promptFile, loc[0], strings.TrimSpace(src[loc[0]:loc[1]]))
	}

	// Fix must be in place — `gh api graphql` with a `reviewThreads`
	// selection somewhere in the prompt body. Both must be present because
	// `gh api graphql` alone is not evidence the query fetches threads.
	if !strings.Contains(src, "gh api graphql") {
		t.Errorf("[mitto-emcc] %s is missing `gh api graphql`; Step 3d must fetch unresolved review threads via GraphQL", promptFile)
	}
	if !strings.Contains(src, "reviewThreads(") {
		t.Errorf("[mitto-emcc] %s is missing a `reviewThreads(` GraphQL field selection; Step 3d's GraphQL query must select `reviewThreads` on the pull request", promptFile)
	}
}
