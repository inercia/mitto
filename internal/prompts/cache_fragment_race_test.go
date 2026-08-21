package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// TestPromptsCache_RecoversAfterLateFragmentInstall reproduces mitto-9jh.1.
//
// Symptom (from log analysis at 2026-07-28T18:54:28):
//   - 6 builtin prompts referencing {{ template "_shared/offer-file-as-bead" . }}
//     failed to load at boot with `template "_shared/offer-file-as-bead" not
//     defined`, then never recovered until the next full restart.
//
// Root cause (from investigate phase): PromptsCache.Get() can be triggered
// BEFORE SetCurrentFragments installs the fragment registry (e.g. by an MCP
// client that connects between mcpSrv.Start() and the fragment bootstrap in
// internal/web/server.go). The load fails closed in PrecompileTemplateConds,
// the failure is cached in c.prompts / c.loadErrors, and subsequent Get()
// calls short-circuit via needsReload() (which only tracks dir mtimes) — so
// installing the registry AFTER the poisoned load does not clear the cache
// and the affected prompt stays invisible.
//
// This test asserts the invariant: once a valid fragment registry is
// installed, PromptsCache must serve the prompt on the next Get(), regardless
// of whether a prior Get() ran against an empty registry. The current
// implementation FAILS this test — that is the reproduction.
func TestPromptsCache_RecoversAfterLateFragmentInstall(t *testing.T) {
	// Isolate the process-wide fragment registry singleton.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	// Point PromptsCache at a temp MITTO_DIR so we control the prompt tree.
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}

	// A prompt that references a fragment via `{{ template "..." . }}`.
	// Mirrors the shape of code/cleanup-code.prompt.yaml and friends.
	consumerYAML := `name: "Cleanup Code Test"
prompt: |
  Header before fragment.
  {{ template "shared/offer-file-as-bead" . }}
  Footer after fragment.
`
	consumerPath := filepath.Join(promptsDir, "cleanup-code-test.prompt.yaml")
	if err := os.WriteFile(consumerPath, []byte(consumerYAML), 0644); err != nil {
		t.Fatalf("write consumer prompt: %v", err)
	}

	// Simulate the race: MCP-triggered Get() lands BEFORE fragments are
	// installed. Clear the registry so PrecompileTemplateConds fails closed.
	SetCurrentFragments(nil)

	cache := NewPromptsCache()
	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("initial Get() returned top-level error: %v", err)
	}

	// Expect: prompt is dropped from the returned slice AND recorded as a
	// load error whose text matches the exact log-observed symptom (this is
	// what the log burst at 18:54:28 corresponds to).
	if len(prompts) != 0 {
		t.Fatalf("pre-registry Get(): len(prompts) = %d, want 0 (all consumers must fail without fragments)", len(prompts))
	}
	loadErrs := cache.LoadErrors()
	if len(loadErrs) != 1 {
		t.Fatalf("pre-registry Get(): len(LoadErrors) = %d, want 1; got %+v", len(loadErrs), loadErrs)
	}
	if !strings.Contains(loadErrs[0].Err.Error(), `template "shared/offer-file-as-bead" not defined`) {
		t.Fatalf("pre-registry Get(): LoadErrors[0] = %v, want to contain %q", loadErrs[0].Err, `template "shared/offer-file-as-bead" not defined`)
	}

	// Now install the proper fragment registry — this is the deferred bootstrap
	// that internal/web/server.go performs at line 1428–1442, AFTER MCP has
	// already been listening. In a correctly-behaved cache, the next Get()
	// must observe the newly-installed registry and re-parse the prompt.
	fragDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fragDir, "shared"), 0755); err != nil {
		t.Fatalf("mkdir fragment dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "shared", "offer-file-as-bead.tmpl"), []byte("fragment body OK"), 0644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	reg, fragErrs, err := ReloadFragmentsFromDirs([]string{fragDir})
	if err != nil {
		t.Fatalf("ReloadFragmentsFromDirs: %v", err)
	}
	if len(fragErrs) != 0 {
		t.Fatalf("fragment load errors: %+v", fragErrs)
	}
	SetCurrentFragments(reg)

	// The bug: needsReload() only tracks directory mtimes, not the fragment
	// registry generation, so this Get() returns the poisoned cached slice
	// (empty). This assertion FAILS on the current code and passes once the
	// fix (either invalidate cache on SetCurrentFragments, or track a
	// fragment-registry generation counter in needsReload) is in place.
	prompts2, err := cache.Get()
	if err != nil {
		t.Fatalf("post-registry Get() returned top-level error: %v", err)
	}
	if len(prompts2) != 1 {
		t.Fatalf("post-registry Get(): len(prompts) = %d, want 1 (cache must re-validate after fragment install); load errors: %+v", len(prompts2), cache.LoadErrors())
	}
	if prompts2[0].Name != "Cleanup Code Test" {
		t.Errorf("post-registry Get(): prompts[0].Name = %q, want %q", prompts2[0].Name, "Cleanup Code Test")
	}
	if len(cache.LoadErrors()) != 0 {
		t.Errorf("post-registry Get(): len(LoadErrors) = %d, want 0; got %+v", len(cache.LoadErrors()), cache.LoadErrors())
	}
}
