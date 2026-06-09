---
icon: "beads"
name: "Status ALL in-progress"
menus: prompts, beadsList
description: "Fact-check implementation status for all in-progress beads in this repo"
backgroundColor: "#FFCCBC"
group: "Tasks"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

# Beads: Status Check — All In-Progress Beads

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

## Step 1 — Fetch in-progress beads

Run:

```bash
bd list --status in_progress --json
```

If **no beads** are in progress: inform the user and stop.

## Step 2 — Fetch full details for all in-progress beads

For **each** in-progress bead, fetch its full details:

```bash
bd show <bead-id> --long --json     # description, acceptance, design, assignee, metadata
bd dep tree <bead-id>               # blockers and dependents
```

Also run locally (once, shared across all beads) to cross-reference implementation evidence:

```bash
git log --oneline --all -200
git branch -a
```

## Step 3 — Fact-check each bead

For **each** in-progress bead, produce a **Status Report** section:

1. **Parse acceptance criteria** from the bead description (or infer them if not explicit)
2. **Cross-reference** each criterion against: linked commits (message, files changed), git log entries mentioning the bead ID, branch names, and the bead's notes/comments
3. **Assess overall completion** based on evidence

Use this structure for each bead:

---

### `<bead-id>` — `<Title>`

**Goal**: one sentence restating what this bead is supposed to deliver.

#### Acceptance Criteria — Status

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| 1 | `<criterion text>` | ✅ Done / ⚠️ Partial / ❌ Not done / ❓ Unknown | `<commit, branch, or code reference>` |

#### Related Development Work

| Type | Reference | Status |
|------|-----------|--------|
| Branch | `<branch name>` | Active / Stale |
| Commit | `<sha> — message` | — |

#### Overall Assessment

- **Completion estimate**: `<Not started / Early / Midway / Nearly done / Done>`
- **What appears to be done**: bullet list
- **What appears to be missing**: bullet list
- **Blockers or risks**: anything preventing completion (e.g., an open blocking bead in `bd dep tree`)

---

## Step 4 — Aggregate summary

After all individual bead sections, produce a **Tracker Overview** table:

| Bead | Title | Completion | Blockers? |
|------|-------|------------|-----------|
| `bd-1` | `<title>` | Midway | Yes — `<short description>` |
| `bd-2` | `<title>` | Nearly done | No |

Then surface beads that may need attention:
- Beads marked `in_progress` with **no evidence of any work** (possibly stale claims)
- Beads that appear **done** but are still open (candidates for `bd close`)

> ⚠️ **This report is read-only.** No code changes and no beads updates will be performed. Use the "Start working on ready" prompt to continue implementation on a specific bead.
