---
name: "Create minions"
description: "Break down a complex problem into parallel tasks, coordinate workers, and iterate until solved"
group: "Work flow"
backgroundColor: "#FFF9C4"
---

<task>
Decompose the current problem into parallel subtasks, dispatch to child conversations,
collect results, and iterate until solved.
</task>

## Prerequisites: Check for Mitto MCP Server

This prompt requires Mitto's MCP server tools.

**Required tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_get_summary`
- `mitto_conversation_new`
- `mitto_conversation_delete`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_ui_options_combo`

If any are missing, **stop** and show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp. Do not proceed.

---

<instructions>

## Overview

Coordinator pattern: decompose → delegate → synthesize → iterate.

```
Analyze Context → Decompose → Select ACP Server → Dispatch → Wait → Synthesize
    ↑                                                              |
    +—— Plan Next Iteration ←——— Not solved? ←————————————————————+
```

Track throughout: overall goal, completed subtasks and outcomes, open items, iteration count (max 3).

---

## Phase 1: Analyze Context

1. `mitto_conversation_get_current(self_id: "init")` → get session_id, `available_acp_servers`
2. Note each server's `name`, `tags` (e.g., `["coding", "fast"]`), and `current` flag
3. `mitto_conversation_get_summary(self_id: <id>, conversation_id: <id>)` → current work context

## Phase 2: Decompose the Problem

Break into **2-4 independent tasks** that are well-defined, parallelizable, non-overlapping, self-contained, with clear success criteria.

Per task define: **Title**, **Description** (what, which files, expected outcome, constraints, prior iteration context), **Files/modules**.

Present as table:

| # | Task Title | Description | Files/Modules | Complexity |
|---|------------|-------------|---------------|------------|

For subsequent iterations, also show: learnings from previous round, new vs. refined tasks, connections to completed work.

## Phase 3: Select ACP Server

Ask via `mitto_ui_options_combo` (timeout: 60s):
```
question: "Which AI agent would you like to use for the parallel tasks?"
options: <available_acp_servers>
```

**On timeout**, auto-select by matching task characteristics to server tags:
- Implementation/coding → prefer `"coding"` or `"fast"` tags
- Complex reasoning/planning → prefer `"reasoning"` or `"planning"` tags
- No match → use current server (`current: true`), then first available

On subsequent iterations, reuse previous selection unless work nature changed significantly.

## Phase 4: Dispatch to Children

For each task (max 4), create sequentially:

1. Build prompt with full task description + **mandatory reporting directive**:
   ```
   When complete, report via mitto_children_tasks_report:
     self_id: <get via mitto_conversation_get_current>
     status: "completed" | "failed" | "partial"
     summary: "<what was accomplished>"
     details: "<files modified, errors, discoveries, open questions>"
   Do this as your final action.
   ```

2. `mitto_conversation_new(self_id, title, initial_prompt, acp_server)`
3. Track returned conversation ID

## Phase 5: Wait for Results

```
mitto_children_tasks_wait(self_id, children_list: [...], timeout_seconds: 600)
```

Inform user: "Waiting for child tasks (iteration N)... Monitor in Conversations panel."

**On timeout**: retry pending children with `mitto_children_tasks_wait` (omit prompt to avoid duplicates). After two timeouts, treat as failure.

## Phase 6: Synthesize and Evaluate

### 1. Categorize results
- **Succeeded**: `completed: true`, status `"completed"`
- **Partial**: `completed: true`, status `"partial"`
- **Failed**: `completed: true`, status `"failed"`
- **Timed out**: `status: "pending"`
- **Not running**: `status: "not_running"`

### 2. Cross-report synthesis
- Merge discoveries and new information
- Check consistency (conflicts, overlapping changes?)
- Identify gaps no child addressed
- Collect open questions
- Evaluate follow-up suggestions

### 3. Assess progress
1. Problem fully solved?
2. Progress made, more work needed?
3. Problem different than expected (re-decompose)?
4. Blocked (need user input)?

### 4. Present status

<output_format>

```markdown
## Iteration <N> Results

### Completed
- **<Task>**: <summary> | Files: <modified>

### Partially Completed
- **<Task>**: <summary> | Remaining: <what's left>

### Failed
- **<Task>**: <error>

### Discoveries / Open Questions
- ...

### Overall Assessment
<synthesis>
```

</output_format>

### 5. Clean up finished children

`mitto_conversation_delete(self_id, conversation_id)` for completed/failed/timed-out children no longer needed. Keep children still being analyzed or pending follow-up.

**If solved** → go to Completion. **If not** → Phase 7.

## Phase 7: Plan Next Iteration

Choose strategy based on Phase 6 assessment:

- **A. Follow-up tasks**: Previous round succeeded but revealed additional work
- **B. Refined retry**: Failed tasks need better instructions; after 2 failures, break down further
- **C. Re-decomposition**: Original breakdown was wrong; return to Phase 2
- **D. Escalate**: Need user input for ambiguities or conflicting requirements

If next step isn't obvious, ask via `mitto_ui_options_combo` (timeout: 120s):
```
options: ["Continue with follow-up tasks", "Retry failed tasks", "Re-decompose", "Let me review", "Stop here"]
```

For new children, include: specific task, summary of prior iterations, relevant findings, known dead-ends to avoid.

Then dispatch (Phase 4) → wait (Phase 5) → synthesize (Phase 6) → repeat if needed.

**Limits**: Max 3 iterations before requiring user confirmation. Max 2 retries per subtask.

---

## Completion

1. Delete all remaining child conversations
2. Present final summary:

```markdown
## Problem Solved

### Summary
<what was accomplished>

### Work completed across <N> iteration(s):
**Iteration 1:** ...
**Iteration 2:** ...

### All files modified:
<merged list>

### Discoveries and notes:
<findings>

### Recommended next steps:
- Review changes
- Run tests
- <specific follow-ups>
```

</instructions>

<rules>
- Max 4 parallel tasks per iteration
- Tasks must be truly independent — no shared file modifications
- Each child prompt must be self-contained with full context
- Include reporting directive in every child prompt
- Get user confirmation before first iteration
- Subsequent iterations: proceed automatically if clear, show the user what you're doing
- If work is simple enough, suggest doing it in current conversation
- Max 3 iterations before escalating
- Max 2 retries per subtask before breaking down further
- Accumulate context across iterations — new children should know prior results
- Act as orchestrator, not worker
- Clean up child conversations after reviewing results
</rules>
