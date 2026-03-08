---
name: "Handoff to existing conversation"
description: "Continue current work in an existing conversation in the same workspace"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

<task>
Continue the current work in an existing conversation that shares the same workspace.
</task>

## Prerequisites: Check for Mitto MCP Server

This prompt requires Mitto's MCP server tools.

**Required tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_list`
- `mitto_conversation_send_prompt`
- `mitto_ui_options_combo`

If any are missing, **stop** and show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp. Do not proceed.

---

<instructions>

## Phase 1: Analyze Context

`mitto_conversation_get_current(self_id: "init")` → get session_id and working directory.

## Phase 2: Find Sibling Conversations

`mitto_conversation_list(working_dir, archived: false, exclude_self: <session_id>)`

If none found: inform user, suggest "Handoff to new conversation". Abort.

## Phase 3: Summarize Current Work

Prepare handoff summary including: context, current state, clear objective, specific instructions, files/modules, success criteria, constraints.

Present to user:

```markdown
## Handoff Summary
**Current Work:** <summary>
**What Needs to Continue:** <next steps>
**Proposed Message:**
---
<complete prompt for target conversation>
---
```

## Phase 4: Select Target

Ask via `mitto_ui_options_combo` (timeout: 60s):
```
question: "Which conversation should continue this work?"
options: <conversations formatted as "Title (running/idle)">
```

On timeout: abort. Do not send without explicit selection.

## Phase 5: Send Handoff

`mitto_conversation_send_prompt(self_id, conversation_id, prompt)`

Structure the prompt with: Context, Current State, Your Task, Instructions, Files to Work On, Success Criteria, Constraints.

## Phase 6: Report

```markdown
Work Handed Off Successfully
**Sent To:** <title> (<id>)
**What Was Sent:** <brief summary>
**To Switch:** Use Conversations panel to navigate to "<title>"
```

</instructions>

<rules>
- Only show conversations in the same workspace
- Exclude current conversation from options
- Get explicit user selection before sending
- Create comprehensive, self-contained handoff prompts
- Include all necessary context — don't assume target knows what we discussed
- Be specific with file paths and next steps
- Clarify unclear tasks in this conversation before handing off
</rules>
