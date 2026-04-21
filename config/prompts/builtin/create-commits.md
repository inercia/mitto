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
- **With Mitto UI**: Use `mitto_ui_form` to let the user choose:
  ```
  mitto_ui_form(self_id: "@mitto:session_id", title: "Create Feature Branch?", html: "
    <label for='action'>Action:</label>
    <select name='action' id='action'>
      <option value='create_branch'>Create feature branch</option>
      <option value='continue'>Continue on current branch</option>
    </select>
    <div>
      <label for='branch_name'>Branch name:</label>
      <input type='text' name='branch_name' id='branch_name' placeholder='feat/my-feature' value='<suggested-name>'>
    </div>
  ")
  ```
  If `action == "create_branch"`: `git checkout -b <branch_name>`
- **Without**: Ask in conversation

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

**With Mitto UI**: Use `mitto_ui_options` for the top-level decision:
```
mitto_ui_options(self_id: "@mitto:session_id", question: "Proposed N commits (see above). How to proceed?",
  options: [{label: "Approve all"}, {label: "Edit commit messages"}, {label: "Modify plan"}, {label: "Cancel"}])
```
- If **"Edit commit messages"**: proceed to step 4a.
- If **"Approve all"**: proceed to step 5.
- If **"Modify plan"**: discuss changes, update plan, return to step 4.
- If **"Cancel"**: stop.

**Without**: Ask in conversation. Wait for explicit approval.

### 4a. Edit Commit Messages (With Mitto UI)

For each commit, present the message in a textbox for editing:
```
mitto_ui_textbox(self_id: "@mitto:session_id",
  title: "Edit commit message (1/N)",
  text: "<type>(<scope>): <description>\n\n<body>",
  result: "edited_text")
```
Use the edited text as the final commit message. If `changed == false`, use the original.

### 5. Execute

Per commit: `git add <files>` → `git commit -m "<message>"`. Report results.

### 6. Update Agent Rules (Optional)

If you discovered project conventions or the user corrected assumptions, update Agent rules/memories (with user approval).

## Guidelines

- Respect `.gitignore`
- Skip empty commits
- Handle binary/large files appropriately
