package prompts

import (
	"testing"
)

// TestAllBuiltinPromptsPrecompile is the paradox-recurrence guard for
// mitto-ezw. It walks the entire embedded builtin corpus under
// config/prompts/builtin, installs the sibling fragment registry, and loads
// every *.prompt.yaml through LoadPromptsFromDirWithErrors — which invokes
// ParsePromptFile → PrecompileTemplateConds against a template set that has
// every registered fragment attached.
//
// The regression this pins: if bootstrap ordering, the fragment scan roots,
// deploy coverage of *.tmpl, or a fragment reference in a newly-added
// builtin prompt drift out of sync, the paradox reappears — every prompt
// that calls {{ template "_shared/…" . }} silently drops out of PromptsCache
// at load time with a "template not defined" precompile error. Testing the
// embedded corpus (not user-authored prompts under MITTO_DIR) keeps the
// signal deterministic: builtin prompts may only reference builtin
// fragments, and both ship from the same tree.
//
// Zero fragment-load errors and zero prompt-load errors are required. A
// single failure fails the test with the offending rel-path and underlying
// error so the fix is targeted.
func TestAllBuiltinPromptsPrecompile(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, fragErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(fragErrs) != 0 {
		for _, fe := range fragErrs {
			t.Errorf("fragment load error: %s: %v", fe.Path, fe.Err)
		}
		t.FailNow()
	}
	if reg.Len() == 0 {
		t.Fatalf("LoadFragmentsFromDir(%s) returned an empty registry; smoke test would give false confidence — a prompt with a broken fragment ref would not fail here", builtinDir)
	}
	SetCurrentFragments(reg)

	prompts, loadErrs, err := LoadPromptsFromDirWithErrors(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDirWithErrors(%s): %v", builtinDir, err)
	}
	if len(prompts) == 0 {
		t.Fatalf("LoadPromptsFromDirWithErrors(%s) returned no prompts; expected the full builtin corpus", builtinDir)
	}
	if len(loadErrs) != 0 {
		for _, le := range loadErrs {
			t.Errorf("prompt failed to load/precompile: %s: %v", le.Path, le.Err)
		}
		t.Fatalf("%d builtin prompt(s) failed precompile with fragment registry installed (%d fragments loaded, %d prompts loaded successfully)",
			len(loadErrs), reg.Len(), len(prompts))
	}

	t.Logf("all %d builtin prompt(s) precompiled successfully against %d fragment(s)", len(prompts), reg.Len())
}
