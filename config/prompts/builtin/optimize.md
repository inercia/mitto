---
name: "Optimize"
description: "Identify and propose performance improvements"
backgroundColor: "#C8E6C9"
---

Analyze the code for performance issues and propose a prioritized list of optimizations.

**Do not make changes immediately. Propose a plan first and wait for approval.**

### 1. Analyze Performance

**Profile first** - Identify actual bottlenecks, don't guess:
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

Present a prioritized table of proposed optimizations:

| Priority | Category | Location | Issue | Proposed Fix | Impact | Effort | Tradeoffs |
|----------|----------|----------|-------|--------------|--------|--------|-----------|
| 1 | Algorithmic | `path/to/file:fn()` | O(n²) loop | Use hash map lookup | High | Medium | More memory |
| 2 | I/O | `path/to/file:fn()` | Unbuffered writes | Add buffering | Medium | Small | None |
| 3 | Caching | `path/to/file:fn()` | Repeated computation | Memoize result | Medium | Small | Memory usage |
| ... | ... | ... | ... | ... | ... | ... | ... |

**Priority levels:**
- **1 (High)**: Clear bottleneck with significant impact
- **2 (Medium)**: Noticeable improvement expected
- **3 (Low)**: Minor improvement, nice-to-have

**Impact levels:**
- **High**: Major performance improvement expected
- **Medium**: Moderate improvement expected
- **Low**: Minor improvement expected

### 3. Wait for Approval

Ask the user to:
- **Approve all** - proceed with all optimizations
- **Approve selected** - specify which items to proceed with (by priority number)
- **Investigate** - get more details or benchmarks on specific items
- **Cancel** - abort without making changes

**Do not proceed until the user explicitly approves.**

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

## Rules

- **Never optimize without proposing first**: Always present the plan and wait for approval
- **Avoid premature optimization**: Focus on measurable improvements
- **Profile before optimizing**: Identify actual bottlenecks, don't guess
- **Document tradeoffs**: Note memory vs speed, complexity vs performance
- **Verify correctness**: Run tests after each optimization
- **Measure improvements**: Quantify the improvement when possible
