---
name: "Commit changes"
description: "Stage and commit changes with descriptive messages"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
---

<task>
Create Git commits for changes in this repository with proper organization and messages.
</task>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_options_buttons`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

<instructions>

### 0. Prerequisites

**Formatting:** Run project formatters. Report any files modified and include in commit analysis.

**Tests:**
- If not run recently: warn user, ask to proceed or run tests first
- If failing: warn user, ask to fix or proceed

### 1. Check Branch

```bash
git branch --show-current
```

If on protected branch (`main`, `master`, `develop`):
- Warn user
- **With Mitto UI**: `mitto_ui_options_buttons` → "Create feature branch / Continue on current"
- **Without**: Ask in conversation
- If creating branch, suggest name based on changes

If on feature branch: fetch and rebase on target branch (usually "main").

### 2. Analyze Changes

- `git status --porcelain`
- Group by scope: feature/component, type (config/docs/deps), path (module/directory)
- Check for staged changes: should they go first or be unstaged?
- Ask if uncertain

### 3. Propose Commits

<output_format>

SEQUENCE | COMMIT MESSAGE | FILES | REASON

Use conventional prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
Files can use wildcards (`*.md`, `docs/*`). Order logically.

</output_format>

### 4. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Modify / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 5. Execute

Per commit: `git add <files>` → `git commit -m "<message>"`. Report results.

### 6. Update Agent Rules (Optional)

If you discovered project conventions or the user corrected assumptions, update Agent rules/memories (with user approval).

</instructions>

<rules>
- Respect `.gitignore`
- Skip empty commits
- Handle binary/large files appropriately
</rules>
