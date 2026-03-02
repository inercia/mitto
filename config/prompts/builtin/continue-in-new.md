---
name: "Continue in new"
description: "Continue current work in a new conversation with a different model"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

Continue the current work in a new conversation, maybe with a different model.

## Prerequisites: Check for Mitto MCP Server

**IMPORTANT**: This prompt requires Mitto's MCP server tools to function. Before proceeding, verify that the required tools are available.

**Required tools:**

- `mitto_conversation_get_current`
- `mitto_conversation_get_summary`
- `mitto_conversation_new`
- `mitto_ui_options_combo`

**Check availability:**

1. Look for these tools in your available tools list
2. If ANY of these tools are missing, **STOP immediately** and inform the user how to install Mitto's MCP server. Mitto's MCP server
is at http://127.0.0.1:5757/mcp, so think about the instructions
for adding it. Then tell the user:

```
❌ This prompt requires Mitto's MCP server, which is not currently available. To use this prompt, you need to add Mitto's MCP server
in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, ABORT the prompt execution. Do not proceed with any of the sections below.**

---

## Use Cases

This is useful when:
- **Planning is done, ready to implement** - Use a faster model for straightforward implementation
- **Switching from exploration to execution** - Move from a reasoning model to an action model
- **Delegating well-defined work** - Hand off clear tasks to a more efficient model
- **Preserving current conversation** - Keep this conversation for planning/review while work happens elsewhere

## Phase 1: Analyze Current Context

First, gather information about the current conversation and available resources:

1. **Get current conversation details** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```
   This will give you the session_id and other metadata.

2. **List available ACP servers** - The response from step 1 includes `available_acp_servers` field showing all configured ACP servers (e.g., "auggie", "claude-code").

3. **Get conversation summary** using `mitto_conversation_get_summary`:
   ```
   self_id: <session_id from step 1>
   conversation_id: <session_id from step 1>
   ```
   This provides context about what we've been working on.

## Phase 2: Prepare Handoff

Analyze the current conversation and prepare a comprehensive, self-contained prompt for the new conversation.

**The handoff prompt must include:**

1. **Context**: What has been discussed/planned so far
2. **Current state**: What's been completed, what remains
3. **Clear objective**: Exactly what needs to be done next
4. **Specific instructions**: Step-by-step guidance if available
5. **Files/modules**: Which parts of the codebase to work on
6. **Success criteria**: How to know when the task is complete
7. **Constraints**: Any important requirements or limitations

**Present the handoff plan to the user:**

```markdown
## Handoff Summary

**Current Work:**
<Brief summary of what we've been doing>

**Ready to Continue:**
<What's ready to be implemented/executed>

**Proposed Handoff Prompt:**
---
<Show the complete prompt that will be sent to the new conversation>
---

**Recommended Model:**
<Suggest a faster/cheaper model if appropriate, or same model if complexity requires it>
```

## Phase 3: Select ACP Server

**Ask the user which ACP server to use** for the new conversation using `mitto_ui_options_combo`:

```
self_id: <session_id from Phase 1>
question: "Which AI agent would you like to use for continuing this work? (For implementation tasks, a faster model might be more efficient)"
options: <array of available_acp_servers from Phase 1>
timeout_seconds: 60
```

**If the user responds**, use their selected ACP server.

**If the request times out** (no response within 60 seconds):
- Make a best-effort guess based on the work characteristics:
  - For **straightforward implementation/execution**: Prefer faster models (e.g., "claude-code" if available)
  - For **complex reasoning/design**: Prefer more capable models (e.g., "auggie" if available)
  - For **unclear or mixed work**: Use the same ACP server as the current conversation
  - If current server is unknown: Use the first available ACP server from the list
- Proceed with the selected server
- Mention in the final report which server was auto-selected due to timeout

## Phase 4: Create New Conversation

**Create the new conversation** using `mitto_conversation_new`:

```
self_id: <session_id from Phase 1>
title: <descriptive title based on the work, e.g., "Implement user authentication">
initial_prompt: <comprehensive handoff prompt from Phase 2>
acp_server: <selected ACP server from Phase 3>
```

**Track the conversation ID** returned by the tool.

## Phase 5: Report Results

After the conversation is created, provide a clear summary:

```markdown
✅ Work Handed Off Successfully

**New Conversation Created:**
- **Title**: <conversation title>
- **Conversation ID**: <id>
- **Using**: <ACP server name><if auto-selected: " (auto-selected due to timeout)">

**What's Next:**
The new conversation will:
1. <First step from the handoff prompt>
2. <Second step>
3. ...

**In This Conversation:**
You can:
- Monitor progress by switching to the new conversation
- Continue planning or reviewing here
- Provide additional guidance if needed

**To Switch:**
Use the Conversations panel to navigate to "<conversation title>"

💡 **Tip**: Keep this conversation open for oversight and review while the implementation happens in the new conversation.
```

## Rules

- **Create comprehensive handoff prompts** - The new conversation should be fully self-contained
- **Include all necessary context** - Don't assume the new conversation knows what we discussed
- **Be specific about next steps** - Clear, actionable instructions
- **Suggest appropriate models** - Faster models for straightforward tasks, same model for complex reasoning
- **Always ask for confirmation** - Don't create conversations without user approval
- **Provide clear titles** - Make it easy to identify the conversation in the UI
- **Don't hand off unclear work** - If the task isn't well-defined, clarify it first in this conversation
- **Include file paths and specifics** - The new conversation needs concrete details to work with
