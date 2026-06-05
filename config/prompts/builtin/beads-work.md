---
name: "Beads: start work"
menus: prompts, beadsIssues
description: "Pick a ready bead from the beads tracker and spawn parallel Mitto conversations to implement it"
backgroundColor: "#B2DFDB"
group: "Beads"
enabledWhen: '!session.isChild && permissions.canStartConversation && permissions.canSendPrompt && commandExists("bd") && dirExists(".beads") && tools.hasPattern("mitto_conversation_*")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.
Available ACP servers: `@mitto:available_acp_servers`
Existing children: `@mitto:children`

# Beads: Start Work on a Bead

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

## Step 0 — Check for prior bead context

Before doing anything else, review the current conversation history to check whether a specific bead has already been discussed (e.g., a bead ID like `bd-1234` was mentioned, its details were fetched, or it was previously selected).

- If a bead **has** been discussed in this conversation:
  Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to ask the user whether to:
  - **Option 1**: `"Start working on [BEAD-ID]: [title]"` — if chosen, skip directly to Step 3 (fetch full details) using that bead ID.
  - **Option 2**: `"Work on a different bead"` — if chosen, continue to Step 1.

- If **no** bead has been previously discussed: skip this step and proceed directly to Step 1.

## Step 1 — Find ready work

Run the following to discover claimable work (open beads with no active blockers):

```bash
bd ready --json
```

If `bd ready` returns nothing, fall back to listing open beads:

```bash
bd list --status open --json
```

## Step 2 — Let the user choose a bead

- If **multiple beads** are found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to present the list and ask which one to work on. Include the bead ID, title, and priority in each option label.
- If **exactly one bead** is found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to confirm before proceeding.
- If **no beads** are found: inform the user and stop.

## Step 3 — Fetch full bead details

Using the selected bead ID, run:

```bash
bd show <bead-id> --long --json        # full fields, metadata, design, acceptance
bd dep tree <bead-id>                   # dependency tree (blockers and what it blocks)
bd show <bead-id> --children --json     # any child beads
```

Analyze all gathered context thoroughly: understand the problem statement, scope, constraints, acceptance criteria, design notes, and any dependencies or prior discussion in notes/comments.

## Step 4 — Claim the bead

Atomically claim the bead so others know it is being worked on:

```bash
bd update <bead-id> --claim
```

This sets the assignee to you and the status to `in_progress` (idempotent if already claimed by you).

## Step 5 — Produce an implementation plan

Create a structured plan with the following sections:

### Goal
One-paragraph summary of what needs to be built or fixed, and why.

### Approach
High-level technical approach: which components are affected, what design decisions are involved, and why this approach was chosen.

### Work Items
A numbered list of concrete, independently executable tasks. Each task must have:
- **Title**: short action-oriented name (e.g., "Add database migration for new column")
- **What to do**: a focused description of the work
- **Inputs / context needed**: what the task needs to know or have access to
- **Definition of done**: how to verify the task is complete

### Open Questions & Risks
- Any ambiguities in the bead that need clarification
- Technical risks or unknowns
- Dependencies on other beads or systems

## Step 6 — Present the plan and iterate

Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to show the plan summary and ask: "Does this plan look correct? Shall I proceed with spawning work conversations?"

- If the user says **No** or provides feedback: revise the plan and present it again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 7.

## Step 7 — Spawn one Mitto conversation per work item

For each work item in the approved plan:

1. Call `mitto_conversation_new_mitto` with `self_id: "@mitto:session_id"` and:
   - `title`: the work item title prefixed with the bead ID (e.g., `"bd-1234 · Add database migration"`)
   - `acp_server`: choose from the available ACP servers listed above — prefer a faster/cheaper model for straightforward tasks, and a slower/more capable model for complex tasks that require deep reasoning
   - `initial_prompt`: a **self-contained** prompt that includes:
     - The full bead ID, title, and description
     - The acceptance criteria from the bead
     - The specific work item title and description
     - The definition of done for this task
     - Any relevant context from the bead's design notes or dependencies
     - Instruction to report back using `mitto_children_tasks_report_mitto` when done

2. Do **not** wait for each conversation to complete before spawning the next — spawn all conversations in parallel.

3. After all conversations are spawned, use `mitto_children_tasks_wait_mitto(self_id: "@mitto:session_id", children_list: [...], task_id: "<bead-id>", timeout_seconds: 600)` to wait for them to report back, then summarise results to the user.

## Step 8 — Close out

Once the work is complete and verified, offer to close the bead:

```bash
bd close <bead-id> --reason "<short summary of what was delivered>"
```

Remind the user to run `bd dolt push` to push the beads data to the remote when appropriate.
