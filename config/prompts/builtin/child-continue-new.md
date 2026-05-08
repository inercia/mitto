---
name: "Continue in new child"
description: "Continue current work in a new conversation, maybe with a different model"
group: "Work flow"
backgroundColor: "#FFF9C4"
enabledWhen: '!session.isChild && permissions.canStartConversation && tools.hasPattern("mitto_conversation_*")'
---

Continue the current work in a new conversation, optionally with a different model.

## Phase 1: Analyze Context

1. Your session ID is `@mitto:session_id` — use this as `self_id` for all MCP tool calls.
2. Available ACP servers for this workspace: `@mitto:available_acp_servers`
   Note each server's name, tags (e.g., `[coding, fast]`, `[reasoning, planning]`), and the `(current)` marker.

## Phase 2: Prepare Handoff

Based on the conversation context, create a self-contained prompt including: context, current state, objective, instructions, files/modules, success criteria, constraints.

Present to user:

```markdown
## Handoff Summary
**Current Work:** <summary>
**Ready to Continue:** <next steps>
**Proposed Handoff Prompt:**
---
<complete prompt>
---
**Recommended Model:** <suggestion based on task complexity>
```

Confirm via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 120s):

```
question: "Create a new child conversation with this handoff?"
options:
  - label: "Create as proposed"
    description: "<one-line summary of the proposed work>"
allow_free_text: true
free_text_placeholder: "Describe what to change or do differently..."
```

On timeout: abort. Do not create a conversation without explicit confirmation.

## Phase 3: Select ACP Server

Ask via `mitto_ui_options` (timeout: 60s):
```
question: "Which AI agent for continuing this work?"
options: <list of server names from @mitto:available_acp_servers>
```

**On timeout**, auto-select by matching work to server tags:
- Implementation/execution → prefer `"coding"`/`"fast"`
- Complex reasoning/design → prefer `"reasoning"`/`"planning"`
- No match → current server, then first available

## Phase 4: Create Conversation

`mitto_conversation_new(self_id: "@mitto:session_id", title, initial_prompt, acp_server)`

## Phase 5: Report

```markdown
✅ Work Handed Off Successfully
**New Conversation:** <title> (<id>)
**Using:** <server> <if auto-selected: "(auto-selected)">
**To Switch:** Use Conversations panel
💡 Keep this conversation open for oversight while implementation happens elsewhere.
```

## Guidelines

- Create comprehensive, self-contained handoff prompts
- Include all necessary context
- Be specific with next steps and file paths
- Suggest appropriate models: faster for straightforward tasks, same for complex reasoning
- Get user confirmation before creating conversations
- Use clear, descriptive titles
- Clarify unclear tasks before handing off
