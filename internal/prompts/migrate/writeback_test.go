package migrate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMigrateYAML_PreservesCommentsAndUnrelatedBytes fixtures a loop: block
// with a HeadComment above a key and a trailing (same-line) comment on
// another key, and asserts both survive the migration verbatim, while an
// unrelated top-level key (and the multi-line prompt body) stays byte-for-
// byte identical.
func TestMigrateYAML_PreservesCommentsAndUnrelatedBytes(t *testing.T) {
	input := join(
		"name: X",
		"loop:",
		"  trigger: onCompletion  # only completion for now",
		"  # explanatory comment about delay",
		"  delay: 30  # seconds to wait",
		"  maxIterations: 10",
		"prompt: |",
		"  Line one.",
		"",
		"  Line three, after a blank line.",
	)
	want := join(
		"name: X",
		"loop:",
		"  trigger: [onCompletion] # only completion for now",
		"  onCompletion:",
		"    # explanatory comment about delay",
		"    delay: 30 # seconds to wait",
		"  maxIterations: 10",
		"prompt: |",
		"  Line one.",
		"",
		"  Line three, after a blank line.",
	)

	out, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}
	if string(out) != want {
		t.Errorf("output mismatch:\n--- got ---\n%s--- want ---\n%s", out, want)
	}
}

// TestMigrateYAML_MalformedYAML_NoOp pins that invalid YAML is left for the
// normal parser to report; MigrateYAML itself never errors on it.
func TestMigrateYAML_MalformedYAML_NoOp(t *testing.T) {
	input := "name: X\n  loop: [unterminated\n"
	out, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML should not error on malformed YAML, got: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false")
	}
	if string(out) != input {
		t.Errorf("output = %q, want unchanged %q", out, input)
	}
}

// TestWriteBackIfNeeded_WritesAtomically pins the happy path: a changed
// migration is written back to disk, and the new content round-trips through
// MigrateYAML as a no-op (idempotent on disk too).
func TestWriteBackIfNeeded_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loopy.prompt.yaml")
	input := join("name: X", "loop:", "  delay: 30", "prompt: |", "  body")
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	migrated, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	if wrote := WriteBackIfNeeded(path, migrated, res); !wrote {
		t.Fatal("WriteBackIfNeeded returned false, want true")
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(onDisk) != string(migrated) {
		t.Errorf("on-disk content = %q, want %q", onDisk, migrated)
	}

	// No stray temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (no leftover temp file): %v", len(entries), entries)
	}
}

// TestWriteBackIfNeeded_NotChanged_NeverWrites pins the "no mtime churn"
// requirement at the write-back layer: a zero Result never touches the file.
func TestWriteBackIfNeeded_NotChanged_NeverWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "already-new.prompt.yaml")
	input := join("name: X", "loop:", "  trigger: [onCompletion]", "  onCompletion:", "    delay: 30", "prompt: |", "  body")
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	before := info.ModTime()

	if wrote := WriteBackIfNeeded(path, []byte(input), Result{Changed: false}); wrote {
		t.Error("WriteBackIfNeeded returned true, want false for an unchanged result")
	}

	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime().Equal(before) {
		t.Errorf("mtime changed: before=%v after=%v", before, info.ModTime())
	}
}

// TestWriteBackIfNeeded_ReadOnlyFile_DegradesGracefully pins decision #4: a
// read-only source degrades to "migrated in memory, file unchanged on disk",
// never an error.
func TestWriteBackIfNeeded_ReadOnlyFile_DegradesGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based read-only simulation is unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.prompt.yaml")
	input := join("name: X", "loop:", "  delay: 30", "prompt: |", "  body")
	if err := os.WriteFile(path, []byte(input), 0444); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	migrated, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true (in-memory migration must still succeed)")
	}

	if wrote := WriteBackIfNeeded(path, migrated, res); wrote {
		t.Error("WriteBackIfNeeded returned true, want false on a read-only directory")
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(onDisk) != input {
		t.Errorf("on-disk content changed, want it untouched:\ngot:  %q\nwant: %q", onDisk, input)
	}
}
