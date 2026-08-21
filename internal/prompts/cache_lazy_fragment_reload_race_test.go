package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// TestPromptsCache_LazyReload_DoesNotRefreshFragmentRegistry reproduces
// mitto-aczx.
//
// Unlike the fs-watcher subscriber path (internal/web/server.go
// OnPromptsChanged), which calls ReloadFragmentsFromDirs + SetCurrentFragments
// BEFORE invalidating PromptsCache when a *.tmpl fragment changes,
// PromptsCache's own lazy modtime-driven reload() (triggered by any Get()
// after a scanned directory's mtime changes — see needsReload) never
// refreshes the fragment registry itself; it only samples whatever
// CurrentFragments() currently holds at that instant.
//
// Symptom (log-observed 2026-08-06T23:39:24): a deploy wrote a new shared
// fragment (_shared/cleanup-child-conversations.tmpl) together with three
// consumer prompts into the same builtin directory tree. The fragment file
// was on disk — ReloadFragmentsFromDirs would have picked it up cleanly —
// but the in-memory fragment registry singleton had not yet been refreshed
// by the fs-watcher's debounced event. A PromptsCache.Get() landing in that
// window re-parsed the three consumers against the STALE registry,
// PrecompileTemplateConds failed with `template
// "_shared/cleanup-child-conversations" not defined`, and all three were
// evicted from the cache for that reload cycle.
//
// This test reproduces the window deterministically (no timing/goroutines
// needed): write the fragment + a consumer prompt directly into the cache's
// scanned prompt directory (which bumps its mtime and forces needsReload()
// to fire), WITHOUT ever calling SetCurrentFragments/ReloadFragmentsFromDirs
// to pick up the new fragment — exactly what a lazy caller (MCP prompt list,
// REST /api/prompts, loop dispatch) does; only the fs-watcher subscriber in
// internal/web/server.go performs that extra refresh step, and it hasn't
// fired yet in this window.
//
// Get() must still return the consumer once the fragment exists on disk in
// the scanned directory tree. This assertion FAILS on the current
// implementation because PromptsCache.reload() never re-scans fragments from
// disk on its own — that is the reproduction.
func TestPromptsCache_LazyReload_DoesNotRefreshFragmentRegistry(t *testing.T) {
	// Isolate the process-wide fragment registry singleton.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}

	// Baseline: registry has no fragments yet (matches process start before
	// any deploy). Prime the cache against an empty prompts dir so its
	// dirModTimes/fragmentsGen reflect a clean "before deploy" snapshot.
	SetCurrentFragments(NewFragmentRegistry())
	cache := NewPromptsCache()
	if _, err := cache.Get(); err != nil {
		t.Fatalf("baseline Get(): %v", err)
	}
	if got := cache.Count(); got != 0 {
		t.Fatalf("baseline Count() = %d, want 0", got)
	}

	// Simulate the deploy: write BOTH the new shared fragment and the
	// consumer prompt into the same directory tree the cache scans,
	// mirroring `mitto prompts update-builtin` writing _shared/*.tmpl and
	// beads/*.prompt.yaml side by side. Deliberately do NOT call
	// ReloadFragmentsFromDirs/SetCurrentFragments here — that step only
	// happens on the fs-watcher's OnPromptsChanged path (internal/web/
	// server.go), not from a lazy Get() caller such as MCP prompt list,
	// REST /api/prompts, or loop dispatch, which is exactly the caller this
	// test models.
	if err := os.MkdirAll(filepath.Join(promptsDir, "_shared"), 0755); err != nil {
		t.Fatalf("mkdir _shared: %v", err)
	}
	fragBody := "Cleanup fragment body."
	fragPath := filepath.Join(promptsDir, "_shared", "cleanup-child-conversations.tmpl")
	if err := os.WriteFile(fragPath, []byte(fragBody), 0644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	consumerYAML := `name: "Reevaluate All Issues Test"
prompt: |
  Header before fragment.
  {{ template "_shared/cleanup-child-conversations" . }}
  Footer after fragment.
`
	consumerPath := filepath.Join(promptsDir, "reevaluate-test.prompt.yaml")
	if err := os.WriteFile(consumerPath, []byte(consumerYAML), 0644); err != nil {
		t.Fatalf("write consumer prompt: %v", err)
	}

	// A lazy Get() lands in the window: the prompts dir mtime changed (both
	// the fragment and the consumer were just written), so needsReload()
	// fires — but nothing has refreshed the fragment registry from disk, so
	// the consumer's PrecompileTemplateConds fails against a registry that
	// still doesn't know about the fragment sitting right next to it.
	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("post-deploy Get() returned top-level error: %v", err)
	}

	found := false
	for _, p := range prompts {
		if p.Name == "Reevaluate All Issues Test" {
			found = true
		}
	}
	if !found {
		loadErrs := cache.LoadErrors()
		t.Fatalf("consumer prompt evicted after fragment+prompt deploy (mitto-aczx): "+
			"len(prompts)=%d, want to find %q; LoadErrors=%+v",
			len(prompts), "Reevaluate All Issues Test", loadErrs)
	}
	for _, le := range cache.LoadErrors() {
		if strings.Contains(le.Err.Error(), "cleanup-child-conversations") {
			t.Errorf("unexpected load error referencing the fragment that now exists on disk: %v", le.Err)
		}
	}
}
