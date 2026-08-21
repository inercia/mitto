package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraFragmentsRenderCorrectly is a smoke test for the mitto-g61 jira
// fragment extraction (extended by mitto-w8jp.2 for access-method,
// resolve-query, and link-bead, and by mitto-w8jp.3 for push-new/md2jira):
// it loads the builtin fragment registry, renders each of the five jira
// prompts that reference `jira/shared/*` fragments (new-ticket, pull-issue,
// push-issue, push-new, sync-tasks), and asserts the rendered output
// contains hallmarks of every extracted fragment (proving the
// {{ template "jira/shared/..." . }} calls actually resolved and inlined
// their bodies).
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
		"JIRA: new ticket": {
			"Prefer MCP tools whenever they are available",    // from jira/shared/access-method
			"jira_get_agile_boards_jira` | `jira board list`", // from jira/shared/access-method CLI table
		},
		"JIRA: pull issue": {
			"Prefer MCP tools whenever they are available", // from jira/shared/access-method
			"jira2md.py",                                    // from jira/shared/jira2md
			"<!-- jira-sync:begin",                          // from jira/shared/managed-body
			"bd comment <bead-id>",                          // from jira/shared/mirror-comments-in
			"Preserve local comments/notes",                 // from jira/shared/mirror-comments-in
			"`Won't Do` / `Won't Fix`",                      // from jira/shared/terminal-status
			"Deployed to Stage",                             // from jira/shared/terminal-status
			"High-water mark — commit `jira_updated` last.", // from jira/shared/link-bead (commit)
		},
		"JIRA: push issue": {
			"Prefer MCP tools whenever they are available", // from jira/shared/access-method
			"`Won't Do` / `Won't Fix`",                     // from jira/shared/terminal-status (via push-transition)
			"jira_get_transitions",                         // from jira/shared/push-transition
			"jira_pushed_status",                           // from jira/shared/push-transition
			"[mitto] <bd-author> @ <bd-created>:",          // from jira/shared/mirror-comments-out
			"jira_pushed_comments",                         // from jira/shared/mirror-comments-out
		},
		"JIRA: push to new": {
			"Prefer MCP tools whenever they are available",            // from jira/shared/access-method
			"jira_get_agile_boards_jira` | `jira board list`",         // from jira/shared/access-method CLI table
			"No \"Jira Tasks\" query is saved for this conversation.", // from jira/shared/resolve-query
			"Link the new bead to its JIRA ticket",                    // from jira/shared/link-bead (create)
			"High-water mark — commit `jira_updated` last.",           // from jira/shared/link-bead (commit)
			"[mitto] <bd-author> @ <bd-created>:",                     // from jira/shared/mirror-comments-out
			"md2jira.py",                                              // from jira/shared/md2jira
		},
		"JIRA: sync tasks": {
			"Prefer MCP tools whenever they are available", // from jira/shared/access-method
			"jira2md.py",                          // from jira/shared/jira2md
			"<!-- jira-sync:begin",                // from jira/shared/managed-body
			"bd comment <bead-id>",                // from jira/shared/mirror-comments-in
			"jira_get_transitions",                // from jira/shared/push-transition
			"[mitto] <bd-author> @ <bd-created>:", // from jira/shared/mirror-comments-out
			"Deployed to Stage",                   // from jira/shared/terminal-status
			"No \"Jira Tasks\" query is saved for this conversation.", // from jira/shared/resolve-query
			"Link the new bead to its JIRA ticket",                    // from jira/shared/link-bead (create)
			"High-water mark — commit `jira_updated` last.",           // from jira/shared/link-bead (commit)
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// wantExactlyOnce guards against a caller keeping its own copy of a
	// sentence that a fragment invocation also renders (mitto-w8jp.2 test
	// phase caught exactly this: pull-issue Step 6.9 and sync-tasks Step 5D
	// each kept an inline "advance the idempotency guard" sentence
	// immediately before the `link-bead` "commit" call, which renders the
	// same sentence itself — the reader saw it twice). Each prompt here
	// invokes `link-bead` with `Phase: "commit"` exactly once, so its
	// hallmark sentence must appear exactly once in the rendered output.
	wantExactlyOnce := map[string]string{
		"JIRA: pull issue":  "High-water mark — commit `jira_updated` last.",
		"JIRA: sync tasks":  "High-water mark — commit `jira_updated` last.",
		"JIRA: push to new": "High-water mark — commit `jira_updated` last.",
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
		if needle, ok := wantExactlyOnce[promptName]; ok {
			if n := strings.Count(out, needle); n != 1 {
				t.Errorf("prompt %q: hallmark %q appeared %d times, want exactly 1 — caller likely duplicates a sentence the fragment already renders", promptName, needle, n)
			}
		}
	}
}
