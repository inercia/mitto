//go:build darwin

package main

// Unit tests for dumpPerfBufferToFile (mitto-sus.2 Test phase). Covers the
// WKWebView-leg manual-playbook dump bind: happy path, nested-directory
// creation, the required-path guard, and overwrite semantics.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDumpPerfBufferToFile_WritesContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "samples.jsonl")
	content := `{"scenario":"composer.keystroke","metric":"mark","value":1.5}` + "\n"

	if err := dumpPerfBufferToFile(target, content); err != nil {
		t.Fatalf("dumpPerfBufferToFile returned error: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("file content = %q, want %q", string(got), content)
	}
}

func TestDumpPerfBufferToFile_CreatesMissingParentDirs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "deeper", "samples.jsonl")

	if err := dumpPerfBufferToFile(target, "line\n"); err != nil {
		t.Fatalf("dumpPerfBufferToFile returned error: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected file to exist after nested-dir creation: %v", err)
	}
}

func TestDumpPerfBufferToFile_EmptyPathIsRejected(t *testing.T) {
	err := dumpPerfBufferToFile("", "content")
	if err == nil {
		t.Fatal("expected an error for an empty path, got nil")
	}
}

func TestDumpPerfBufferToFile_EmptyContentWritesEmptyFile(t *testing.T) {
	// perfMarks.js's dumpPerfBufferToFile() calls the bind with "" when the
	// in-page buffer is empty (no samples recorded yet) — must not error.
	dir := t.TempDir()
	target := filepath.Join(dir, "empty.jsonl")

	if err := dumpPerfBufferToFile(target, ""); err != nil {
		t.Fatalf("dumpPerfBufferToFile returned error for empty content: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty file content, got %q", string(got))
	}
}

func TestDumpPerfBufferToFile_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "samples.jsonl")

	if err := dumpPerfBufferToFile(target, "first\n"); err != nil {
		t.Fatalf("first write returned error: %v", err)
	}
	if err := dumpPerfBufferToFile(target, "second\n"); err != nil {
		t.Fatalf("second write returned error: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != "second\n" {
		t.Fatalf("file content = %q, want overwrite to %q", string(got), "second\n")
	}
}
