---
name: "Continue work in child"
description: "Resume work in an existing child conversation"
group: "Work flow"
backgroundColor: "#B3E5FC"
enabledWhen: "children.exists"
---

Continue working on a task by sending instructions to an existing child conversation.

## Prerequisites: Check for Mitto MCP Server

This prompt requires Mitto's MCP server tools.

**Required tools:**
- `mitto_conversation_list`
- `mitto_conversation_get_summary`
- `mitto_conversation_send_prompt`
- `mitto_ui_options_combo`

If any are missing, **stop** and show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp. Do not proceed.

---

## Phase 1: Get Context

Your session ID is `@mitto:session_id`.
Existing children: `@mitto:children`

List child conversations:
`mitto_conversation_list(self_id: "@mitto:session_id", working_dir: "@mitto:working_dir", archived: false)`

Filter results to only show conversations where the parent is this session.

If no children found: inform user "No child conversations found. Use 'Create minions' or 'Handoff to new conversation' to create one." Abort.

## Phase 2: Select Child

Present children with their status:

Ask via `mitto_ui_options_combo` (timeout: 60s):
```
question: "Which child conversation should continue working?"
options: <children formatted as "Title - ACP Server (running/idle)">
```

On timeout: abort. Do not send without explicit selection.

## Phase 3: Get Child Summary

`mitto_conversation_get_summary(self_id: "@mitto:session_id", conversation_id: <selected_child_id>)`

Review what the child has accomplished and where it left off.

## Phase 4: Prepare Instructions

Based on the child's current state and the overall goal, prepare continuation instructions:

Present to user:
```markdown
## Continue Child Work

**Child:** <title> (<id>)
**Current Status:** <summary of what child has done>
**Last Activity:** <when>

**Proposed Instructions:**
---
<continuation prompt for the child>
---
```

Ask user to confirm or modify the instructions.

## Phase 5: Send Instructions

`mitto_conversation_send_prompt(self_id: "@mitto:session_id", conversation_id: <child_id>, prompt: <instructions>)`

## Phase 6: Report

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
