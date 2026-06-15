package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// commitFile writes content to dir/file and commits it with a hermetic author.
func commitFile(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	runGit(t, dir, "add", file)
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", file)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestIsAncestor(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	ctx := context.Background()
	base := CurrentCommit(repo)
	commitFile(t, repo, "a.txt", "a")
	head := CurrentCommit(repo)

	if !IsAncestor(ctx, repo, base, head) {
		t.Errorf("base should be an ancestor of head")
	}
	if IsAncestor(ctx, repo, head, base) {
		t.Errorf("head should not be an ancestor of base")
	}
	if IsAncestor(ctx, repo, "deadbeef", head) {
		t.Errorf("missing ref should yield false")
	}
}

func TestIsDirty(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	ctx := context.Background()
	if IsDirty(ctx, repo) {
		t.Errorf("clean repo reported dirty")
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsDirty(ctx, repo) {
		t.Errorf("modified repo reported clean")
	}
}

func TestAheadBehind(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	ctx := context.Background()
	runGit(t, repo, "checkout", "-b", "feature")
	commitFile(t, repo, "f1", "f1")
	commitFile(t, repo, "f2", "f2")
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "m1", "m1")

	ahead, behind, err := AheadBehind(ctx, repo, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if ahead != 2 || behind != 1 {
		t.Errorf("AheadBehind = (%d, %d), want (2, 1)", ahead, behind)
	}
}

func TestListBranchesAndDefault(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	ctx := context.Background()
	runGit(t, repo, "branch", "devel")

	branches, err := ListBranches(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(branches, "main") || !contains(branches, "devel") {
		t.Errorf("ListBranches = %v, want to contain main and devel", branches)
	}
	if got := DefaultBranch(ctx, repo); got != "main" {
		t.Errorf("DefaultBranch = %q, want %q", got, "main")
	}
}

func TestWorktreeBranches(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "wt")
	branch := BranchName("wtb")
	if err := AddWorktree(ctx, repo, wt, branch, ""); err != nil {
		t.Fatal(err)
	}

	m, err := WorktreeBranches(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["main"]; !ok {
		t.Errorf("expected main in worktree branches: %v", m)
	}
	if _, ok := m[branch]; !ok {
		t.Errorf("expected %s in worktree branches: %v", branch, m)
	}
}
