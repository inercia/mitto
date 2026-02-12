---
name: "Submit changes"
description: "Submit changes"
backgroundColor: "#B2DFDB"
---

# Submit Changes

Submit the current work by preparing, committing (if needed),
and pushing changes to a pull request.

## Workflow

### 1. Check for Uncommitted Changes

**Detect uncommitted work:**
```bash
git status --porcelain
```

If there are uncommitted changes:

- Inform the user: "You have uncommitted changes. Please commit them first using the 'Commit Changes' prompt, then run this prompt again."
- Stop execution and wait for the user to commit.

### 2. Ensure Feature Branch

**Check current branch:**
```bash
git branch --show-current
```

If on `main`, `master`, or another protected branch:

- Analyze recent branch naming conventions:
  ```bash
  git branch -r --sort=-committerdate | head -20
  ```
- Identify the pattern (e.g., `username/feature-name`, `feature/description`, `TICKET-123-description`)
- Suggest a branch name following the detected convention
- Ask the user to confirm or provide a different name
- Create and switch to the new branch:
  ```bash
  git checkout -b <branch-name>
  ```

### 3. Identify Target Remote and Branch

**IMPORTANT:** Never assume which remote to use. Users working with forks typically have:
- `origin` pointing to their fork (where they push branches)
- `upstream` pointing to the main repository (where PRs target)

#### Step 3a: List Available Remotes

```bash
# List all configured remotes
git remote -v

# Check upstream tracking for current branch
git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null
```

#### Step 3b: Check for Existing Pull Request

Use the GitHub API (via `github-api` tool) to detect if there's an open PR for the current branch:

```
GET /repos/{owner}/{repo}/pulls?head={username}:{current-branch}&state=open
```

**If a PR exists:**
- Extract the `base.ref` (target branch name, e.g., `main`)
- Extract the `base.repo.full_name` to identify the **target repository** (where the PR will merge)
- Match the target repository to a configured remote by comparing URLs
- Also identify the **push remote** (usually `origin` - your fork where the PR source branch lives)

**Example:** If PR targets `upstream-org/repo:main` and your remotes are:
- `origin` → `your-fork/repo` (push here)
- `upstream` → `upstream-org/repo` (PR targets here)

Then:
- **Target for rebase check:** `upstream/main`
- **Push remote:** `origin`

#### Step 3c: If No PR Exists, Infer Target

When no PR exists, gather information to determine the likely target:

```bash
# Check default branch for each remote
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
git symbolic-ref refs/remotes/upstream/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'

# If symbolic-ref fails, check remote's default branch
git remote show origin 2>/dev/null | grep 'HEAD branch' | awk '{print $NF}'
git remote show upstream 2>/dev/null | grep 'HEAD branch' | awk '{print $NF}'
```

**Priority order for determining PR target:**
1. **`upstream` remote**: If present, likely the main repo in a fork workflow
2. **`origin` remote**: Common default when not using forks
3. **Tracking branch configuration**: What the current branch is set to track

#### Step 3d: Confirm with User

**Always ask the user to confirm** if any of these conditions apply:
- Multiple remotes are configured (e.g., both `origin` and `upstream`)
- The detected target differs from a simple `origin/main` setup
- No tracking information exists and no PR was found

Present the decision to the user:

```
🔍 Remote Detection Results:

Available remotes:
  - origin → git@github.com:your-username/repo.git (your fork)
  - upstream → git@github.com:upstream-org/repo.git (main repo)

Detected configuration:
  - PR will target: upstream/main
  - Push to: origin/<current-branch>
  - Reason: [PR #123 already targets upstream-org/repo:main | upstream remote present | single remote configured]

Is this correct? (yes/no/specify different target)
```

**Do not proceed** until the user confirms the target remote and branch.

### 4. Check if Rebase is Needed

**Fetch the latest changes from the target remote:**

```bash
git fetch <target-remote>
```

**Check if the current branch is behind the target branch:**

```bash
# Count commits we're behind
git rev-list --count HEAD..<target-remote>/<target-branch>
```

**If the branch is behind (count > 0):**

- Inform the user: "Your branch is behind `<target-remote>/<target-branch>` by X commit(s). Please rebase your changes first using the 'Rebase changes' prompt, then run this prompt again."
- Stop execution and wait for the user to rebase.

**If the branch is up to date (count = 0):**

- Continue with the submission process.

### 5. Check for Existing PR

**GitHub:**
```bash
gh pr status
gh pr view --json state,url 2>/dev/null
```

**GitLab:**
```bash
glab mr view 2>/dev/null
```

### 5A. If NO Existing PR → Create New PR

1. Push the branch to the **push remote** (confirmed in Step 3):
   ```bash
   # Push to your fork/origin, NOT the target remote (upstream)
   git push -u <push-remote> <branch-name>
   ```

2. Create the pull request targeting the **target remote/branch** (confirmed in Step 3):
   ```bash
   # GitHub - specify base repo and branch if using fork workflow
   gh pr create --fill --base <target-branch>

   # GitLab
   glab mr create --fill
   ```

3. Provide the PR/MR URL to the user.

### 5B. If PR Already Exists → Update PR

Since we already verified the branch is up to date with the target branch in step 4:

1. Force push to update the PR (push to your fork, not the target remote):
   ```bash
   # Push to the same remote where the PR source branch lives
   git push --force-with-lease <push-remote> <branch-name>
   ```

2. Inform the user the PR has been updated with a link.

### 6. Update Agent Rules (Optional)

After completing the workflow, if during this session:

- You asked the user for clarification about project conventions
- You discovered project-specific patterns or preferences
- The user corrected your assumptions

Then update the appropriate Agent rules or memories in order
to memorize these learnings for future sessions:

- Add branch naming conventions discovered
- Add remote/upstream preferences
- Add any project-specific PR workflows or requirements

Ask the user before making changes to rules files.

## Summary Report

At the end, show a summary like this:

✅ Changes Submitted
📋 Status:
- Branch: <branch-name>
- Action: [Created new PR | Updated existing PR]
- Rebase: [Not needed | Completed successfully | N/A]
🔗 PR: <pr-url>
📝 Rules Updated: [Yes - added X | No updates needed]

## Rules

- **Never assume which remote to use** - always detect via PR or ask the user when multiple remotes exist
- **Distinguish between target and push remotes** - in fork workflows, you push to `origin` but PR targets `upstream`
- **Never force push without `--force-with-lease`** to prevent overwriting others' work
- **Always confirm branch names** with the user before creating
- **Ask before resolving ambiguous remotes** - don't assume
- **Stop and inform** if uncommitted changes exist - don't auto-commit
- **Preserve user's commit history** - use rebase, not merge, for updating branches
- **Document learnings** in rules files only with user approval