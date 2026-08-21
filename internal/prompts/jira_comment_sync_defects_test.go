package prompts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraCommentSyncDefectsHallmarks pins the two coupled defects investigated
// for mitto-ec8:
//
//   - Defect A — batch search does not carry comments on JIRA Server/DC, so
//     `sync-tasks` Step 5C must fetch comments per-ticket (via
//     `jira_get_issue_jira` on the MCP path, `jira issue view <KEY> --comments`
//     on the CLI path) BEFORE running the mirror-comments-in loop; and the
//     shared `mirror-comments-in.tmpl` (as inlined into both callers) must
//     state that comments must come from the per-issue endpoint, not the
//     batch search payload.
//   - Defect B — `jira_updated` is a high-water mark and must only advance
//     AFTER comments are mirrored. Concretely, the `bd update ... --set-metadata
//     jira_updated=<ticket.updated>` invocation must appear strictly AFTER the
//     last `bd comment ...` invocation in the rendered output of both
//     `JIRA: sync tasks` and `JIRA: pull issue`. Both prompts' Guidelines must
//     also state the high-water-mark invariant.
//
// Before the fix lands, none of these hallmarks are in the rendered prompt
// bodies so this test fails — that failure IS the reproduction. The fix phase
// makes the edits described in the mitto-ec8 Investigation comment and turns
// this test green.
func TestJiraCommentSyncDefectsHallmarks(t *testing.T) {
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

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// Substring hallmarks per prompt name — each maps to one mitto-ec8 DoD item.
	wantHallmarks := map[string][]string{
		"JIRA: sync tasks": {
			// Defect B — high-water-mark invariant in Guidelines.
			"high-water mark",
			// Defect A — the shared mirror-comments-in fragment (inlined
			// into sync-tasks Step 5C) must warn that comments come from
			// the per-issue endpoint, not the batch search payload.
			"per-issue endpoint",
		},
		"JIRA: pull issue": {
			// Defect B — high-water-mark invariant in Guidelines.
			"high-water mark",
			// Defect A — same shared-fragment preamble, also inlined here.
			"per-issue endpoint",
		},
	}

	for _, promptName := range []string{"JIRA: sync tasks", "JIRA: pull issue"} {
		hallmarks := wantHallmarks[promptName]
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
				t.Errorf("prompt %q: rendered output missing hallmark %q — mitto-ec8 defect not fixed", promptName, needle)
			}
		}

		// Defect B ordering check: the LAST `bd update ... --set-metadata
		// jira_updated=...` invocation must appear strictly AFTER the LAST
		// `bd comment ...` invocation in the rendered output. Today both
		// prompts write jira_updated in the body-update step (Step 5.1 /
		// Step 5), which precedes the mirror-comments step — so this
		// assertion fails until the fix moves the high-water-mark write
		// past the comment loop.
		// Match on the anchor phrase itself — the `bd update` head may be
		// on a preceding line (backslash continuation), so a same-line
		// regex would miss it. Every occurrence of the anchor in the
		// rendered output is a high-water-mark write.
		reUpdatedHWM := regexp.MustCompile(`--set-metadata jira_updated=`)
		reBdComment := regexp.MustCompile(`bd comment `)
		hwmMatches := reUpdatedHWM.FindAllStringIndex(out, -1)
		commentMatches := reBdComment.FindAllStringIndex(out, -1)
		if len(hwmMatches) == 0 {
			t.Errorf("prompt %q: rendered output has no `bd update … --set-metadata jira_updated=` invocation (mitto-ec8 Defect B expects exactly one, written LAST)", promptName)
			continue
		}
		if len(commentMatches) == 0 {
			t.Errorf("prompt %q: rendered output has no `bd comment` invocation (comment mirroring must exist for the ordering check to be meaningful)", promptName)
			continue
		}
		lastHWM := hwmMatches[len(hwmMatches)-1][0]
		lastComment := commentMatches[len(commentMatches)-1][0]
		if lastHWM < lastComment {
			t.Errorf("prompt %q: last `bd update … --set-metadata jira_updated=` at offset %d appears BEFORE last `bd comment` at offset %d — mitto-ec8 Defect B: jira_updated must be written strictly AFTER comment mirroring so a partial-failure never poisons the idempotency guard", promptName, lastHWM, lastComment)
		}
	}
}
