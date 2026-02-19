---
name: "Commit changes"
description: "Stage and commit changes with descriptive messages"
backgroundColor: "#B2DFDB"
---

Follow this workflow to create Git commits for the
changes that we have in this repository:

### 0. Prerequisites

Before analyzing changes, verify the codebase is in a good state:

**Formatting:**

1. Identify the project's formatters (check for Makefile targets, package.json scripts, or common tools)
2. Run the appropriate formatters for code and documentation
3. If any files are modified by the formatters, report which files were changed and include them in the commit analysis

**Tests:**

1. Check if tests were run recently in this session
2. If tests have not been run, or if code has changed since the last test run:
   - ⚠️ Warn the user: "Tests have not been run recently. Consider running tests before committing to ensure the changes work correctly."
   - Ask if they want to proceed anyway or run tests first
3. If tests were run and failed:
   - ⚠️ Warn the user: "Tests are currently failing. Committing with failing tests is not recommended."
   - Ask if they want to fix the tests first or proceed anyway

### 1. Check Current Branch

Before creating commits, verify we're on an appropriate branch:

1. Run `git branch --show-current` to identify the current branch
2. If on a protected branch (`main`, `master`, `develop`):
   - ⚠️ Warn the user: "You are currently on the `<branch>` branch. It's recommended to create commits on a feature branch."
   - **Using Mitto UI tools (if available):** Use `mitto_ui_options_buttons` with:
     ```
     Question: "You're on a protected branch. What would you like to do?"
     Options: ["Create feature branch", "Continue on current branch"]
     ```
   - **Fallback:** Ask in conversation if they want to create a feature branch or continue
   - If creating a branch, suggest a name based on the changes (e.g., `feat/add-feature-x` or `fix/issue-description`)

3. If already on a feature branch,
   - fetch latest changes in the target branch (usually "main") and rebase the
      current branch on top of them.
   - **Using Mitto UI tools (if available):** Use `mitto_ui_options_buttons` with:
     ```
     Question: "Already on feature branch. What would you like to do?"
     Options: ["Continue on current branch", "Create new feature branch"]
     ```
   - **Fallback:** Ask in conversation which option they prefer
   - If creating a new branch, suggest a name based on the changes

### 2. Analyze Changes

- Run `git status --porcelain` to list all changes
- Verify the directory is a valid Git repository
- Group changes by scope:
  - **Feature/component**: Related source files + their tests
  - **Type**: Config files, documentation, dependencies
  - **Path**: Files in the same module/directory
- Make sure there are no staged changes. If there are, check
  how they relate to the changes you are seeing, if they should go first,
  or if they should be unstaged in order to be included in the commits
  you are going to propose.
- If you are not sure about anything, just ask me.

### 3. Propose Commits

Present a table, formatted as Markdown, with each proposed commit:

SEQUENCE-NUMBER | COMMIT MESSAGE | FILES | REASON

Use conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
Files can be expressed with Unix shell-style wildcards (e.g. `*.md`, `docs/*`).

Order commits logically (e.g., implementation before documentation).

### 4. Wait for Approval

**Using Mitto UI tools (if available):**

If the `mitto_ui_options_buttons` tool is available, use it to present the approval options:

```
Question: "How would you like to proceed with the proposed commits?"
Options: ["Approve all", "Modify", "Cancel"]
```

If the user selects "Modify", follow up with a text conversation to get the specific changes (reorder, merge, split, edit messages).

**Fallback (if Mitto UI tools are not available):**

Ask the user in the conversation to choose one of these options:

- **Approve all** - proceed with commits
- **Modify** - specify changes (reorder, merge, split, edit messages)
- **Cancel** - abort without committing

**Do not commit until the user explicitly approves.**

### 5. Execute Commits

For each approved commit:

1. `git add <files>`
2. `git commit -m "<message>"`

Report success or errors after execution.

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

## Rules

- Respect `.gitignore`
- Skip empty commits
- Handle binary/large files appropriately
