---
name: "Create PR"
description: "Guide through creating a pull request"
backgroundColor: "#B2DFDB"
---

Guide me through creating a pull request for the committed changes.

### 1. Prepare the Code

**Format code:**
- Identify and run the project's code formatters (e.g., `go fmt`, `prettier`, `black`, `rustfmt`)
- Fix any formatting issues

**Run tests:**
- If tests were run recently in this session and no code changes have been made since, skip re-running
- Otherwise, identify and run the project's test suite
- If tests fail, report the failures and ask how to proceed

### 2. Ensure Feature Branch

Check the current branch: `git branch --show-current`

**If on main/master:**
1. Review the commits to understand the changes: `git log --oneline -5`
2. Create a descriptive branch name based on the changes:
   - Use existing project conventions (e.g., `feature/`, `fix/`, `chore/` prefixes)
   - Keep it short but descriptive (e.g., `feature/add-user-auth`, `fix/login-validation`)
3. Create and switch to the new branch: `git checkout -b <branch-name>`

**If already on a feature branch:**
- Continue with the current branch

### 3. Sync with Upstream

Ensure your branch is up-to-date before creating the PR:

1. Fetch the latest changes: `git fetch origin`
2. Identify the base branch: `git symbolic-ref refs/remotes/origin/HEAD | sed 's@^refs/remotes/origin/@@'`
3. Rebase onto the latest base branch: `git rebase origin/<base-branch>`
4. If there are merge conflicts:
   - Report the conflicting files
   - Help resolve each conflict
   - Continue the rebase: `git rebase --continue`
   - If conflicts are too complex, ask the user how to proceed (abort, manual resolution, etc.)

### 4. Generate PR Title and Description

**Analyze the commits** to create a good PR title and description:

```bash
git log --oneline origin/<base-branch>..HEAD
```

**PR Title:**
- Summarize the overall change in one line (50-72 chars ideal)
- Use conventional commit style if the project follows it (e.g., `feat: add user authentication`)
- Be specific: "Fix login validation error" not "Fix bug"
- If multiple commits, describe the overall goal, not individual changes

**PR Description:**
Structure the description as:

```markdown
## Summary
Brief explanation of what this PR does and why.

## Changes
- List key changes (can be derived from commit messages)
- Group related changes together
- Highlight any breaking changes or important notes

## Testing
How the changes were tested (if applicable).

## Related Issues
Fixes #123, Relates to #456 (if any issue references found in commits/branch)
```

Present the proposed title and description for approval before creating the PR.

### 5. Push and Create Pull Request

After approval, push the branch and create the PR:

```bash
git push --force-with-lease -u origin HEAD
gh pr create --title "<approved-title>" --body "<approved-description>"
```

### 6. Report PR Link

After the PR is created, prominently display:

```
✅ Pull Request Created Successfully!

🔗 PR URL: <the-pr-url>

Share this link to request reviews and approvals.
```

Also show how to view the PR in browser: `gh pr view --web`

## Rules

- Always run formatters and tests before creating the PR
- Respect `.gitignore`
- Never force push without explicit permission
- Assume commits are already created (use "Create commits" prompt if needed first)

