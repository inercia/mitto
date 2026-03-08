---
name: "Refactor"
description: "Propose refactoring improvements for better code quality"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Before proposing refactorings, read the code thoroughly to understand its current
structure, callers, and dependents. When analyzing multiple files, read them in parallel
to build context faster. Do not speculate about code you have not opened.
</investigate_before_answering>

<task>
Analyze the code and propose a prioritized list of refactoring improvements.

Propose a plan first and wait for approval before making any changes.
</task>

<scope>
Preserve external behavior — this is refactoring, not rewriting. Make one type of
change at a time so each change is easy to review and revert if needed. Do not add
new features or functionality as part of the refactoring.
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

### 1. Analyze the Code

Investigate the following areas:

| Area | What to Look For |
|------|------------------|
| Naming | Unclear or misleading names for variables, functions, types |
| Structure | Disorganized code, related functionality scattered across files |
| Single Responsibility | Functions/classes doing too many things |
| DRY | Repeated patterns that could be extracted |
| Error Handling | Inconsistent or uninformative error messages |
| Idioms | Code not following language-specific best practices |

### 2. Propose Refactoring Plan

<output_format>

| Priority | Category | Location | Issue | Proposed Change | Benefit | Effort |
|----------|----------|----------|-------|-----------------|---------|--------|
| 1 | Structure | `path/to/file` | Related functions scattered | Group into module | Better organization | Medium |
| 2 | DRY | `path/to/files` | Duplicated validation logic | Extract to helper | Less duplication | Small |
| 3 | Naming | `path/to/file:fn()` | Unclear function name | Rename to `descriptiveName()` | Clarity | Small |

**Priority levels:**
- **1 (High)**: Significantly improves maintainability or readability
- **2 (Medium)**: Noticeable improvement to code quality
- **3 (Low)**: Minor improvement, nice-to-have

**Effort levels:**
- **Small**: Quick change, low risk
- **Medium**: Moderate change, some risk
- **Large**: Significant change, higher risk

</output_format>

### 3. Wait for Approval

**Using Mitto UI tools (if available):**

If the `mitto_ui_options_buttons` tool is available, use it to present the approval options:

```
Question: "How would you like to proceed with the refactoring plan?"
Options: ["Approve all", "Approve selected", "Investigate", "Cancel"]
```

If the user selects "Approve selected" or "Investigate", follow up with a text conversation to get the specific item numbers.

**Fallback (if Mitto UI tools are not available):**

Ask the user in the conversation to choose one of these options:

- **Approve all** - proceed with all refactorings
- **Approve selected** - specify which items to proceed with (by priority number)
- **Investigate** - get more details on specific items before deciding
- **Cancel** - abort without making changes

Wait for the user to explicitly approve before proceeding.

### 4. Execute Approved Refactorings

For each approved item:
1. Make one type of change at a time
2. Run tests after each change to catch regressions early
3. Preserve external behavior
4. Report the result

#### Delegating Significant Refactorings to Child Conversations

When an approved refactoring requires **significant work** — such as restructuring a module,
extracting a subsystem, reorganizing code across many files, or untangling deeply coupled
components — consider delegating it to a new Mitto child conversation. This keeps the main
refactoring workflow focused on coordination and allows heavy restructuring to happen in parallel.

**When to delegate (any of the following):**
- The refactoring spans 3+ files or involves moving code between modules
- The refactoring requires extracting a new module, class, or subsystem
- Multiple independent refactorings can be parallelized
- The refactoring involves reorganizing a large file into smaller, focused units

**Choosing the right ACP server (requires Mitto MCP tools):**

1. **Get your current conversation ID** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```

2. **Select the best-suited ACP server** from the `available_acp_servers` returned in step 1.
   Each server entry includes optional `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`)
   that describe its strengths. Match the server to the nature of the work:

   - **Well-defined, mechanical refactorings** (renaming across files, extracting a clearly
     identified helper function, moving functions between existing modules, removing duplication
     with an obvious shared abstraction): Prefer servers tagged with `"coding"` and/or `"fast"`
     — these are fast-thinking agents optimized for implementation tasks with clear scope.
   - **Complex or judgment-heavy refactorings** (deciding how to decompose a tangled module,
     choosing the right abstraction boundaries, restructuring code where the target design isn't
     obvious, untangling circular dependencies): Prefer servers tagged with `"reasoning"` and/or
     `"planning"` — these are more sophisticated agents better suited for tasks requiring deeper
     analysis and architectural judgment.
   - **If no tags match** or no tags are available: Fall back to the current conversation's ACP
     server (the one with `current: true`).
   - **If the current server cannot be determined**: Use the first available ACP server.

3. **Create a new conversation** for each significant refactoring using `mitto_conversation_new`:
   ```
   self_id: <your session_id>
   title: "Refactor: <brief description of the refactoring>"
   initial_prompt: |
     You are performing a code refactoring. Here is the context:

     **Repository**: <repo info>
     **Branch**: <current branch>

     **Refactoring task**:
     <detailed description of the structural issue and the proposed improvement>

     **Files involved**:
     <list of files to examine and/or modify>

     **What needs to be done**:
     <specific instructions: what to restructure, the target design, and the expected benefit>

     **Constraints**:
     - Preserve external behavior — this is refactoring, not rewriting
     - Make one type of change at a time so each change is easy to review
     - Only modify files related to this specific refactoring
     - Run tests after making changes to catch regressions early
     - Follow the project's existing code style and conventions
     - Do not add new features or functionality as part of the refactoring

     When you complete this task, report your results back to the parent conversation
     using the `mitto_children_tasks_report` MCP tool:

     mitto_children_tasks_report(
       self_id: <your session ID - get it via mitto_conversation_get_current>,
       status: "completed" or "failed" or "partial",
       summary: "<brief description of what was accomplished>",
       details: "<detailed info: files modified, structural changes made, tests run, any issues encountered>"
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
   - Check that the structural improvement matches the intended design
   - If a child failed or partially completed, either retry with refined instructions or handle
     the remaining work in the current conversation

6. **Clean up** child conversations after incorporating their work:
   ```
   mitto_conversation_delete(self_id: <your session_id>, conversation_id: <child_id>)
   ```

**If Mitto tools are not available**, execute all refactorings directly in the current
conversation as described in the steps above (make the change, test, report).

### 5. Report Summary

After completing approved changes:

```markdown
## Refactoring Summary

### Changes Made
| Item | Change | Benefit | Verified |
|------|--------|---------|----------|
| #1 | Grouped related functions into module | Better organization | ✅ Tests pass |
| #2 | Extracted validation helper | Less duplication | ✅ Tests pass |

### Skipped Items
- Item #3: Skipped per user request
```

</instructions>

<rules>
- Propose refactorings before implementing them, and wait for user approval
- Preserve external behavior — refactoring changes structure, not functionality
- Make one type of change at a time, because isolated changes are easier to review and revert
- Run tests after each change to catch regressions early
- Explain the benefit of each change, so the user understands why it improves the code
- For significant refactorings, consider delegating to child conversations to parallelize the work
- When delegating, match the ACP server to the task: use fast-thinking, coding agents for mechanical restructuring; use reasoning/planning agents for complex decompositions or architectural decisions
- Limit parallel child conversations to 4 at a time to avoid overwhelming the system
</rules>
