---
name: "Handoff to new conversation"
description: "Continue current work in a new conversation, maybe with a different model"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

<task>
Continue the current work in a new conversation, optionally with a different model.
</task>

## Prerequisites: Check for Mitto MCP Server

This prompt requires Mitto's MCP server tools.

**Required tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_get_summary`
- `mitto_conversation_new`
- `mitto_ui_options_combo`

If any are missing, **stop** and show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp. Do not proceed.

---

<instructions>

## Phase 1: Analyze Context

1. `mitto_conversation_get_current(self_id: "init")` → session_id, `available_acp_servers`
2. Note server `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`) and `current` flag
3. `mitto_conversation_get_summary(self_id, conversation_id)` → current work context

## Phase 2: Prepare Handoff

Create a self-contained prompt including: context, current state, objective, instructions, files/modules, success criteria, constraints.

Present to user with recommended model:

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

## Phase 3: Select ACP Server

Ask via `mitto_ui_options_combo` (timeout: 60s):
```
question: "Which AI agent for continuing this work?"
options: <available_acp_servers>
```

**On timeout**, auto-select by matching work to server tags:
- Implementation/execution → prefer `"coding"`/`"fast"`
- Complex reasoning/design → prefer `"reasoning"`/`"planning"`
- No match → current server, then first available

## Phase 4: Create Conversation

`mitto_conversation_new(self_id, title, initial_prompt, acp_server)`

## Phase 5: Report

```markdown
✅ Work Handed Off Successfully
**New Conversation:** <title> (<id>)
**Using:** <server> <if auto-selected: "(auto-selected)">
**To Switch:** Use Conversations panel
💡 Keep this conversation open for oversight while implementation happens elsewhere.
```

</instructions>

<rules>
- Create comprehensive, self-contained handoff prompts
- Include all necessary context
- Be specific with next steps and file paths
- Suggest appropriate models: faster for straightforward tasks, same for complex reasoning
- Get user confirmation before creating conversations
- Use clear, descriptive titles
- Clarify unclear tasks before handing off
</rules>
