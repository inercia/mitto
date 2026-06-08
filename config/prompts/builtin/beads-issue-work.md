---
icon: "beads"
name: "Start work"
menus: beadsIssues
requires: parameters
description: "Plan this bead and spawn parallel Mitto conversations to implement it"
backgroundColor: "#B2DFDB"
group: "Tasks"
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

## Step 5 — Dispatch work items to child conversations

Only parallelize work items that are **truly independent** (no shared files, no ordering dependency). Run trivial or tightly-coupled items inline in this conversation rather than dispatching a separate conversation for each.

For each parallelizable work item in the approved plan, **reuse a suitable existing child when possible, otherwise create a new one**:

1. **Reuse vs. create:**
   - Check the existing children listed above (`@mitto:children`). If one is **idle** (not currently running) and a good fit for this work item (same workspace, related prior task), **reuse it** by sending the worker prompt with
     `mitto_conversation_send_prompt_mitto(self_id: "@mitto:session_id", conversation_id: "<existing-child-id>", prompt: "<worker prompt>")`.
   - Otherwise create a new conversation with `mitto_conversation_new_mitto(self_id: "@mitto:session_id", ...)`:
     - `title`: the work item title prefixed with the bead ID (e.g., `"${ISSUE_ID} · Add database migration"`)
     - `beads_issue`: `${ISSUE_ID}` (links the worker conversation to this bead)
     - `acp_server`: choose from the available ACP servers listed above — prefer a faster/cheaper model for straightforward tasks, and a slower/more capable model for complex tasks that require deep reasoning

2. The **worker prompt** (reused or new) must be **self-contained** and include:
   - The full bead ID, title, and description
   - The acceptance criteria from the bead
   - The specific work item title and description
   - The definition of done for this task
   - Any relevant context from the bead's design notes or dependencies
   - Instruction to report back using `mitto_children_tasks_report_mitto` when done

3. Do **not** wait for each conversation before dispatching the next — dispatch all in parallel.

## Step 6 — Log work start on the bead

Immediately after dispatching, record a progress comment in the bead's history so the tracker reflects that work has begun and where it is happening:

```bash
bd comment ${ISSUE_ID} "Started work. Plan: <N> work items. Dispatched to: <child titles / IDs> (reused: <which, if any>)."
```

## Step 7 — Wait for workers and synthesise

Use `mitto_children_tasks_wait_mitto(self_id: "@mitto:session_id", children_list: [...], task_id: "${ISSUE_ID}", timeout_seconds: 600)` to wait for the workers to report back. On timeout, retry the pending children with the **same `task_id`** (omit the prompt to avoid duplicates). Summarise the consolidated results to the user, and log a short progress comment for any notable milestone or blocker:

```bash
bd comment ${ISSUE_ID} "Progress: <what completed / what remains / blockers>."
```

## Step 8 — Log completion and close out

Once the work is complete and verified, record a completion comment in the bead's history, then offer to close it:

```bash
bd comment ${ISSUE_ID} "Completed: <what was delivered, key changes, verification performed>."
bd close ${ISSUE_ID} --reason "<short summary of what was delivered>"
```

After closing, clean up any finished child conversations that are no longer needed with `mitto_conversation_delete_mitto(self_id: "@mitto:session_id", conversation_id: "<child-id>")`.
