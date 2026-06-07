---
icon: "beads"
name: "Show status"
menus: beadsIssues
requires: parameters
description: "Fact-check this bead's implementation status against the codebase"
backgroundColor: "#F0F4C3"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

# Beads: Status Check — One Bead

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

The **target bead** is `${ISSUE_ID}`.

## Step 1 — Fetch full bead details

Load everything about the target bead:

```bash
bd show ${ISSUE_ID} --long --json     # description, acceptance, design, assignee, metadata
bd dep tree ${ISSUE_ID}               # blockers and dependents
```

Also run locally to gather implementation evidence:

```bash
# Find commits that reference this bead ID
git log --oneline --all | grep -i "${ISSUE_ID}"

# Check branches containing the bead ID
git branch -a | grep -i "${ISSUE_ID}"
```

## Step 2 — Fact-check implementation status

Analyse all gathered evidence and produce a **Status Report** for the bead:

### Bead: `${ISSUE_ID}` — `<Title>`

**Goal** (one sentence restating what this bead is supposed to deliver)

#### Acceptance Criteria — Status

For each acceptance criterion listed in the bead (or inferred from the description if not explicitly listed):

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| 1 | `<criterion text>` | ✅ Done / ⚠️ Partial / ❌ Not done / ❓ Unknown | `<commit, branch, or code reference>` |

#### Related Development Work

| Type | Reference | Status |
|------|-----------|--------|
| Branch | `<branch name>` | Active / Stale |
| Commit | `<sha> — message` | — |

#### Overall Assessment

- **Completion estimate**: `<percentage or qualitative: Not started / Early / Midway / Nearly done / Done>`
- **What appears to be done**: bullet list of concrete evidence of completed work
- **What appears to be missing**: bullet list of acceptance criteria with no evidence of completion
- **Blockers or risks**: anything preventing completion (e.g., an open blocking bead in `bd dep tree`, a failing test, an unanswered question in notes)

> ⚠️ **This report is read-only.** No code changes and no beads updates will be performed. Use the "Start work" prompt to continue implementation.
