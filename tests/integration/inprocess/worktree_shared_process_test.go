//go:build integration

package inprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
)

// TestWorktreeSharedProcess verifies that two sessions whose working directories
// differ (the registered workspace root and a subdirectory inside it) share a
// SINGLE shared ACP process, while each session keeps its own per-session cwd.
//
// This is the git-agnostic plumbing for the worktree epic: the shared process is
// keyed by the owning workspace UUID (repo root), but the per-session cwd flows
// independently to session/new.
func TestWorktreeSharedProcess(t *testing.T) {
	ts := SetupTestServer(t)

	// The default workspace dir is <tmp>/workspace (see SetupTestServer).
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	subDir := filepath.Join(workspaceDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Session A: working dir == registered workspace root (exact match).
	sessA, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "A",
		WorkingDir: workspaceDir,
	})
	if err != nil {
		t.Fatalf("CreateSession A failed: %v", err)
	}

	// Session B: working dir == subdirectory of the workspace (owned, not exact).
	sessB, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "B",
		WorkingDir: subDir,
	})
	if err != nil {
		t.Fatalf("CreateSession B failed: %v", err)
	}

	// Both sessions must share ONE shared ACP process. The shared process is
	// created eagerly at session-create time (getSharedProcess), so no prompt is
	// needed for this assertion.
	if count := ts.Server.GetSessionManager().ACPProcessCount(); count != 1 {
		t.Fatalf("expected 1 shared ACP process, got %d", count)
	}

	// Each session must keep its own per-session cwd: A at the workspace root,
	// B at the subdirectory. This proves the per-session cwd is decoupled from
	// the workspace identity even though they share a process.
	bsA := ts.Server.GetSessionManager().GetSession(sessA.SessionID)
	bsB := ts.Server.GetSessionManager().GetSession(sessB.SessionID)
	if bsA == nil || bsB == nil {
		t.Fatalf("expected both background sessions to exist (A=%v, B=%v)", bsA != nil, bsB != nil)
	}

	if got := bsA.GetWorkingDir(); got != workspaceDir {
		t.Errorf("session A working dir = %q, want %q", got, workspaceDir)
	}
	if got := bsB.GetWorkingDir(); got != subDir {
		t.Errorf("session B working dir = %q, want %q", got, subDir)
	}
	if bsA.GetWorkingDir() == bsB.GetWorkingDir() {
		t.Errorf("expected distinct per-session working dirs, both = %q", bsA.GetWorkingDir())
	}
}

// initGitRepo initializes a hermetic git repository at dir with one empty
// commit so that `git worktree add -b <branch>` has a valid HEAD to branch from.
// Global/system git config is suppressed to keep the test deterministic.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Mitto Test")
	runGit("config", "commit.gpgsign", "false")
	runGit("commit", "--allow-empty", "-m", "init")
}

// TestWorktreeLifecycle_CreateAndDelete verifies the default-on worktree
// lifecycle: with no explicit per-folder or global override, creating a session
// in a git workspace materializes a dedicated worktree (the session cwd diverges
// to it and metadata records the path + branch) because worktrees are ON by
// default when available, and deleting the session removes the worktree from
// disk. The per-folder/global precedence rules are covered by the
// ResolveWorktreesEnabled unit truth table.
func TestWorktreeLifecycle_CreateAndDelete(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	// No WorktreesEnabled override on the workspace and no global setting: the
	// default (ON when the working dir is a git repo) must take effect.
	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces = []config.WorkspaceSettings{{
			ACPServer:  "mock-acp",
			WorkingDir: repoDir,
		}}
		c.DefaultWorkingDir = repoDir
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "wt",
		WorkingDir: repoDir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Worktrees are ON by default in a git repo (no per-folder/global override):
	// a dedicated in-project worktree is materialized and recorded in metadata.
	cwd := assertWorktreeCreated(t, ts, sess, repoDir)

	// Deleting the session must remove the worktree from disk.
	if err := ts.Client.DeleteSession(sess.SessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	if _, statErr := os.Stat(cwd); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree %q to be removed after delete (stat err=%v)", cwd, statErr)
	}
}

// wtBoolPtr returns a pointer to b. Used to populate the tri-state
// WorktreesEnabled override (*bool) in test configs.
func wtBoolPtr(b bool) *bool { return &b }

// assertWorktreeCreated asserts that the session was placed in a dedicated
// in-project git worktree: the cwd diverged from repoDir into the
// <repoDir>/.mitto/worktrees directory, exists on disk, and the path + branch
// are recorded in metadata. Returns the resolved worktree path.
func assertWorktreeCreated(t *testing.T, ts *TestServer, sess *client.SessionInfo, repoDir string) string {
	t.Helper()
	bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if bs == nil {
		t.Fatalf("expected background session %q to exist", sess.SessionID)
	}
	cwd := bs.GetWorkingDir()
	if cwd == repoDir {
		t.Fatalf("expected worktree cwd to diverge from repo root %q", repoDir)
	}
	worktreesDir := appdir.WorkspaceWorktreesDir(repoDir)
	if !strings.HasPrefix(cwd, worktreesDir) {
		t.Errorf("worktree cwd %q is not under %q", cwd, worktreesDir)
	}
	if fi, statErr := os.Stat(cwd); statErr != nil || !fi.IsDir() {
		t.Errorf("expected worktree dir at %q to exist (err=%v)", cwd, statErr)
	}
	meta, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.WorktreePath != cwd {
		t.Errorf("metadata WorktreePath = %q, want %q", meta.WorktreePath, cwd)
	}
	if meta.WorktreeBranch == "" {
		t.Errorf("expected metadata WorktreeBranch to be set")
	}
	return cwd
}

// assertNoWorktree asserts that NO worktree was created for the session: the cwd
// stays at the plain working dir and metadata records no worktree path/branch.
func assertNoWorktree(t *testing.T, ts *TestServer, sess *client.SessionInfo, workingDir string) {
	t.Helper()
	bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if bs == nil {
		t.Fatalf("expected background session %q to exist", sess.SessionID)
	}
	if got := bs.GetWorkingDir(); got != workingDir {
		t.Errorf("expected session cwd to stay at %q (no worktree), got %q", workingDir, got)
	}
	meta, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.WorktreePath != "" {
		t.Errorf("expected no WorktreePath in metadata, got %q", meta.WorktreePath)
	}
	if meta.WorktreeBranch != "" {
		t.Errorf("expected no WorktreeBranch in metadata, got %q", meta.WorktreeBranch)
	}
}

// TestWorktree_PerFolderDisable_OverridesDefaultOn verifies that a per-folder
// WorktreesEnabled=false wins over the default-ON policy: even in a git repo the
// session stays in the plain repo root and no worktree is created.
func TestWorktree_PerFolderDisable_OverridesDefaultOn(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces = []config.WorkspaceSettings{{
			ACPServer:        "mock-acp",
			WorkingDir:       repoDir,
			WorktreesEnabled: wtBoolPtr(false),
		}}
		c.DefaultWorkingDir = repoDir
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "folder-off",
		WorkingDir: repoDir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertNoWorktree(t, ts, sess, repoDir)
}

// TestWorktree_GlobalDisable_NoWorktree verifies that the global
// conversations.worktrees_enabled=false setting (with no per-folder override)
// disables worktrees even in a git repo.
func TestWorktree_GlobalDisable_NoWorktree(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces = []config.WorkspaceSettings{{
			ACPServer:  "mock-acp",
			WorkingDir: repoDir,
		}}
		c.DefaultWorkingDir = repoDir
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			WorktreesEnabled: wtBoolPtr(false),
		}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "global-off",
		WorkingDir: repoDir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertNoWorktree(t, ts, sess, repoDir)
}

// TestWorktree_PerFolderEnable_OverridesGlobalDisable verifies the override is
// bidirectional: with the global setting OFF but a per-folder WorktreesEnabled=
// true, the folder wins and a worktree is created.
func TestWorktree_PerFolderEnable_OverridesGlobalDisable(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces = []config.WorkspaceSettings{{
			ACPServer:        "mock-acp",
			WorkingDir:       repoDir,
			WorktreesEnabled: wtBoolPtr(true),
		}}
		c.DefaultWorkingDir = repoDir
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			WorktreesEnabled: wtBoolPtr(false),
		}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "folder-on",
		WorkingDir: repoDir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertWorktreeCreated(t, ts, sess, repoDir)
}

// TestWorktree_NonGitFolder_NoWorktree verifies the git-availability gate: a
// non-git working directory never gets a worktree, even with an explicit global
// enable and no opt-out. Worktrees are only ever materialized inside a git repo.
func TestWorktree_NonGitFolder_NoWorktree(t *testing.T) {
	dir := t.TempDir() // intentionally NOT a git repo

	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces = []config.WorkspaceSettings{{
			ACPServer:  "mock-acp",
			WorkingDir: dir,
		}}
		c.DefaultWorkingDir = dir
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			WorktreesEnabled: wtBoolPtr(true),
		}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "nongit",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertNoWorktree(t, ts, sess, dir)
}
