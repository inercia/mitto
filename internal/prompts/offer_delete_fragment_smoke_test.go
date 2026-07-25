package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestOfferDeleteConversationFragmentRenders is a smoke test for the
// _shared/offer-delete-conversation fragment: it loads the builtin fragment
// registry, renders each consuming prompt on-demand (no loop context), and
// asserts that hallmark substrings from the fragment body appear in the
// rendered output. Presence of a hallmark in the rendered output means the
// {{ template "_shared/offer-delete-conversation" . }} call actually
// resolved and inlined its body.
func TestOfferDeleteConversationFragmentRenders(t *testing.T) {
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

	// Hallmarks unique to the fragment body — short phrases callers do NOT
	// paraphrase locally, so their presence in the rendered output means the
	// fragment inlined (rather than the caller having its own copy).
	const (
		hallmarkHeading     = "All done — delete this conversation now?"
		hallmarkTimeoutRule = "**On timeout** (no response): only delete this conversation if **all** of the following hold"
		hallmarkUnavailable = "If the `mitto_*` tools are unavailable, skip this step silently."
	)

	// Rendered on-demand (not loop) — the offer-delete step normally runs at
	// the end of an interactive session.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", IsLoop: false, HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Topic": "test topic", "Pull": "true", "Push": "true"},
	}

	consumers := []string{
		// beads/ folder (6).
		"Cleanup stale issues",
		"Group issues in Epics",
		"Overview",
		"Reevaluate all issues",
		"Status ALL in-progress",
		"Start working on ready",
		// beads-issues/ folder (8).
		"Assess issue readiness",
		"Decompose issue",
		"Recalculate issue dependencies",
		"Discuss a topic",
		"Investigate more",
		"Check if issue resolved",
		"Show status",
		"Start work",
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, hallmark := range []string{hallmarkHeading, hallmarkTimeoutRule, hallmarkUnavailable} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline", name, hallmark)
			}
		}
	}
}
