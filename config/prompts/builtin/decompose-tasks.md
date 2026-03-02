---
name: "Decompose"
description: "Break down the current problem into parallel, independent tasks"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

Decompose the current problem into multiple, parallelizable tasks that can be executed independently in separate conversations.

## Prerequisites: Check for Mitto MCP Server

**IMPORTANT**: This prompt requires Mitto's MCP server tools to function. Before proceeding, verify that the required tools are available.

**Required tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_get_summary`
- `mitto_conversation_new`
- `mitto_ui_options_combo`

**Check availability:**
1. Look for these tools in your available tools list
2. If ANY of these tools are missing, **STOP immediately** and inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
❌ This prompt requires Mitto's MCP server, which is not currently available. To use this prompt, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, ABORT the prompt execution. Do not proceed with any of the sections below.**

---

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

## Phase 2: Decompose the Problem

Analyze the current work and break it down into **2-4 independent tasks** that:

- **Are well-defined and clear**: Each task has a specific, achievable goal
- **Are parallelizable**: Tasks can be worked on simultaneously without conflicts
- **Don't overlap**: Each task works on different parts of the codebase
- **Are self-contained**: Each task has all the context it needs to succeed
- **Have clear success criteria**: It's obvious when the task is complete

For each task, define:
- **Title**: Short, descriptive name (e.g., "Update API endpoints", "Add unit tests")
- **Description**: Detailed instructions including:
  - What needs to be done
  - Which files/modules to work on
  - Expected outcome
  - Any constraints or requirements
- **Files/modules**: Specific parts of the codebase this task will modify

**Present the decomposition plan to the user** in a clear table format:

| # | Task Title | Description | Files/Modules | Estimated Complexity |
|---|------------|-------------|---------------|---------------------|
| 1 | ... | ... | ... | Low/Medium/High |
| 2 | ... | ... | ... | Low/Medium/High |
| ... | ... | ... | ... | ... |

## Phase 3: Select ACP Server

**Ask the user which ACP server to use** for the sub-tasks using `mitto_ui_options_combo`:

```
self_id: <session_id from Phase 1>
question: "Which AI agent would you like to use for the parallel tasks?"
options: <array of available_acp_servers from Phase 1>
timeout_seconds: 60
```

**If the user responds**, use their selected ACP server.

**If the request times out** (no response within 60 seconds):
- Make a best-effort guess based on the task characteristics:
  - For **implementation/coding tasks**: Prefer faster models (e.g., "claude-code" if available)
  - For **complex reasoning/planning**: Prefer more capable models (e.g., "auggie" if available)
  - For **mixed tasks**: Use the first available ACP server from the list
- Proceed with the selected server
- Mention in the final report which server was auto-selected due to timeout

## Phase 4: Create Parallel Conversations

For each task (maximum 4 tasks):

1. **Create a new conversation** using `mitto_conversation_new`:
   ```
   self_id: <session_id from Phase 1>
   title: <task title>
   initial_prompt: <detailed task description with full context>
   acp_server: <selected ACP server from Phase 3>
   ```

2. **Track the conversation ID** returned by the tool

**Important**: Create conversations sequentially (one at a time), not all at once, to avoid overwhelming the system.

## Phase 5: Report Results

After all conversations are created, provide a summary:

```markdown
✅ Task Decomposition Complete

Created <N> parallel tasks using <ACP server><if auto-selected: " (auto-selected due to timeout)">:

1. **<Task 1 Title>** (Conversation ID: <id>)
   - Working on: <files/modules>
   - Goal: <brief description>

2. **<Task 2 Title>** (Conversation ID: <id>)
   - Working on: <files/modules>
   - Goal: <brief description>

...

💡 **Next Steps:**
- Monitor progress in each conversation
- Tasks will work independently and in parallel
- Review and integrate results when tasks complete
- Use the Conversations panel to switch between tasks

⚠️ **Important:**
- Each task works on different files to avoid conflicts
- Review each task's changes before merging
- Coordinate integration if tasks have dependencies
```

## Rules

- **Never create more than 4 parallel tasks** - This prevents overwhelming the system
- **Ensure tasks are truly independent** - No shared file modifications
- **Provide complete context in each task** - Each conversation should be self-sufficient
- **Use clear, specific titles** - Make it easy to identify tasks in the UI
- **Always ask for confirmation** - Don't start tasks without user approval
- **Handle errors gracefully** - If conversation creation fails, report it clearly
- **Don't decompose trivial problems** - If the work is simple, suggest doing it in the current conversation instead
