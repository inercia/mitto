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

### 2. Identify Target Remote and Branch

**IMPORTANT:** Never assume which remote to use. Users working with forks typically have:
- `origin` pointing to their fork
- `upstream` pointing to the main repository

#### Step 2a: List Available Remotes

```bash
# List all configured remotes
git remote -v
```

#### Step 2b: Check for Existing Pull Request

Use the GitHub API (via `github-api` tool) to detect if there's an open PR for the current branch:

```
GET /repos/{owner}/{repo}/pulls?head={username}:{current-branch}&state=open
```

**If a PR exists:**
- Extract the `base.ref` (target branch name, e.g., `main`)
- Extract the `base.repo.full_name` to identify the target repository
- Match the target repository to a configured remote:
  - Compare `base.repo.clone_url` or `base.repo.ssh_url` with `git remote -v` output
  - This determines whether to use `origin`, `upstream`, or another remote

**Example:** If PR targets `upstream-org/repo:main` and your remotes are:
- `origin` → `your-fork/repo`
- `upstream` → `upstream-org/repo`

Then use `upstream/main` as the rebase target.

#### Step 2c: If No PR Exists, Infer Target

When no PR exists, gather information to determine the likely target:

```bash
# Check upstream tracking configuration for current branch
git config --get branch.$(git branch --show-current).remote
git config --get branch.$(git branch --show-current).merge

# Check default branch for each remote
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
git symbolic-ref refs/remotes/upstream/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'

# If symbolic-ref fails, check remote's default branch
git remote show origin 2>/dev/null | grep 'HEAD branch' | awk '{print $NF}'
git remote show upstream 2>/dev/null | grep 'HEAD branch' | awk '{print $NF}'
```

**Priority order for inference:**
1. **Tracking branch**: If the current branch tracks a remote branch, use that remote
2. **`upstream` remote**: If present, likely the main repo in a fork workflow
3. **`origin` remote**: Common default, but may be user's fork

#### Step 2d: Confirm with User

**Always ask the user to confirm** if any of these conditions apply:
- Multiple remotes are configured (e.g., both `origin` and `upstream`)
- The detected remote differs from `origin`
- No tracking information exists and no PR was found
- The current branch name suggests it might target a non-default branch

Present the decision to the user:

```
🔍 Remote Detection Results:

Available remotes:
  - origin → git@github.com:your-username/repo.git
  - upstream → git@github.com:upstream-org/repo.git

Detected target: upstream/main
Reason: [PR #123 targets upstream-org/repo:main | upstream remote typically represents the main repository | tracking branch configured]
```

**Using Mitto UI tools (if available):** Use `mitto_ui_ask_yes_no` to confirm:
```
Question: "Detected rebase target: upstream/main. Is this correct?"
Yes label: "Yes, proceed"
No label: "No, let me specify"
```

If the user answers "No", follow up in conversation to get the correct target.

**Fallback (if Mitto UI tools are not available):**

Ask: "Is this correct? (yes/no/specify different target)"

**Do not proceed** until the user confirms the target remote and branch.

### 3. Fetch Upstream Changes

Fetch the latest changes from the remote repository:

```bash
# Fetch the confirmed target remote
git fetch <target-remote>

# Optionally fetch all remotes for completeness
git fetch --all
```

Show the user what commits will be rebased:

```bash
git log --oneline <target-remote>/<target-branch>..HEAD
```

### 4. Handle Rebase State

**If a rebase is already in progress:**

1. Check for unresolved conflicts: `git diff --name-only --diff-filter=U`
2. If conflicts exist, go to step 5 (Conflict Resolution)
3. If no conflicts, continue the rebase: `git rebase --continue`

**If no rebase is in progress:**

Start the rebase onto the confirmed target (from Step 2):

```bash
git rebase <target-remote>/<target-branch>
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

Present conflicts to the user:

First, show the conflict details:

```
📍 Conflict in: <filename>

**Incoming (from <target-branch>):**
<their changes>

**Ours (from <current-branch>):**
<our changes>
```

**Using Mitto UI tools (if available):** Use `mitto_ui_options_buttons` to present resolution options:

```
Question: "How would you like to resolve this conflict in <filename>?"
Options: ["Accept theirs", "Accept ours", "Combine both", "Custom"]
```

If the user selects "Custom", follow up in conversation to get the desired resolution.

**Fallback (if Mitto UI tools are not available):**

Present options in conversation:
1. Accept incoming (theirs)
2. Accept ours
3. Combine both changes
4. Custom resolution (I'll describe what I want)

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

Rebased X commits onto <target-remote>/<target-branch>

To verify the result:
- View commit history: git log --oneline -10
- Compare with target: git log --oneline <target-remote>/<target-branch>..HEAD
```

### 7. Submit changes

Suggest the user to submit changes by pushing to their remote branch.

**Important:** After a rebase, you'll typically need to force push:

```bash
# Push to origin (your fork) - NOT the upstream remote
git push --force-with-lease origin <current-branch>
```

**Note:** If working with a fork workflow:
- Push to `origin` (your fork), not `upstream` (the main repo)
- The PR will automatically update with the rebased commits

## Rules

- Think carefully before stashing, committing or discarding uncommitted changes: no changes should be lost !!!
- **Never assume which remote to use** - always detect and confirm with the user
- Ask the user if not sure what to do
- Always fetch before rebasing to have the latest upstream changes
- Never force push without `--force-with-lease` to prevent data loss
- **When pushing after rebase:** Push to the branch's tracking remote (usually `origin`), not necessarily the rebase target remote
- If the rebase becomes too complex (many conflicts, repeated issues), offer to abort: `git rebase --abort`
- Preserve commit messages and authorship during rebase
- If unsure about a conflict resolution, always ask the user
- After pushing, remind the user that collaborators may need to reset their local branches
