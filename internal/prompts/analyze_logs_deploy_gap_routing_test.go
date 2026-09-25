package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// TestAnalyzeLogs_DeployGapRoutingBeforeGenericReopen pins the mitto-cqiu fix:
// `ci/analyze-logs.prompt.yaml` § 3.3 must route a recurring, closed `bug`
// bead that carries the terminal `fixed` label to its `operational,deploy-gap`
// sibling instead of unconditionally reopening it. Reopening such a bead
// causes a `fixed -> stripped -> fixed` churn cycle with
// `loop-processing.prompt.yaml`'s terminal-label stripper whenever the code
// fix has merged but not yet deployed (observed repeatedly on mitto-efw).
//
// This is a static-content regression test (no template rendering needed):
// the new bullet and the preserved generic-reopen bullet are unconditional
// markdown, not gated by any `{{ if }}`, so asserting directly on the raw
// prompt body is sufficient and mirrors the "text-level regression
// assertion" approach used elsewhere for prompt content (see
// TestBeadsLoopPrompts_Defects_mittoD6h_NoIdlePollGuard).
func TestAnalyzeLogs_DeployGapRoutingBeforeGenericReopen(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin", "ci/analyze-logs.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("ci/analyze-logs.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// The deploy-gap routing rule must exist, be unconditional (not gated on
	// any parameter), and explicitly forbid reopening the code bead.
	for _, want := range []string{
		"carries the terminal `fixed` label",
		"do **not** reopen the code bead",
		"operational sibling",
		"operational,deploy-gap",
		"dependencies[]?.id==$id",
		"Never `bd update <code-bead-id> --status open`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ci/analyze-logs.prompt.yaml § 3.3: missing expected deploy-gap routing text %q", want)
		}
	}

	// The generic "closed bead that has recurred -> reopen" rule must still
	// exist (for non-bug types, or bugs without the terminal `fixed` label),
	// but it must be evaluated AFTER the deploy-gap routing rule so a fixed
	// code bug never falls through to the unconditional reopen.
	routingIdx := strings.Index(body, "carries the terminal `fixed` label")
	genericReopenIdx := strings.Index(body, "has recurred** (any other type, or a `bug` without")
	if routingIdx < 0 || genericReopenIdx < 0 {
		t.Fatalf("could not locate both § 3.3 bullets: routingIdx=%d genericReopenIdx=%d", routingIdx, genericReopenIdx)
	}
	if routingIdx >= genericReopenIdx {
		t.Errorf("deploy-gap routing bullet (offset %d) must precede the generic reopen bullet (offset %d)", routingIdx, genericReopenIdx)
	}

	// The generic reopen rule's own reopen command must still be present and
	// scoped to the "any other type / not-fixed bug" case only.
	if !strings.Contains(body, "bd update <id> --status open") {
		t.Errorf("ci/analyze-logs.prompt.yaml § 3.3: generic reopen command missing")
	}
}

// TestAnalyzeLogs_RendersWithDeployGapRouting is a lightweight render smoke
// test complementing TestBuiltinPrompts_AllRenderWithoutError: it renders the
// full "Analyze logs" prompt (with fragments installed, matching production)
// and confirms the deploy-gap routing text survives templating unchanged.
func TestAnalyzeLogs_RendersWithDeployGapRouting(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	out := renderBuiltinPromptWithFragments(t, "Analyze logs", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "test-session"},
		Args:    map[string]string{"Logs": "/var/log/app.log"},
	})
	for _, want := range []string{
		"carries the terminal `fixed` label",
		"operational sibling",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered 'Analyze logs' prompt missing %q", want)
		}
	}
}
