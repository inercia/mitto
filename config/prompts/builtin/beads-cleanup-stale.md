---
name: "Beads: cleanup stale issues"
menus: prompts, beadsList
description: "Find stale, obsolete, or duplicate beads and close them after confirmation"
backgroundColor: "#BCAAA4"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Cleanup Stale Issues

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

Your job is to keep the tracker healthy by finding beads that should no longer be open —
**stale**, **obsolete**, **already-done**, or **duplicate** — and closing them, but only
after the user confirms. Closing is **never** done without explicit approval.

## Step 1 — Gather the full picture

Run the following to load all the data you need:

```bash
bd list --json                  # every bead (all statuses)
bd ready --json                 # claimable beads (no active blockers)
```

For candidate beads, fetch full detail so your reasoning is grounded:

```bash
bd show <bead-id> --long --json     # description, acceptance, design, metadata, timestamps
bd dep tree <bead-id>               # blockers and what it blocks
```

Cross-reference real implementation progress to detect work that is already finished:

```bash
git log --oneline --all -200
git branch -a
```

## Step 2 — Identify closure candidates

Reason carefully and classify each candidate into one of these categories:

- **Already done** — acceptance criteria are met (evidence in git log, branches, or notes)
  but the bead is still open. Candidate for `bd close`.
- **Stale** — no activity for a long time and no longer relevant; superseded by other work
  or by changes in direction.
- **Obsolete** — describes work that no longer applies (removed feature, abandoned approach).
- **Duplicate** — substantially overlaps another bead. Keep the better one, close the other,
  and link them as related first.

Be conservative: when in doubt, treat a bead as **keep**, not close. Do **not** propose
closing a bead that is actively `in_progress` with real evidence of ongoing work, unless it
is a clear duplicate.

## Step 3 — Compose a proposal

Produce a **Cleanup Report** with a table of every bead you recommend closing:

| Bead | Title | Category | Reason | Keep instead? |
|------|-------|----------|--------|---------------|
| `bd-1` | `<title>` | Already done | Implemented in `<sha>` | — |
| `bd-2` | `<title>` | Duplicate | Overlaps `bd-5` | Keep `bd-5` |
| `bd-3` | `<title>` | Obsolete | Feature removed | — |

Also list beads you considered but decided to **keep**, with a one-line reason, so the user
can see your judgement was deliberate.

If **nothing** should be closed, say so clearly and skip Steps 4–5 — but still give the
final summary in Step 6.

## Step 4 — Confirm before closing anything

This cleanup is **read-only until you confirm**. Present your single best proposal and
confirm via `mitto_ui_options_mitto(self_id: "@mitto:session_id", allow_free_text: true)`,
e.g. "Close these N beads?" with options:

- **"Close all proposed beads"**
- **"Close only some"** (let the user specify which via free text)
- **"Don't close anything — report only"**

Honour the user's choice. Never close a bead they did not approve.

## Step 5 — Apply the approved closures

For duplicates, link the two beads as related **before** closing the duplicate:

```bash
bd dep relate <duplicate-id> <keep-id>
```

Then close each approved bead with a clear, specific reason:

```bash
bd close <bead-id> --reason "<why it is being closed, e.g. 'Implemented in abc1234' or 'Duplicate of bd-5'>"
```

Report any command that failed and why.

## Step 6 — Final summary

Always finish with two clearly separated sections:

### Closed
A bullet list of what was **actually closed** (or, if the user declined, what was
**proposed** for closure but kept), each with its category and reason.

### Tracker health now
A brief snapshot after cleanup: how many beads remain open, how many are ready
(unblocked), and any follow-ups worth noting (e.g. beads that became unblocked by a closure).

Then remind the user to run `bd dolt push` to push the beads data to the remote when appropriate.
