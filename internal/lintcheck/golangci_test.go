// Package lintcheck holds a regression test for pre-existing golangci-lint
// (staticcheck) issues that made `make lint-go` exit non-zero (mitto-r58s,
// mitto-14d). mitto-r58s was originally filed reporting 7 issues, but that
// count reflected golangci-lint's default `max-same-issues: 3` cap (per
// unique linter+message text) silently truncating the real total; a
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

// TestGolangciLint_NoIssuesInAffectedPackages reproduces mitto-r58s and
// mitto-14d: pre-existing staticcheck issues on code untouched by the
// features that most recently touched these packages made the full
// `golangci-lint run` gate exit non-zero.
//
//   - mitto-r58s: SA1019 uses of the deprecated session.LoopPrompt.Trigger
//     field, plus one S1008, across internal/mcpserver and
//     internal/web/handlers test files.
//   - mitto-14d: ST1012 (error var bareInvalidArgument400Err should have
//     name of the form errFoo) in
//     internal/conversation/loop_runner_test.go, and S1008 (should use
//     'return len(fs.TaskLabelColors) == 0' instead of
//     'if ... { return false }; return true') in
//     internal/workspaces/folders.go.
//
// It skips when golangci-lint isn't installed (e.g. a bare `go test` in a
// minimal environment) — CI's separate "Lint" job already runs the
// equivalent `make lint-go` check via golangci-lint-action, so this test is
// a convenience/regression guard for environments that do have the binary,
// not the sole enforcement point.
//
// Deliberately a SINGLE golangci-lint invocation covering every guarded
// package, rather than one invocation per bead: running the golangci-lint
// v2.11.3 binary twice in the same test-binary process (even sequentially,
// in separate Test functions) has been observed to crash with a nil-deref
// panic inside its internal analysis-cache goroutines
// (goanalysis.(*loadingPackage).analyzeRecursive) — a bug in the tool
// itself, reproducible with `-count=2` on this test alone, independent of
// which packages are scanned. Consolidating avoids that flake entirely.
//
// GOTOOLCHAIN=go1.26.1 is forced because the installed golangci-lint
// binary was built with go1.26.1; running it under a system Go of a
// different minor version (e.g. 1.27.1) fails to load export data for the
// standard library and never reaches the staticcheck analysis at all
// (typecheck errors like "export data version 4 is greater than maximum
// supported version 2"), which would make this test report a false
// negative for the actual staticcheck issues being guarded here.
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
		"./internal/conversation/...",
		"./internal/workspaces/...",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.1")

	// The exit code is authoritative for pass/fail (same as `make lint-go`);
	// golangci-lint prints a trailing "0 issues." summary line to stdout even
	// on a clean run, so success is NOT the same as empty output.
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("golangci-lint reported issues (mitto-r58s / mitto-14d not fixed):\n%s", out)
	}
}
