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

This prompt requires Mitto's MCP server tools to function. Before proceeding, verify that the required tools are available.

**Required tools:**

- `mitto_conversation_get_current`
- `mitto_conversation_list`
- `mitto_conversation_send_prompt`
- `mitto_ui_options_combo`

**Check availability:**

1. Look for these tools in your available tools list
2. If any of these tools are missing, **stop** and inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
This prompt requires Mitto's MCP server, which is not currently available. To use this prompt, you need to add Mitto's MCP server
in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, abort the prompt execution. Do not proceed with any of the sections below.**

---

<instructions>

## Use Cases

This is useful when:
- **Sharing context across conversations** - Pass findings or instructions to another active conversation
- **Delegating follow-up work** - Hand off a well-defined task to a conversation that already has relevant context
- **Coordinating parallel work** - Send updated instructions to a conversation working on a related task
- **Continuing after context limits** - Resume work in a conversation that has room for more context

## Phase 1: Analyze Current Context

First, gather information about the current conversation:

1. **Get current conversation details** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```
   This will give you the session_id, working directory, and other metadata.

2. **Note the working directory** from the response — this will be used to find sibling conversations in the same workspace.

## Phase 2: Find Sibling Conversations

**List conversations in the same workspace** using `mitto_conversation_list` with filters:

```
working_dir: <working_dir from Phase 1>
archived: false
exclude_self: <session_id from Phase 1>
```

This returns only active (non-archived) conversations that share the same workspace, excluding the current conversation.

**If no conversations are found:**
- Inform the user: "No other active conversations found in this workspace. You can use 'Handoff to new conversation' to create a new one."
- Abort — do not proceed.

## Phase 3: Summarize Current Work

Analyze the current conversation and prepare a summary of what needs to be handed off.

**The handoff summary must include:**

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

**What Needs to Continue:**
<What's ready to be implemented/executed>

**Proposed Message to Send:**
---
<Show the complete prompt that will be sent to the target conversation>
---
```

## Phase 4: Select Target Conversation

**Ask the user which conversation to send the work to** using `mitto_ui_options_combo`:

Build the options list from the conversations found in Phase 2. Each option should show the conversation title and a brief status indicator:

```
self_id: <session_id from Phase 1>
question: "Which conversation should continue this work?"
options: <array of conversation titles from Phase 2, formatted as "Title (status)">
timeout_seconds: 60
```

For each conversation, format the option as:
- `"<title> (running)"` if `is_running` is true
- `"<title> (idle)"` if `is_running` is false
- `"<title>"` if the title is descriptive enough

**If the user responds**, use their selected conversation.

**If the request times out** (no response within 60 seconds):
- Abort — do not send to a conversation without explicit user selection
- Inform the user: "No selection was made. Please run this prompt again and select a target conversation."

## Phase 5: Send Handoff Prompt

**Send the handoff prompt** to the selected conversation using `mitto_conversation_send_prompt`:

```
self_id: <session_id from Phase 1>
conversation_id: <selected conversation's session_id>
prompt: <comprehensive handoff prompt from Phase 3>
```

The prompt sent to the target conversation should be structured as:

```
## Handoff from: <current conversation title>

### Context
<Summary of what was discussed and decided>

### Current State
<What has been completed so far>

### Your Task
<Clear description of what needs to be done next>

### Instructions
<Step-by-step guidance>

### Files to Work On
<Specific file paths and what to do with them>

### Success Criteria
<How to know when done>

### Constraints
<Any limitations or requirements>
```

## Phase 6: Report Results

After the prompt is sent, provide a clear summary:

```markdown
Work Handed Off Successfully

**Sent To:**
- **Conversation**: <conversation title>
- **Conversation ID**: <id>

**What Was Sent:**
<Brief summary of the handoff prompt>

**What's Next:**
The target conversation will:
1. <First step from the handoff prompt>
2. <Second step>
3. ...

**In This Conversation:**
You can:
- Monitor progress by switching to the target conversation
- Continue planning or reviewing here
- Send additional instructions if needed

**To Switch:**
Use the Conversations panel to navigate to "<conversation title>"
```

</instructions>

<rules>
- Only show conversations in the same workspace
- Exclude the current conversation from options
- Get explicit user selection before sending — do not send without confirmation
- Create comprehensive handoff prompts — the target conversation needs full context
- Include all necessary context — do not assume the target conversation knows what we discussed
- Be specific about next steps — provide clear, actionable instructions
- Clarify unclear tasks in this conversation first before handing off
- Include file paths and specifics — the target conversation needs concrete details to work with
- Handle empty results gracefully — if no sibling conversations exist, suggest alternatives
</rules>
