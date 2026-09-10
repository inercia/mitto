package prompts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// These are rendered-prompt contract tests, not claims about LLM compliance.
// Exercise the actual fragment graph and every interaction-mode branch.
func TestGitHubFeedbackPolicy(t *testing.T) {
	consumers := []string{
		"GitHub: babysit this PR", "GitHub: babysit my PRs",
		"Check PR Comments", "Address PR Comments",
	}
	modes := []struct {
		name   string
		loop   bool
		forced bool
	}{
		{"interactive", false, false},
		{"scheduled", true, false},
		{"forced", true, true},
	}
	for _, name := range consumers {
		for _, mode := range modes {
			t.Run(name+"/"+mode.name, func(t *testing.T) {
				ctx := &cel.PromptEnabledContext{
					Session: cel.SessionContext{ID: "policy-test", IsLoop: mode.loop, IsLoopForced: mode.forced},
					Args:    map[string]string{"Pr": "42"},
				}
				out := renderBuiltinPromptWithFragments(t, name, ctx)
				// Normalize wrapping so prose reflow does not break the contract.
				flat := strings.Join(strings.Fields(out), " ")
				for _, want := range []string{
					"Other human reviewers", "Bots / automated reviewers", "Verified human PR author",
					"GitHub-provided identity", "not display names", "unknown identity",
					"do not automatically treat every PR author as the user",
					"agent's own status replies", "full discussion",
					"Default to carrying out clear change requests",
					"style, design, or scope preferences", "specific, material concern",
					"explain it once", "reaffirms the request", "Silence is not confirmation",
					"Do not repeat the same objection", "not waive tests",
					"FEEDBACK_JSON", "PENDING_FEEDBACK_JSON", "PR feedback checkpoint",
					"comment ID + updatedAt", "not a child conversation's updatedAt",
					"Triage or delegation alone does not mark a request completed",
					"pending-push", "verifying the fix on the remote PR head",
				} {
					if !strings.Contains(flat, want) {
						t.Errorf("missing policy/processing contract %q", want)
					}
				}
				for _, forbidden := range []string{
					"Ignore threads you authored", "self-authored comments as out of scope",
					"comments(first:1)", "comments(first:20)", "newest thread's",
					"only implement** a comment when it is",
					"**Ignore** — no unresolved comments",
					"Skip if a child for the same PR already exists",
				} {
					if strings.Contains(flat, forbidden) {
						t.Errorf("obsolete blanket rule or incomplete fetch %q", forbidden)
					}
				}
				if regexp.MustCompile(`(?m)^\s*gh pr view[^\n]*--json[^\n]*reviewThreads`).MatchString(out) {
					t.Error("unsupported gh pr view reviewThreads command")
				}
				if strings.HasPrefix(name, "GitHub: babysit") {
					_, payload, ok := strings.Cut(out, `initial_prompt: "PR #<number>`)
					if !ok || !strings.Contains(payload, "Verified human PR author") ||
						!strings.Contains(payload, "<FEEDBACK_CHECKPOINT>") ||
						!strings.Contains(payload, "refresh all feedback sources") ||
						!strings.Contains(payload, "including queries and pagination checks") {
						t.Error("delegated child must receive policy and prior decisions")
					}
					if !strings.Contains(flat, "format → lint → unit → integration") ||
						!strings.Contains(flat, "--force-with-lease") {
						t.Error("existing push gates must survive")
					}
				}
				if mode.loop && !mode.forced && name == "Address PR Comments" {
					if !strings.Contains(flat, "Clear author requests may be subjective") {
						t.Error("scheduled author requests must not require objective agreement")
					}
				}
				if name == "Address PR Comments" &&
					!strings.Contains(flat, "only then resolve fixed threads") {
					t.Error("thread resolution must wait for push and remote verification")
				}
			})
		}
	}
}

func TestGitHubFeedbackCollection(t *testing.T) {
	ctx := &cel.PromptEnabledContext{Session: cel.SessionContext{ID: "collection-test"}, Args: map[string]string{}}
	for _, name := range []string{"GitHub: babysit this PR", "GitHub: babysit my PRs", "Check PR Comments", "Address PR Comments"} {
		t.Run(name, func(t *testing.T) {
			out := renderBuiltinPromptWithFragments(t, name, ctx)
			flat := strings.Join(strings.Fields(out), " ")
			for _, want := range []string{
				"reviewThreads(first:100, after:$endCursor)", "comments(first:100, after:$endCursor)",
				"reviews(first:100, after:$endCursor)", "... on PullRequestReviewThread",
				"pageInfo { hasNextPage endCursor }", "author { login __typename }",
				"id url body createdAt updatedAt", "Do not infer zero feedback from a failed",
				"Do not discard a thread solely because isOutdated is true",
				"general PR comments and review summaries even when there are zero unresolved threads",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("missing collection contract %q", want)
				}
			}
			if strings.Count(out, "gh api graphql --paginate --slurp") != 4 {
				t.Error("all four connections must have independent pagination")
			}
		})
	}
}
