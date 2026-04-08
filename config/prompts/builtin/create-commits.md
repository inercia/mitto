---
name: "Commit changes"
description: "Stage and commit changes with descriptive messages"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
---

Create Git commits for changes in this repository with proper organization and messages.

### 0. Prerequisites

**Formatting:** Run project formatters. Report any files modified and include in commit analysis.

**Tests:**
- If not run recently: warn user, ask to proceed or run tests first
- If failing: warn user, ask to fix or proceed

### 1. Ensure Feature Branch

```bash
git branch --show-current
```

If on `main`/`master`/protected branch:
- Check recent branch naming: `git branch -r --sort=-committerdate | head -20`
- Suggest branch name following detected convention (based on the changes to commit)
- **With Mitto UI**: `mitto_ui_options_buttons` → "Create feature branch / Continue on current branch"
- **Without**: Ask in conversation
- If confirmed: `git checkout -b <branch-name>`

If on feature branch: fetch and rebase on target branch (usually "main").

### 2. Analyze Changes

- `git status --porcelain`
- Group by scope: feature/component, type (config/docs/deps), path (module/directory)
- Check for staged changes: should they go first or be unstaged?
- Ask if uncertain

### 3. Propose Commits

SEQUENCE | COMMIT MESSAGE | FILES | REASON

Use conventional prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
Files can use wildcards (`*.md`, `docs/*`). Order logically.

### 4. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Modify / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 5. Execute

Per commit: `git add <files>` → `git commit -m "<message>"`. Report results.

### 6. Update Agent Rules (Optional)

If you discovered project conventions or the user corrected assumptions, update Agent rules/memories (with user approval).

## Guidelines

- Respect `.gitignore`
- Skip empty commits
- Handle binary/large files appropriately
