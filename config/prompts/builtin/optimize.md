---
name: "Optimize"
description: "Identify and propose performance improvements"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Read relevant code paths thoroughly. Identify actual bottlenecks based on code
structure — do not guess. Read multiple files in parallel.
</investigate_before_answering>

<task>
Analyze for performance issues. Propose a prioritized plan and wait for approval.
</task>

<scope>
Focus on measurable performance improvements. Do not refactor for style unless it
directly contributes to the performance gain.
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

### 1. Analyze Performance

Profile first — identify actual bottlenecks:

| Category | What to Look For |
|----------|------------------|
| Algorithmic | O(n²) where O(n log n) is possible |
| Memory | Excessive allocations, unnecessary copies, leaks |
| I/O | Unbuffered operations, synchronous blocking, N+1 queries |
| Caching | Repeated expensive computations, missing memoization |
| Concurrency | Sequential work that could be parallelized, lock contention |

### 2. Propose Plan

<output_format>

| Priority | Category | Location | Issue | Fix | Impact | Effort | Tradeoffs |
|----------|----------|----------|-------|-----|--------|--------|-----------|
| 1 | Algorithmic | `file:fn()` | O(n²) loop | Hash map | High | Medium | More memory |

Priority: 1=clear bottleneck, 2=noticeable improvement, 3=minor.

</output_format>

### 3. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Approve selected / Investigate / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 4. Execute

Per item: implement, verify (tests), measure if possible, report.

#### Delegating Significant Optimizations to Child Conversations

For optimizations spanning 3+ files, algorithm rewrites, concurrency additions, or multiple parallelizable items, delegate to Mitto child conversations.

**Choosing the right ACP server:**

1. `mitto_conversation_get_current(self_id: "init")` → get `available_acp_servers`
2. Match server tags to task:
   - Well-defined optimizations (buffering, memoization) → prefer `"coding"`/`"fast"` servers
   - Complex optimizations (concurrency redesign, algorithmic tradeoffs) → prefer `"reasoning"`/`"planning"` servers
   - No match → current server, then first available
3. `mitto_conversation_new` with full context, constraints, and reporting directive
4. `mitto_children_tasks_wait(timeout_seconds: 600)`
5. Review results, verify correctness, check tradeoffs
6. `mitto_conversation_delete` for completed children

**Without Mitto tools**: execute directly.

### 5. Summary

```markdown
## Optimization Summary
### Changes Made
| Item | Change | Expected Impact | Verified |
|------|--------|-----------------|----------|
| #1 | Replaced O(n²) with hash map | High | ✅ Tests pass |
### Skipped Items
- Item #N: Skipped per user request
```

</instructions>

<rules>
- Propose before implementing; wait for approval
- Profile before optimizing — focus on actual bottlenecks, not guesses
- Document tradeoffs (memory vs speed, complexity vs performance)
- Verify correctness with tests after each optimization
- Quantify improvements when possible
- For significant optimizations, consider delegating to child conversations
- Match ACP server to task: coding agents for clear optimizations, reasoning agents for complex redesigns
- Max 4 parallel child conversations
</rules>
