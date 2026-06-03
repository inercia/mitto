---
name: "Continue work in existing child"
description: "Continue work in an existing child conversation"
group: "Work flow"
menus: conversation
backgroundColor: "#FFF9C4"
enabledWhen: 'children.exists && permissions.canSendPrompt && tools.hasPattern("mitto_conversation_*") && !session.isPeriodicConversation'
---

Continue working on this by sending instructions to an existing child conversation.

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

## Phase 3: Prepare Instructions

Based on the conversation context and the overall goal, prepare continuation instructions for the child.

Present to user:

```markdown
## Continue Child Work

**Child:** <title> (<id>)

**Proposed Instructions:**
---
<continuation prompt for the child>
---
```

Confirm via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 120s):

```
question: "Send these instructions to <child title>?"
options:
  - label: "Send as proposed"
    description: "<one-line summary of the proposed instructions>"
allow_free_text: true
free_text_placeholder: "Describe what to do differently..."
```

On timeout: abort. Do not send without explicit confirmation.

## Phase 4: Send Instructions

`mitto_conversation_send_prompt(self_id: "@mitto:session_id", conversation_id: <child_id>, prompt: <confirmed instructions>)`

## Phase 5: Report

```markdown
✅ Instructions Sent

**Sent To:** <title> (<id>)
**Instructions:** <brief summary>

The child conversation will continue working. You can:
- Monitor progress in the Conversations panel
- Wait for a status report from the child
- Use "Continue work in child" again to send more instructions
```

## Guidelines

- Review child's current state before sending instructions
- Build on what the child has already accomplished
- Be specific about what to do next
- Don't repeat work the child has already done
- Consider if the child is currently busy (running) vs idle
- Get user confirmation before sending
