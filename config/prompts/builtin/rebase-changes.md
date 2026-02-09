---
name: "Rebase changes"
description: "Rebase changes on top of main"
backgroundColor: "#B2DFDB"
---

Rebase the current branch onto the target branch,
resolving conflicts and pushing the result.

### 1. Check Repository Status

Inspect the current state of the git repository:

```bash
# Check current branch
git branch --show-current

# Check for uncommitted changes
git status --porcelain

# Check if a rebase is already in progress
git status
```

**If there are uncommitted changes:**

- Ask the user whether to stash, commit, or discard them before proceeding
- Do not proceed with rebase until working directory is clean

### 2. Identify Target Branch

First, check if there's an open Pull/Merge Request for the current branch to determine the real merge target:

```bash
# GitHub: Check for open PR
gh pr view --json baseRefName,number,title 2>/dev/null

# GitLab: Check for open MR
glab mr view --json targetBranch 2>/dev/null
```

**If a Pull/Merge Request exists:**
- Use the target branch from the response as the rebase target
- This ensures we rebase onto the actual branch where the PR/MR will be merged

**If no Pull/Merge Request exists or CLI is not available:**
- Fall back to detecting the default branch:

```bash
# Identify the default branch (usually main or master)
git symbolic-ref refs/remotes/origin/HEAD | sed 's@^refs/remotes/origin/@@'
```

### 3. Fetch Upstream Changes

Fetch the latest changes from the remote repository:

```bash
# Fetch all remotes
git fetch --all
```

Show the user what commits will be rebased:

```bash
git log --oneline origin/<target-branch>..HEAD
```

### 4. Handle Rebase State

**If a rebase is already in progress:**

1. Check for unresolved conflicts: `git diff --name-only --diff-filter=U`
2. If conflicts exist, go to step 5 (Conflict Resolution)
3. If no conflicts, continue the rebase: `git rebase --continue`

**If no rebase is in progress:**

Start the rebase onto the target branch:

```bash
git rebase origin/<target-branch>
```

### 5. Conflict Resolution

When conflicts occur during rebase, iterate through each one:

**For each conflicting file:**

1. **Identify the conflict**: `git diff --name-only --diff-filter=U`
2. **Examine the conflict**: View the file to understand both sides
3. **Analyze the changes**:
   - What is the incoming change (from target branch)?
   - What is our change (from current branch)?
   - Are these changes in the same logical area?

**Automatic resolution** - Resolve automatically when:
- The conflict is purely whitespace or formatting
- One side adds code and the other modifies unrelated code
- The resolution is a clear merge of both changes (e.g., both adding different items to a list)
- Import statement ordering conflicts

**Ask the user** when:
- Both sides modify the same logic differently
- The semantic meaning of code changes with different resolutions
- There are multiple reasonable ways to resolve the conflict
- The conflict involves complex refactoring

Present conflicts to the user as:

```
📍 Conflict in: <filename>

**Incoming (from <target-branch>):**
<their changes>

**Ours (from <current-branch>):**
<our changes>

Options:
1. Accept incoming (theirs)
2. Accept ours
3. Combine both changes
4. Custom resolution (I'll describe what I want)
```

**After resolving each file:**

```bash
git add <resolved-file>
```

**Continue the rebase:**

```bash
git rebase --continue
```

**Iterate** through this process until all conflicts are resolved and the rebase completes.

### 6. Report

Report the result:

```console
✅ Rebase completed successfully!

Rebased X commits onto origin/<target-branch>
Branch has been pushed to remote.

To verify the result:
- View commit history: git log --oneline -10
- Compare with remote: git diff origin/<current-branch>
```

### 7. Submit changes

Suggest the user to submit changes, for example by
pushing to the current, remote branch with `git push`.

## Rules

- Always fetch before rebasing to have the latest upstream changes
- Never force push without `--force-with-lease` to prevent data loss
- If the rebase becomes too complex (many conflicts, repeated issues), offer to abort: `git rebase --abort`
- Preserve commit messages and authorship during rebase
- If unsure about a conflict resolution, always ask the user
- After pushing, remind the user that collaborators may need to reset their local branches
