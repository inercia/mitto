---
name: "Simplify"
description: "Simplify implementation while preserving functionality"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Before simplifying, read the current implementation thoroughly. Understand what
the code does and why it's structured this way. Check for callers and dependents
to ensure changes preserve external behavior.
</investigate_before_answering>

<task>
Simplify the current implementation while preserving functionality.
</task>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: This prompt can work without Mitto's MCP server, but provides a better user experience with it.

**Optional tools:**
- `mitto_conversation_get_current`
- `mitto_conversation_new`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_conversation_delete`

**Check availability:**
1. Look for these tools in your available tools list
2. If ANY of these tools are missing, inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
💡 This prompt works better with Mitto's MCP server for parallel task delegation. To enable this feature, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, proceed with the sections below without delegation support.**

---

<instructions>

### 1. Look for Simplification Opportunities

1. **Redundant code**: Duplicate logic that can be consolidated
2. **Over-engineering**: Abstractions that add complexity without value
3. **Deep nesting**: Flatten conditionals using early returns or guard clauses
4. **Long functions**: Break into smaller, focused functions
5. **Complex conditionals**: Simplify boolean logic, use lookup tables
6. **Unnecessary state**: Remove variables that can be computed on demand

### 2. For each change:

- Explain what you're simplifying and why
- Show before/after comparison
- Verify behavior is preserved by running tests

### 3. Execute Approved Simplifications

For each approved item:
1. Make the simplification
2. Verify nothing breaks (run tests)
3. Report the result

#### Delegating Significant Simplifications to Child Conversations

When a simplification requires **significant work** — such as consolidating duplicate logic
spread across many files, removing deep layers of over-engineering, restructuring a complex
module into simpler components, or breaking apart a large tangled function — consider delegating
it to a new Mitto child conversation. This keeps the main simplification workflow focused on
coordination and allows heavy restructuring to happen in parallel.

**When to delegate (any of the following):**
- The simplification spans 3+ files or requires understanding a wide dependency graph
- The simplification involves removing an over-engineered abstraction layer and rewiring callers
- Multiple independent simplifications can be parallelized
- The simplification requires breaking a large, complex function or module into simpler pieces

**Choosing the right ACP server (requires Mitto MCP tools):**

1. **Get your current conversation ID** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```

2. **Select the best-suited ACP server** from the `available_acp_servers` returned in step 1.
   Each server entry includes optional `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`)
   that describe its strengths. Match the server to the nature of the work:

   - **Well-defined, mechanical simplifications** (consolidating obvious duplicates, flattening
     straightforward nested conditionals, inlining trivial wrapper functions across files):
     Prefer servers tagged with `"coding"` and/or `"fast"` — these are fast-thinking agents
     optimized for implementation tasks with clear scope.
   - **Complex or judgment-heavy simplifications** (deciding which abstraction layers to remove
     and how to rewire dependents, choosing how to decompose a tangled function when multiple
     designs are possible, simplifying code where the "simpler" version isn't obvious): Prefer
     servers tagged with `"reasoning"` and/or `"planning"` — these are more sophisticated agents
     better suited for tasks requiring deeper analysis and design judgment.
   - **If no tags match** or no tags are available: Fall back to the current conversation's ACP
     server (the one with `current: true`).
   - **If the current server cannot be determined**: Use the first available ACP server.

3. **Create a new conversation** for each significant simplification using `mitto_conversation_new`:
   ```
   self_id: <your session_id>
   title: "Simplify: <brief description of the simplification>"
   initial_prompt: |
     You are simplifying code while preserving functionality. Here is the context:

     **Repository**: <repo info>
     **Branch**: <current branch>

     **Simplification task**:
     <detailed description of the complexity issue and the proposed simplification>

     **Files involved**:
     <list of files to examine and/or modify>

     **What needs to be done**:
     <specific instructions: what to simplify, the target design, and why it's simpler>

     **Constraints**:
     - Preserve external behavior — simplify structure, not functionality
     - Only modify files related to this specific simplification
     - Run tests after making changes to verify behavior is preserved
     - Follow the project's existing code style and conventions
     - Explain what you're simplifying and why in commit messages

     When you complete this task, report your results back to the parent conversation
     using the `mitto_children_tasks_report` MCP tool:

     mitto_children_tasks_report(
       self_id: <your session ID - get it via mitto_conversation_get_current>,
       status: "completed" or "failed" or "partial",
       summary: "<brief description of what was accomplished>",
       details: "<detailed info: files modified, simplifications made, tests run, any issues encountered>"
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
   - Verify that external behavior is preserved and tests still pass
   - Check that the code is genuinely simpler, not just different
   - If a child failed or partially completed, either retry with refined instructions or handle
     the remaining work in the current conversation

6. **Clean up** child conversations after incorporating their work:
   ```
   mitto_conversation_delete(self_id: <your session_id>, conversation_id: <child_id>)
   ```

**If Mitto tools are not available**, execute all simplifications directly in the current
conversation as described in the steps above (simplify, test, report).

</instructions>

<rules>
- Preserve external behavior — simplify structure, not functionality
- Explain what you're simplifying and why, so the user understands the benefit
- Verify behavior is preserved by running tests after each change
- Show before/after comparisons so improvements are clear
- For significant simplifications, consider delegating to child conversations to parallelize the work
- When delegating, match the ACP server to the task: use fast-thinking, coding agents for mechanical simplifications; use reasoning/planning agents for complex restructurings where the simpler design isn't obvious
- Limit parallel child conversations to 4 at a time to avoid overwhelming the system
</rules>
