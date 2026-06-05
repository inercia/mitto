---
name: "Beads issue: decompose"
menus: beadsIssues
requires: parameters
description: "Break this bead into child beads with dependencies and create them automatically"
backgroundColor: "#D1C4E9"
group: "Beads"
enabledWhen: '!session.isChild && commandExists("bd") && dirExists(".beads")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# Beads: Decompose a Bead into Child Beads

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`. Beads supports first-class parent/child hierarchy and blocking dependencies.

The **target bead** is `${ISSUE_ID}`.

## Step 1 — Fetch full bead details

Load everything about the target bead:

```bash
bd show ${ISSUE_ID} --long --json     # full fields, design, acceptance, metadata
bd show ${ISSUE_ID} --children --json # existing children (if any)
bd dep tree ${ISSUE_ID}               # existing dependencies
```

Analyse all gathered context thoroughly: understand the full scope, acceptance criteria, constraints, and any prior discussion.

## Step 2 — Critically evaluate whether decomposition is warranted

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

## Step 3 — Produce a decomposition plan

Create a breakdown with:

### Parent Bead Summary
Brief restatement of what the parent bead (`${ISSUE_ID}`) is about.

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

## Step 4 — Present the plan and iterate

Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to show the decomposition plan and ask: "Does this breakdown look correct? Shall I create these child beads?"

- If the user says **No** or provides feedback: revise and present again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 5.

## Step 5 — Create child beads

For each approved child bead, create it as a child of the parent. Write the description to a temporary file and pass it via `--body-file` to preserve Markdown formatting:

```bash
bd create "<child title>" \
  --parent ${ISSUE_ID} \
  --type <type> \
  --priority <priority> \
  --body-file /tmp/child-bead.md
```

Capture each new child bead ID from the output. Child beads inherit the parent's labels by default.

## Step 6 — Wire up dependencies between children

For each dependency identified in the plan (child B cannot start until child A is done), create a blocking dependency:

```bash
bd dep add <blocked-child-id> <blocker-child-id>   # blocker blocks blocked
```

Use `--no-cycle-check` only for bulk wiring, then verify with `bd dep cycles`.

## Step 7 — Confirm results

After all child beads are created and wired, present a summary listing:
- Each created child bead ID and title
- The dependency edges created between them
- Any failures or warnings from `bd`

Run `bd dep tree ${ISSUE_ID}` to display the final structure, and remind the user to run `bd dolt push` to push the beads data to the remote when appropriate.
