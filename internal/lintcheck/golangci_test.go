// Package lintcheck holds a regression test for mitto-r58s: pre-existing
// golangci-lint (staticcheck) issues that made `make lint-go` exit non-zero
// — SA1019 uses of the deprecated session.LoopPrompt.Trigger field, plus one
// S1008 in auth.go. The bead was originally filed reporting 7 issues, but
// that count reflected golangci-lint's default `max-same-issues: 3` cap
// (per unique linter+message text) silently truncating the real total; a
// `--max-same-issues=0` run during the fix phase found 25 genuine SA1019
// hits across internal/mcpserver and internal/web/handlers test files. See
// the bead's Investigation/Fix comments for the full file/line inventory.
//
// There is no runtime behavior difference to unit test here — the only way
// to observe this class of bug is to run the static analyzer itself, so
// that is what this test does, scoped to just the affected packages for
// speed.
package lintcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// moduleRoot walks up from this test file's own location to find the
// directory containing go.mod, independent of the test binary's working
// directory (which `go test` sets to the package dir, but we don't want to
// rely on that assumption staying true).
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod walking up from %s", file)
		}
		dir = parent
	}
}

// TestGolangciLint_NoIssuesInAffectedPackages reproduces mitto-r58s:
// golangci-lint flags staticcheck issues across internal/mcpserver,
// internal/web/handlers, and internal/web/middleware. It skips when
// golangci-lint isn't installed (e.g. a bare `go test` in a minimal
// environment) — CI's separate "Lint" job already runs the equivalent
// `make lint-go` check via golangci-lint-action, so this test is a
// convenience/regression guard for environments that do have the binary,
// not the sole enforcement point.
func TestGolangciLint_NoIssuesInAffectedPackages(t *testing.T) {
	bin, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Skip("golangci-lint not installed; skipping (CI's Lint job covers this via make lint-go)")
	}

	root := moduleRoot(t)
	cmd := exec.Command(bin, "run", "--timeout=5m",
		"./internal/mcpserver/...",
		"./internal/web/handlers/...",
		"./internal/web/middleware/...",
	)
	cmd.Dir = root

	// The exit code is authoritative for pass/fail (same as `make lint-go`);
	// golangci-lint prints a trailing "0 issues." summary line to stdout even
	// on a clean run, so success is NOT the same as empty output.
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("golangci-lint reported issues (mitto-r58s not fixed):\n%s", out)
	}
}
