package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraDeferredIsLocalOnlyHallmarks pins the "deferred is local-only" contract
// for mitto-je7: the three JIRA-sync builtin prompts (push-issue, sync-tasks,
// pull-issue) and the shared push-transition fragment must acknowledge the
// `deferred` bd status explicitly so a locally-parked bead never triggers a
// JIRA-side write.
//
// Concretely, once the fix lands the rendered prompt bodies must:
//
//   - state the invariant ("deferred is local-only") in the Guidelines of both
//     push-issue and sync-tasks;
//   - present a distinct deferred-branch reconciliation dialog on both the
//     push side (push-issue Step 4.5, sync-tasks Step 8A) and the pull side
//     (pull-issue Step 7.5) whose question wording names the bead as
//     "deferred locally but JIRA <KEY>" and whose safe option is exactly
//     "Leave deferred" — critically, the deferred branch must NOT offer
//     "Reopen the JIRA ticket" (that would push a JIRA write derived from a
//     purely local scheduling choice, violating the invariant).
//
// Before the fix lands, none of these hallmarks are in the rendered prompt
// bodies so this test fails — that failure IS the reproduction. The fix phase
// makes the edits described in the mitto-je7 Investigation comment and turns
// this test green.
func TestJiraDeferredIsLocalOnlyHallmarks(t *testing.T) {
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

	// Present a JIRA-named tool so the MCP-gated branches render (mirrors the
	// setup used by TestJiraPullAutoReopenHallmarks in jira_pull_reopen_test.go).
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "s-test",
			Name:          "Test",
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Args:  map[string]string{"IssueID": "mitto-abc", "Pull": "true", "Push": "true"},
		Tools: cel.NewReachableToolsContext([]string{"jira_get_issue_jira"}),
	}

	// Hallmarks per prompt name — each substring maps to one item in the
	// mitto-je7 Definition of Done.
	wantHallmarks := map[string][]string{
		"JIRA: push issue": {
			"deferred is local-only",    // invariant added to Guidelines
			"deferred locally but JIRA", // Step 4.5 deferred-branch question
			"Leave deferred",            // Step 4.5 deferred-branch safe option (no reopen)
		},
		"JIRA: sync tasks": {
			"deferred is local-only",    // invariant added to Guidelines
			"deferred locally but JIRA", // Step 8A deferred-branch question
			"Leave deferred",            // Step 8A / 5B deferred-branch safe option (no reopen)
		},
		"JIRA: pull issue": {
			"Leave deferred", // Step 7.5 deferred-branch safe option (no reopen)
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for promptName, hallmarks := range wantHallmarks {
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			continue
		}
		for _, needle := range hallmarks {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — deferred-is-local-only contract not honored (mitto-je7)", promptName, needle)
			}
		}
	}
}
