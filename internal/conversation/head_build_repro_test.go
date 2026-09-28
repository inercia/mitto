//go:build integration

package conversation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHEADBuildsCleanly_mitto2g34 reproduces mitto-2g34: committed HEAD fails
// to build because ApplyModelTag (bgsession_config.go) calls
// SelectHighestPriorityModel, but that function's definition currently exists
// only as an uncommitted working-tree change in constraints.go (added
// alongside mitto-9eci's call site in commit 04e5d9f7, never itself
// committed).
//
// This test never touches the live, shared working tree (which already has
// the uncommitted fix and would build/pass trivially). Instead it builds a
// disposable, detached git worktree of HEAD and asserts that package
// compiles. It fails now with "undefined: SelectHighestPriorityModel" and
// will pass once the Fix phase commits the missing definition.
func TestHEADBuildsCleanly_mitto2g34(t *testing.T) {
	repoRootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	root := strings.TrimSpace(string(repoRootOut))

	tmpDir, err := os.MkdirTemp("", "mitto-2g34-repro-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wt := filepath.Join(tmpDir, "worktree")
	addCmd := exec.Command("git", "worktree", "add", "-q", "--detach", wt, "HEAD")
	addCmd.Dir = root
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add failed: %v\n%s", err, out)
	}
	defer func() {
		rmCmd := exec.Command("git", "worktree", "remove", "--force", wt)
		rmCmd.Dir = root
		_ = rmCmd.Run()
	}()

	buildCmd := exec.Command("go", "build", "./internal/conversation/")
	buildCmd.Dir = wt
	out, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("committed HEAD does not build (mitto-2g34):\n%s", out)
	}
}
