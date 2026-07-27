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
	// Step 3d now consumes the shared fragment via
	//   {{ template "github/shared/pr-fetch-review-threads" . }}
	// The GraphQL query lives in the fragment file; concatenate both so the
	// hallmark checks pass regardless of whether the query is inlined in the
	// prompt or (as of the shared-fragment refactor) pulled from the fragment.
	const fragmentFile = "github/shared/pr-fetch-review-threads.tmpl"

	body, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+promptFile)
	if err != nil {
		t.Fatalf("read embedded prompt %s: %v", promptFile, err)
	}
	fragBody, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+fragmentFile)
	if err != nil {
		t.Fatalf("read embedded fragment %s: %v", fragmentFile, err)
	}
	src := string(body)
	combined := src + "\n" + string(fragBody)

	// Prompt must actually consume the fragment (or inline the GraphQL query
	// itself). Guard against a future refactor that drops the fragment call
	// without replacing it.
	if !strings.Contains(src, `template "github/shared/pr-fetch-review-threads"`) &&
		!strings.Contains(src, "gh api graphql") {
		t.Errorf("[mitto-emcc] %s no longer references the pr-fetch-review-threads fragment nor inlines `gh api graphql`; Step 3d must fetch unresolved review threads via GraphQL", promptFile)
	}

	// Defect vector — `gh pr view ... --json reviewThreads` as an actual
	// executable command line (only leading whitespace before `gh`, i.e.
	// inside a ```bash fence), NOT as inline-code prose warning of the
	// form `` `gh pr view --json reviewThreads` is not supported ``. The
	// executable form is what caused the silent-skip regression; the prose
	// warning is now part of the fix and must be tolerated.
	brokenGhPrView := regexp.MustCompile(`(?m)^\s*gh pr view[^\n]*--json[^\n]*reviewThreads`)
	if loc := brokenGhPrView.FindStringIndex(combined); loc != nil {
		t.Errorf("[mitto-emcc] %s (or its consumed fragment) still contains the broken `gh pr view ... --json reviewThreads` invocation at offset %d — `gh pr view` does not expose `reviewThreads` as a --json field (GraphQL only); Step 3d must use `gh api graphql` instead. Matching snippet: %q",
			promptFile, loc[0], strings.TrimSpace(combined[loc[0]:loc[1]]))
	}

	// Fix must be in place — `gh api graphql` with a `reviewThreads`
	// selection somewhere in the prompt body or its fragment. Both must be
	// present because `gh api graphql` alone is not evidence the query
	// fetches threads.
	if !strings.Contains(combined, "gh api graphql") {
		t.Errorf("[mitto-emcc] %s (nor fragment %s) contains `gh api graphql`; Step 3d must fetch unresolved review threads via GraphQL", promptFile, fragmentFile)
	}
	if !strings.Contains(combined, "reviewThreads(") {
		t.Errorf("[mitto-emcc] %s (nor fragment %s) contains a `reviewThreads(` GraphQL field selection; Step 3d's GraphQL query must select `reviewThreads` on the pull request", promptFile, fragmentFile)
	}
}
