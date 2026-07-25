package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraFragmentsRenderCorrectly is a smoke test for the mitto-g61 jira
// fragment extraction: it loads the builtin fragment registry, renders each
// of the three jira prompts that were migrated (pull-issue, push-issue,
// sync-tasks), and asserts the rendered output contains hallmarks of every
// extracted fragment (proving the {{ template "jira/..." . }} calls actually
// resolved and inlined their bodies).
func TestJiraFragmentsRenderCorrectly(t *testing.T) {
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

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "s-test",
			Name:          "Test",
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Args: map[string]string{"IssueID": "mitto-abc", "Pull": "true", "Push": "true"},
	}

	// Hallmarks per prompt name → substrings that must appear in the rendered
	// body if the referenced fragment(s) actually inlined. Each hallmark is a
	// short, unique phrase from a fragment (not from the caller's own text).
	wantHallmarks := map[string][]string{
		"JIRA: pull issue": {
			"jira2md.py",                    // from jira/jira2md
			"<!-- jira-sync:begin",          // from jira/managed-body
			"bd comment <bead-id>",          // from jira/mirror-comments-in
			"Preserve local comments/notes", // from jira/mirror-comments-in
			"`Won't Do` / `Won't Fix`",      // from jira/terminal-status
			"Deployed to Stage",             // from jira/terminal-status
		},
		"JIRA: push issue": {
			"`Won't Do` / `Won't Fix`",            // from jira/terminal-status (via push-transition)
			"jira_get_transitions",                // from jira/push-transition
			"jira_pushed_status",                  // from jira/push-transition
			"[mitto] <bd-author> @ <bd-created>:", // from jira/mirror-comments-out
			"jira_pushed_comments",                // from jira/mirror-comments-out
		},
		"JIRA: sync tasks": {
			"jira2md.py",                          // from jira/jira2md
			"<!-- jira-sync:begin",                // from jira/managed-body
			"bd comment <bead-id>",                // from jira/mirror-comments-in
			"jira_get_transitions",                // from jira/push-transition
			"[mitto] <bd-author> @ <bd-created>:", // from jira/mirror-comments-out
			"Deployed to Stage",                   // from jira/terminal-status
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
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline correctly", promptName, needle)
			}
		}
	}
}
