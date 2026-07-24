package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReloadFragmentsFromDirs_LastWins verifies the merge order: the later
// directory in the slice overrides same-name fragments from earlier
// directories (matching FragmentRegistry.Merge and the builtin < settings <
// workspace priority chain).
func TestReloadFragmentsFromDirs_LastWins(t *testing.T) {
	earlier := t.TempDir()
	later := t.TempDir()

	writeFile(t, filepath.Join(earlier, "shared.tmpl"), "from-earlier")
	writeFile(t, filepath.Join(later, "shared.tmpl"), "from-later")
	writeFile(t, filepath.Join(earlier, "only-earlier.tmpl"), "e-only")
	writeFile(t, filepath.Join(later, "only-later.tmpl"), "l-only")

	reg, loadErrs, err := ReloadFragmentsFromDirs([]string{earlier, later})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs returned error: %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("loadErrs = %v, want none", loadErrs)
	}

	got, ok := reg.Get("shared")
	if !ok {
		t.Fatal("shared fragment missing from merged registry")
	}
	if got != "from-later" {
		t.Errorf("shared fragment body = %q, want %q (later wins)", got, "from-later")
	}
	if _, ok := reg.Get("only-earlier"); !ok {
		t.Error("only-earlier fragment missing")
	}
	if _, ok := reg.Get("only-later"); !ok {
		t.Error("only-later fragment missing")
	}
}

// TestReloadFragmentsFromDirs_MissingDirTolerated verifies missing directories
// in the slice do not cause a top-level error — they contribute an empty
// registry and the caller receives the successfully-loaded rest.
func TestReloadFragmentsFromDirs_MissingDirTolerated(t *testing.T) {
	present := t.TempDir()
	absent := filepath.Join(t.TempDir(), "does-not-exist")

	writeFile(t, filepath.Join(present, "x.tmpl"), "body")

	reg, loadErrs, err := ReloadFragmentsFromDirs([]string{absent, present, absent})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs returned error: %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("loadErrs = %v, want none", loadErrs)
	}
	if _, ok := reg.Get("x"); !ok {
		t.Error("x fragment missing from merged registry")
	}
}

// TestReloadFragmentsFromDirs_AccumulatesPerFileErrors verifies per-file
// parse errors are accumulated across all dirs but do not abort the merge —
// the returned registry still contains every good fragment.
func TestReloadFragmentsFromDirs_AccumulatesPerFileErrors(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	writeFile(t, filepath.Join(dirA, "good-a.tmpl"), "a {{ .Args.X }}")
	writeFile(t, filepath.Join(dirA, "broken.tmpl"), "{{ if .Args.X ")
	writeFile(t, filepath.Join(dirB, "good-b.tmpl"), "b {{ .Args.X }}")

	reg, loadErrs, err := ReloadFragmentsFromDirs([]string{dirA, dirB})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs returned error: %v", err)
	}
	if len(loadErrs) != 1 {
		t.Fatalf("loadErrs = %v, want exactly 1 (broken.tmpl)", loadErrs)
	}
	if !strings.Contains(loadErrs[0].Path, "broken.tmpl") {
		t.Errorf("loadErrs[0].Path = %q, want to contain broken.tmpl", loadErrs[0].Path)
	}
	if _, ok := reg.Get("good-a"); !ok {
		t.Error("good-a fragment missing")
	}
	if _, ok := reg.Get("good-b"); !ok {
		t.Error("good-b fragment missing")
	}
	if _, ok := reg.Get("broken"); ok {
		t.Error("broken fragment must not be present in registry")
	}
}

// TestReloadFragmentsAfterEdit_RenderReflectsChange is the acceptance-driven
// integration test: install a fragment, render a prompt referencing it, edit
// the fragment on disk, reload via ReloadFragmentsFromDirs +
// SetCurrentFragments, re-render, and verify the updated body is used. This
// mirrors what Server.OnPromptsChanged does when a HasFragmentChanges event
// arrives from the fs-watcher.
func TestReloadFragmentsAfterEdit_RenderReflectsChange(t *testing.T) {
	// Isolate the process-wide singleton so parallel tests are unaffected.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	dir := t.TempDir()
	fragPath := filepath.Join(dir, "greeting.tmpl")
	if err := os.WriteFile(fragPath, []byte("hello v1"), 0644); err != nil {
		t.Fatalf("write initial fragment: %v", err)
	}

	reg, loadErrs, err := ReloadFragmentsFromDirs([]string{dir})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs (initial): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("loadErrs (initial) = %v, want none", loadErrs)
	}
	SetCurrentFragments(reg)

	body := `{{ template "greeting" . }}`
	out, err := RenderPromptTemplate("t", body, nil, nil)
	if err != nil {
		t.Fatalf("initial render error: %v", err)
	}
	if out != "hello v1" {
		t.Fatalf("initial render = %q, want %q", out, "hello v1")
	}

	// Simulate an on-disk edit landing later than the mtime resolution.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(fragPath, []byte("hello v2"), 0644); err != nil {
		t.Fatalf("write updated fragment: %v", err)
	}

	reg2, loadErrs2, err := ReloadFragmentsFromDirs([]string{dir})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs (reload): %v", err)
	}
	if len(loadErrs2) != 0 {
		t.Fatalf("loadErrs (reload) = %v, want none", loadErrs2)
	}
	SetCurrentFragments(reg2)

	out2, err := RenderPromptTemplate("t", body, nil, nil)
	if err != nil {
		t.Fatalf("re-render error: %v", err)
	}
	if out2 != "hello v2" {
		t.Errorf("re-render after edit = %q, want %q", out2, "hello v2")
	}
}

// TestFragmentRegistry_Reload (mitto-g61.6 test #10) is the minimal Get()
// -oriented sibling of TestReloadFragmentsAfterEdit_RenderReflectsChange: it
// locks the semantic contract of FragmentRegistry.Get() after a reload,
// independent of the render pipeline. Complements the render-flavored test
// so a regression in the pure loader/registry path is caught even if the
// render integration is temporarily disabled.
func TestFragmentRegistry_Reload(t *testing.T) {
	dir := t.TempDir()
	fragPath := filepath.Join(dir, "k.tmpl")
	if err := os.WriteFile(fragPath, []byte("v1"), 0644); err != nil {
		t.Fatalf("write initial fragment: %v", err)
	}

	reg1, loadErrs, err := LoadFragmentsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir (initial): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("loadErrs (initial) = %v, want none", loadErrs)
	}
	got, ok := reg1.Get("k")
	if !ok {
		t.Fatal("initial: k missing from registry")
	}
	if got != "v1" {
		t.Errorf("initial Get(k) = %q, want %q", got, "v1")
	}

	// Simulate an on-disk edit landing later than the mtime resolution.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(fragPath, []byte("v2"), 0644); err != nil {
		t.Fatalf("write updated fragment: %v", err)
	}

	reg2, loadErrs2, err := LoadFragmentsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir (reload): %v", err)
	}
	if len(loadErrs2) != 0 {
		t.Fatalf("loadErrs (reload) = %v, want none", loadErrs2)
	}
	got2, ok := reg2.Get("k")
	if !ok {
		t.Fatal("reload: k missing from registry")
	}
	if got2 != "v2" {
		t.Errorf("reload Get(k) = %q, want %q", got2, "v2")
	}

	// Original registry (reg1) is unaffected — reload returns a new registry.
	if v, _ := reg1.Get("k"); v != "v1" {
		t.Errorf("reg1 Get(k) after reload = %q, want %q (registries are immutable snapshots)", v, "v1")
	}
}
