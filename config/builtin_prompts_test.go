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

// TestDeployBuiltinPrompts_PreservesSubdirectoryStructure pins the Phase A
// recursive-embed contract (mitto-j88.1): every rel-path returned by
// ListEmbeddedPrompts must land at the same rel-path on disk after
// DeployBuiltinPrompts. Regression pin for the WalkDir-based Deploy path
// (a top-level ReadDir-only variant would silently no-op on nested files).
// The check is meaningful even while the embedded tree is flat — it also
// asserts Deploy never drops a top-level file — and becomes structurally
// meaningful once Phase B introduces topic-based subfolders.
func TestDeployBuiltinPrompts_PreservesSubdirectoryStructure(t *testing.T) {
	targetDir := t.TempDir()

	result, err := DeployBuiltinPrompts(targetDir, true)
	if err != nil {
		t.Fatalf("DeployBuiltinPrompts failed: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("DeployBuiltinPrompts reported errors: %v", result.Errors)
	}

	embedded, err := ListEmbeddedPrompts()
	if err != nil {
		t.Fatalf("ListEmbeddedPrompts failed: %v", err)
	}
	if len(embedded) == 0 {
		t.Fatal("expected at least one embedded builtin prompt")
	}

	// Every embedded rel-path must exist on disk under the same rel-path.
	// ListEmbeddedPrompts returns forward-slash rel-paths (fs.WalkDir keys);
	// convert to a native path for the on-disk stat.
	for _, relSlash := range embedded {
		onDisk := filepath.Join(targetDir, filepath.FromSlash(relSlash))
		if _, err := os.Stat(onDisk); err != nil {
			t.Errorf("expected embedded rel-path %q to be deployed at %s, but stat failed: %v", relSlash, onDisk, err)
		}
	}

	// Every rel-path Deploy claimed it wrote must match one embedded rel-path.
	// Deploy's report vocabulary is the same forward-slash rel-path set.
	embeddedSet := make(map[string]struct{}, len(embedded))
	for _, r := range embedded {
		embeddedSet[r] = struct{}{}
	}
	for _, rel := range result.Deployed {
		if _, ok := embeddedSet[rel]; !ok {
			t.Errorf("Deploy reported %q but it is not in the embedded rel-path set", rel)
		}
	}
}

// TestEnsureBuiltinPrompts_PrunesStaleNestedFiles pins the recursive-prune
// half of Phase A (mitto-j88.1): a stale *.prompt.yaml under a nested
// subdirectory must be removed on the next EnsureBuiltinPrompts run, and
// the now-empty parent directory must also be swept away so the on-disk
// layout mirrors the embedded layout exactly (no orphan dirs left behind).
func TestEnsureBuiltinPrompts_PrunesStaleNestedFiles(t *testing.T) {
	targetDir := t.TempDir()

	// First run deploys the embedded builtin prompts.
	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	// Seed a stale nested *.prompt.yaml under a subdirectory that is NOT
	// part of the embedded set. The subdirectory itself must also be new
	// (i.e. not one of the embedded groups), so the sweep can prove it
	// removes the now-empty parent.
	staleDir := filepath.Join(targetDir, "misc")
	stale := filepath.Join(staleDir, "stale.prompt.yaml")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("failed to create stale dir: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed stale nested file: %v", err)
	}

	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if !changed {
		t.Error("expected EnsureBuiltinPrompts to report changes after pruning a stale nested file")
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected stale nested file %s to be pruned, but stat=%v", stale, err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("expected empty stale dir %s to be swept, but stat=%v", staleDir, err)
	}
}

// TestEnsureBuiltinPrompts_CreatesNestedDirsIdempotently guards against a
// common WalkDir-conversion bug where directory MkdirAll flips a mtime and
// tricks the change detector on the second run. Two consecutive runs with
// no drift must report changed=false on the second. This complements
// TestEnsureBuiltinPrompts_NoStaleFilesIsIdempotent by exercising the same
// idempotence contract through the recursive code path (mitto-j88.1).
func TestEnsureBuiltinPrompts_CreatesNestedDirsIdempotently(t *testing.T) {
	targetDir := t.TempDir()

	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}
	// First run on an empty targetDir MUST report changed=true (it just
	// deployed the whole embedded set from scratch); this guards against a
	// regression where the walker skips fresh writes.
	if !changed {
		t.Error("expected first EnsureBuiltinPrompts on an empty dir to report changed=true")
	}

	changed2, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if changed2 {
		t.Error("expected second EnsureBuiltinPrompts to report changed=false (idempotent)")
	}
}
