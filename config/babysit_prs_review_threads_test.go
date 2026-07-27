package config

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// mittoEMCCCase describes one prompt + fragment(s) pair that used to carry
// the broken `gh pr view --json reviewThreads` form and now must fetch review
// threads via `gh api graphql`.
type mittoEMCCCase struct {
	// prompt is the .prompt.yaml file that historically carried the broken
	// invocation (relative to BuiltinPromptsDir).
	prompt string
	// fragmentRef is the `template "..."` substring that MUST appear in the
	// prompt body to prove it consumes a GraphQL-based fragment (or the empty
	// string if the prompt is allowed to inline the GraphQL directly).
	fragmentRef string
	// fragment is the .tmpl file whose body is concatenated with the prompt
	// before running the hallmark checks. If empty, only the prompt body is
	// checked (prompt is required to inline `gh api graphql` itself).
	fragment string
}

// TestBabysitPRs_ReviewThreadsQuery_mittoEMCC is the regression guard for
// mitto-emcc: several github/*.prompt.yaml prompts used
//
//	gh pr view <n> --json reviewThreads --jq '...'
//
// but `gh pr view` does NOT support `reviewThreads` as a `--json` field —
// that field is only exposed via GraphQL. On `gh` 2.96.0 the command exits
// nonzero with `Unknown JSON field: "reviewThreads"`; the agent then read
// the count as 0 and treated every PR as having no unresolved threads,
// silently skipping (and never spawning) review-comment-fix children.
//
// This test locks in the fix across every prompt that used to carry the
// broken form: the retrieval must use `gh api graphql` with a
// `reviewThreads` selection instead of the broken `gh pr view --json
// reviewThreads` form. Each prompt either inlines the GraphQL query or
// consumes a shared fragment that does.
func TestBabysitPRs_ReviewThreadsQuery_mittoEMCC(t *testing.T) {
	cases := []mittoEMCCCase{
		{
			prompt:      "github/babysit-my-prs.prompt.yaml",
			fragmentRef: `template "github/shared/pr-fetch-review-threads"`,
			fragment:    "github/shared/pr-fetch-review-threads.tmpl",
		},
		{
			prompt:      "github/babysit-this-pr.prompt.yaml",
			fragmentRef: `template "github/shared/pr-fetch-review-threads"`,
			fragment:    "github/shared/pr-fetch-review-threads.tmpl",
		},
		{
			prompt:      "github/check-pr-comments.prompt.yaml",
			fragmentRef: `template "github/shared/pr-fetch-comments"`,
			fragment:    "github/shared/pr-fetch-comments.tmpl",
		},
		{
			prompt:      "github/address-pr-comments.prompt.yaml",
			fragmentRef: `template "github/shared/pr-fetch-comments"`,
			fragment:    "github/shared/pr-fetch-comments.tmpl",
		},
	}

	brokenGhPrView := regexp.MustCompile(`(?m)^\s*gh pr view[^\n]*--json[^\n]*reviewThreads`)

	for _, c := range cases {
		t.Run(c.prompt, func(t *testing.T) {
			body, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+c.prompt)
			if err != nil {
				t.Fatalf("read embedded prompt %s: %v", c.prompt, err)
			}
			src := string(body)
			combined := src
			if c.fragment != "" {
				fragBody, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+c.fragment)
				if err != nil {
					t.Fatalf("read embedded fragment %s: %v", c.fragment, err)
				}
				combined = src + "\n" + string(fragBody)
			}

			// Prompt must actually consume the fragment (or inline the GraphQL query
			// itself). Guard against a future refactor that drops the fragment call
			// without replacing it.
			if c.fragmentRef != "" &&
				!strings.Contains(src, c.fragmentRef) &&
				!strings.Contains(src, "gh api graphql") {
				t.Errorf("[mitto-emcc] %s no longer references the expected fragment (%s) nor inlines `gh api graphql`; the retrieval step must fetch unresolved review threads via GraphQL", c.prompt, c.fragmentRef)
			}

			// Defect vector — `gh pr view ... --json reviewThreads` as an actual
			// executable command line (only leading whitespace before `gh`, i.e.
			// inside a ```bash fence), NOT as inline-code prose warning of the
			// form `` `gh pr view --json reviewThreads` is not supported ``. The
			// executable form is what caused the silent-skip regression; the prose
			// warning is now part of the fix and must be tolerated.
			if loc := brokenGhPrView.FindStringIndex(combined); loc != nil {
				t.Errorf("[mitto-emcc] %s (or its consumed fragment) still contains the broken `gh pr view ... --json reviewThreads` invocation at offset %d — `gh pr view` does not expose `reviewThreads` as a --json field (GraphQL only); the retrieval must use `gh api graphql` instead. Matching snippet: %q",
					c.prompt, loc[0], strings.TrimSpace(combined[loc[0]:loc[1]]))
			}

			// Fix must be in place — `gh api graphql` with a `reviewThreads`
			// selection somewhere in the prompt body or its fragment. Both must
			// be present because `gh api graphql` alone is not evidence the
			// query fetches threads.
			if !strings.Contains(combined, "gh api graphql") {
				t.Errorf("[mitto-emcc] %s (nor fragment %s) contains `gh api graphql`; the retrieval must fetch unresolved review threads via GraphQL", c.prompt, c.fragment)
			}
			if !strings.Contains(combined, "reviewThreads(") {
				t.Errorf("[mitto-emcc] %s (nor fragment %s) contains a `reviewThreads(` GraphQL field selection; the GraphQL query must select `reviewThreads` on the pull request", c.prompt, c.fragment)
			}
		})
	}
}
