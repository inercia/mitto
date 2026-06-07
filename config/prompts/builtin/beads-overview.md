---
icon: "beads"
name: "Overview"
menus: prompts, beadsList
description: "Read-only health snapshot of the whole tracker: ready, blocked, in-progress, stale, and dependency cycles"
backgroundColor: "#CFD8DC"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Project Overview

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

Your job is to give a clear, **read-only** snapshot of the entire tracker — the "where do we
stand?" / standup view. **Do not modify, close, reprioritise, or create any bead.** This prompt
only reads and reports; if changes are warranted, point the user to the prompt that performs them.

## Step 1 — Board snapshot

```bash
bd status            # counts by state, ready work, lead time, recent 24h activity
```

This is the headline. Capture the totals (open / in_progress / blocked / closed) and the recent
activity figures.

## Step 2 — What's ready now

```bash
bd ready --json      # open beads with no active blockers — actionable right now
```

Note how many are ready and identify the highest-priority handful (by priority, then age).

## Step 3 — What's blocked, and dependency health

```bash
bd blocked --json    # beads waiting on a blocker
bd dep cycles        # dependency cycles (should be none)
```

For the notable blocked beads, identify **which bead is blocking** them. Flag any **cycle** as a
problem that needs fixing (suggest the per-bead "Update dependencies" prompt to repair it).

## Step 4 — In-progress health & stale work

```bash
bd list --status in_progress --json   # what is actively being worked
bd stale --json                        # not updated recently (possibly abandoned/forgotten)
```

Cross-reference: in-progress beads that also appear as stale are the most at-risk (claimed but
untouched). Call these out explicitly.

## Step 5 — Present the overview

Summarise concisely for the user. Keep it scannable:

### Board

- **Totals**: open / in-progress / blocked / closed (from `bd status`).
- **Recent activity**: what changed in the last 24h.

### Ready to start (top picks)

- A short ranked list of the highest-value unblocked beads (ID · title · priority), so the user can
  immediately pick something to work on.

### Blocked

- Beads that are blocked and the blocker for each. Note any chains where one bead is gating several
  others.

### In progress

- Active beads and their owners. **Highlight** any that are also stale (at risk of being abandoned).

### Stale / needs attention

- Forgotten or untouched beads worth triaging.

### Health flags

- Dependency cycles (if any), beads blocked by already-closed beads, or anything structurally off.

## Step 6 — Suggest next actions (do not perform them)

Based on what the snapshot reveals, point the user to the right follow-up prompt — without taking
the action yourself:

- Lots of ready work → **"Beads: start work"** (or per-bead **"Start work"**).
- Stale / obsolete / duplicate beads → **"Beads: cleanup stale issues"**.
- Priorities or dependencies look off → **"Beads: reevaluate all issues"**.
- A specific bead is vague or under-specified → per-bead **"Investigate more"**.
- Dependency cycle or wrong blockers → per-bead **"Update dependencies"**.

End with a one-line bottom line: is the board healthy, and what is the single most useful next step?
