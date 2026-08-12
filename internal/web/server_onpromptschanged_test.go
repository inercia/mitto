package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/cel"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
)

// TestOnPromptsChanged_SwapsFragmentRegistryBeforeCacheReload pins mitto-ag0
// (parent mitto-ezw): the fs-watcher subscriber must install the fresh
// fragment registry BEFORE reloading the prompts cache, so consumers that
// reference `{{ template "…" . }}` fragments precompile against the fresh
// registry and are not dropped from promptsByName with `template "…" not
// defined`.
//
// The pre-fix ordering (cache reload first, then SetCurrentFragments) parsed
// every consumer against the stale registry, recorded a LoadError, and
// silently dropped the consumer. That was the exact data-loss window observed
// on 2026-07-27 (480 WARN `failed to load prompt file`, 43 loop-firing
// failures, 4 dropped user-composed queued prompts).
//
// The test starts from a nil process-wide fragment registry (the state right
// after `NewServer` but before the fs-watcher has ever fired), plants a
// consumer prompt + its fragment on disk, then fires
// `OnPromptsChanged{HasFragmentChanges: true}` and asserts the consumer
// resolves cleanly. If the ordering regresses, PrecompileTemplateConds runs
// against the nil registry, the consumer is dropped, and the assertions fail.
func TestOnPromptsChanged_SwapsFragmentRegistryBeforeCacheReload(t *testing.T) {
	// Isolate MITTO_DIR so PromptsCache.getAllDirs() and
	// Server.getFragmentScanDirs() both read only the files we plant.
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Force the process-wide fragment registry to the true default (nil) so
	// the first precompile attempt would fail if the ordering regressed.
	prevReg := prompts.CurrentFragments()
	prompts.SetCurrentFragments(nil)
	t.Cleanup(func() { prompts.SetCurrentFragments(prevReg) })

	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	fragDir := filepath.Join(promptsDir, "_shared")
	if err := os.MkdirAll(fragDir, 0o755); err != nil {
		t.Fatalf("mkdir fragments dir: %v", err)
	}

	// Fragment scanned by getFragmentScanDirs() via the MITTO_DIR/prompts/
	// scan root → registered under the name "_shared/foo".
	fragPath := filepath.Join(fragDir, "foo.tmpl")
	if err := os.WriteFile(fragPath, []byte("hello from foo"), 0o644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}

	// Consumer prompt at MITTO_DIR/prompts/consumer.prompt.yaml that
	// references the fragment via native `{{ template "…" . }}` syntax —
	// exactly the mitto-g61 attach path.
	consumerBody := `name: "consumer"
prompt: |
  intro {{ template "_shared/foo" . }} outro
`
	consumerPath := filepath.Join(promptsDir, "consumer.prompt.yaml")
	if err := os.WriteFile(consumerPath, []byte(consumerBody), 0o644); err != nil {
		t.Fatalf("write consumer prompt: %v", err)
	}

	// Minimal Server: a real PromptsCache scoped to the temp MITTO_DIR and a
	// live GlobalEventsManager (OnPromptsChanged bails early when nil).
	s := &Server{
		config: Config{
			PromptsCache: prompts.NewPromptsCache(),
			MittoConfig:  &configPkg.Config{},
		},
		eventsManager: NewGlobalEventsManager(),
	}

	// Fire the fs-watcher event. HasFragmentChanges: true is the branch that
	// installs a fresh registry. If ordering regresses (reload before swap),
	// the cache reload precompiles the consumer against the nil registry.
	s.OnPromptsChanged(configPkg.PromptsChangeEvent{
		HasFragmentChanges: true,
		HasPromptChanges:   true,
		ChangedDirs:        []string{promptsDir},
		Timestamp:          time.Now(),
	})

	// Outcome assertion 1: no PromptsCache LoadError for the consumer. The
	// characteristic regression signature is `template "_shared/foo" not
	// defined` surfaced through PrecompileTemplateConds.
	for _, pe := range s.config.PromptsCache.LoadErrors() {
		if strings.Contains(pe.Path, "consumer") {
			t.Fatalf("consumer prompt should load cleanly after OnPromptsChanged; got LoadError path=%s err=%v", pe.Path, pe.Err)
		}
		// Any residual error mentioning _shared/foo is the same regression
		// class, even if the path attribution shifts.
		if pe.Err != nil && strings.Contains(pe.Err.Error(), "_shared/foo") {
			t.Fatalf("expected no fragment-resolution errors after ordering fix; got path=%s err=%v", pe.Path, pe.Err)
		}
	}

	// Outcome assertion 2: consumer resolves by name via PromptsCache. If it
	// were dropped from promptsByName the loop below never finds it.
	all, err := s.config.PromptsCache.Get()
	if err != nil {
		t.Fatalf("PromptsCache.Get() error: %v", err)
	}
	var found bool
	for _, p := range all {
		if p.Name == "consumer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("consumer prompt not found in PromptsCache after OnPromptsChanged; %d prompts loaded", len(all))
	}

	// Ordering witness: the process-wide fragment registry MUST be installed
	// (non-nil, non-empty) once OnPromptsChanged returns. This is the
	// side-effect that makes assertions 1 & 2 pass: with the pre-fix order
	// this happened AFTER the cache reload, i.e. too late to rescue the
	// consumer's precompile. With the fix it happens BEFORE the reload.
	reg := prompts.CurrentFragments()
	if reg == nil {
		t.Fatal("expected process-wide fragment registry to be installed after OnPromptsChanged; got nil")
	}
	if reg.Len() == 0 {
		t.Fatalf("expected fragment registry to contain _shared/foo; got empty registry (dirs scanned: %v)", s.getFragmentScanDirs())
	}
	if _, ok := reg.Get("_shared/foo"); !ok {
		t.Errorf("expected fragment _shared/foo to be installed; registry has %d entries", reg.Len())
	}
}

func TestOnPromptsChanged_DefersDuringBulkDeployment(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	previous := prompts.CurrentFragments()
	t.Cleanup(func() { prompts.SetCurrentFragments(previous) })
	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "old.tmpl"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "old.prompt.yaml"), []byte("name: Old\nprompt: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry, _, err := prompts.ReloadFragmentsFromDirs([]string{promptsDir})
	if err != nil {
		t.Fatal(err)
	}
	prompts.SetCurrentFragments(registry)

	cache := prompts.NewPromptsCache()
	if _, err := cache.Get(); err != nil {
		t.Fatal(err)
	}
	loadedAt := cache.LoadedAt()
	s := &Server{
		config:        Config{PromptsCache: cache, MittoConfig: &configPkg.Config{}},
		eventsManager: NewGlobalEventsManager(),
	}

	finish, err := prompts.BeginDeployment(promptsDir)
	if err != nil {
		t.Fatal(err)
	}
	partial := "name: New\nprompt: |\n  {{ template \"missing\" . }}\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "new.prompt.yaml"), []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	s.OnPromptsChanged(configPkg.PromptsChangeEvent{
		HasFragmentChanges: true,
		HasPromptChanges:   true,
		ChangedDirs:        []string{promptsDir},
		Timestamp:          time.Now(),
	})

	if prompts.CurrentFragments() != registry {
		t.Fatal("fragment registry changed during active bulk deployment")
	}
	if !cache.LoadedAt().Equal(loadedAt) {
		t.Fatal("prompt cache reloaded during active bulk deployment")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
}

// TestOnPromptsChanged_InvalidatesGlobCache pins mitto-ayl.1: firing
// Server.OnPromptsChanged must invalidate the CEL glob-mode
// FileExists/DirExists cache across ALL folders (not just one), since
// PromptsChangeEvent carries prompt directories rather than workspace roots
// and so has no per-folder scope to invalidate selectively.
func TestOnPromptsChanged_InvalidatesGlobCache(t *testing.T) {
	dir := t.TempDir()
	tfPath := filepath.Join(dir, "a.tf")
	if err := os.WriteFile(tfPath, []byte("resource {}"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	fileExists := func() bool {
		funcs := cel.BuildTemplateFuncMap(&cel.PromptEnabledContext{
			Workspace: cel.WorkspaceContext{Folder: dir},
		})
		fn, ok := funcs["FileExists"].(func(string) bool)
		if !ok {
			t.Fatalf("FuncMap[\"FileExists\"] has unexpected type %T", funcs["FileExists"])
		}
		return fn("**/*.tf")
	}

	// Prime the glob cache with a match, then delete the file so a
	// stale-within-TTL read would still (wrongly) return true.
	if !fileExists() {
		t.Fatalf("priming FileExists(**/*.tf) = false, want true")
	}
	if err := os.Remove(tfPath); err != nil {
		t.Fatal(err)
	}
	if !fileExists() {
		t.Fatalf("cached FileExists(**/*.tf) after remove = false, want true (within TTL)")
	}

	s := &Server{
		config: Config{
			PromptsCache: prompts.NewPromptsCache(),
			MittoConfig:  &configPkg.Config{},
		},
		eventsManager: NewGlobalEventsManager(),
	}
	s.OnPromptsChanged(configPkg.PromptsChangeEvent{
		ChangedDirs: []string{t.TempDir()},
		Timestamp:   time.Now(),
	})

	if fileExists() {
		t.Errorf("FileExists(**/*.tf) after OnPromptsChanged = true, want false (glob cache should have been invalidated)")
	}
}
