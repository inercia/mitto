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

### 3. Identify Remote and Upstream

**Detect remotes and upstream branch:**
```bash
git remote -v
git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null
```

If multiple remotes exist or upstream is unclear:

- List available remotes and ask the user which one to use for the PR
- Common patterns: `origin` for personal fork, `upstream` for main repository

### 4. Check for Existing PR

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

1. Push the branch to remote:
   ```bash
   git push -u <remote> <branch-name>
   ```

2. Create the pull request:
   ```bash
   # GitHub
   gh pr create --fill
   
   # GitLab
   glab mr create --fill
   ```

3. Provide the PR/MR URL to the user.

### 5B. If PR Already Exists → Update PR

1. Fetch latest changes:
   ```bash
   git fetch origin main
   ```

2. Rebase on top of main:
   ```bash
   git rebase origin/main
   ```

3. If rebase conflicts occur:
   - Show the conflicting files
   - Resolve conflicts one by one
   - For each conflict, explain what changed and propose a resolution
   - After resolving, continue the rebase:
     ```bash
     git add <resolved-files>
     git rebase --continue
     ```

4. Force push to update the PR:
   ```bash
   git push --force-with-lease
   ```

5. Inform the user the PR has been updated with a link.

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

```
✅ Changes Submitted

📋 Status:
- Branch: <branch-name>
- Action: [Created new PR | Updated existing PR]
- Rebase: [Not needed | Completed successfully | N/A]

🔗 PR: <pr-url>

📝 Rules Updated: [Yes - added X | No updates needed]
```

## Rules

- **Never force push without `--force-with-lease`** to prevent overwriting others' work
- **Always confirm branch names** with the user before creating
- **Ask before resolving ambiguous remotes** - don't assume
- **Stop and inform** if uncommitted changes exist - don't auto-commit
- **Preserve user's commit history** - use rebase, not merge, for updating branches
- **Document learnings** in rules files only with user approval