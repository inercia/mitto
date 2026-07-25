package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestInteractionModeFragmentsRenderCorrectly is a smoke test for the
// _shared/interaction-mode* and _shared/silent-notify-only fragments: it
// loads the builtin fragment registry, renders each consuming prompt in
// scheduled-loop mode, and asserts that hallmark substrings from the
// referenced fragment appear in the rendered output. Presence of a hallmark
// in the rendered output means the {{ template "_shared/..." . }} call
// actually resolved and inlined its body.
func TestInteractionModeFragmentsRenderCorrectly(t *testing.T) {
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

	// Hallmarks unique to each fragment body — short phrases callers do NOT
	// paraphrase locally, so their presence in the rendered output means the
	// fragment inlined (rather than the caller having its own copy).
	const (
		// silent-notify-only: appears in the silent branch of every consumer.
		hallmarkSilentNotifyOnly = "They will stall an unattended run."
		// interaction-mode: appears in the interactive branch (sync-tasks style).
		hallmarkInteractionMode = "This is the default when run on demand."
		// interaction-mode-babysit: appears in the interactive branch (babysit style).
		hallmarkInteractionModeBabysit = "You may freely interact with the user using `mitto_ui_options`, `mitto_ui_form`, and other interactive tools in addition to `mitto_ui_notify`."
	)

	// ctxLoop: scheduled loop run (silent branch renders).
	ctxLoop := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", IsLoop: true, IsLoopForced: false, HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Pull": "true", "Push": "true"},
	}
	// ctxOnDemand: normal conversation (interactive branch renders).
	ctxOnDemand := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", IsLoop: false, HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Pull": "true", "Push": "true"},
	}

	// Consumers of _shared/silent-notify-only (via interaction-mode,
	// interaction-mode-babysit, or hand-rolled inclusion). Rendered in loop
	// mode so the silent branch fires and the fragment's bullets appear.
	silentConsumers := []string{
		// Direct callers (via _shared/interaction-mode fragment).
		"JIRA: sync tasks",
		"JIRA: pull issue",
		"JIRA: push issue",
		"GitHub: sync tasks",
		// Direct callers (via _shared/interaction-mode-babysit fragment).
		"GitHub: babysit contributions",
		// Hand-rolled callers that include _shared/silent-notify-only inline.
		"Fix CI",
		"Fix errors",
		"Address PR Comments",
		"GitHub: babysit my PRs",
		"Check PR Comments",
		"Architectural Analysis",
		"Continue",
		"Loop fixing bug",
		"Loop implementing feature",
		"Loop until issue complete",
		"Publish post",
		"Triage untriaged bugs",
		"GitHub: babysit this PR",
		"GitHub: post-merge cleanup",
		"GitHub: review PRs requests in slack",
	}

	// Consumers of _shared/interaction-mode (whose interactive branch has
	// the sync-tasks-style hallmark). Rendered on-demand so interactive
	// branch fires.
	interactionModeConsumers := []string{
		"JIRA: sync tasks",
		"JIRA: pull issue",
		"JIRA: push issue",
		"GitHub: sync tasks",
	}

	// Consumers of _shared/interaction-mode-babysit (whose interactive
	// branch has the babysit-style hallmark). Rendered on-demand.
	interactionModeBabysitConsumers := []string{
		"GitHub: babysit contributions",
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}
	assertRenders := func(t *testing.T, ctx *cel.PromptEnabledContext, promptName, hallmark string) {
		t.Helper()
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			return
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			return
		}
		if !strings.Contains(out, hallmark) {
			t.Errorf("prompt %q (IsLoop=%v): rendered output missing hallmark %q — fragment did not inline",
				promptName, ctx.Session.IsLoop, hallmark)
		}
	}

	for _, name := range silentConsumers {
		assertRenders(t, ctxLoop, name, hallmarkSilentNotifyOnly)
	}
	for _, name := range interactionModeConsumers {
		assertRenders(t, ctxOnDemand, name, hallmarkInteractionMode)
	}
	for _, name := range interactionModeBabysitConsumers {
		assertRenders(t, ctxOnDemand, name, hallmarkInteractionModeBabysit)
	}
}
