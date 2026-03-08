---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Before proposing any cleanup, read the relevant code files and search for references
thoroughly. When analyzing multiple files, read them in parallel to build context
faster. Do not speculate about whether code is unused — verify by searching for
references first.
</investigate_before_answering>

<task>
Analyze the codebase for cleanup opportunities and propose a prioritized list of changes.

Propose a plan first and wait for approval before making any changes.
</task>

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

### 1. Analyze the Codebase

Investigate the following areas:

**Opportunities for improved modularity:**

- Identify code that is duplicated across modules
- Find modules that have grown too large and could be split
- Look for cohesive groups of functions that could be extracted into a new module

**Unused Imports:**

Use the project's tools to detect unused imports:

| Language | Common Tools |
|----------|--------------|
| Go | `goimports`, `gopls` |
| JavaScript/TypeScript | ESLint with `no-unused-vars`, IDE refactoring |
| Python | `autoflake`, `pylint`, IDE refactoring |
| Rust | `cargo clippy`, compiler warnings |
| Java | IDE refactoring, `checkstyle` |

**Dead Code:**

Use static analysis tools to find unused code:

| Language | Tools for Dead Code Detection |
|----------|------------------------------|
| Go | `golangci-lint` (unused, deadcode linters) |
| JavaScript/TypeScript | ESLint, `ts-prune` |
| Python | `vulture`, `pylint` |
| Rust | `cargo clippy`, compiler warnings |
| Java | IDE inspections, `spotbugs` |

Look for:

- Private/unexported functions never called within the module
- Public/exported functions with no references in the codebase
- Constants and variables defined but never used
- Class members/struct fields never accessed
- Test helpers no longer used by any tests

**Commented-Out Code:**

Search for large blocks of commented-out code that should be removed.

**Outdated Documentation:**

- Find documentation referencing non-existent code,
  deleted features, or old APIs.
- Check if existing comments in the code are still relevant or accurate.

**Obsolete Test Code:**

Look for unused test helpers, fixtures, and mock implementations.

### 2. Propose Cleanup Plan

<output_format>

| Priority | Category | Location | Description | Risk | Effort |
|----------|----------|----------|-------------|------|--------|
| 1 | Dead Code | `path/to/file` | Remove unused function `oldHelper()` | Low | Small |
| 2 | Imports | `path/to/file` | Remove 3 unused imports | Low | Small |
| 3 | Documentation | `docs/api.md` | Update outdated API references | Low | Medium |
| ... | ... | ... | ... | ... | ... |

**Priority levels:**

- **1 (High)**: Clear dead code, no risk of breaking anything
- **2 (Medium)**: Likely unused, low risk
- **3 (Low)**: Potentially unused, needs careful verification

**Risk levels:**

- **Low**: Clearly unused, safe to remove
- **Medium**: Appears unused but verify before removing
- **High**: Public API or widely referenced, needs careful analysis

</output_format>

### 3. Wait for Approval

**Using Mitto UI tools (if available):**

If the `mitto_ui_options_buttons` tool is available, use it to present the approval options:

```
Question: "How would you like to proceed with the cleanup plan?"
Options: ["Approve all", "Approve selected", "Investigate", "Cancel"]
```

If the user selects "Approve selected" or "Investigate", follow up with a text conversation to get the specific item numbers.

**Fallback (if Mitto UI tools are not available):**

Ask the user in the conversation to choose one of these options:

- **Approve all** - proceed with all cleanup items
- **Approve selected** - specify which items to proceed with (by priority number)
- **Investigate** - get more details on specific items before deciding
- **Cancel** - abort without making changes

Wait for the user to explicitly approve before proceeding.

### 4. Execute Approved Changes

For each approved item:
1. Make the change
2. Verify nothing breaks (run linter, tests)
3. Report the result

#### Delegating Significant Cleanup Work to Child Conversations

When approved cleanup items require **significant effort** — such as large-scale refactors,
removing deeply intertwined dead code, restructuring modules, or rewriting outdated documentation
across many files — consider delegating them to new Mitto child conversations. This keeps the
main cleanup workflow focused on coordination and allows heavy work to happen in parallel.

**When to delegate (any of the following):**
- The cleanup item spans 3+ files or requires understanding complex dependency chains
- The cleanup involves restructuring or splitting a large module
- Multiple independent cleanup items can be parallelized
- The item requires substantial rewriting (e.g., updating outdated docs across several files)

**Choosing the right ACP server (requires Mitto MCP tools):**

1. **Get your current conversation ID** using `mitto_conversation_get_current`:
   ```
   self_id: "init"
   ```

2. **Select the best-suited ACP server** from the `available_acp_servers` returned in step 1.
   Each server entry includes optional `tags` (e.g., `["coding", "fast"]`, `["reasoning", "planning"]`)
   that describe its strengths. Match the server to the nature of the work:

   - **Well-defined, straightforward tasks** (removing clearly dead code, deleting unused imports
     across files, removing commented-out blocks): Prefer servers tagged with `"coding"` and/or
     `"fast"` — these are fast-thinking agents optimized for implementation tasks with clear scope.
   - **Complex or ambiguous tasks** (refactoring tangled modules, deciding what to keep vs. remove
     when dependencies are unclear, rewriting documentation that requires understanding architecture):
     Prefer servers tagged with `"reasoning"` and/or `"planning"` — these are more sophisticated
     agents better suited for tasks requiring deeper analysis and judgment.
   - **If no tags match** or no tags are available: Fall back to the current conversation's ACP
     server (the one with `current: true`).
   - **If the current server cannot be determined**: Use the first available ACP server.

3. **Create a new conversation** for each significant cleanup task using `mitto_conversation_new`:
   ```
   self_id: <your session_id>
   title: "Cleanup: <brief description of the cleanup task>"
   initial_prompt: |
     You are performing a code cleanup task. Here is the context:

     **Repository**: <repo info>
     **Branch**: <current branch>

     **Cleanup task**:
     <detailed description of what needs to be cleaned up>

     **Files involved**:
     <list of files to examine and/or modify>

     **What needs to be done**:
     <specific instructions: what to remove, refactor, or rewrite, and the expected outcome>

     **Constraints**:
     - Only modify files related to this specific cleanup task
     - Search for references before declaring code unused — external code may depend on public APIs
     - Run linter and tests after making changes to verify nothing is broken
     - Follow the project's existing code style and conventions
     - Be conservative with public APIs — they might be used by external consumers

     When you complete this task, report your results back to the parent conversation
     using the `mitto_children_tasks_report` MCP tool:

     mitto_children_tasks_report(
       self_id: <your session ID - get it via mitto_conversation_get_current>,
       status: "completed" or "failed" or "partial",
       summary: "<brief description of what was accomplished>",
       details: "<detailed info: files modified, code removed, tests run, any issues encountered>"
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
   - Verify that the reported changes are correct and don't remove code that is still in use
   - Run tests and linter to confirm nothing is broken
   - If a child failed or partially completed, either retry with refined instructions or handle
     the remaining work in the current conversation

6. **Clean up** child conversations after incorporating their work:
   ```
   mitto_conversation_delete(self_id: <your session_id>, conversation_id: <child_id>)
   ```

**If Mitto tools are not available**, execute all cleanup items directly in the current
conversation as described in the steps above (make the change, verify, report).

### 5. Report Summary

After completing approved changes:

```markdown
## Cleanup Summary

### Changes Made
- `path/to/file`: Removed unused function `oldHelper()`
- `path/to/file`: Removed 3 unused imports

### Verification
- ✅ All tests passing
- ✅ Linter checks passing
- ✅ Code formatted correctly

### Skipped Items
- Item #4: Skipped per user request
```

</instructions>

<rules>
- Propose changes before removing code, and wait for user approval
- Search for references before declaring code unused, because external code may depend on public APIs
- Rely on version control history for code recovery — don't worry about "losing" deleted code
- Run tests after changes to catch unexpected breakage, since cleanup changes can have subtle side effects
- Be conservative with public APIs — they might be used by external consumers not visible in this codebase
- Update related documentation when removing code, to keep docs in sync
- For significant cleanup items, consider delegating to child conversations to parallelize the work
- When delegating, match the ACP server to the task: use fast-thinking, coding agents for well-defined removals; use reasoning/planning agents for complex refactors or ambiguous cleanup decisions
- Limit parallel child conversations to 4 at a time to avoid overwhelming the system
</rules>
