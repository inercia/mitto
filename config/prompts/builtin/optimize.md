---
name: "Optimize"
description: "Identify and propose performance improvements"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Before proposing optimizations, read the relevant code paths thoroughly. Profile and
identify actual bottlenecks based on the code structure — do not guess. When analyzing
multiple files, read them in parallel to build context faster.
</investigate_before_answering>

<task>
Analyze the code for performance issues and propose a prioritized list of optimizations.

Propose a plan first and wait for approval before making any changes.
</task>

<scope>
Focus on measurable performance improvements. Do not refactor code for style or
readability unless it directly contributes to the performance gain. Each optimization
should have a clear, quantifiable benefit.
</scope>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: This prompt can work without Mitto's MCP server, but provides a better user experience with it.

**Optional tools:**
- `mitto_ui_options_buttons`
- `mitto_conversation_get_current`
- `mitto_conversation_new`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_conversation_delete`

**Check availability:**
1. Look for these tools in your available tools list
2. If ANY of these tools are missing, inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
💡 This prompt works better with Mitto's MCP server for interactive prompts. To enable interactive UI features, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, proceed with the sections below using text-based conversation instead.**

---

<instructions>

### 1. Analyze Performance

**Profile first** — Identify actual bottlenecks based on code analysis:
- Review code for common performance anti-patterns
- Look for hot paths and frequently executed code
- Identify I/O operations, loops, and data processing

**Categories to investigate:**

| Category | What to Look For |
|----------|------------------|
| Algorithmic | Inefficient algorithms, O(n²) where O(n log n) is possible |
| Memory | Excessive allocations, unnecessary copies, memory leaks |
| I/O | Unbuffered operations, synchronous blocking, N+1 queries |
| Caching | Repeated expensive computations, missing memoization |
| Concurrency | Sequential work that could be parallelized, lock contention |

### 2. Propose Optimization Plan

<output_format>

| Priority | Category | Location | Issue | Proposed Fix | Impact | Effort | Tradeoffs |
|----------|----------|----------|-------|--------------|--------|--------|-----------|
| 1 | Algorithmic | `path/to/file:fn()` | O(n²) loop | Use hash map lookup | High | Medium | More memory |
| 2 | I/O | `path/to/file:fn()` | Unbuffered writes | Add buffering | Medium | Small | None |
| 3 | Caching | `path/to/file:fn()` | Repeated computation | Memoize result | Medium | Small | Memory usage |

**Priority levels:**
- **1 (High)**: Clear bottleneck with significant impact
- **2 (Medium)**: Noticeable improvement expected
- **3 (Low)**: Minor improvement, nice-to-have

**Impact levels:**
- **High**: Major performance improvement expected
- **Medium**: Moderate improvement expected
- **Low**: Minor improvement expected

</output_format>

### 3. Wait for Approval

**Using Mitto UI tools (if available):**

If the `mitto_ui_options_buttons` tool is available, use it to present the approval options:

```
Question: "How would you like to proceed with the optimization plan?"
Options: ["Approve all", "Approve selected", "Investigate", "Cancel"]
```

If the user selects "Approve selected" or "Investigate", follow up with a text conversation to get the specific item numbers or benchmark requests.

**Fallback (if Mitto UI tools are not available):**

Ask the user in the conversation to choose one of these options:

- **Approve all** - proceed with all optimizations
- **Approve selected** - specify which items to proceed with (by priority number)
- **Investigate** - get more details or benchmarks on specific items
- **Cancel** - abort without making changes

Wait for the user to explicitly approve before proceeding.

### 4. Execute Approved Optimizations

For each approved item:
1. Implement the optimization
2. Verify correctness (run tests)
3. Measure improvement if possible
4. Report the result

#### Delegating Significant Optimizations to Child Conversations

When an approved optimization requires **significant work** — such as rewriting a core algorithm,
restructuring data pipelines, adding concurrency to sequential code, or changes spanning multiple
modules — consider delegating it to a new Mitto child conversation. This keeps the main
optimization workflow focused on coordination and allows heavy implementation to happen in parallel.

**When to delegate (any of the following):**
- The optimization spans 3+ files or touches a critical code path with many callers
- The optimization involves rewriting an algorithm or data structure
- Multiple independent optimizations can be parallelized
- The optimization requires adding concurrency primitives or significant architectural changes

**Choosing the right ACP server (requires Mitto MCP tools):**

1. **Get your current conversation ID** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```

2. **Select the best-suited ACP server** from the `available_acp_servers` returned in step 1.
   Each server entry includes optional `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`)
   that describe its strengths. Match the server to the nature of the work:

   - **Well-defined, straightforward optimizations** (adding buffering, replacing a known
     inefficient pattern, adding memoization with clear cache keys): Prefer servers tagged with
     `"coding"` and/or `"fast"` — these are fast-thinking agents optimized for implementation
     tasks with clear scope.
   - **Complex or exploratory optimizations** (redesigning data flow for concurrency, choosing
     between algorithmic approaches with unclear tradeoffs, optimizations requiring profiling and
     iterative measurement): Prefer servers tagged with `"reasoning"` and/or `"planning"` — these
     are more sophisticated agents better suited for tasks requiring deeper analysis and judgment.
   - **If no tags match** or no tags are available: Fall back to the current conversation's ACP
     server (the one with `current: true`).
   - **If the current server cannot be determined**: Use the first available ACP server.

3. **Create a new conversation** for each significant optimization using `mitto_conversation_new`:
   ```
   self_id: <your session_id>
   title: "Optimize: <brief description of the optimization>"
   initial_prompt: |
     You are implementing a performance optimization. Here is the context:

     **Repository**: <repo info>
     **Branch**: <current branch>

     **Optimization task**:
     <detailed description of the performance issue and the proposed fix>

     **Files involved**:
     <list of files to examine and/or modify>

     **What needs to be done**:
     <specific instructions: what to change, the expected performance improvement, and any tradeoffs>

     **Constraints**:
     - Only modify files related to this specific optimization
     - Run tests after making changes to verify correctness is preserved
     - Follow the project's existing code style and conventions
     - Document any tradeoffs introduced (memory vs speed, complexity vs performance)
     - Measure improvement if possible (benchmarks, profiling)

     When you complete this task, report your results back to the parent conversation
     using the `mitto_children_tasks_report` MCP tool:

     mitto_children_tasks_report(
       self_id: <your session ID - get it via mitto_conversation_get_current>,
       status: "completed" or "failed" or "partial",
       summary: "<brief description of what was accomplished>",
       details: "<detailed info: files modified, optimization applied, performance measurements, tradeoffs, any issues encountered>"
     )

     Call mitto_conversation_get_current first to get your self_id, then call
     mitto_children_tasks_report with your results. Do this as your final action.
   acp_server: <selected ACP server based on task complexity>
   ```

4. **Wait for child conversations to complete** using `mitto_children_tasks_wait`:
   ```
   self_id: <your session_id>
   children_list: [<child_id_1>, <child_id_2>, ...]
   timeout_seconds: 600
   ```

5. **Review the results** from each child:
   - Verify that the reported changes are correct and tests still pass
   - Check that the optimization doesn't introduce unacceptable tradeoffs
   - If a child failed or partially completed, either retry with refined instructions or handle
     the remaining work in the current conversation

6. **Clean up** child conversations after incorporating their work:
   ```
   mitto_conversation_delete(self_id: <your session_id>, conversation_id: <child_id>)
   ```

**If Mitto tools are not available**, implement all optimizations directly in the current
conversation as described in the steps above (implement, verify, measure, report).

### 5. Report Summary

After completing approved changes:

```markdown
## Optimization Summary

### Changes Made
| Item | Change | Expected Impact | Verified |
|------|--------|-----------------|----------|
| #1 | Replaced O(n²) with hash map | High | ✅ Tests pass |
| #2 | Added I/O buffering | Medium | ✅ Tests pass |

### Skipped Items
- Item #3: Skipped per user request
```

</instructions>

<rules>
- Propose optimizations before implementing them, and wait for user approval
- Profile before optimizing — focus on actual bottlenecks identified through code analysis, not guesses
- Document tradeoffs for each optimization (memory vs speed, complexity vs performance), because the user needs to make informed decisions
- Verify correctness by running tests after each optimization
- Quantify improvements when possible, to validate the optimization was worthwhile
- For significant optimizations, consider delegating to child conversations to parallelize the work
- When delegating, match the ACP server to the task: use fast-thinking, coding agents for well-defined optimizations; use reasoning/planning agents for complex algorithmic redesigns or exploratory performance work
- Limit parallel child conversations to 4 at a time to avoid overwhelming the system
</rules>
