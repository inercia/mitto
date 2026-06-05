---
name: "Beads: capture loose ends"
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

## Step 4 — Propose the beads to file

Present a single, concise proposal listing every bead you intend to create as a table:

| # | Title | Type | Priority | Why it matters |
|---|-------|------|----------|----------------|
| 1 | `<title>` | bug | P2 | Discovered while fixing X; crashes on empty input |
| 2 | `<title>` | task | P3 | Part B of the original request, not yet done |

Assign sensible defaults: `bug` for defects, `task`/`feature`/`chore` otherwise; priority by
real impact (P0 highest .. P4 lowest), defaulting to P2/P3 for follow-ups.

## Step 5 — Confirm before creating anything

This is **read-only until you confirm**. Present your best proposal and confirm via
`mitto_ui_options_mitto(self_id: "@mitto:session_id", allow_free_text: true)`, e.g.
"File these N beads for the loose ends from this conversation?" with options:

- **"Create all proposed beads"**
- **"Create only some"** (let the user specify which via free text)
- **"Don't create anything — report only"**

Honour the user's choice exactly. Do not create beads they did not approve.

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

Then remind the user to run `bd dolt push` to push the beads data to the remote when
appropriate.
