package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestPhaseGuidelinesFooterFragmentRenders is a smoke test for the
// beads-issues/shared/phase-guidelines-footer fragment: renders each of the 7
// beads-issues phase prompts and asserts the fragment's three-bullet
// output appears verbatim with the caller-declared parameters
// (IncludeItemNote, TagNoun, TagPrefix, Tier).
func TestPhaseGuidelinesFooterFragmentRenders(t *testing.T) {
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

	// (prompt name, expected tier-tag prefix, expected tier, expected
	// tag-noun, whether the "or from `Item.*`" tail should appear on
	// the Live-state bullet).
	type consumer struct {
		name, prefix, tier, noun string
		includeItemNote          bool
	}
	consumers := []consumer{
		{"Feature — plan phase", "Plan", "Reasoning", "plan", true},
		{"Feature — implement phase", "Implementation", "Coding", "summary", false},
		{"Feature — test phase", "Testing", "Coding", "summary", false},
		{"Feature — review phase", "Review", "Reasoning", "summary", false},
		{"Bug fix — reproduce phase", "Reproduction", "Coding", "summary", false},
		{"Bug fix — investigate phase", "Investigation", "Reasoning", "findings", true},
		{"Bug fix — fix phase", "Fix", "Coding", "summary", false},
	}

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
			t.Errorf("render %q: %v", c.name, err)
			continue
		}
		// The three shared bullets must render.
		if !strings.Contains(out, "- **Live state only.** Always re-read labels via `bd show --json` at the start of") {
			t.Errorf("%q: missing Live-state bullet header", c.name)
		}
		if !strings.Contains(out, "- **Always log to the tracker** with `bd comment` so progress is auditable.") {
			t.Errorf("%q: missing Always-log bullet", c.name)
		}
		wantTierTag := "- **Tier tag every comment.** Prefix the " + c.noun +
			" with `" + c.prefix + " [tier: " + c.tier + "]:` so"
		if !strings.Contains(out, wantTierTag) {
			t.Errorf("%q: missing tier-tag bullet with prefix %q / tier %q / noun %q\nWANT: %s", c.name, c.prefix, c.tier, c.noun, wantTierTag)
		}
		// IncludeItemNote branch controls the trailing "or from `Item.*`".
		hasItemNote := strings.Contains(out, "the phase; never assume labels from a prior run or from `Item.*`.")
		if hasItemNote != c.includeItemNote {
			t.Errorf("%q: IncludeItemNote branch mismatch (got %v, want %v)", c.name, hasItemNote, c.includeItemNote)
		}
	}
}
