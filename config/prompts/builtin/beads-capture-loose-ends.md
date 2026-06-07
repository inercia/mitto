---
icon: "beads"
name: "Capture loose ends"
menus: prompts
description: "Scan the conversation for unsolved problems and untracked work, then file beads for each"
backgroundColor: "#DCEDC8"
group: "Beads"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Capture Loose Ends

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

While working on a focused task, it's common to discover **other** problems or to leave
parts of a request unfinished. Your job here is to comb back through **this conversation**,
identify everything that was detected but **not** resolved, and file a bead for each so
nothing is lost.

## Step 1 — Mine the conversation for loose ends

Review the full conversation history and extract every item that deserves its own bead.
Use the context you already have — do not ask the user to re-explain what happened. Look for:

- **Unsolved problems**: bugs, errors, failing tests, or warnings that were observed but
  left unfixed because we focused on something else.
- **Incomplete requests**: things the user asked for that were **not** done. For example, if
  the user wanted A, B, and C and only A was delivered, B and C each become a bead.
- **Side discoveries**: problems noticed in passing while fixing the main issue (e.g. a
  related bug, a fragile code path, a missing edge-case handler).
- **Deferred / parked work**: anything explicitly postponed ("let's do this later", "out of
  scope for now", TODOs, tech debt, missing tests, missing docs).

For each candidate, capture: a short title, what it is, why it matters, where it lives
(files/components), and any context from the conversation (root cause, repro, proposed fix).

If you find **nothing** worth tracking, say so clearly and stop — do not invent work.

## Step 2 — Learn the project's conventions

So your beads match the project's style:

```bash
bd list --limit 20 --json   # recent beads: common labels, types, priorities
bd types                    # valid issue types in this project
```

## Step 3 — Avoid duplicates

Check whether any candidate is already tracked before creating it:

```bash
bd list --json             # all beads (scan titles/descriptions for overlap)
```

Drop or merge any candidate that clearly duplicates an existing open bead. Note which
existing bead it matched so you can report it.

## Step 4 — Assemble the candidate beads

Build the list of candidates internally. For each, assign sensible defaults: `bug` for defects,
`task`/`feature`/`chore` otherwise; priority by real impact (P0 highest .. P4 lowest), defaulting
to P2/P3 for follow-ups. Number them so each maps to a checkbox in the form below.

## Step 5 — Let the user accept or ignore each bead via a checkbox form

This is **read-only until you confirm**. Present **every** candidate in a single
`mitto_ui_form_mitto(self_id: "@mitto:session_id")` as a checkbox, **checked by default**, so the
user can simply uncheck the ones to ignore. Put the key facts (type, priority, title, and a short
"why") in each checkbox's label:

```html
<p>Select which loose ends to file as beads. Unchecked items are skipped.</p>

<label><input type="checkbox" name="bead_1" checked /> [bug · P2] Crash on empty input — found while fixing X</label>
<label><input type="checkbox" name="bead_2" checked /> [task · P3] Part B of the original request — not yet done</label>
<label><input type="checkbox" name="bead_3" checked /> [chore · P3] Add missing tests for the parser</label>
```

Use a stable `name` like `bead_<N>` for each candidate (matching the numbering from Step 4). The
form's Submit/Cancel buttons are added automatically.

Interpreting the result:
- A candidate is **approved** only if its `bead_<N>` key is **present** in the returned values —
  checked boxes are submitted, unchecked boxes are omitted entirely.
- If the user **cancels** the form, or submits with **everything unchecked**, create nothing and
  report that no beads were filed.

Create only the approved candidates. Do not create beads the user unchecked.

## Step 6 — Create the approved beads

For each approved item, compose a well-structured Markdown description (Summary, Findings /
Context, Proposed Solution if known, Acceptance Criteria). Write it to a temporary file and
pass it via `--body-file` to preserve formatting and avoid shell-quoting issues:

```bash
bd create "<title>" \
  --type <type> \
  --priority <priority> \
  --labels "<comma,separated,labels>" \
  --body-file /tmp/bead-loose-end.md
```

Capture each new bead ID from the output. Report any creation that fails and why.

## Step 7 — Wire relationships (optional)

If any new bead clearly blocks or relates to another (new or existing) bead, link them:

```bash
bd dep add <blocked-id> <blocker-id>          # blocker blocks blocked
bd link <id> <other-id> --type related        # non-blocking relationship
```

Then verify integrity:

```bash
bd dep cycles
```

## Step 8 — Final summary

Finish with a short summary listing:

- Each **created** bead (ID + title), grouped by type.
- Any candidates **skipped** as duplicates (and which existing bead they matched).
- Any failures or warnings from `bd`.
