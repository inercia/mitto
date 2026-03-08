---
name: "Create minions"
description: "Break down a complex problem into parallel tasks, coordinate workers, and iterate until solved"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

<task>
Decompose the current problem into parallel subtasks, dispatch them to child conversations,
collect results, and iterate — refining the approach, spawning follow-up work, or
re-decomposing as needed until the overall problem is solved.
</task>

## Prerequisites: Check for Mitto MCP Server

This prompt requires Mitto's MCP server tools to function. Before proceeding,
verify that the required tools are available.

**Required tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_get_summary`
- `mitto_conversation_new`
- `mitto_conversation_delete`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_ui_options_combo`

**Check availability:**
1. Look for these tools in your available tools list
2. If any of these tools are missing, **stop** and inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
This prompt requires Mitto's MCP server, which is not currently available. To use this prompt, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, abort the prompt execution. Do not proceed with any of the sections below.**

---

<instructions>

## Overview: The Iterative Workflow

This prompt implements a **coordinator pattern**: you (the parent) act as an orchestrator that breaks work into pieces, delegates to workers, reviews their output, and decides what to do next. Complex problems rarely resolve in a single pass — expect to iterate.

```
Analyze Context
     |
     v
Decompose Problem  <------------------------------+
     |                                             |
     v                                             |
Select ACP Server                                  |
     |                                             |
     v                                             |
Dispatch to Children                               |
     |                                             |
     v                                             |
Wait for Results                                   |
     |                                             |
     v                                             |
Synthesize & Evaluate ----> Problem solved? --Yes--> Done
     |
     No / Partially
     |
     v
Plan Next Iteration -------> Loop back ------------+
```

Throughout this workflow, **maintain a mental ledger** of:
- The overall goal and what "done" looks like
- Which subtasks have been completed and their outcomes
- What remains open, what was discovered, what changed
- How many iteration rounds have been executed (max 3 before escalating)

---

## Phase 1: Analyze Current Context

First, gather information about the current conversation and available resources:

1. **Get current conversation details** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```
   This will give you the session_id and other metadata.

2. **List available ACP servers** - The response from step 1 includes `available_acp_servers` field showing all configured ACP servers. Each server entry includes a `name`, an optional `type`, optional `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`), and a `current` flag indicating whether it's the ACP server used by this conversation. **Pay attention to the tags** — they describe each server's strengths and will be used for automatic selection if the user doesn't respond in Phase 3.

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
  - Context from previous iterations if applicable (see Phase 7)
- **Files/modules**: Specific parts of the codebase this task will modify

**Present the decomposition plan to the user** in a clear table format:

| # | Task Title | Description | Files/Modules | Estimated Complexity |
|---|------------|-------------|---------------|---------------------|
| 1 | ... | ... | ... | Low/Medium/High |
| 2 | ... | ... | ... | Low/Medium/High |
| ... | ... | ... | ... | ... |

If this is a **subsequent iteration** (not the first pass), also show:
- What was learned from the previous round
- Which tasks are new vs. refined from earlier attempts
- How the new tasks connect to previously completed work

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
- Use the `tags` from the `available_acp_servers` list (obtained in Phase 1 via `mitto_conversation_get_current`) to determine the most appropriate server for the tasks:
  - **Match task characteristics to server tags**: For example, if the tasks are implementation/coding work, prefer a server tagged with `"coding"` or `"fast"`; if the tasks require complex reasoning or planning, prefer a server tagged with `"reasoning"` or `"planning"`.
  - **If multiple servers match**: Prefer the one whose tags are the closest match to the task requirements.
  - **If no tags are available or no tags match**: Fall back to using the same ACP server as the current conversation (the one with `current: true` in the `available_acp_servers` list).
  - **If the current server cannot be determined**: Use the first available ACP server from the list.
- Proceed with the selected server
- Mention in the final report which server was auto-selected due to timeout and which tags influenced the decision

**Note**: On subsequent iterations, reuse the previously selected server unless the nature of the work has changed significantly. Don't re-ask unless there's a reason to switch.

## Phase 4: Dispatch to Children

For each task (maximum 4 tasks):

1. **Build the initial prompt** for the child conversation. The prompt must include:
   - The full task description with all necessary context
   - Context from previous iterations if this is a follow-up task (what was tried before, what was learned, what to do differently)
   - A **mandatory reporting directive** at the end:

   ```
   When you complete this task, report your results back to the parent conversation
   using the `mitto_children_tasks_report` MCP tool:

   mitto_children_tasks_report(
     self_id: <your session ID - get it via mitto_conversation_get_current>,
     status: "completed" or "failed" or "partial",
     summary: "<brief description of what was accomplished>",
     details: "<detailed information: files modified, errors encountered, discoveries, open questions, suggestions for follow-up>"
   )

   Call mitto_conversation_get_current first to get your self_id, then call
   mitto_children_tasks_report with your results. Do this as your final action.
   ```

2. **Create a new conversation** using `mitto_conversation_new`:
   ```
   self_id: <session_id from Phase 1>
   title: <task title>
   initial_prompt: <task description + reporting directive from above>
   acp_server: <selected ACP server from Phase 3>
   ```

3. **Track the conversation ID** returned by the tool

Create conversations sequentially (one at a time), not all at once, to avoid overwhelming the system.

## Phase 5: Wait for Results

After all child conversations are created, **wait for them to complete** using `mitto_children_tasks_wait`:

```
self_id: <session_id from Phase 1>
children_list: [<child_id_1>, <child_id_2>, ...]
timeout_seconds: 600
```

This tool will:
- Send the prompt to each child that hasn't already reported
- Block until all children report back or the timeout expires (default: 10 minutes)
- Return consolidated results from all children

**While waiting**, inform the user:

```
Waiting for child tasks to complete (iteration <N>)...
Children can report at any time. This will block for up to 10 minutes.
You can monitor individual conversations in the Conversations panel.
```

### Handle timeout

If `mitto_children_tasks_wait` returns with `timed_out: true`:
- Check which children *did* report (look for `completed: true` entries in `reports`)
- For children still pending, call `mitto_children_tasks_wait` again with only those children **and without a prompt** (omit the `prompt` field or pass an empty string). This avoids re-enqueuing duplicate report-request messages — the tool will just wait for the children to report:

  ```
  self_id: <session_id from Phase 1>
  children_list: [<pending_child_ids>]
  timeout_seconds: 600
  ```

- If a child times out twice, treat it as a failure and proceed to Phase 6

## Phase 6: Synthesize and Evaluate

This is the critical phase. You are not just checking pass/fail — you are **synthesizing results across all children** to understand the state of the overall problem.

### Step 1: Collect and organize results

For each child in the response `reports` map, categorize:
- **Succeeded**: `completed: true` and report `status` is `"completed"` with no errors
- **Partially completed**: `completed: true` but report `status` is `"partial"` — work was done but the task isn't fully resolved
- **Failed**: `completed: true` but report `status` is `"failed"` — the child ran into errors it couldn't overcome
- **Timed out**: `status: "pending"` — child did not report before timeout
- **Not running**: `status: "not_running"` — child conversation was closed/archived

### Step 2: Synthesize findings

Look **across** the reports, not just at each one in isolation:

- **Merge discoveries**: Did children uncover new information? Dependencies between subsystems? Unexpected complexity? Edge cases?
- **Check consistency**: Do the children's outputs fit together? Are there conflicts (e.g., two children modified overlapping code despite the plan saying they wouldn't)?
- **Identify gaps**: Are there aspects of the original problem that no child addressed? Things that fell between the cracks?
- **Collect open questions**: Gather unresolved questions from children — these may inform the next iteration.
- **Evaluate suggestions**: Children may suggest follow-up work. Assess whether those suggestions are actionable.

### Step 3: Evaluate overall progress

Ask yourself:

1. **Is the overall problem solved?** — All subtasks succeeded and together they address the original goal completely.
2. **Is significant progress made but more work needed?** — Some subtasks are done, but new subtasks have emerged from what was learned.
3. **Has the problem turned out to be different than expected?** — The decomposition was wrong; the problem needs to be re-analyzed and broken down differently.
4. **Are we blocked?** — Critical failures or ambiguities that require user input.

### Step 4: Present status to the user

<output_format>

```markdown
## Iteration <N> Results

### Completed
- **<Task 1>**: <summary> | Files: <files_modified>
- **<Task 2>**: <summary> | Files: <files_modified>

### Partially Completed
- **<Task 3>**: <summary> | Remaining: <what's left>

### Failed
- **<Task 4>**: <error summary>

### Discoveries
- <Merged discoveries from all children>

### Open Questions
- <Gathered from children's reports>

### Overall Assessment
<Your synthesis: is the problem solved, partially solved, or does it need re-decomposition?>
```

</output_format>

### Step 5: Clean up finished children

After reviewing results, delete child conversations that are no longer needed using `mitto_conversation_delete`:

```
mitto_conversation_delete(
  self_id: <your session_id>,
  conversation_id: <child_id>
)
```

**Delete when:**
- A child completed successfully and its results have been reviewed and incorporated
- A child failed and you're going to create a replacement with refined instructions
- A child timed out or is no longer running and won't be retried

**Keep when:**
- A child's work is still being analyzed or may need follow-up prompting
- You plan to include the child in a subsequent `mitto_children_tasks_wait` call

This keeps the conversation list clean and signals that you're done with those workers.

**If the problem is solved**: proceed to the Completion section at the end.

**If more work is needed**: proceed to Phase 7.

## Phase 7: Plan Next Iteration

When the problem isn't fully solved after a round, plan the next iteration. This is where the orchestrator adds real value — you don't just blindly retry; you incorporate what was learned.

### Decide the iteration strategy

Choose the approach based on your Phase 6 assessment:

**A. Follow-up tasks** — The previous round succeeded but revealed additional work:
- Children's reports may include `suggestions` or `open_questions` that point to new subtasks
- Results from one child may require integration work or changes in areas another child touched
- New requirements may have surfaced (e.g., "the API endpoint works, but we also need migration scripts")
- Create new tasks that build on previous results and include the relevant context

**B. Refined retry** — Some tasks failed and need to be attempted again with better instructions:
- Include the specific error details from the previous attempt
- Provide guidance on what to do differently (different approach, avoid the problematic path, etc.)
- If a task failed twice, consider breaking it into smaller pieces instead of retrying as-is

**C. Re-decomposition** — The original breakdown was wrong:
- The problem is more interconnected than initially thought
- A different partitioning of work is needed
- Perhaps fewer, larger tasks or more, smaller tasks would work better
- Go back to Phase 2 with the new understanding

**D. Escalate** — You need user input:
- Ambiguities that can't be resolved without domain knowledge
- Conflicting requirements discovered by different children
- Architectural decisions that the user should make

### Ask the user how to proceed

If the next step isn't obvious, use `mitto_ui_options_combo`:

```
self_id: <session_id from Phase 1>
question: "Iteration <N> complete. <Brief status>. How would you like to proceed?"
options: [
  "Continue with follow-up tasks (recommended: <brief description>)",
  "Retry failed tasks with refined instructions",
  "Re-decompose the problem with new understanding",
  "Let me review the details first",
  "Stop here - I'll take it from here"
]
timeout_seconds: 120
```

If the user selects "Let me review the details first", present the full report data from all children and wait for further instructions.

If the request times out, proceed with the recommended option if one was clearly best, otherwise present the full report and wait.

### Execute the next iteration

1. **Build new task list** incorporating lessons learned:
   - For follow-up tasks: reference the completed work and build on it
   - For retries: include error context and what to do differently
   - For re-decomposition: present a new task table to the user (back to Phase 2)

2. **Provide accumulated context** to new children. Each new child's prompt should include:
   - Its specific task
   - A brief summary of what previous iterations accomplished (so it doesn't redo finished work)
   - Relevant findings from previous children (e.g., "Child A found that the database schema needs migration — account for this")
   - What to avoid (known dead-ends from previous attempts)

3. **Dispatch and wait** (Phases 4-5 again, with the new tasks)

4. **Synthesize again** (Phase 6) and repeat if necessary

### Iteration limits

- **Maximum 3 full iterations** (initial + 2 follow-ups) before requiring explicit user confirmation to continue
- After 3 iterations, present a comprehensive summary of all work done and ask the user whether to continue, change approach, or stop
- Each individual subtask should not be retried more than twice — if it fails twice, either break it down further or escalate

---

## Completion

When the overall problem is solved (all necessary work is done across however many iterations it took):

1. **Delete all remaining child conversations** that contributed to the solution:
   ```
   mitto_conversation_delete(self_id, child_id)  // for each child
   ```

2. **Present the final summary**:

```markdown
## Problem Solved

### Summary
<High-level description of what was accomplished>

### Work completed across <N> iteration(s):

**Iteration 1:**
1. **<Task>**: <outcome>
2. **<Task>**: <outcome>

**Iteration 2:** (if applicable)
1. **<Task>**: <outcome>
...

### All files modified:
<Merged list of all files_modified across all children, all iterations>

### Discoveries and notes:
<Important findings that emerged during the process>

### Cleanup:
Deleted <N> child conversation(s).

### Recommended next steps:
- Review the changes made across all tasks
- Run tests to verify integration
- <Any specific follow-up the user should be aware of>
```

</instructions>

<rules>
- Limit to 4 parallel tasks per iteration, to prevent overwhelming the system
- Ensure tasks within an iteration are truly independent — no shared file modifications within the same batch
- Provide complete context in each task, because each conversation should be self-sufficient
- Include the reporting directive in every child prompt, so results flow back to the coordinator
- Use clear, specific titles to make it easy to identify tasks in the UI
- Get user confirmation before the first iteration
- For subsequent iterations, proceed automatically if the next step is clear, but show the user what you're doing
- If the work is simple enough, suggest doing it in the current conversation instead of decomposing
- Maximum 3 iterations before escalating — present a summary and ask the user before continuing
- Retry each subtask at most twice — after two failures, break it down further or escalate
- Accumulate context across iterations — new children should know what previous rounds accomplished and discovered
- Act as orchestrator, not worker — your job is to decompose, coordinate, synthesize, and decide
- Clean up child conversations after reviewing their results to keep the conversation list tidy
</rules>
