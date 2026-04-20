---
name: "Submit changes"
description: "Submit changes"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
---

Submit current work by preparing, committing (if needed), and pushing changes to a pull request.

### 1. Check for Uncommitted Changes

```bash
git status --porcelain
```

If uncommitted changes exist: inform user to commit first (use "Commit Changes" prompt), then stop.

### 2. Ensure Feature Branch

```bash
git branch --show-current
```

If on `main`/`master`/protected branch:

- Check recent branch naming: `git branch -r --sort=-committerdate | head -20`
- Suggest branch name following detected convention
- Confirm with user, then: `git checkout -b <branch-name>`

### 3. Identify Target Remote and Branch

#### 3a. List Remotes

```bash
git remote -v
git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null
```

#### 3b. Check for Existing PR

Use GitHub API: `GET /repos/{owner}/{repo}/pulls?head={username}:{branch}&state=open`

If PR exists: extract `base.ref`, `base.repo.full_name` (target repo), identify push remote (usually `origin`).

#### 3c. If No PR, Infer Target

```bash
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
git symbolic-ref refs/remotes/upstream/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
```

Priority: `upstream` → `origin` → tracking branch.

#### 3d. Confirm with User

Confirm if: multiple remotes, non-standard setup, no tracking/PR found.

**With Mitto UI**: `mitto_ui_options(self_id: "@mitto:session_id", ...)` → "PR targets upstream/main, push to origin. Correct?"
**Without**: Ask in conversation.

### 4. Check if Rebase Needed

```bash
git fetch <target-remote>
git rev-list --count HEAD..<target-remote>/<target-branch>
```

If behind: inform user to rebase first (use "Rebase changes" prompt), then stop.

### 5. Create or Update PR

**No existing PR:**

```bash
git push -u <push-remote> <branch-name>
gh pr create --fill --base <target-branch>    # GitHub
glab mr create --fill                          # GitLab
```

**PR exists:**
```bash
git push --force-with-lease <push-remote> <branch-name>
```

Provide PR/MR URL.

### 6. Update Agent Rules (Optional)

If during this session you discovered project conventions, patterns, or preferences (or the user corrected assumptions),
update Agent rules/memories. Ask user before modifying rules files.

## Summary Report

✅ Changes Submitted
📋 Status:
- Branch: <branch-name>
- Action: [Created new PR | Updated existing PR]
- Rebase: [Not needed | Completed | N/A]
🔗 PR: <pr-url>
📝 Rules Updated: [Yes - added X | No updates needed]

## Guidelines

- Detect correct remote via PR or ask user — do not assume
- Distinguish target vs push remote: push to `origin`, PR targets `upstream` in fork workflows
- Use `--force-with-lease` when force pushing
- Confirm branch names before creating
- Ask before resolving ambiguous remotes
- Stop if uncommitted changes exist — do not auto-commit
- Use rebase, not merge, for updating branches
- Document learnings in rules only with user approval
