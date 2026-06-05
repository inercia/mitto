---
name: "Beads: reevaluate all issues"
menus: prompts, beadsList
description: "Reevaluate priority, dependencies, and importance of all beads, propose changes, and surface what to do now"
backgroundColor: "#FFCC80"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Reevaluate All Issues

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

Your job is to take a fresh, critical look at **every** open bead in this repo and reassess
its **priority**, **dependencies**, and **importance** — then propose corrections where the
current state no longer reflects reality.

## Step 1 — Gather the full picture

Run the following to load all the data you need:

```bash
bd list --json                  # every bead (all statuses)
bd ready --json                 # beads with no active blockers (claimable now)
bd dep cycles                   # detect dependency cycles
```

For any bead whose scope, blockers, or rationale is unclear, fetch its full detail and
dependency tree (do this for the beads that matter — you do not need every field of every bead):

```bash
bd show <bead-id> --long --json     # full description, acceptance, design, metadata
bd dep tree <bead-id>               # blockers and what it blocks
```

Optionally cross-reference real implementation progress so importance reflects reality:

```bash
git log --oneline --all -200
git branch -a
```

## Step 2 — Reevaluate every open bead

For each non-closed bead, reason carefully across three axes:

- **Priority (P0–P4)**: Does the assigned priority still match the bead's real impact and
  urgency? Look for under-prioritised work (high user/blocking impact at low priority) and
  over-prioritised work (P0/P1 that is actually speculative or low-impact).
- **Dependencies**: Are the blocking relationships correct and complete? Look for
  **missing** dependencies (a bead that clearly cannot start before another), **stale**
  dependencies (blockers that are already closed/obsolete), and **cycles**.
- **Importance**: How much does this bead matter relative to the others *right now*?
  Consider user impact, how many other beads it unblocks, staleness, and whether it is a
  duplicate or no longer relevant.

## Step 3 — Detect anomalies

Explicitly look for and flag:

- **Priority inversions** — a high-priority bead blocked by a lower-priority one.
- **Blocked-by-closed** — beads still listing blockers that are already done.
- **Dependency cycles** — from `bd dep cycles`.
- **Stale claims** — `in_progress` beads with no evidence of work.
- **Orphans / duplicates** — beads that overlap heavily or appear abandoned.

## Step 4 — Compose a proposal

Produce a concise **Reevaluation Report** with a table of every change you recommend:

| Bead | Title | Change | From → To | Why |
|------|-------|--------|-----------|-----|
| `bd-1` | `<title>` | Priority | P3 → P1 | Blocks 3 other beads |
| `bd-2` | `<title>` | Add dep | — → blocked by `bd-9` | Needs schema first |
| `bd-3` | `<title>` | Remove dep | blocked by `bd-7` (closed) | Blocker already done |

If, after analysis, **no changes are warranted**, say so clearly and skip Steps 5–6 — but
still produce the final summary in Step 7.

## Step 5 — Confirm before changing anything

This reevaluation is **read-only until you confirm**. Present your single best proposal and
confirm via `mitto_ui_options_mitto(self_id: "@mitto:session_id", allow_free_text: true)`,
e.g. "Apply these N proposed changes to the beads tracker?" with options:

- **"Apply all proposed changes"**
- **"Apply only some"** (let the user specify which via free text)
- **"Don't change anything — report only"**

Honour the user's choice. Do not apply changes they did not approve.

## Step 6 — Apply the approved changes

For each approved change, run the appropriate command:

```bash
bd update <bead-id> --priority <0-4>          # reprioritise
bd dep add <blocked-id> <blocker-id>          # add a blocking dependency
bd dep remove <blocked-id> <blocker-id>       # remove a stale dependency
```

After wiring dependencies, verify integrity:

```bash
bd dep cycles
```

Report any command that failed and why.

## Step 7 — Final summary

Always finish with two clearly separated sections:

### Changes
A bullet summary of what was **actually changed** (or, if the user declined, what was
**proposed** but not applied). Group by type: priority changes, dependency changes, other.

### Most important things to do now
A short, ranked list (top ~3–5) of the beads that matter most to tackle next, based on your
reevaluation — favouring high-priority, ready (unblocked) work that unblocks the most other
beads. For each, give the bead ID, title, and a one-line justification.

Then remind the user to run `bd dolt push` to push the beads data to the remote when appropriate.
