---
icon: "beads"
name: "Decompose"
menus: prompts
description: "Break a bead into child beads with dependencies and create them automatically"
backgroundColor: "#D1C4E9"
group: "Tasks"
enabledWhen: '!session.isChild && commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Decompose a Bead into Child Beads

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`. Beads supports first-class parent/child hierarchy and blocking dependencies.

## Step 1 — Find beads to decompose

Run:

```bash
bd ready --json                 # claimable open beads
bd list --status open --json    # all open beads (fallback / broader set)
```

## Step 2 — Let the user choose a bead

- If **multiple beads** are found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to present the list and ask which one to decompose. Include the bead ID and title in each option label.
- If **exactly one bead** is found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to confirm before proceeding.
- If **no beads** are found: inform the user and stop.

## Step 3 — Fetch full bead details

For the selected bead, run:

```bash
bd show <bead-id> --long --json     # full fields, design, acceptance, metadata
bd show <bead-id> --children --json # existing children (if any)
bd dep tree <bead-id>               # existing dependencies
```

Analyse all gathered context thoroughly: understand the full scope, acceptance criteria, constraints, and any prior discussion.

## Step 4 — Critically evaluate whether decomposition is warranted

Before proposing child beads, reason carefully:

**Do NOT decompose if:**
- The bead describes a single, atomic change (e.g., "Update config value X", "Fix typo in error message")
- The work is tightly coupled and cannot be delivered or reviewed independently in parts
- The bead already has child beads
- The bead is small (e.g., ≤ 1–2 days of work) with clear, narrow acceptance criteria

**Decompose if:**
- The bead spans multiple independent concerns (e.g., backend + frontend + docs)
- Different parts can be parallelised across agents or team members
- The bead is large enough that a single PR would be difficult to review
- Multiple distinct acceptance criteria map cleanly to separate deliverables

If decomposition is **not** recommended: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to inform the user of your reasoning and ask if they want to proceed anyway. If they say No, stop here.

## Step 5 — Produce a decomposition plan

Create a breakdown with:

### Parent Bead Summary
Brief restatement of what the parent bead is about.

### Decomposition Rationale
Why splitting this bead makes sense: what the independent concerns are and how parallelism or reviewability is improved.

### Proposed Child Beads
For each proposed child bead, provide:
- **Title**: concise, action-oriented (will become the bead title)
- **Description**: what needs to be done and why, written as if it were a standalone bead
- **Acceptance Criteria**: specific, testable conditions for "done"
- **Type & Priority**: the bead type (task/bug/feature/chore) and priority (P0–P4)
- **Dependencies**: list any sibling child beads that must be completed first (a "blocks" relationship)

### What Stays in the Parent
Describe what (if anything) remains in the parent bead — e.g., coordination, final integration testing, or documentation.

## Step 6 — Present the plan and iterate

Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to show the decomposition plan and ask: "Does this breakdown look correct? Shall I create these child beads?"

- If the user says **No** or provides feedback: revise and present again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 7.

## Step 7 — Create child beads

For each approved child bead, create it as a child of the parent. Write the description to a temporary file and pass it via `--body-file` to preserve Markdown formatting:

```bash
bd create "<child title>" \
  --parent <parent-id> \
  --type <type> \
  --priority <priority> \
  --body-file /tmp/child-bead.md
```

Capture each new child bead ID from the output. Child beads inherit the parent's labels by default.

## Step 8 — Wire up dependencies between children

For each dependency identified in the plan (child B cannot start until child A is done), create a blocking dependency:

```bash
bd dep add <blocked-child-id> <blocker-child-id>   # blocker blocks blocked
```

Use `--no-cycle-check` only for bulk wiring, then verify with `bd dep cycles`.

## Step 9 — Confirm results

After all child beads are created and wired, present a summary listing:
- Each created child bead ID and title
- The dependency edges created between them
- Any failures or warnings from `bd`

Record the decomposition in the parent bead's history for future reference. Write the breakdown summary — the **decomposition rationale**, each child bead (**ID + title**), and the **dependency edges** created — to a temp file and post it as a comment, then add a terse audit note:

```bash
bd comment <parent-id> --file /tmp/decomposition-summary.md   # analysis + design + resulting structure
bd update <parent-id> --append-notes "Decomposed into <N> sub-issues (<child-ids>): <one-line rationale for the breakdown>."
```

Run `bd dep tree <parent-id>` to display the final structure.
