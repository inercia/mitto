package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureBuiltinPrompts_PrunesStaleFiles verifies that EnsureBuiltinPrompts
// removes stale builtin files that are not part of the embedded set: both
// orphaned *.prompt.yaml files (consolidated/removed in a newer build) and
// legacy old-format *.md builtin files left over from pre-migration versions.
// Files with other extensions and the embedded prompts themselves must survive.
func TestEnsureBuiltinPrompts_PrunesStaleFiles(t *testing.T) {
	targetDir := t.TempDir()

	// First run deploys the embedded builtin prompts.
	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	embedded, err := ListEmbeddedPrompts()
	if err != nil {
		t.Fatalf("ListEmbeddedPrompts failed: %v", err)
	}
	if len(embedded) == 0 {
		t.Fatal("expected at least one embedded builtin prompt")
	}

	// Seed stale files that must be pruned on the next run.
	staleYAML := filepath.Join(targetDir, "totally-removed.prompt.yaml")
	staleMD1 := filepath.Join(targetDir, "legacy-old.md")
	staleMD2 := filepath.Join(targetDir, "another-legacy.md")
	for _, p := range []string{staleYAML, staleMD1, staleMD2} {
		if err := os.WriteFile(p, []byte("stale"), 0644); err != nil {
			t.Fatalf("failed to seed stale file %s: %v", p, err)
		}
	}

	// Seed a file with an unrelated extension that must be preserved (scope
	// is limited to *.prompt.yaml and legacy *.md files only).
	keepTXT := filepath.Join(targetDir, "keep-me.txt")
	if err := os.WriteFile(keepTXT, []byte("keep"), 0644); err != nil {
		t.Fatalf("failed to seed keep file: %v", err)
	}

	// Second run should prune the stale files and report that it changed state.
	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if !changed {
		t.Error("expected EnsureBuiltinPrompts to report changes after pruning stale files")
	}

	// Stale files must be gone.
	for _, p := range []string{staleYAML, staleMD1, staleMD2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected stale file %s to be pruned, but it still exists (err=%v)", p, err)
		}
	}

	// Unrelated file must be preserved.
	if _, err := os.Stat(keepTXT); err != nil {
		t.Errorf("expected unrelated file %s to be preserved, but stat failed: %v", keepTXT, err)
	}

	// Embedded prompts must still be present.
	for _, name := range embedded {
		p := filepath.Join(targetDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected embedded prompt %s to be preserved, but stat failed: %v", name, err)
		}
	}
}

// TestEnsureBuiltinPrompts_NoStaleFilesIsIdempotent verifies that running
// EnsureBuiltinPrompts twice with no stale files reports no changes on the
// second run (nothing to deploy, update, or prune).
func TestEnsureBuiltinPrompts_NoStaleFilesIsIdempotent(t *testing.T) {
	targetDir := t.TempDir()

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if changed {
		t.Error("expected no changes on second run with no stale files")
	}
}
