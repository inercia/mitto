package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// initRepoWithIdentity creates a repo with a local user identity in its config so
// git commands run by production code (which use no hermetic env) can commit.
func initRepoWithIdentity(t *testing.T) string {
	t.Helper()
	dir := initRepo(t)
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "user.email", "test@test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// addWorktreeWithCommit creates a session worktree off the repo's current HEAD
// and adds one commit in it; it returns the worktree path and branch name.
func addWorktreeWithCommit(t *testing.T, repo, name string) (string, string) {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	branch := BranchName(name)
	if err := AddWorktree(context.Background(), repo, wt, branch, ""); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	commitFile(t, wt, name+".txt", "content "+name)
	return wt, branch
}

func TestMergeBack_Rebase_TargetNotCheckedOut(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	runGit(t, repo, "branch", "devel") // target, not checked out anywhere
	wt, branch := addWorktreeWithCommit(t, repo, "s1")

	res, err := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, Target: "devel"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != "" {
		t.Fatalf("unexpected reason=%q detail=%q", res.Reason, res.Detail)
	}
	if !IsAncestor(ctx, repo, branch, "devel") {
		t.Errorf("devel does not contain the source branch after merge-back")
	}
	if ahead, behind, _ := AheadBehind(ctx, repo, "devel", branch); ahead != 0 || behind != 0 {
		t.Errorf("devel and source diverge: ahead=%d behind=%d", ahead, behind)
	}
}

func TestMergeBack_Rebase_TargetCheckedOutClean(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	runGit(t, repo, "checkout", "-b", "devel") // checked out in the main repo, clean
	wt, branch := addWorktreeWithCommit(t, repo, "s2")

	res, err := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, Target: "devel"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != "" {
		t.Fatalf("unexpected reason=%q detail=%q", res.Reason, res.Detail)
	}
	if !IsAncestor(ctx, repo, branch, "devel") {
		t.Errorf("devel does not contain the source branch after fast-forward")
	}
}

func TestMergeBack_DirtyWorktree(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	runGit(t, repo, "branch", "devel")
	wt, branch := addWorktreeWithCommit(t, repo, "s3")
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _ := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, Target: "devel"})
	if res.Reason != MergeBackDirtyWorktree {
		t.Errorf("reason=%q, want %q", res.Reason, MergeBackDirtyWorktree)
	}
}

func TestMergeBack_Conflict(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	runGit(t, repo, "checkout", "-b", "devel")
	commitFile(t, repo, "README.md", "devel change")
	runGit(t, repo, "checkout", "main")
	wt, branch := addWorktreeWithCommit(t, repo, "s4")
	commitFile(t, wt, "README.md", "source change")

	res, _ := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, Target: "devel"})
	if res.Reason != MergeBackConflict {
		t.Errorf("reason=%q, want %q (detail=%q)", res.Reason, MergeBackConflict, res.Detail)
	}
	if IsDirty(ctx, wt) {
		t.Errorf("worktree left dirty after the rebase was aborted")
	}
}

func TestMergeBack_NewBranch(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	wt, branch := addWorktreeWithCommit(t, repo, "s5")

	res, err := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, NewBranch: "feature-x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != "" {
		t.Fatalf("unexpected reason=%q detail=%q", res.Reason, res.Detail)
	}
	if res.Target != "feature-x" {
		t.Errorf("target=%q, want feature-x", res.Target)
	}
	if !RefExists(ctx, repo, "feature-x") {
		t.Errorf("feature-x was not created")
	}
	if !IsAncestor(ctx, repo, branch, "feature-x") {
		t.Errorf("feature-x does not contain the source branch")
	}
}

func TestMergeBack_TargetCheckedOutDirty(t *testing.T) {
	requireGit(t)
	repo := initRepoWithIdentity(t)
	ctx := context.Background()
	runGit(t, repo, "checkout", "-b", "devel")
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, branch := addWorktreeWithCommit(t, repo, "s6")

	res, _ := MergeBack(ctx, MergeBackOptions{RepoDir: repo, WorktreeDir: wt, SourceBranch: branch, Target: "devel"})
	if res.Reason != MergeBackTargetCheckedOutDirty {
		t.Errorf("reason=%q, want %q", res.Reason, MergeBackTargetCheckedOutDirty)
	}
}
