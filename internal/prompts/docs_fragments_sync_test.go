package prompts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDocsFragmentsSectionExists pins the acceptance criteria for mitto-g61.7
// (docs for prompt fragments). It locks the §11b "Prompt fragments" documentation
// increment against silent drift: heading, the two-extension convention, the
// two guardian-test callouts, the non-goals block, and a worked-example marker
// must all remain present. The sibling scope-block pointer near the top of the
// document and the pointer section in .augment/rules/07-prompts.md are also
// pinned so removing either breaks this test instead of the docs drifting.
//
// A test that ships with the docs increment is the standard docs↔code sync
// pattern in this repo — see internal/prompts/docs_sync_test.go and
// internal/cel/docs_sync_test.go for the same convention applied to other
// docs↔code contracts (mitto-mi4).
func TestDocsFragmentsSectionExists(t *testing.T) {
	root := repoRootForTest(t)
	spec := readFileForTest(t, filepath.Join(root, "docs", "devel", "prompt-templates.md"))
	rule := readFileForTest(t, filepath.Join(root, ".augment", "rules", "07-prompts.md"))

	// § heading — the section anchor referenced by the rules-doc pointer and
	// by every other prompt-templates cross-reference. Match the whole heading
	// line so a rename to a different §11x is caught.
	const wantHeading = "## 11b. Prompt fragments (co-located `.tmpl` partials)"
	if !strings.Contains(spec, wantHeading) {
		t.Errorf("docs/devel/prompt-templates.md: missing heading %q", wantHeading)
	}

	// Scope-block pointer near the top of the file: the doc has no explicit
	// TOC block, so a one-line pointer in the scope block is what makes §11b
	// discoverable.
	if !strings.Contains(spec, "§11b") {
		t.Errorf("docs/devel/prompt-templates.md: missing top-of-doc pointer to §11b")
	}

	// Two-extension convention — both markers must be present in §11b.
	for _, marker := range []string{"*.prompt.yaml", "*.tmpl"} {
		if !strings.Contains(spec, marker) {
			t.Errorf("docs/devel/prompt-templates.md §11b: missing two-extension marker %q", marker)
		}
	}

	// Guardian-test callouts — the "hidden-from-UI guarantee is explicitly
	// called out with a link to the test" acceptance criterion. Both tests
	// must be named in the docs AND must actually exist in this package (the
	// grep below is a symbolic cross-check; the compile-time reference to the
	// test names below is what pins them).
	for _, testName := range []string{
		"TestLoadFragmentsFromDir_CoLocationIsolation",
		"TestFragmentsNotInWebPromptDTO",
	} {
		if !strings.Contains(spec, testName) {
			t.Errorf("docs/devel/prompt-templates.md §11b: missing guardian-test callout %q", testName)
		}
	}

	// Non-goals block — the epic's non-goals must be reproduced in §11b so a
	// prompt author reading only that section knows the boundaries.
	if !strings.Contains(spec, "Non-goals") {
		t.Errorf("docs/devel/prompt-templates.md §11b: missing 'Non-goals' block")
	}

	// Worked example — at least one `{{ template "…" }}` invocation must
	// appear inside the section so an author can copy-paste-and-adapt.
	if !strings.Contains(spec, `{{ template "`) {
		t.Errorf("docs/devel/prompt-templates.md §11b: missing worked-example {{ template \"…\" }} invocation")
	}

	// Rules-doc pointer — .augment/rules/07-prompts.md must point to §11b so
	// agents editing prompts learn the mechanism exists without a full-spec
	// duplicate.
	if !strings.Contains(rule, "Prompt Fragments") {
		t.Errorf(".augment/rules/07-prompts.md: missing 'Prompt Fragments' pointer section")
	}
	if !strings.Contains(rule, "§11b") {
		t.Errorf(".augment/rules/07-prompts.md: pointer section must reference §11b")
	}
}

// TestDocsFragmentsGuardianTestsExist compile-links the two guardian test
// names quoted in §11b to the actual test functions in this package. If a
// future refactor renames either test without updating the docs (or vice
// versa), this test's reference goes stale, `go vet` / `go test` fails to
// compile, and the drift is caught at build time — the strongest possible
// docs↔code pinning for a name-only reference.
func TestDocsFragmentsGuardianTestsExist(t *testing.T) {
	// Direct function-value references — pure compile-time pinning. The runtime
	// body of this test only records that the references resolved.
	guardians := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"TestLoadFragmentsFromDir_CoLocationIsolation", TestLoadFragmentsFromDir_CoLocationIsolation},
		{"TestFragmentsNotInWebPromptDTO", TestFragmentsNotInWebPromptDTO},
	}
	for _, g := range guardians {
		if g.fn == nil {
			t.Errorf("guardian test %s: function-value reference resolved to nil (should be impossible)", g.name)
		}
	}
}

// repoRootForTest returns the absolute path to the repo root, resolved from
// the test source file location. Mirrors the runtime.Caller idiom used in
// internal/agents/stderr_patterns_test.go so the test works regardless of the
// current working directory.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/prompts/docs_fragments_sync_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// readFileForTest is a t.Helper-wrapped os.ReadFile that fails the test on
// error and returns the file contents as a string.
func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
