package mac

// Reproduction for mitto-x3t2: macOS Keychain prompts for password after
// every rebuild because dev app bundles are always ad-hoc signed
// (`codesign --sign -`), which has no stable identity for the Keychain ACL
// to anchor on.
//
// This test pins the *build-tooling* half of the bug: the Makefile's
// `build-mac-app` target hard-codes the signing identity to the ad-hoc
// sentinel `-` with no override hook, so a developer cannot opt into a
// stable, ACL-friendly signing identity even if they already have one
// (e.g. a self-signed "Mitto Dev" certificate). It runs `make -n
// build-mac-app CODESIGN_IDENTITY=<test identity>` (dry run — nothing is
// actually built or signed) and asserts the emitted `codesign` command
// line honors the override.
//
// Today this FAILS: the codesign line always reads `--sign -` regardless
// of `CODESIGN_IDENTITY`, because the Makefile has no such variable. The
// fix phase is expected to add `CODESIGN_IDENTITY ?= -` and thread it into
// the `codesign --sign "$(CODESIGN_IDENTITY)"` invocation, which flips
// this test to green.

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRootFromThisFile returns the repository root, computed relative to
// this source file's own path so the test is independent of the working
// directory `go test` happens to be invoked from.
func repoRootFromThisFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	// this file: <repoRoot>/platform/mac/build_codesign_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

var codesignLineRE = regexp.MustCompile(`(?m)^.*codesign .*--sign\s+\S+.*$`)

func TestBuildMacApp_CodesignIdentity_IsConfigurable(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found on PATH; skipping Makefile reproduction test")
	}

	repoRoot := repoRootFromThisFile(t)
	const testIdentity = "mitto-x3t2-test-identity"

	cmd := exec.Command("make", "-n", "build-mac-app", "CODESIGN_IDENTITY="+testIdentity) // #nosec G204 -- fixed args, no user input
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n build-mac-app failed: %v\noutput:\n%s", err, out)
	}

	match := codesignLineRE.FindString(string(out))
	if match == "" {
		t.Fatalf("could not find a `codesign ... --sign <identity>` line in dry-run output:\n%s", out)
	}

	if strings.Contains(match, "--sign "+testIdentity) || strings.Contains(match, `--sign "`+testIdentity+`"`) {
		return // fixed: CODESIGN_IDENTITY is honored
	}

	t.Fatalf(
		"mitto-x3t2: build-mac-app ignores CODESIGN_IDENTITY=%s and still ad-hoc signs.\n"+
			"Got codesign line: %s\n"+
			"This is the reported bug: the sign step is hard-coded to `--sign -` "+
			"(ad-hoc), so every rebuild gets a fresh cdhash and macOS Keychain "+
			"treats each build as an untrusted caller, re-prompting for the "+
			"login password every time. Fix: introduce `CODESIGN_IDENTITY ?= -` "+
			"in the Makefile and thread it into the codesign invocation.",
		testIdentity, match,
	)
}
