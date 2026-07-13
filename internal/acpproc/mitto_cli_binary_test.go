package acpproc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// pickMittoCLIBinary is the pure decision core of resolveMittoCLIBinary,
// factored so we can drive it with a fake "current executable" path from
// tests. It must stay logically in sync with resolveMittoCLIBinary.
func pickMittoCLIBinary(exe string) string {
	if filepath.Base(exe) == "mitto-app" {
		sibling := filepath.Join(filepath.Dir(exe), "mitto")
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling
		}
	}
	return exe
}

func TestPickMittoCLIBinary_MittoAppRewritesToSibling(t *testing.T) {
	dir := t.TempDir()
	mittoApp := filepath.Join(dir, "mitto-app")
	mittoCLI := filepath.Join(dir, "mitto")
	if err := os.WriteFile(mittoApp, []byte("app"), 0755); err != nil {
		t.Fatalf("write mitto-app: %v", err)
	}
	if err := os.WriteFile(mittoCLI, []byte("cli"), 0755); err != nil {
		t.Fatalf("write mitto: %v", err)
	}

	got := pickMittoCLIBinary(mittoApp)
	if got != mittoCLI {
		t.Fatalf("expected rewrite to sibling %q, got %q", mittoCLI, got)
	}
}

func TestPickMittoCLIBinary_MittoAppNoSiblingKeepsExe(t *testing.T) {
	dir := t.TempDir()
	mittoApp := filepath.Join(dir, "mitto-app")
	if err := os.WriteFile(mittoApp, []byte("app"), 0755); err != nil {
		t.Fatalf("write mitto-app: %v", err)
	}

	got := pickMittoCLIBinary(mittoApp)
	if got != mittoApp {
		t.Fatalf("expected fallback to %q, got %q", mittoApp, got)
	}
}

func TestPickMittoCLIBinary_MittoCLIUnchanged(t *testing.T) {
	dir := t.TempDir()
	mittoCLI := filepath.Join(dir, "mitto")
	if err := os.WriteFile(mittoCLI, []byte("cli"), 0755); err != nil {
		t.Fatalf("write mitto: %v", err)
	}

	got := pickMittoCLIBinary(mittoCLI)
	if got != mittoCLI {
		t.Fatalf("expected %q unchanged, got %q", mittoCLI, got)
	}
}

func TestPickMittoCLIBinary_SiblingIsDirectoryIgnored(t *testing.T) {
	dir := t.TempDir()
	mittoApp := filepath.Join(dir, "mitto-app")
	if err := os.WriteFile(mittoApp, []byte("app"), 0755); err != nil {
		t.Fatalf("write mitto-app: %v", err)
	}
	// A "mitto" directory next to mitto-app must not be picked up as the CLI.
	if err := os.Mkdir(filepath.Join(dir, "mitto"), 0755); err != nil {
		t.Fatalf("mkdir mitto: %v", err)
	}

	got := pickMittoCLIBinary(mittoApp)
	if got != mittoApp {
		t.Fatalf("expected fallback to %q when sibling is a directory, got %q", mittoApp, got)
	}
}

func TestPickMittoCLIBinary_ArbitraryTestBinaryUnchanged(t *testing.T) {
	// Simulates the go test runner: the currently running binary is neither
	// mitto nor mitto-app; it must be returned as-is.
	fake := filepath.Join(t.TempDir(), "acpproc.test")
	if err := os.WriteFile(fake, []byte("test"), 0755); err != nil {
		t.Fatalf("write test binary: %v", err)
	}

	got := pickMittoCLIBinary(fake)
	if got != fake {
		t.Fatalf("expected %q unchanged, got %q", fake, got)
	}
}

// TestResolveMittoCLIBinary_RealCall exercises the real helper (which calls
// os.Executable) to guarantee it does not error under test and returns a
// non-empty path. The concrete value is the go test binary, which is neither
// `mitto` nor `mitto-app`, so it must pass through unchanged.
func TestResolveMittoCLIBinary_RealCall(t *testing.T) {
	if runtime.GOOS == "" {
		t.Skip("unsupported test environment")
	}
	got, err := resolveMittoCLIBinary()
	if err != nil {
		t.Fatalf("resolveMittoCLIBinary: %v", err)
	}
	if got == "" {
		t.Fatalf("expected non-empty path")
	}
	exe, _ := os.Executable()
	if got != exe {
		t.Fatalf("expected pass-through of %q, got %q", exe, got)
	}
}
