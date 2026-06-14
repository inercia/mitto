---
icon: "sync"
name: "Merge to base branch"
menus: prompts
description: "Merge this conversation's worktree branch into its base branch"
group: "Git"
backgroundColor: "#B2DFDB"
enabledWhen: 'session.hasWorktree'
---

Merge this conversation's worktree branch into its base branch, locally. This is a
**local merge only** — do NOT push and do NOT open a PR (use "Submit changes" for that),
and do NOT remove the worktree (Mitto manages worktree lifecycle).

This conversation runs in its own git worktree:

- Worktree branch: `@mitto:worktree_branch`
- Worktree path: `@mitto:worktree_path`
- Recorded base branch: `@mitto:worktree_base_branch`

### 1. Resolve the Target Base Branch

If `@mitto:worktree_base_branch` is non-empty, that is the target base branch.

Otherwise, fall back to the repository's default branch:

```bash
git -C "@mitto:worktree_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@'
```

If that yields nothing, use `main` if it exists, else `master`.

### 2. Confirm the Target with the User

Confirm the resolved target branch in **one** concise prompt, allowing the user to type a
different target:

```
mitto_ui_options(self_id: "@mitto:session_id", allow_free_text: true,
  prompt: "Merge `@mitto:worktree_branch` into `<resolved-target>`?",
  options: ["Yes, merge into <resolved-target>"])
```

If the prompt times out, **abort** without merging. If the user supplies free text, use it as
the target base branch.

### 3. Ensure a Clean Worktree

```bash
git -C "@mitto:worktree_path" status --porcelain
```

If there are uncommitted changes, commit them first (or ask the user how to proceed) before
merging. Do not merge with a dirty worktree.

### 4. Locate the Main Checkout

The merge must run from the **main checkout**, NOT inside the worktree: the worktree has
`@mitto:worktree_branch` checked out and the base branch is checked out in the main working
tree, so git forbids checking out the base inside the worktree.

Derive the main checkout from the worktree — it is the FIRST entry of `worktree list`:

```bash
git -C "@mitto:worktree_path" worktree list
```

Use the path of the first entry as `<repo-root>`. Alternatively,
`git -C "@mitto:worktree_path" rev-parse --git-common-dir` returns `<repo>/.git`, whose parent
is the main checkout.

### 5. Merge

From the main checkout, make sure the target base branch is checked out there:

```bash
git -C <repo-root> checkout <target-base-branch>
```

Then merge the feature branch, preferring a traceable merge commit:

```bash
git -C <repo-root> merge --no-ff @mitto:worktree_branch
```

Fall back to a fast-forward merge if `--no-ff` is not appropriate.

Resolve trivial conflicts. If conflicts are **non-trivial**, STOP and report rather than
guessing.

### 6. Do NOT Push or Open a PR

This prompt performs a local merge only. Do not push, do not open a PR, and do not remove the
worktree.

## Summary Report

✅ Merged locally
📋 Status:

- Source branch: `@mitto:worktree_branch`
- Target branch: `<target-base-branch>`
- Merge commit: `<sha or "fast-forward / none">`
- Conflicts: `<none | resolved: list>`
