---
name: "Continue in existing child and wait"
description: "Continue work in an existing child conversation and wait for the response"
group: "Work flow"
backgroundColor: "#FFF9C4"
enabledWhen: 'children.exists && permissions.canSendPrompt && tools.hasPattern("mitto_conversation_*")'
---

Continue working on this by sending instructions to an existing child conversation,
and wait for the child conversation to report.

## Phase 1: Context

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

Available ACP servers:
@mitto:available_acp_servers

Existing children:
@mitto:children

Filter results to only show conversations where the parent is this session.

If no children found: inform user "No child conversations found. Use 'Create minions' or 'Handoff to new conversation' to create one." Abort.

## Phase 2: Select Child

Present children with their status:

Ask via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 60s):

```
question: "Which child conversation should continue working?"
options: <children formatted as "Title - ACP Server (running/idle)">
```

On timeout: abort. Do not send without explicit selection.

## Phase 3: Prepare and Send Instructions

Choose a short, descriptive `task_id` for this work or iteration (e.g., `"investigate-bug-1"`, `"iter2-fix-tests"`).

Prepare continuation instructions for the current goals but in another conversation.
Think about what we were trying to accomplish.
Add some meaningful context for the other agent.

Append the following text to these instructions:

```
When complete, report via

mitto_children_tasks_report:
    self_id: "\@mitto:session_id"
    task_id: "<task_id from the parent's wait call>"
    status: "completed" | "failed" | "partial"
    summary: "<what was accomplished>"
    details: "<files modified, errors, discoveries, open questions>")

Do this as your final action.
```

Send the instructions with:

`mitto_conversation_send_prompt(self_id: "@mitto:session_id", conversation_id: <child_id>, prompt: <instructions>)`

## Phase 4: Wait for Results

Wait for the results from the child conversation with:

```
mitto_children_tasks_wait(self_id, children_list: [...], task_id: "<task_id>", timeout_seconds: 600)
```

Inform user: "Waiting for child tasks (iteration N)... Monitor in Conversations panel."

**On timeout**: retry pending children with `mitto_children_tasks_wait` using the **same `task_id`** (omit prompt to avoid duplicates). Reports already received are preserved. After two timeouts, treat as failure.

## Guidelines

- Review child's current state before sending instructions
- Build on what the child has already accomplished
- Be specific about what to do next
- Don't repeat work the child has already done
- Consider if the child is currently busy (running) vs idle
- Get user confirmation before sending
