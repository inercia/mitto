//go:build integration

package acp

// Real-ACP integration test validating per-session working-directory (cwd)
// isolation against a git worktree.
//
// This test is SKIPPED by default. It is excluded from `make test-go` (no
// build tag) and from `make test-integration` (which only runs
// ./tests/integration/...). It additionally skips at runtime unless a real
// ACP command is provided. To run it:
//
//	MITTO_TEST_REAL_ACP_COMMAND='auggie --acp --workspace-root "$MITTO_WORKING_DIR"' \
//	  go test -tags=integration -v ./internal/acp/ -run TestWorktreeCwdIsolation_RealACP
//
// The ACP process is launched pinned to the repo root (process cwd =
// repoRoot), while the ACP session is opened with cwd = worktree. The test
// proves the agent honors the per-session cwd: a file it creates in "the
// current directory" lands in the worktree, not in the repo root.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runGit runs a git command in dir and fails the test on error.
//
// It runs hermetically: global/system git config is ignored and terminal
// prompts are disabled, so the temp repo can never block on a global
// pre-commit hook, a GPG signing pinentry, or a credential prompt waiting on
// a non-TTY stdin.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	// -c flags neutralize common global config that can hang or alter behavior.
	full := append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func TestWorktreeCwdIsolation_RealACP(t *testing.T) {
	command := os.Getenv("MITTO_TEST_REAL_ACP_COMMAND")
	if command == "" {
		t.Skip("set MITTO_TEST_REAL_ACP_COMMAND to a real ACP command to run this test")
	}

	// Set up a temp git repository (this is the process/workspace root).
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Mitto Test")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("main repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "initial commit")

	// Create a linked worktree on a divergent branch, OUTSIDE the repo root,
	// so that any leakage into repoRoot is unambiguous.
	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	runGit(t, repoRoot, "worktree", "add", "-b", "feature/wt", worktreeDir)

	// Confirm the worktree is on its own branch.
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = worktreeDir
	if out, err := branchCmd.Output(); err != nil {
		t.Fatalf("worktree branch check failed: %v", err)
	} else if got := strings.TrimSpace(string(out)); got != "feature/wt" {
		t.Fatalf("worktree on wrong branch: got %q want feature/wt", got)
	}

	// Collect agent output (the callback may fire from SDK goroutines).
	var mu sync.Mutex
	var buf strings.Builder
	output := func(msg string) {
		mu.Lock()
		buf.WriteString(msg)
		mu.Unlock()
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Launch the ACP process pinned to the repo root.
	conn, err := NewConnection(ctx, command, repoRoot, nil, true, output, logger, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Open a session whose cwd DIVERGES from the process cwd: the worktree.
	if err := conn.NewSession(ctx, worktreeDir); err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	const marker = "MITTO_WORKTREE_MARKER.txt"
	const token = "worktree-cwd-isolation-ok"
	prompt := fmt.Sprintf(
		"Do exactly two things and report the results:\n"+
			"1. Run the shell command `pwd` and tell me the absolute path it prints.\n"+
			"2. Create a new file named %s (use a relative path, i.e. in your current "+
			"working directory) whose entire contents are the single line: %s\n"+
			"Use your shell/file tools directly; do not ask for confirmation.",
		marker, token)

	if err := conn.Prompt(ctx, prompt); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Core isolation assertions: the marker must land in the worktree and must
	// NOT leak into the repo root, even though the process cwd is the repo root.
	markerInWorktree := filepath.Join(worktreeDir, marker)
	markerInRepo := filepath.Join(repoRoot, marker)

	if _, err := os.Stat(markerInWorktree); err != nil {
		t.Fatalf("marker file not found in worktree (%s): %v\n--- agent output ---\n%s",
			markerInWorktree, err, buf.String())
	}
	if _, err := os.Stat(markerInRepo); err == nil {
		t.Fatalf("marker file leaked into repo root (%s); per-session cwd was NOT honored", markerInRepo)
	}

	// Corroborate via the agent's reported pwd. Compare against the
	// symlink-resolved worktree path (macOS temp dirs are under /private).
	resolved, _ := filepath.EvalSymlinks(worktreeDir)
	out := buf.String()
	if !strings.Contains(out, worktreeDir) && (resolved == "" || !strings.Contains(out, resolved)) {
		t.Logf("note: agent output did not echo the worktree path verbatim "+
			"(file isolation still proven). worktree=%s resolved=%s\n--- output ---\n%s",
			worktreeDir, resolved, out)
	}
}
