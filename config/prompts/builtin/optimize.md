---
name: "Optimize"
description: "Identify and fix performance issues"
backgroundColor: "#F8BBD9"
---

Identify and fix performance issues in the code.

### Analysis:

1. **Profile first**: Identify actual bottlenecks, don't guess
2. **Measure**: Establish baseline performance metrics

### Common optimizations:

1. **Algorithmic**: Better data structures, reduced complexity (O(n²) → O(n log n))
2. **Memory**: Reduce allocations, reuse buffers, avoid copies
3. **I/O**: Batch operations, use buffering, async where appropriate
4. **Caching**: Cache expensive computations, use memoization
5. **Concurrency**: Parallelize independent work, reduce lock contention

### For each optimization:
- Explain the bottleneck identified
- Show the optimization applied
- Quantify the improvement (or expected improvement)
- Note any tradeoffs (memory vs speed, complexity vs performance)

Avoid premature optimization. Focus on measurable improvements.

