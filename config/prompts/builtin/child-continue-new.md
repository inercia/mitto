---
icon: "play"
name: "Continue in new"
description: "Continue the current work in a new conversation — in this or another workspace"
group: "Work flow"
menus: prompts, conversation
backgroundColor: "#FFF9C4"
enabledWhen: 'permissions.canStartConversation && tools.hasPattern("mitto_conversation_*") && !session.isPeriodicConversation'
---

Continue the current work in a brand-new conversation. Let the user choose which
workspace to start it in (this one or another), optionally with a different model.

## Phase 1: Context

1. Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.
2. Available ACP servers for this workspace: `@mitto:available_acp_servers`
   Note each server's name, tags (e.g., `[coding, fast]`, `[reasoning, planning]`), and the `(current)` marker.
3. Your current workspace UUID is `@mitto:workspace_uuid`.

## Phase 2: Select Workspace

Call `mitto_workspace_list()` to get all workspaces.

Ask via `mitto_ui_options(self_id: "@mitto:session_id", ...)` (timeout: 60s):

```
question: "Which workspace should the new conversation run in?"
options: <workspaces formatted as "Name — ACP Server (working_dir)", current workspace first and labelled "(current)">
```

On timeout: abort. Do not proceed without explicit selection.

- If the user picks the **current** workspace, you'll create the conversation here.
- If the user picks **another** workspace, you'll pass its UUID as `workspace`. This
  requires the `can_interact_other_workspaces` permission — if creation later fails
  with a permission error, inform the user they need to enable that flag.

## Phase 3: Prepare Handoff

Based on the conversation context, create a self-contained prompt including: context,
current state, objective, instructions, files/modules, success criteria, constraints.
When targeting another workspace, also bridge the gap — explain how this work relates
to that project, since it has no context from this conversation.

Present to user:

```markdown
## Handoff Summary
**Target Workspace:** <name>
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
question: "Create a new conversation with this handoff?"
options:
  - label: "Create as proposed"
    description: "<one-line summary of the proposed work>"
allow_free_text: true
free_text_placeholder: "Describe what to change or do differently..."
```

On timeout: abort. Do not create a conversation without explicit confirmation.

## Phase 4: Select ACP Server

If the target is the current workspace, choose from `@mitto:available_acp_servers`.
If the target is another workspace, use the ACP servers reported for it by
`mitto_workspace_list` (prefer the one marked `is_default` for that folder).

Ask via `mitto_ui_options` (timeout: 60s):
```
question: "Which AI agent for continuing this work?"
options: <list of server names for the chosen workspace>
```

**On timeout**, auto-select by matching work to server tags:
- Implementation/execution → prefer `"coding"`/`"fast"`
- Complex reasoning/design → prefer `"reasoning"`/`"planning"`
- No match → current/default server, then first available

## Phase 5: Create Conversation

- Same workspace:
  `mitto_conversation_new(self_id: "@mitto:session_id", title, initial_prompt, acp_server)`
- Another workspace (include `workspace`):
  `mitto_conversation_new(self_id: "@mitto:session_id", workspace: "<target_uuid>", title, initial_prompt, acp_server)`

## Phase 6: Report

```markdown
✅ Work Handed Off Successfully
**New Conversation:** <title> (<id>)
**Workspace:** <name>
**Using:** <server> <if auto-selected: "(auto-selected)">
**To Switch:** Use the Conversations panel
💡 Keep this conversation open for oversight while implementation happens elsewhere.
```

## Guidelines

- Create comprehensive, self-contained handoff prompts — a different workspace has no context
- Include all necessary context: file paths, decisions, constraints, current state
- Be specific with next steps
- Suggest appropriate models: faster for straightforward tasks, same for complex reasoning
- Get user confirmation before creating conversations
- Use clear, descriptive titles that make sense in the target workspace
- Clarify unclear tasks before handing off
