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
</rules>
