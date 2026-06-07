---
icon: "beads"
name: "Check if resolved"
menus: beadsIssues
requires: parameters
description: "Check if this bead is done, obsolete, or a duplicate, then close it, keep it open, or spin off follow-ups"
backgroundColor: "#C5E1A5"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Should This Issue Be Closed?

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

The **target bead** is `${ISSUE_ID}`. Your job is to investigate, against the **actual state of
the codebase**, whether this bead is still worth keeping open — i.e. whether its requirements are
**already implemented or fixed**, the work has become **obsolete**, or it **duplicates** another
bead — then recommend whether to close it, keep it open, or spin off follow-up beads for any
remaining work.

## Step 1 — Load the bead's full detail

Fetch everything the bead promises to deliver:

```bash
bd show ${ISSUE_ID} --long --json     # full description, acceptance criteria, design, metadata
bd dep tree ${ISSUE_ID}               # blockers and what it blocks
```

Identify the concrete acceptance criteria (or infer them from the description if none are listed).

## Step 2 — Research the bead's status

Do **not** judge from the bead text alone. Gather hard evidence from the repository:

- **Code changes**: search the codebase for the files, symbols, APIs, UI, or config the bead
  describes. Does the implementation/fix now exist? For a bug, is the defective path gone or
  guarded?
- **Obsolescence**: has the surrounding design changed so the work no longer applies (feature
  removed, approach abandoned, requirement superseded)?
- **Duplication**: is this bead substantially covered by another open bead?
- **Commits & branches** referencing the bead:

  ```bash
  git log --oneline --all | grep -i "${ISSUE_ID}"   # commits citing this bead
  git branch -a | grep -i "${ISSUE_ID}"             # branches for this bead
  git log --oneline --all -200                       # recent work that may have resolved it
  ```

- **Tests**: locate tests covering the bead's behaviour. If they exist and are cheap to run, run
  the relevant ones and record the result. Note any missing coverage.

Cross-reference each acceptance criterion against this evidence.

## Step 3 — Summarise your findings

Produce a concise **Resolution Report**:

### Bead: `${ISSUE_ID}` — `<Title>`

- **Verdict**: Still relevant / Fully resolved / Partially resolved / Obsolete / Duplicate
- **Acceptance criteria status** — per criterion: ✅ Done / ⚠️ Partial / ❌ Not done / ❓ Unknown,
  each with its evidence (commit, branch, file/symbol, or test result).
- **What was implemented**: concrete, evidence-backed list of completed work.
- **What remains**: anything unaddressed, plus any **partially completed work or edge cases** the
  implementation does not yet cover. For a duplicate, name the bead it overlaps.
- **Relevant code locations & test results**: the key files/symbols and the outcome of any tests
  you ran (or "no tests found").

Be conservative: when evidence is **ambiguous or unknown**, treat the bead as **still relevant**
and keep it open. Do not close an `in_progress` bead with active work unless it is a clear
duplicate.

## Step 4 — Decide with the user

If the verdict is **Still relevant**, say so — this is read-only; do not modify the bead — then go
straight to the final **Clean up this conversation** step (skip the modification steps below).
Otherwise the investigation stays **read-only until you confirm**: closing is **never**
done without explicit approval. Present your verdict and the summary, then confirm the next action
via `mitto_ui_options_mitto(self_id: "@mitto:session_id", allow_free_text: true)`, e.g. "This bead
looks `<verdict>`. What should I do?" with options:

- **"Close it"** — done / obsolete / duplicate; no remaining work to track.
- **"Keep it open"** — work still remains on this bead.
- **"Create follow-up tickets"** — the core is done but additional/edge-case work was discovered.

Honour the user's choice exactly. Do not modify anything they did not approve.

### If "Create follow-up tickets"

Before confirming, propose **specific next steps** as concrete follow-up beads — for each, give a
clear **title**, a one-line scope, and a suggested priority. List them in the options prompt (or via
free text) so the user can approve, edit, or drop individual items.

## Step 5 — Apply the approved action

**Close it** (only if approved). For a **duplicate**, link the two beads as related **before**
closing the duplicate:

```bash
bd dep relate ${ISSUE_ID} <keep-id>
```

Then close with a clear, specific reason:

```bash
bd close ${ISSUE_ID} --reason "<why, e.g. 'Implemented in abc1234; tests pass' / 'Feature removed, obsolete' / 'Duplicate of bd-5'>"
```

**Keep it open**: append an audit note recording what the investigation found and why the bead
stays open, so the finding is not lost:

```bash
bd update ${ISSUE_ID} --append-notes "<what changed and why — e.g. 'Investigated: core is done but <X> remains; keeping the bead open to track it.'>"
```

**Create follow-up tickets** (only the ones approved). For each:

```bash
bd create "<follow-up title>" -d "<scope and definition of done>" -p <0-4>
bd dep relate ${ISSUE_ID} <new-id>      # link the follow-up to the original bead
```

If the core work is done and only the follow-ups remain, ask whether to also close `${ISSUE_ID}`
(now that the leftover work is tracked separately) and act on the answer.

Report any command that failed and why.

## Step 6 — Final summary

Finish with a short summary stating the final verdict, what action was taken (bead **closed** with
its reason, **kept open**, and/or **follow-up beads created** with their IDs and titles), and any
remaining work worth flagging.

## Step 7 — Clean up this conversation

This conversation was spawned solely to check whether `${ISSUE_ID}` should be closed, and that
determination is now complete (any durable outcome already lives in the bead). Tidy up so the
conversation list does not accumulate finished one-off checks:

1. Notify the user of the outcome so they still get feedback after the conversation disappears:

   `mitto_ui_notify_mitto(self_id: "@mitto:session_id", title: "Resolved-check: ${ISSUE_ID}", message: "<verdict and action taken, e.g. 'Closed as duplicate of bd-5' / 'Still relevant — kept open'>", style: "success")`

2. Self-destruct this conversation:

   `mitto_conversation_delete_mitto(self_id: "@mitto:session_id", conversation_id: "self")`

Run this on **every** completion path — whether the bead was closed, kept open, given follow-ups, or
judged still relevant — because the conversation has served its single purpose. The deletion is
deferred until your turn finishes, so the notification is delivered first. If the delete tool is
unavailable, skip this step silently.