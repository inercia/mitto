package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraDeferredIsOrthogonalToSyncHallmarks pins the mitto-d8x invariant that
// supersedes mitto-je7's "deferred is local-only" contract:
//
//	The `jira-sync` label controls whether a bead syncs. The bead's `status`
//	(including `deferred`) is orthogonal to sync direction and completeness.
//	`deferred` is a local work-planning flag — it never gates pull, push, body
//	refresh, comment mirroring, or auto-close-on-JIRA-terminal, and never
//	translates to a JIRA-side write.
//
// Once the fix lands the rendered prompt bodies must:
//
//   - drop all four "Deferred branch" reconciliation sub-sections (pull-issue
//     Step 7.5, sync-tasks pull-side, push-issue Step 4.5, sync-tasks Step 8A)
//     and their "Deferred-vs-terminal" preamble case — no dialog ever fires
//     for a `deferred` + JIRA-terminal case;
//   - broaden the pull-issue Step 7 / sync-tasks Step 5B auto-close trigger
//     to include `deferred` alongside `open`/`in_progress`/`blocked`;
//   - rewrite the Guidelines bullet in push-issue and sync-tasks to state the
//     new invariant ("orthogonal to JIRA sync" / "`jira-sync` label alone").
//
// Before the fix lands, both directions of this test fail — the anti-hallmarks
// are still present in the rendered output and the positive hallmarks are not.
// The fix phase makes the edits described in the mitto-d8x "Proposed changes"
// section and turns this test green. The companion mitto-je7 test
// (TestJiraDeferredIsLocalOnlyHallmarks) enforces the now-superseded contract
// and is deleted by the fix phase.
func TestJiraDeferredIsOrthogonalToSyncHallmarks(t *testing.T) {
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
	// setup used by TestJiraDeferredIsLocalOnlyHallmarks / TestJiraPullAutoReopenHallmarks).
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

	// Anti-hallmarks per prompt — substrings that MUST NOT appear in the rendered
	// prompt body under the new invariant. Each one maps to one deleted item
	// in the mitto-d8x Definition of Done.
	forbidden := map[string][]string{
		"JIRA: push issue": {
			"Deferred branch",           // Step 4.5 deferred-branch subsection heading — deleted
			"Leave deferred",            // Step 4.5 safe-option label — deleted with the branch
			"deferred locally but JIRA", // Step 4.5 deferred-branch question wording — deleted
		},
		"JIRA: sync tasks": {
			"Deferred branch",                   // Step 8A deferred-branch heading — deleted
			"Deferred pull-side reconciliation", // Step 5B pull-side deferred bullet — deleted
			"Leave deferred",                    // safe-option label — deleted with both branches
			"deferred locally but JIRA",         // Step 8A deferred-branch question wording — deleted
		},
		"JIRA: pull issue": {
			"Deferred branch",           // Step 7.5 deferred-branch subsection heading — deleted
			"Deferred-vs-terminal",      // Step 7.5 preamble case 2 — deleted
			"Leave deferred",            // Step 7.5 safe-option label — deleted with the branch
			"deferred locally but JIRA", // Step 7.5 deferred-branch question wording — deleted
		},
	}

	// Positive hallmarks per prompt — substrings that MUST appear under the new
	// invariant. `blocked`, or `deferred` is the exact wording of the broadened
	// auto-close trigger; `orthogonal to JIRA sync` is the rewritten Guidelines
	// bullet.
	required := map[string][]string{
		"JIRA: push issue": {
			"orthogonal to JIRA sync", // Guidelines bullet rewrite
		},
		"JIRA: sync tasks": {
			"`blocked`, or `deferred`", // Step 5B broadened auto-close trigger
			"orthogonal to JIRA sync",  // Guidelines bullet rewrite
		},
		"JIRA: pull issue": {
			"`blocked`, or `deferred`", // Step 7 broadened auto-close trigger
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for _, promptName := range []string{"JIRA: push issue", "JIRA: sync tasks", "JIRA: pull issue"} {
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
		for _, needle := range forbidden[promptName] {
			if strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output still contains forbidden hallmark %q — mitto-d8x invariant not honored (deferred branch should have been deleted)", promptName, needle)
			}
		}
		for _, needle := range required[promptName] {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing required hallmark %q — mitto-d8x invariant not honored (new wording not applied)", promptName, needle)
			}
		}
	}
}
