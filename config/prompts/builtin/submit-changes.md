---
icon: "globe"
name: "Submit changes"
menus: prompts
description: "Submit changes"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
enabledWhen: 'fileExists(".git/config") || fileExists(".git")'
---

Submit current work by preparing, committing (if needed), and pushing changes to a pull request.

> **Worktree-isolated conversation:** if `@mitto:worktree_base_branch` is non-empty, this
> conversation already runs on its own branch `@mitto:worktree_branch` (worktree at
> `@mitto:worktree_path`), created from `@mitto:worktree_base_branch`. In that case **skip step 2**
> (you are already on a feature branch) and use `@mitto:worktree_base_branch` as the PR base in
> steps 3 and 5 instead of inferring it.

### 1. Check for Uncommitted Changes

```bash
git status --porcelain
```

If uncommitted changes exist: inform user to commit first (use "Commit Changes" prompt), then stop.

### 2. Ensure Feature Branch

> Skip this step entirely when `@mitto:worktree_base_branch` is set — the conversation's worktree
> is already on its own branch.

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

If `@mitto:worktree_base_branch` is set, use it as the target branch — no inference needed.

Otherwise:

```bash
git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
git symbolic-ref refs/remotes/upstream/HEAD 2>/dev/null | sed 's@^refs/remotes/@@'
```

Priority: `@mitto:worktree_base_branch` → `upstream` → `origin` → tracking branch.

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

**PR exists:**
```bash
git push --force-with-lease <push-remote> <branch-name>
```
Provide PR/MR URL.

**No existing PR — With Mitto UI:**

First, generate a PR title and description from the commits. Then present a form for the user to review and customize. When `@mitto:worktree_base_branch` is set, pre-select it as the base branch:

```
mitto_ui_form(self_id: "@mitto:session_id", title: "Create Pull Request", html: "
  <label for='pr_title'>Title:</label>
  <input type='text' name='pr_title' id='pr_title' value='<generated-title>' placeholder='PR title'>
  <label for='base_branch'>Base branch:</label>
  <select name='base_branch' id='base_branch'>
    <option value='main' selected>main</option>
    <option value='develop'>develop</option>
  </select>
  <div>
    <label><input type='checkbox' name='draft' value='true'> Create as draft</label>
  </div>
  <label for='reviewers'>Reviewers (comma-separated):</label>
  <input type='text' name='reviewers' id='reviewers' placeholder='username1, username2'>
")
```

Then present the PR description in a textbox for editing:

```
mitto_ui_textbox(self_id: "@mitto:session_id",
  title: "Edit PR Description",
  text: "<generated-description-markdown>",
  result: "edited_text")
```

Use the form values and edited description to create the PR:
```bash
git push -u <push-remote> <branch-name>
gh pr create --title "<pr_title>" --body "<description>" --base <base_branch> [--draft] [--reviewer <reviewers>]
```

**No existing PR — Without Mitto UI:**
```bash
git push -u <push-remote> <branch-name>
gh pr create --fill --base <target-branch>    # GitHub
glab mr create --fill                          # GitLab
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
