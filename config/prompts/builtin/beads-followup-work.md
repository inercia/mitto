---
icon: "beads"
name: "Identify follow-up work"
menus: prompts, conversations
description: "Review the conversation for incomplete work, follow-up items, and edge cases, organize them (grouping related items under epics), and file them as beads"
backgroundColor: "#DCEDC8"
group: "Tasks"
enabledWhen: 'commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Identify Follow-up Work

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`. Beads supports first-class parent/child hierarchy (epics) and blocking dependencies.

While working on a focused task, it's common to discover **other** problems, to leave parts of a request unfinished, or to spot edge cases worth handling later. Your job here is to comb back through **this conversation**, extract everything that warrants future work, organize it into a clean structure, and file a bead for each so nothing is lost.

## Step 1 — Mine the conversation for follow-up work

Review the full conversation history and extract every item that deserves its own bead. Use the context you already have — do not ask the user to re-explain what happened. Look for:

- **Unsolved problems**: bugs, errors, failing tests, or warnings that were observed but left unfixed because we focused on something else.
- **Incomplete requests**: things the user asked for that were **not** done. For example, if the user wanted A, B, and C and only A was delivered, B and C each become a bead.
- **Side discoveries**: problems noticed in passing while doing the main work (e.g. a related bug, a fragile code path, a missing edge-case handler).
- **Edge cases & hardening**: inputs, states, or failure modes that the current work does not yet handle and should.
- **Deferred / parked work**: anything explicitly postponed ("let's do this later", "out of scope for now"), TODOs, tech debt, missing tests, or missing docs.

For each candidate, capture: a short **title**, a **description** (what it is, why it matters, where it lives — files/components — and any context such as root cause, repro, or proposed fix), and a suggested **type** and **priority**.

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

Drop or merge any candidate that clearly duplicates an existing open bead. Note which existing bead it matched so you can report it.

## Step 4 — Organize into a logical structure

Group the surviving candidates so related work lives together instead of as a flat pile:

- **Cluster related items** that share a theme, feature, or component. When a cluster has **two or more** related items, propose an **epic** (parent bead) to hold them, and make those items its **children**. An epic needs a clear title and a one-line purpose.
- **Keep genuinely standalone items at the top level** — do not invent an epic for a single item.
- Within each epic, note any **ordering dependencies** between children (child B can't start until child A is done) — you'll wire these as blocking edges later.
- For each item (and each epic), assign sensible defaults: `bug` for defects, `task`/`feature`/`chore` otherwise; `epic` for the parents; priority by real impact (P0 highest .. P4 lowest), defaulting to P2/P3 for follow-ups.

Number every epic and item so each maps to a checkbox in the next step.

## Step 5 — Present the structured list and let the user accept or ignore each item

This is **read-only until you confirm**. First, **print the organized list as a regular message** so the user can see the structure — epics with their children nested beneath them, then standalone items — each line formatted as:

> **[<type> · <priority>] <title>** — <one-line why>

Then present **every** epic and item in a single `mitto_ui_form_mitto(self_id: "@mitto:session_id")` as checkboxes, **checked by default**, so the user can simply uncheck what to skip. Nest children under their epic visually:

```html
<p>Select what to file as beads. Unchecked items are skipped. Epics group their children.</p>

<label><input type="checkbox" name="epic_1" checked /> [epic · P2] Parser hardening</label>
<label>&nbsp;&nbsp;&nbsp;&nbsp;<input type="checkbox" name="bead_1" checked /> [bug · P2] Crash on empty input — found while fixing X</label>
<label>&nbsp;&nbsp;&nbsp;&nbsp;<input type="checkbox" name="bead_2" checked /> [chore · P3] Add missing parser tests</label>
<label><input type="checkbox" name="bead_3" checked /> [task · P3] Part B of the original request — not yet done</label>
```

Use stable names: `epic_<N>` for proposed epics and `bead_<N>` for items (matching the numbering from Step 4). Submit/Cancel buttons are added automatically.

Interpreting the result:
- An epic or item is **approved** only if its key is **present** in the returned values — checked boxes are submitted, unchecked boxes are omitted entirely.
- If an **epic is unchecked but some of its children are checked**, create those children as **standalone top-level beads** so the work is not lost.
- If the user **cancels** the form, or submits with **everything unchecked**, create nothing and report that no beads were filed.

## Step 6 — Create the approved beads

Create approved **epics first** so children can reference them. For each bead, compose a well-structured Markdown description (Summary, Findings / Context, Proposed Solution if known, Acceptance Criteria), write it to a temporary file, and pass it via `--body-file` to preserve formatting and avoid shell-quoting issues:

```bash
# Epic (parent)
bd create "<epic title>" --type epic --priority <priority> --labels "<labels>" --body-file /tmp/bead-epic.md
# Child of an approved epic
bd create "<title>" --parent <epic-id> --type <type> --priority <priority> --body-file /tmp/bead-item.md
# Standalone item (no epic)
bd create "<title>" --type <type> --priority <priority> --labels "<labels>" --body-file /tmp/bead-item.md
```

Capture each new bead ID from the output. Report any creation that fails and why.

## Step 7 — Wire relationships

For each ordering dependency identified in Step 4 (child B can't start until child A is done), add a blocking edge; link non-blocking relationships as `related`:

```bash
bd dep add <blocked-id> <blocker-id>          # blocker blocks blocked
bd link <id> <other-id> --type related        # non-blocking relationship
bd dep cycles                                 # verify integrity
```

For each created epic, record the grouping in its history with a terse audit note:

```bash
bd update <epic-id> --append-notes "Grouped <N> follow-ups (<child-ids>) from conversation review: <one-line rationale>."
```

## Step 8 — Final summary

Finish with a short summary listing:

- Each **created epic** (ID + title) with its children indented beneath it, then standalone beads — grouped for readability.
- The dependency edges created between them.
- Any candidates **skipped** as duplicates (and which existing bead they matched).
- Any failures or warnings from `bd`.
