---
name: "Rebase changes"
description: "Rebase changes on top of main"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
---


Rebase the current branch onto the target branch, resolving conflicts and pushing the result.


## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_ask_yes_no`
- `mitto_ui_options_buttons`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

### 1. Check Repository Status

```bash
git branch --show-current
git status --porcelain
git status
```

If uncommitted changes exist, ask user whether to stash, commit, or discard. Do not proceed until clean.

### 2. Identify Target Remote and Branch

#### 2a. List Remotes

```bash
git remote -v
```

#### 2b. Check for Existing PR

Use GitHub API: `GET /repos/{owner}/{repo}/pulls?head={username}:{branch}&state=open`

If PR exists: extract `base.ref` and `base.repo.full_name`, match to a configured remote.

#### 2c. If No PR, Infer Target

```bash
git config --get branch.$(git branch --show-current).remote
git config --get branch.$(git branch --show-current).merge
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
git symbolic-ref refs/remotes/upstream/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
```

Priority: tracking branch → `upstream` remote → `origin` remote.

#### 2d. Confirm with User

Confirm if: multiple remotes, detected remote differs from `origin`, no tracking/PR found, branch name suggests non-default target.

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Detected rebase target: upstream/main. Correct?"
**Without**: Ask in conversation.

### 3. Fetch and Preview

```bash
git fetch <target-remote>
git log --oneline <target-remote>/<target-branch>..HEAD
```

### 4. Rebase

If rebase already in progress: check for conflicts (`git diff --name-only --diff-filter=U`), resolve or continue.

Otherwise: `git rebase <target-remote>/<target-branch>`

### 5. Conflict Resolution

Per conflicting file:

1. Identify: `git diff --name-only --diff-filter=U`
2. Examine both sides
3. Analyze: incoming vs. ours, same logical area?

**Auto-resolve** when: whitespace/formatting, non-overlapping additions, clear merge of both, import ordering.

**Ask user** when: same logic modified differently, semantic meaning changes, multiple valid resolutions, complex refactoring.

**With Mitto UI**: `mitto_ui_options_buttons` → "Accept theirs / Accept ours / Combine both / Custom"
**Without**: Present options in conversation.

After each file: `git add <file>` → `git rebase --continue`. Iterate until complete.

### 6. Report

```console
✅ Rebase completed successfully!
Rebased X commits onto <target-remote>/<target-branch>
```

### 7. Push

```bash
git push --force-with-lease origin <current-branch>
```

In fork workflows: push to `origin` (your fork), not `upstream`.

## Guidelines

- Protect uncommitted changes — no work should be lost
- Detect the correct remote and confirm with user
- Ask when uncertain about any decision
- Fetch before rebasing
- Use `--force-with-lease` when force pushing
- Push to the branch's tracking remote (usually `origin`), not the rebase target remote
- Offer to abort (`git rebase --abort`) if rebase becomes too complex
- Preserve commit messages and authorship
- Ask rather than guess on conflict resolution
- Remind user that collaborators may need to reset local branches after push
