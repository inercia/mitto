---
icon: "beads"
name: "Start working on ready"
menus: prompts, beadsList
description: "Pick a ready (not-in-progress) bead, claim it, and launch a worker conversation that runs the per-issue Start Work prompt"
backgroundColor: "#B2DFDB"
group: "Beads"
enabledWhen: '!session.isChild && permissions.canStartConversation && commandExists("bd") && dirExists(".beads") && tools.hasPattern("mitto_conversation_*")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.
Available ACP servers: `@mitto:available_acp_servers`

# Beads: Start Work on a Bead

Beads is a CLI issue tracker (`bd`). Issues are called "beads" and have IDs like `bd-xyz`.

This prompt is a **launcher**: it finds a ready bead, claims it, and spawns a dedicated worker conversation that runs the per-issue **"Start work"** prompt against that bead. The worker conversation owns the actual planning, implementation, and progress/completion logging.

## Step 0 — Check for prior bead context

Before doing anything else, review the current conversation history to check whether a specific bead has already been discussed (e.g., a bead ID like `bd-1234` was mentioned, its details were fetched, or it was previously selected).

- If a bead **has** been discussed in this conversation:
  Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to ask the user whether to:
  - **Option 1**: `"Start working on [BEAD-ID]: [title]"` — if chosen, skip directly to Step 3 (claim) using that bead ID.
  - **Option 2**: `"Work on a different bead"` — if chosen, continue to Step 1.

- If **no** bead has been previously discussed: skip this step and proceed directly to Step 1.

## Step 1 — Find ready work

Run the following to discover claimable work (open beads with no active blockers):

```bash
bd ready --json
```

Filter the results to **exclude any bead whose `status` is already `in_progress`** — those are already being worked on and should not be presented. If `bd ready` returns nothing (or everything is filtered out), fall back to listing open, not-in-progress beads:

```bash
bd list --status open --json
```

If after filtering there are **no ready, not-in-progress beads**, inform the user that there is nothing to start and stop.

## Step 2 — Let the user choose a bead

- If **multiple beads** are available: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to present the list and ask which one to work on. Include the bead ID, title, and priority in each option label.
- If **exactly one bead** is available: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to confirm before proceeding.

## Step 3 — Claim the bead

Atomically claim the selected bead so others know it is being worked on:

```bash
bd update <bead-id> --claim
```

This sets the assignee to you and the status to `in_progress` (idempotent if already claimed by you).

## Step 4 — Launch the worker conversation

Spawn a new conversation that runs the per-issue **"Start work"** prompt against the claimed bead. Pass the prompt **by name** and supply the bead ID as an argument — `mitto_conversation_new` resolves the named prompt and substitutes `${ISSUE_ID}` for you, so there is no need to fetch the prompt text first:

```
mitto_conversation_new_mitto(
  self_id: "@mitto:session_id",
  title: "<bead-id>: <bead title>",
  beads_issue: "<bead-id>",
  acp_server: "<chosen server>",
  prompt_name: "Start work",
  arguments: { "ISSUE_ID": "<bead-id>" },
)
```

- `title`: the bead ID and title (e.g., `"bd-1234: Add database migration"`).
- `beads_issue`: the claimed bead ID (links the worker conversation to this bead).
- `acp_server`: choose from the available ACP servers listed above — prefer a faster/cheaper model for straightforward beads, and a slower/more capable model for complex beads that require deep reasoning.
- `prompt_name`: the per-issue **"Start work"** prompt (mutually exclusive with `initial_prompt`).
- `arguments`: fills the prompt's `${ISSUE_ID}` placeholder with the claimed bead ID.

## Step 5 — Report back

Tell the user that the worker conversation has been launched for the bead (give its title and bead ID), and that they can monitor its progress in the Conversations panel. The worker handles planning, implementation, and logging its own progress/completion comments on the bead — do **not** duplicate that work here.
