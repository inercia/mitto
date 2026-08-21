package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/cel"
)

// TestOnBeadsChanged_InvalidatesGlobCache pins mitto-ayl.1: firing
// Server.OnBeadsChanged for a working dir must also invalidate the CEL
// glob-mode FileExists/DirExists cache for that same folder, riding the
// existing event.WorkingDirs loop alongside cel.InvalidateBeadsCache. This
// lets a glob gate (e.g. FileExists("**/SKILL.md")) observe an external
// mutation (bd from another process, git pull) without waiting out
// globCacheTTL.
func TestOnBeadsChanged_InvalidatesGlobCache(t *testing.T) {
	dir := t.TempDir()
	tfPath := filepath.Join(dir, "a.tf")
	if err := os.WriteFile(tfPath, []byte("resource {}"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	// Exercise the same public surface a prompt's enabledWhen/template uses:
	// BuildTemplateFuncMap's "FileExists" closure, scoped to dir.
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

	// Prime the glob cache: a match for "**/*.tf" under dir.
	if !fileExists() {
		t.Fatalf("priming FileExists(**/*.tf) = false, want true")
	}

	// Delete the file. Within TTL, a repeat call must still see the cached
	// (now stale) true.
	if err := os.Remove(tfPath); err != nil {
		t.Fatal(err)
	}
	if !fileExists() {
		t.Fatalf("cached FileExists(**/*.tf) after remove = false, want true (within TTL)")
	}

	// Minimal Server: OnBeadsChanged only needs s.eventsManager (nil is
	// fine — it returns early after the invalidation loop runs).
	s := &Server{}
	s.OnBeadsChanged(watcher.BeadsChangeEvent{
		WorkingDirs: []string{dir},
		ChangedDirs: []string{filepath.Join(dir, ".beads")},
		Timestamp:   time.Now(),
	})

	// The glob cache entry for dir must have been dropped, forcing a
	// re-walk that observes the deletion.
	if fileExists() {
		t.Errorf("FileExists(**/*.tf) after OnBeadsChanged = true, want false (glob cache should have been invalidated)")
	}
}
