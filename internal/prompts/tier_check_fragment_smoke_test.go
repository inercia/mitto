package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestTierCheckFragmentRenders is a smoke test for the
// beads-issues/shared/tier-check fragment: renders each of the 7 phase-prompt
// consumers in BOTH branches (tier confirmed via matching ModelTags, and
// tier-degraded when no tag matches) and asserts that the fragment's
// per-tier / per-phase hallmark substrings appear correctly (with the
// $target bead ID substituted into the degraded-run `bd comment`
// command). A regression that breaks the dict parameterization, the
// Model function branch, or the fragment attachment is caught by this.
func TestTierCheckFragmentRenders(t *testing.T) {
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
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// (prompt name, expected tier, expected phase-marker used in the
	// degraded-run `[phase: <phase>]` comment).
	type consumer struct{ name, tier, phase string }
	consumers := []consumer{
		{"Feature — plan phase", "Reasoning", "plan"},
		{"Feature — implement phase", "Coding", "implement"},
		{"Feature — test phase", "Coding", "test"},
		{"Feature — review phase", "Reasoning", "review"},
		{"Bug fix — reproduce phase", "Coding", "reproduce"},
		{"Bug fix — investigate phase", "Reasoning", "investigate"},
		{"Bug fix — fix phase", "Coding", "fix"},
	}

	// Confirmed branch — Session.ModelTags contains the phase's declared
	// tier so `Model .Tier` == true and the "✓ … tier confirmed —
	// proceed." line renders.
	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{
				ID: "sess-1", Name: "N", HasMessages: true,
				BeadsIssue: "mitto-abc", HasBeadsIssue: true,
				ModelName: "test-model", ModelTags: []string{c.tier},
			},
			Args: map[string]string{"Commit": "true"},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (confirmed): %v", c.name, err)
			continue
		}
		// Heading and the tier-declaration line.
		if !strings.Contains(out, "## Tier check") {
			t.Errorf("%q (confirmed): missing '## Tier check' heading", c.name)
		}
		wantDecl := "This phase is declared to run on the **" + c.tier + "** tier"
		if !strings.Contains(out, wantDecl) {
			t.Errorf("%q (confirmed): missing declaration %q", c.name, wantDecl)
		}
		wantConfirm := "✓ " + c.tier + " tier confirmed — proceed."
		if !strings.Contains(out, wantConfirm) {
			t.Errorf("%q (confirmed): missing confirm line %q", c.name, wantConfirm)
		}
		// The degraded-run branch must NOT render when the tag matches.
		if strings.Contains(out, "**Tier-degraded run.**") {
			t.Errorf("%q (confirmed): degraded-run branch leaked when Model %q was true", c.name, c.tier)
		}
	}

	// Degraded branch — Session.ModelTags does NOT contain the phase's
	// declared tier, so `Model .Tier` == false and the warning + bd
	// comment command render, with the ModelName/ModelTags interpolated
	// from the dict.
	for _, c := range consumers {
		body := byName[c.name]
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{
				ID: "sess-1", Name: "N", HasMessages: true,
				BeadsIssue: "mitto-abc", HasBeadsIssue: true,
				ModelName: "wrong-model", ModelTags: []string{"NotATier"},
			},
			Args: map[string]string{"Commit": "true"},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (degraded): %v", c.name, err)
			continue
		}
		if !strings.Contains(out, "**Tier-degraded run.**") {
			t.Errorf("%q (degraded): missing tier-degraded warning", c.name)
		}
		// The bd comment substitutes $target (mitto-abc), the phase, the
		// declared tier, and the running model name/tags.
		wantCmd := "bd comment mitto-abc \"⚠ tier-degraded [phase: " + c.phase +
			"]: declared " + c.tier + " but running on 'wrong-model' (tags: NotATier)."
		if !strings.Contains(out, wantCmd) {
			t.Errorf("%q (degraded): missing bd comment %q", c.name, wantCmd)
		}
		// The confirm line must NOT render when the tag mismatches.
		if strings.Contains(out, "tier confirmed — proceed.") {
			t.Errorf("%q (degraded): confirm-branch leaked when Model %q was false", c.name, c.tier)
		}
	}

	// Unknown-model degraded branch — ModelName empty and ModelTags nil
	// exercise the "<unknown>" / "none" formatters inside the fragment.
	for _, c := range consumers {
		body := byName[c.name]
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{
				ID: "sess-1", Name: "N", HasMessages: true,
				BeadsIssue: "mitto-abc", HasBeadsIssue: true,
			},
			Args: map[string]string{"Commit": "true"},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (unknown): %v", c.name, err)
			continue
		}
		if !strings.Contains(out, "Active model: `<unknown>` (tags: none)") {
			t.Errorf("%q (unknown): missing '<unknown>' / 'none' formatting", c.name)
		}
	}
}
