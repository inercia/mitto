package prompts

import "testing"

// installBuiltinFragmentsForTest loads the on-disk fragment registry from
// config/prompts/builtin (relative to the internal/prompts test binary) and
// installs it as the process-wide CurrentFragments singleton for the duration
// of the test, restoring the previous registry via t.Cleanup.
//
// It exists so that any test which parses a builtin prompt via ParsePromptFile
// (or renders it via RenderPromptTemplate) can resolve `{{ template
// "_shared/..." . }}` references — introduced by mitto-g61.9 when the
// "## Session Context" preamble was extracted into a shared fragment.
//
// Tests that specifically validate nil-registry behavior must NOT call this
// helper; the pilot fragment test and TestRenderPromptTemplate_Fragments
// install their own registry directly.
func installBuiltinFragmentsForTest(t *testing.T) {
	t.Helper()
	const builtinDir = "../../config/prompts/builtin"

	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(%s) per-file errors: %+v", builtinDir, loadErrs)
	}
	SetCurrentFragments(reg)
}
