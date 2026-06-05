---
name: "Beads issue: still relevant?"
menus: beadsIssues
requires: parameters
description: "Check whether this beads issue is still relevant, or already done/obsolete, and close it if so"
backgroundColor: "#FFE0B2"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Is This Issue Still Relevant?

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

The **target bead** is `${ISSUE_ID}`. Your job is to determine whether the problem or
feature it describes is **still relevant**, or whether it can already be considered
**fixed / implemented / obsolete** — and, if so, close it.

## Step 1 — Load the bead's full detail

Fetch everything about the target bead:

```bash
bd show ${ISSUE_ID} --long --json     # full description, acceptance, design, metadata
bd dep tree ${ISSUE_ID}               # blockers and what it blocks
```

Understand precisely what this bead promises to deliver: the problem to fix, the feature to
build, and any acceptance criteria.

## Step 2 — Gather evidence from the actual codebase

Do not decide from the bead text alone — verify against the real state of the project:

- **For a bug**: inspect the relevant files/components. Is the defect still present, or has
  the code already been fixed (the buggy path no longer exists, a guard was added, the test
  now passes)? Reproduce mentally or by running the relevant test if cheap to do so.
- **For a feature / task**: search the codebase for the described capability. Is it already
  implemented (the API, UI, config, or behaviour now exists)?
- **For obsolescence**: has the surrounding design changed so the work no longer applies
  (feature removed, approach abandoned, requirement superseded)?

Cross-reference development history for direct evidence:

```bash
git log --oneline --all | grep -i "${ISSUE_ID}"   # commits referencing this bead
git branch -a | grep -i "${ISSUE_ID}"             # branches for this bead
git log --oneline --all -200                       # recent work that may have resolved it
```

Use codebase search/inspection on the files and symbols named in the bead to confirm.

## Step 3 — Reach a relevance verdict

Classify the bead into exactly one category, grounded in the evidence from Step 2:

- **Still relevant** — the problem/feature is real and unaddressed. Keep it open.
- **Already done** — the bug is fixed or the feature is implemented (cite the commit, branch,
  or code that proves it). Candidate for closure.
- **Obsolete** — the work no longer applies (removed feature, abandoned approach, superseded
  by a different solution). Candidate for closure.
- **Duplicate** — substantially covered by another open bead. Candidate for closure (keep the
  better one).

Be conservative: when the evidence is **ambiguous or unknown**, treat the bead as **still
relevant** and do not close it. Do not close an `in_progress` bead with active work unless it
is a clear duplicate.

## Step 4 — Report the verdict

Produce a concise **Relevance Report** for the bead:

### Bead: `${ISSUE_ID}` — `<Title>`

- **Verdict**: Still relevant / Already done / Obsolete / Duplicate
- **Evidence**: the concrete facts behind the verdict (commits, branches, file/symbol
  references, or "no implementation found"). For a duplicate, name the bead it overlaps.
- **Acceptance criteria status** (if the bead lists any): which are met vs unmet.

If the verdict is **Still relevant**, state that clearly and **stop here** — this is
read-only; do not modify the bead.

## Step 5 — Confirm before closing

Only for **Already done / Obsolete / Duplicate**. Closing is **never** done without explicit
approval. Present your verdict and confirm via
`mitto_ui_options_mitto(self_id: "@mitto:session_id", allow_free_text: true)`, e.g.
"This bead looks `<verdict>`. Close it?" with options:

- **"Yes — close it"**
- **"No — keep it open"**
- **"Keep open but adjust"** (let the user say what to change via free text)

Honour the user's choice exactly.

## Step 6 — Close the bead (if approved)

For a **duplicate**, link the two beads as related **before** closing the duplicate:

```bash
bd dep relate ${ISSUE_ID} <keep-id>
```

Then close with a clear, specific reason:

```bash
bd close ${ISSUE_ID} --reason "<why, e.g. 'Implemented in abc1234' / 'Feature removed, obsolete' / 'Duplicate of bd-5'>"
```

Report any command that failed and why.

## Step 7 — Final summary

Finish with a short summary stating the final verdict, whether the bead was **closed** (with
its reason) or **kept open**, and any follow-ups worth noting (e.g. a sibling bead that became
unblocked). Then remind the user to run `bd dolt push` to push the beads data to the remote
when appropriate.
