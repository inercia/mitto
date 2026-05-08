---
name: "Continue in another workspace"
description: "Hand off current work to a new conversation in a different workspace"
group: "Work flow"
backgroundColor: "#E1BEE7"
enabledWhen: 'permissions.canStartConversation && permissions.canInteractOtherWorkspaces && tools.hasPattern("mitto_conversation_*")'
---

Hand off current work to a new conversation in a different workspace.

## Phase 1: Context

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

## Phase 2: Select Target Workspace

Call `mitto_workspace_list()` to get all workspaces.

Filter out your own workspace (match by `@mitto:workspace_uuid`). If no other workspaces exist, inform user "No other workspaces available." and abort.

Ask via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 60s):

```
question: "Which workspace should continue this work?"
options: <workspaces formatted as "Name — ACP Server (working_dir)">
```

On timeout: abort. Do not proceed without explicit selection.

## Phase 3: Analyze Context and Propose Work

Review recent conversation history to understand what we've been working on:

`mitto_conversation_history(self_id: "@mitto:session_id", last_n: 20, include_data: true)`

Also learn about the target workspace by checking its metadata from the workspace list (description, URL, group) to understand what the target workspace is about.

Analyze the conversation to determine the single most likely next task that should be handed off to the target workspace. Consider:
- The most natural continuation of the current work
- Unfinished tasks or next steps mentioned in the conversation
- Work that specifically belongs in the target workspace's project

If no obvious next task emerges, consider other available work (e.g., open issues, incomplete tasks, code cleanup for recently modified files).

Present your best proposal via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 120s):

```
question: "What work should be continued in the other workspace?"
options:
  - label: "<best proposal — short descriptive label>"
    description: "<concrete, self-contained instruction for the target conversation>"
  <only include additional options if there are genuinely distinct alternative tasks worth mentioning — do not pad the list>
allow_free_text: true
free_text_placeholder: "Describe custom work to hand off to the other workspace..."
```

The first option should be your single best proposal. Only add more options if they represent genuinely different tasks — do not artificially generate alternatives.

On timeout: abort. Do not proceed without explicit selection.

## Phase 4: Prepare Handoff

Using the selected option (or free text), create a self-contained handoff prompt that:
1. Explains the context from this conversation (what we were working on, current state)
2. Bridges to the target workspace (how the work relates to that project)
3. Includes the selected work item as the primary task
4. Provides relevant file paths, decisions made, and constraints

Present to user:

```markdown
## Cross-Workspace Handoff

**From:** @mitto:session_name (current workspace)
**To:** <target workspace name>

**Selected Work:** <selected option label>

**Handoff Prompt:**
---
<complete, self-contained prompt for the new conversation incorporating the selected work>
---
```

Ask user to confirm or modify the prompt via `mitto_ui_options`.

## Phase 5: Create Conversation

`mitto_conversation_new(self_id: "@mitto:session_id", workspace: "<target_workspace_uuid>", title: "<descriptive title>", initial_prompt: "<handoff prompt>")`

## Phase 6: Report

```markdown
✅ Work Handed Off to Another Workspace

**New Conversation:** <title> (<id>)
**Workspace:** <workspace name>
**Using:** <ACP server>

The new conversation will start working immediately. You can:
- Switch to the target workspace to monitor progress
- Continue working here on other tasks
```

## Guidelines

- Create comprehensive, self-contained handoff prompts — the target workspace has no context from this conversation
- Bridge the gap: explain WHY this work is relevant to the target workspace/project
- Include all necessary context: file paths, decisions, constraints, current state
- Be specific about what needs to happen next
- Get user confirmation before creating the conversation
- Use clear, descriptive titles that make sense in the target workspace's context
