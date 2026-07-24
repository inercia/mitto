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

	// Every prompt rel-path Deploy claimed it wrote must match one embedded
	// prompt rel-path. Deploy's report vocabulary is a union of the prompt
	// and fragment sets (mitto-g61.2), so we filter Deployed down to
	// `.prompt.yaml` entries — fragments (`.tmpl`) are covered separately by
	// TestDeployBuiltinPrompts_DeploysEmbeddedFragments.
	embeddedSet := make(map[string]struct{}, len(embedded))
	for _, r := range embedded {
		embeddedSet[r] = struct{}{}
	}
	for _, rel := range result.Deployed {
		if !hasSuffix(rel, ".prompt.yaml") {
			continue
		}
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
	staleDir := filepath.Join(targetDir, "stale-orphan-group")
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

// TestIsBuiltinManagedFile pins the "which extensions does the deployer own?"
// contract (mitto-g61.2): the predicate must accept `.prompt.yaml` and `.tmpl`
// and reject every other extension the deployer must leave untouched. Legacy
// `.md` is intentionally NOT part of the managed set here — the prune step
// handles it as a one-way stale-cleanup case (never re-deployed).
func TestIsBuiltinManagedFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"foo.prompt.yaml", true},
		{"beads/foo.prompt.yaml", true}, // rel-path suffix still ends in .prompt.yaml
		{"pr-comments.tmpl", true},
		{"github/pr-comments.tmpl", true},
		{"legacy.md", false},
		{"random.txt", false},
		{"noext", false},
		{"foo.yaml", false},         // not the full ".prompt.yaml" suffix
		{"foo.tmpl.bak", false},     // suffix must actually be .tmpl
		{"foo.prompt.yaml~", false}, // editor backup — not managed
		{"", false},
	}
	for _, tc := range cases {
		if got := isBuiltinManagedFile(tc.name); got != tc.want {
			t.Errorf("isBuiltinManagedFile(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestListEmbeddedFragments_ReturnsFragmentSet asserts the ListEmbeddedFragments
// API surface: it must not error, must return only `.tmpl` rel-paths (never
// `.prompt.yaml`), and must be disjoint from ListEmbeddedPrompts() so callers
// that need the full managed set can safely union both lists (mitto-g61.2).
func TestListEmbeddedFragments_ReturnsFragmentSet(t *testing.T) {
	fragments, err := ListEmbeddedFragments()
	if err != nil {
		t.Fatalf("ListEmbeddedFragments failed: %v", err)
	}

	prompts, err := ListEmbeddedPrompts()
	if err != nil {
		t.Fatalf("ListEmbeddedPrompts failed: %v", err)
	}

	// Every entry must have the .tmpl suffix (fragment-only scope).
	for _, name := range fragments {
		if !hasSuffix(name, ".tmpl") {
			t.Errorf("ListEmbeddedFragments returned %q, which does not have the .tmpl suffix", name)
		}
	}

	// Prompts and fragments must be disjoint sets — the split is the whole
	// point of the two lists.
	promptSet := make(map[string]struct{}, len(prompts))
	for _, p := range prompts {
		promptSet[p] = struct{}{}
	}
	for _, f := range fragments {
		if _, clash := promptSet[f]; clash {
			t.Errorf("rel-path %q appears in BOTH ListEmbeddedPrompts and ListEmbeddedFragments (must be disjoint)", f)
		}
	}

	// Log the current fragment count so a future embed regression (fragments
	// added upstream but list is empty here) is visible in test output.
	t.Logf("ListEmbeddedFragments returned %d fragment(s)", len(fragments))
}

// hasSuffix is a tiny local helper to keep the test file dependency-free.
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// TestDeployBuiltinPrompts_DeploysEmbeddedFragments pins the widened Deploy
// walk (mitto-g61.2): every rel-path returned by ListEmbeddedFragments must
// land on disk at the same rel-path after DeployBuiltinPrompts, mirroring the
// prompt-side contract in TestDeployBuiltinPrompts_PreservesSubdirectoryStructure.
//
// The test passes vacuously today (no `.tmpl` files are embedded yet — child #8
// lands the pilot fragment), but structurally locks the walker: once fragments
// exist in the embed tree, any regression that silently skips them will make
// this test fail.
func TestDeployBuiltinPrompts_DeploysEmbeddedFragments(t *testing.T) {
	targetDir := t.TempDir()

	result, err := DeployBuiltinPrompts(targetDir, true)
	if err != nil {
		t.Fatalf("DeployBuiltinPrompts failed: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("DeployBuiltinPrompts reported errors: %v", result.Errors)
	}

	fragments, err := ListEmbeddedFragments()
	if err != nil {
		t.Fatalf("ListEmbeddedFragments failed: %v", err)
	}
	if len(fragments) == 0 {
		t.Log("no embedded fragments yet — test is structurally meaningful once child #8 lands the pilot")
	}

	// Every embedded fragment rel-path must exist on disk under the same
	// rel-path. Forward-slash rel-paths from fs.WalkDir get converted to
	// native paths for the on-disk stat.
	for _, relSlash := range fragments {
		onDisk := filepath.Join(targetDir, filepath.FromSlash(relSlash))
		if _, err := os.Stat(onDisk); err != nil {
			t.Errorf("expected embedded fragment %q to be deployed at %s, but stat failed: %v", relSlash, onDisk, err)
		}
	}

	// Every fragment rel-path Deploy claimed it wrote must match one embedded
	// fragment. Deploy's report vocabulary is a union of the prompt and
	// fragment rel-path sets, so we filter the Deployed slice down to entries
	// with the .tmpl suffix and check they are in the embedded fragment set.
	fragmentSet := make(map[string]struct{}, len(fragments))
	for _, r := range fragments {
		fragmentSet[r] = struct{}{}
	}
	for _, rel := range result.Deployed {
		if !hasSuffix(rel, ".tmpl") {
			continue
		}
		if _, ok := fragmentSet[rel]; !ok {
			t.Errorf("Deploy reported .tmpl %q but it is not in the embedded fragment set", rel)
		}
	}
}

// TestEnsureBuiltinPrompts_PrunesStaleTmpl pins the widened prune filter
// (mitto-g61.2) for the top-level case: a stale `.tmpl` at the target-dir
// top level that has no counterpart in the embedded set is stale and must
// be removed on the next run, and the run must report `changed=true`.
func TestEnsureBuiltinPrompts_PrunesStaleTmpl(t *testing.T) {
	targetDir := t.TempDir()

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	// Seed a stale top-level `.tmpl` with a rel-path that is guaranteed not
	// to be in the embedded fragment set (no such fragment exists yet, and
	// this name is intentionally not a plausible future path).
	staleTmpl := filepath.Join(targetDir, "definitely-not-embedded.tmpl")
	if err := os.WriteFile(staleTmpl, []byte("stale fragment"), 0644); err != nil {
		t.Fatalf("failed to seed stale tmpl: %v", err)
	}

	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if !changed {
		t.Error("expected EnsureBuiltinPrompts to report changes after pruning a stale .tmpl")
	}
	if _, err := os.Stat(staleTmpl); !os.IsNotExist(err) {
		t.Errorf("expected stale tmpl %s to be pruned, but stat=%v", staleTmpl, err)
	}
}

// TestEnsureBuiltinPrompts_PrunesStaleNestedTmpl mirrors the existing
// `.prompt.yaml` nested-prune coverage for the widened `.tmpl` prune scope
// (mitto-g61.2): a stale `.tmpl` under an orphan subdirectory must be
// removed, and the now-empty parent must also be swept away by the
// bottom-up directory sweep.
func TestEnsureBuiltinPrompts_PrunesStaleNestedTmpl(t *testing.T) {
	targetDir := t.TempDir()

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	staleDir := filepath.Join(targetDir, "orphan-fragment-group")
	stale := filepath.Join(staleDir, "stale.tmpl")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("failed to create stale dir: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale nested fragment"), 0644); err != nil {
		t.Fatalf("failed to seed stale nested tmpl: %v", err)
	}

	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if !changed {
		t.Error("expected EnsureBuiltinPrompts to report changes after pruning a stale nested .tmpl")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected stale nested tmpl %s to be pruned, but stat=%v", stale, err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("expected empty orphan dir %s to be swept, but stat=%v", staleDir, err)
	}
}

// TestEnsureBuiltinPrompts_PreservesEmbeddedFragments pins the widened prune
// scope's "keep embedded" half (mitto-g61.2): after seeding an unrelated
// stale `.tmpl`, every rel-path in ListEmbeddedFragments must still survive
// the pruning pass. Vacuous today (no embedded fragments), but locks the
// contract for when child #8 lands the pilot fragment.
func TestEnsureBuiltinPrompts_PreservesEmbeddedFragments(t *testing.T) {
	targetDir := t.TempDir()

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	fragments, err := ListEmbeddedFragments()
	if err != nil {
		t.Fatalf("ListEmbeddedFragments failed: %v", err)
	}

	// Seed a stale `.tmpl` that must NOT be in the embedded set to force
	// the prune pass to actually run.
	staleTmpl := filepath.Join(targetDir, "not-embedded-anywhere.tmpl")
	if err := os.WriteFile(staleTmpl, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed stale tmpl: %v", err)
	}

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}

	for _, relSlash := range fragments {
		onDisk := filepath.Join(targetDir, filepath.FromSlash(relSlash))
		if _, err := os.Stat(onDisk); err != nil {
			t.Errorf("expected embedded fragment %q to be preserved at %s, but stat failed: %v", relSlash, onDisk, err)
		}
	}
	if _, err := os.Stat(staleTmpl); !os.IsNotExist(err) {
		t.Errorf("expected stale tmpl %s to be pruned, but stat=%v", staleTmpl, err)
	}
}

// TestEnsureBuiltinPrompts_TmplNoStaleIsIdempotent extends the idempotence
// contract to the widened `.tmpl` walk (mitto-g61.2): once an initial run has
// deployed the full embedded set (including any fragments), the next run with
// no on-disk drift must report `changed=false`. A `changed=true` here would
// signal an accidental unconditional write on the fragment code path (e.g.
// missing content-diff guard in the widened walk).
//
// This test intentionally overlaps with TestEnsureBuiltinPrompts_NoStaleFilesIsIdempotent
// but is retained as a phase-scoped pin: it lives with the other `.tmpl`
// coverage so a future regression to the fragment code path is diagnostically
// obvious.
func TestEnsureBuiltinPrompts_TmplNoStaleIsIdempotent(t *testing.T) {
	targetDir := t.TempDir()

	// First run deploys the whole embedded set (prompts + any fragments).
	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatalf("initial EnsureBuiltinPrompts failed: %v", err)
	}

	// Second run with no drift must report changed=false — proving fragment
	// writes are content-gated exactly like prompt writes.
	changed, err := EnsureBuiltinPrompts(targetDir)
	if err != nil {
		t.Fatalf("second EnsureBuiltinPrompts failed: %v", err)
	}
	if changed {
		t.Error("expected second EnsureBuiltinPrompts to report changed=false (idempotent across the widened .tmpl walk)")
	}
}
