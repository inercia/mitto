---
name: "Beads issue: start work"
menus: beadsIssues
requires: parameters
description: "Plan this bead and spawn parallel Mitto conversations to implement it"
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

The **target bead** is `${ISSUE_ID}`.

## Step 1 — Fetch full bead details

Load everything about the target bead:

```bash
bd show ${ISSUE_ID} --long --json        # full fields, metadata, design, acceptance
bd dep tree ${ISSUE_ID}                   # dependency tree (blockers and what it blocks)
bd show ${ISSUE_ID} --children --json     # any child beads
```

Analyze all gathered context thoroughly: understand the problem statement, scope, constraints, acceptance criteria, design notes, and any dependencies or prior discussion in notes/comments.

## Step 2 — Claim the bead

Atomically claim the bead so others know it is being worked on:

```bash
bd update ${ISSUE_ID} --claim
```

This sets the assignee to you and the status to `in_progress` (idempotent if already claimed by you).

## Step 3 — Produce an implementation plan

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

## Step 4 — Present the plan and iterate

Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to show the plan summary and ask: "Does this plan look correct? Shall I proceed with spawning work conversations?"

- If the user says **No** or provides feedback: revise the plan and present it again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 5.

## Step 5 — Spawn one Mitto conversation per work item

For each work item in the approved plan:

1. Call `mitto_conversation_new_mitto` with `self_id: "@mitto:session_id"` and:
   - `title`: the work item title prefixed with the bead ID (e.g., `"${ISSUE_ID} · Add database migration"`)
   - `acp_server`: choose from the available ACP servers listed above — prefer a faster/cheaper model for straightforward tasks, and a slower/more capable model for complex tasks that require deep reasoning
   - `initial_prompt`: a **self-contained** prompt that includes:
     - The full bead ID, title, and description
     - The acceptance criteria from the bead
     - The specific work item title and description
     - The definition of done for this task
     - Any relevant context from the bead's design notes or dependencies
     - Instruction to report back using `mitto_children_tasks_report_mitto` when done

2. Do **not** wait for each conversation to complete before spawning the next — spawn all conversations in parallel.

3. After all conversations are spawned, use `mitto_children_tasks_wait_mitto(self_id: "@mitto:session_id", children_list: [...], task_id: "${ISSUE_ID}", timeout_seconds: 600)` to wait for them to report back, then summarise results to the user.

## Step 6 — Close out

Once the work is complete and verified, offer to close the bead:

```bash
bd close ${ISSUE_ID} --reason "<short summary of what was delivered>"
```

Remind the user to run `bd dolt push` to push the beads data to the remote when appropriate.
