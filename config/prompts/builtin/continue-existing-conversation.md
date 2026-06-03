---
name: "Continue in existing conversation"
description: "Send work to an existing conversation in any workspace"
group: "Work flow"
backgroundColor: "#B3E5FC"
enabledWhen: 'permissions.canSendPrompt && tools.hasPattern("mitto_conversation_*") && !session.isPeriodicConversation'
---

Continue current work by sending instructions to an existing conversation.

## Phase 1: Context

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

## Phase 2: List and Select Conversation

Call `mitto_conversation_list(self_id: "@mitto:session_id", archived: false)` to get all active conversations.

Exclude your own session from the list (ID: `@mitto:session_id`).

If no other conversations exist, inform user "No other active conversations found. Use 'Continue in new child' to create one." and abort.

Present conversations grouped by workspace. Ask via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 60s):

```
question: "Which conversation should continue this work?"
options: <conversations formatted as "[WorkspaceName] Title (session_id) — ACP Server, status">
```

Include status indicators: 🟢 running, ⚪ idle, 🔒 locked.

On timeout: abort. Do not send without explicit selection.

## Phase 3: Analyze Context and Propose Work

Review YOUR recent conversation history to understand current work:
`mitto_conversation_history(self_id: "@mitto:session_id", last_n: 20, include_data: true)`

Review the TARGET conversation's recent history to understand its context:
`mitto_conversation_history(self_id: "@mitto:session_id", conversation_id: "<target_id>", last_n: 10, include_data: true)`

Analyze both conversations to determine the single most likely next task that should be sent to the target. Consider:
- The most natural continuation of the current work
- Work that builds on what the target conversation has already accomplished
- Tasks that align with the target conversation's existing focus

If no obvious next task emerges, consider other available work (e.g., open issues, incomplete tasks, code cleanup for recently modified files).

Present your best proposal via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 120s):

```
question: "What work should be sent to the target conversation?"
options:
  - label: "<best proposal — short descriptive label>"
    description: "<concrete, actionable instruction for the target conversation>"
  <only include additional options if there are genuinely distinct alternative tasks worth mentioning — do not pad the list>
allow_free_text: true
free_text_placeholder: "Describe custom work to pass to the other conversation..."
```

The first option should be your single best proposal. Only add more options if they represent genuinely different tasks — do not artificially generate alternatives. Reference the target conversation's existing work where relevant so the instruction has continuity.

On timeout: abort. Do not send without explicit selection.

## Phase 4: Prepare Instructions

Using the selected option (or free text), create instructions that:
1. Reference what the target conversation has been working on (so it has continuity)
2. Include the selected work item as the primary task
3. Provide clear, actionable next steps
4. Include relevant file paths, decisions, and constraints

Present to user:

```markdown
## Send Work to Existing Conversation

**Target:** <title> (<id>)
**Workspace:** <workspace name>
**Target's Recent Work:** <brief summary of what that conversation has been doing>
**Selected Work:** <selected option label>

**Proposed Instructions:**
---
<instructions for the target conversation incorporating the selected work>
---
```

Ask user to confirm or modify via `mitto_ui_options`.

## Phase 5: Send Instructions

If the target is in the same workspace:
`mitto_conversation_send_prompt(self_id: "@mitto:session_id", conversation_id: "<target_id>", prompt: "<instructions>")`

If the target is in a different workspace, include the `workspace` parameter:
`mitto_conversation_send_prompt(self_id: "@mitto:session_id", conversation_id: "<target_id>", workspace: "<workspace_uuid>", prompt: "<instructions>")`

## Phase 6: Report

```markdown
✅ Instructions Sent

**Sent To:** <title> (<id>)
**Workspace:** <workspace name>
**Instructions:** <brief summary>

The target conversation will process your instructions when idle. You can:
- Monitor progress in the Conversations panel
- Switch to the target conversation
- Continue working here on other tasks
```

## Guidelines

- Review both conversations' histories before preparing instructions
- Build on what the target conversation has already accomplished
- Be specific about what to do next — don't repeat work already done
- Consider if the target is currently busy (running) vs idle
- For cross-workspace sends, the agent needs the `can_interact_other_workspaces` permission — if the send fails with a permission error, inform the user they need to enable that flag
- Get user confirmation before sending
- If conversations are in the same workspace, don't include the `workspace` parameter
