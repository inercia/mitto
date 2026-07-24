package prompts

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// caseSensitiveFS reports whether the given directory lives on a
// case-sensitive filesystem. Used to skip tests that rely on being able to
// create two files whose names differ only in case (impossible on the
// default APFS / HFS+ configurations of macOS).
func caseSensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	lower := filepath.Join(dir, "case-probe.tmp")
	upper := filepath.Join(dir, "CASE-PROBE.TMP")
	if err := os.WriteFile(lower, []byte("l"), 0644); err != nil {
		t.Fatalf("probe write failed: %v", err)
	}
	defer os.Remove(lower)
	if err := os.WriteFile(upper, []byte("u"), 0644); err != nil {
		// If we cannot even create the upper-case sibling we cannot decide;
		// treat as case-insensitive so callers skip the fragile branch.
		return false
	}
	defer os.Remove(upper)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("probe readdir failed: %v", err)
	}
	distinct := 0
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "case-probe.tmp") {
			distinct++
		}
	}
	return distinct == 2
}

// writeFile is a t.Helper() wrapper that creates parent directories as
// needed and writes the payload with 0644.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLoadFragmentsFromDir_NonExistent returns an empty (non-nil) registry
// and no errors — matching LoadPromptsFromDirWithErrors' tolerance for
// absent directories.
func TestLoadFragmentsFromDir_NonExistent(t *testing.T) {
	reg, loadErrs, err := LoadFragmentsFromDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir returned error: %v", err)
	}
	if len(loadErrs) != 0 {
		t.Errorf("loadErrs = %v, want none", loadErrs)
	}
	if reg == nil {
		t.Fatal("registry must be non-nil for missing dir")
	}
	if reg.Len() != 0 {
		t.Errorf("Len = %d, want 0", reg.Len())
	}
}

// TestLoadFragmentsFromDir_Empty walks an empty directory and returns an
// empty registry.
func TestLoadFragmentsFromDir_Empty(t *testing.T) {
	reg, loadErrs, err := LoadFragmentsFromDir(t.TempDir())
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	if reg.Len() != 0 {
		t.Errorf("Len = %d, want 0", reg.Len())
	}
}

// TestLoadFragmentsFromDir_SingleFragment loads a single top-level fragment
// and exposes it via Get/All/Names. Uses .Args.Name (a map access) so
// missingkey=zero handles the empty PromptEnabledContext used for load-time
// validation.
func TestLoadFragmentsFromDir_SingleFragment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simple.tmpl"), "hello {{ .Args.Name }}")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	body, ok := reg.Get("simple")
	if !ok {
		t.Fatal(`Get("simple") = _, false; want true`)
	}
	if body != "hello {{ .Args.Name }}" {
		t.Errorf("body = %q, want %q", body, "hello {{ .Args.Name }}")
	}
	if got := reg.Names(); !reflect.DeepEqual(got, []string{"simple"}) {
		t.Errorf("Names() = %v, want [simple]", got)
	}
}

// TestLoadFragmentsFromDir_NestedSubdirectory walks nested directories and
// produces slash-namespaced names.
func TestLoadFragmentsFromDir_NestedSubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "github", "pr-comments.tmpl"), "pr body")
	writeFile(t, filepath.Join(dir, "top.tmpl"), "top body")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	got := reg.Names()
	want := []string{"github/pr-comments", "top"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if b, _ := reg.Get("github/pr-comments"); b != "pr body" {
		t.Errorf(`Get("github/pr-comments") = %q, want "pr body"`, b)
	}
}

// TestLoadFragmentsFromDir_CoLocationIsolation is the acceptance-criterion
// test called out on the bead: given a directory containing both
// foo.prompt.yaml and foo.tmpl and bar/baz.tmpl, LoadFragmentsFromDir
// returns exactly {foo, bar/baz} and never touches the .prompt.yaml file.
func TestLoadFragmentsFromDir_CoLocationIsolation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "foo.tmpl"), "fragment foo")
	writeFile(t, filepath.Join(dir, "foo.prompt.yaml"), `name: "Foo"
prompt: |
  I am a prompt, not a fragment.
`)
	writeFile(t, filepath.Join(dir, "bar", "baz.tmpl"), "fragment bar/baz")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}

	got := reg.Names()
	want := []string{"bar/baz", "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	// Symmetric check: LoadPromptsFromDir must not pick up the .tmpl files.
	prompts, perr := LoadPromptsFromDir(dir)
	if perr != nil {
		t.Fatalf("LoadPromptsFromDir failed: %v", perr)
	}
	if len(prompts) != 1 || prompts[0].Name != "Foo" {
		t.Errorf("LoadPromptsFromDir crossed over into fragments: %+v", prompts)
	}
}

// TestLoadFragmentsFromDir_IgnoresNonTmpl proves that unrelated file types
// are silently skipped (no load error).
func TestLoadFragmentsFromDir_IgnoresNonTmpl(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.md"), "readme")
	writeFile(t, filepath.Join(dir, "notes.txt"), "notes")
	writeFile(t, filepath.Join(dir, "keep.tmpl"), "keep me")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	if got := reg.Names(); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Errorf("Names() = %v, want [keep]", got)
	}
}

// TestLoadFragmentsFromDir_CaseInsensitiveExtension proves the extension
// match is case-insensitive: foo.TMPL / bar.TmPl are recognised.
func TestLoadFragmentsFromDir_CaseInsensitiveExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha.TMPL"), "A")
	writeFile(t, filepath.Join(dir, "beta.TmPl"), "B")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	got := reg.Names()
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// TestLoadFragmentsFromDir_BrokenSyntax surfaces a parse-time syntax error
// as a FragmentLoadError, while a sibling good fragment still loads.
func TestLoadFragmentsFromDir_BrokenSyntax(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.tmpl"), "good {{ .Args.X }}")
	// Missing closing brace — text/template parse fails.
	writeFile(t, filepath.Join(dir, "broken.tmpl"), "{{ if .Args.X ")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil {
		t.Fatalf("walk-level error: %v", err)
	}
	if len(loadErrs) != 1 {
		t.Fatalf("loadErrs = %v, want 1 entry", loadErrs)
	}
	if loadErrs[0].Path != "broken.tmpl" {
		t.Errorf("loadErr.Path = %q, want %q", loadErrs[0].Path, "broken.tmpl")
	}
	if _, ok := reg.Get("broken"); ok {
		t.Error("broken fragment must not be registered")
	}
	if _, ok := reg.Get("good"); !ok {
		t.Error("good fragment must still be registered")
	}
	// Error() and Unwrap() surface the underlying error.
	if !strings.Contains(loadErrs[0].Error(), "broken.tmpl") {
		t.Errorf("Error() missing path: %q", loadErrs[0].Error())
	}
	if loadErrs[0].Unwrap() == nil {
		t.Error("Unwrap() must return the underlying error")
	}
}

// TestLoadFragmentsFromDir_InvalidCELInCond validates that a fragment
// containing a Cond literal with invalid CEL is caught at load time,
// exercising the condStub reuse pattern.
func TestLoadFragmentsFromDir_InvalidCELInCond(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cel-bad.tmpl"),
		`{{ if Cond "this is ::: not valid CEL" }}x{{ end }}`)

	_, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil {
		t.Fatalf("walk-level error: %v", err)
	}
	if len(loadErrs) != 1 {
		t.Fatalf("loadErrs = %v, want 1 entry", loadErrs)
	}
	if !strings.Contains(loadErrs[0].Error(), "cel-bad.tmpl") {
		t.Errorf("Error() missing path: %q", loadErrs[0].Error())
	}
}

// TestLoadFragmentsFromDir_ValidCELInCond proves that a fragment with a
// well-formed Cond literal loads cleanly.
func TestLoadFragmentsFromDir_ValidCELInCond(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cel-good.tmpl"),
		`{{ if Cond "Session.IsChild" }}child{{ end }}`)

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("unexpected error: err=%v, loadErrs=%v", err, loadErrs)
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}
}

// TestLoadFragmentsFromDir_DuplicateName demonstrates the first-wins
// duplicate detection. Only runs on a case-sensitive filesystem because
// the collision requires two distinct filesystem entries whose names
// differ only in the .tmpl / .TMPL casing.
func TestLoadFragmentsFromDir_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	if !caseSensitiveFS(t, dir) {
		t.Skipf("skipping: %s filesystem appears case-insensitive", runtime.GOOS)
	}
	writeFile(t, filepath.Join(dir, "foo.tmpl"), "first")
	writeFile(t, filepath.Join(dir, "foo.TMPL"), "second")

	reg, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil {
		t.Fatalf("walk-level error: %v", err)
	}
	if len(loadErrs) != 1 {
		t.Fatalf("loadErrs = %v, want exactly 1 (the duplicate)", loadErrs)
	}
	if !strings.Contains(loadErrs[0].Error(), "duplicate fragment name") {
		t.Errorf("loadErr = %q, want duplicate marker", loadErrs[0].Error())
	}
	// One entry survives (first-wins by walk order — foo.TMPL sorts before
	// foo.tmpl lexically, so the uppercase file lands first).
	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}
}

// TestValidateFragmentName_Rejects table-drives the name-rule guard.
func TestValidateFragmentName_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"absolute", "/etc/passwd"},
		{"dotdot", "a/../b"},
		{"empty-segment", "a//b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFragmentName(tc.in); err == nil {
				t.Errorf("validateFragmentName(%q) = nil, want error", tc.in)
			}
		})
	}
}

// TestFragmentRegistry_Get returns (body,false) for unknown names.
func TestFragmentRegistry_Get(t *testing.T) {
	reg := NewFragmentRegistry()
	if _, ok := reg.Get("missing"); ok {
		t.Error(`Get("missing") = _, true; want false`)
	}
	reg.entries["k"] = "v"
	body, ok := reg.Get("k")
	if !ok || body != "v" {
		t.Errorf(`Get("k") = %q, %v; want "v", true`, body, ok)
	}
}

// TestFragmentRegistry_All_ReturnsCopy proves that mutating the returned
// map does not affect the registry.
func TestFragmentRegistry_All_ReturnsCopy(t *testing.T) {
	reg := NewFragmentRegistry()
	reg.entries["a"] = "1"

	out := reg.All()
	out["a"] = "MUTATED"
	out["b"] = "NEW"

	if got, _ := reg.Get("a"); got != "1" {
		t.Errorf("registry mutated via All() return: got %q, want %q", got, "1")
	}
	if _, ok := reg.Get("b"); ok {
		t.Error("registry mutated via All() return: unexpected key b")
	}
}

// TestFragmentRegistry_Names_Sorted proves the ascending sort.
func TestFragmentRegistry_Names_Sorted(t *testing.T) {
	reg := NewFragmentRegistry()
	reg.entries["b/2"] = ""
	reg.entries["a/1"] = ""
	reg.entries["a/0"] = ""
	got := reg.Names()
	want := []string{"a/0", "a/1", "b/2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// TestFragmentRegistry_Merge_LaterWins proves the priority-chain semantics:
// the argument to Merge overrides entries with the same name.
func TestFragmentRegistry_Merge_LaterWins(t *testing.T) {
	base := NewFragmentRegistry()
	base.entries["shared"] = "base"
	base.entries["base-only"] = "b"

	over := NewFragmentRegistry()
	over.entries["shared"] = "over"
	over.entries["over-only"] = "o"

	base.Merge(over)

	if got, _ := base.Get("shared"); got != "over" {
		t.Errorf(`Get("shared") = %q, want "over" (later wins)`, got)
	}
	if got, _ := base.Get("base-only"); got != "b" {
		t.Errorf(`Get("base-only") = %q, want "b"`, got)
	}
	if got, _ := base.Get("over-only"); got != "o" {
		t.Errorf(`Get("over-only") = %q, want "o"`, got)
	}
}

// TestFragmentRegistry_Merge_NilSafe merging nil does not panic and is a
// no-op.
func TestFragmentRegistry_Merge_NilSafe(t *testing.T) {
	reg := NewFragmentRegistry()
	reg.entries["a"] = "1"
	reg.Merge(nil)
	if reg.Len() != 1 {
		t.Errorf("Len after nil merge = %d, want 1", reg.Len())
	}
}

// TestFragmentRegistry_Len tracks the entry count.
func TestFragmentRegistry_Len(t *testing.T) {
	reg := NewFragmentRegistry()
	if reg.Len() != 0 {
		t.Errorf("empty Len = %d, want 0", reg.Len())
	}
	reg.entries["a"] = ""
	reg.entries["b"] = ""
	if reg.Len() != 2 {
		t.Errorf("Len = %d, want 2", reg.Len())
	}
}

// TestFragmentLoadError_ErrorAndUnwrap covers the Error/Unwrap contract.
func TestFragmentLoadError_ErrorAndUnwrap(t *testing.T) {
	underlying := errors.New("underlying failure")
	e := FragmentLoadError{Path: "x/y.tmpl", Err: underlying}

	if got := e.Error(); !strings.Contains(got, "x/y.tmpl") || !strings.Contains(got, "underlying failure") {
		t.Errorf("Error() = %q, missing path or underlying msg", got)
	}
	if !errors.Is(e, underlying) {
		t.Error("errors.Is(FragmentLoadError, underlying) = false")
	}
}
