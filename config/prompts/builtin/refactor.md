---
name: "Refactor"
description: "Propose refactoring improvements for better code quality"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Read the code thoroughly: current structure, callers, dependents. Read multiple
files in parallel. Do not speculate about code you haven't opened.
</investigate_before_answering>

<task>
Analyze and propose a prioritized list of refactoring improvements.
Propose a plan and wait for approval.
</task>

<scope>
Preserve external behavior. One type of change at a time. No new features.
</scope>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_options_buttons`
- `mitto_conversation_get_current`
- `mitto_conversation_new`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_conversation_delete`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

<instructions>

### 1. Analyze

| Area | What to Look For |
|------|------------------|
| Naming | Unclear or misleading names |
| Structure | Related functionality scattered across files |
| SRP | Functions/classes doing too many things |
| DRY | Repeated patterns that could be extracted |
| Error Handling | Inconsistent or uninformative errors |
| Idioms | Code not following language best practices |

### 2. Propose Plan

<output_format>

| Priority | Category | Location | Issue | Change | Benefit | Effort |
|----------|----------|----------|-------|--------|---------|--------|
| 1 | Structure | `path/file` | Related fns scattered | Group into module | Better organization | Medium |

Priority: 1=significant maintainability gain, 2=noticeable quality improvement, 3=minor.
Effort: Small (low risk), Medium (some risk), Large (higher risk).

</output_format>

### 3. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Approve selected / Investigate / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 4. Execute

Per item: make one type of change, run tests, preserve external behavior, report.

#### Delegating Significant Refactorings to Child Conversations

For refactorings spanning 3+ files, module extraction, or multiple parallelizable items, delegate to Mitto child conversations.

**Choosing the right ACP server:**

1. `mitto_conversation_get_current(self_id: "init")` → get `available_acp_servers`
2. Match server tags to task:
   - Mechanical restructuring (renames, moves, obvious extractions) → prefer `"coding"`/`"fast"` servers
   - Complex decompositions, architectural decisions → prefer `"reasoning"`/`"planning"` servers
   - No match → current server, then first available
3. `mitto_conversation_new` with full context, constraints (preserve behavior, no new features), and reporting directive
4. `mitto_children_tasks_wait(timeout_seconds: 600)`
5. Review results, verify behavior preserved, run tests
6. `mitto_conversation_delete` for completed children

**Without Mitto tools**: execute directly.

### 5. Summary

```markdown
## Refactoring Summary
### Changes Made
| Item | Change | Benefit | Verified |
|------|--------|---------|----------|
| #1 | Grouped related functions | Better organization | ✅ Tests pass |
### Skipped Items
- Item #N: Skipped per user request
```

</instructions>

<rules>
- Propose before implementing; wait for approval
- Preserve external behavior
- One type of change at a time
- Run tests after each change
- Explain the benefit of each change
- For significant refactorings, consider delegating to child conversations
- Match ACP server to task: coding agents for mechanical changes, reasoning agents for complex decompositions
- Max 4 parallel child conversations
</rules>
