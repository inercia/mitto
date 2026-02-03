---
name: "Create commits"
description: "Stage and commit changes with descriptive messages"
backgroundColor: "#B2DFDB"
---

Follow this workflow to create Git commits for the changes that we have in this repository:

### 1. Analyze Changes

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

### 2. Propose Commits

Present a table, formatted as Markdown, with each proposed commit:

SEQUENCE-NUMBER | COMMIT MESSAGE | FILES | REASON

Use conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
Files can be expressed with Unix shell-style wildcards (e.g. `*.md`, `docs/*`).

Order commits logically (e.g., implementation before documentation).

### 3. Wait for Approval

Ask the user to:

- **Approve all** - proceed with commits
- **Modify** - specify changes (reorder, merge, split, edit messages)
- **Cancel** - abort without committing

**Do not commit until the user explicitly approves.**

### 4. Execute Commits

For each approved commit:

1. `git add <files>`
2. `git commit -m "<message>"`

Report success or errors after execution.

## Rules

- Respect `.gitignore`
- Skip empty commits
- Handle binary/large files appropriately

