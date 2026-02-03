---
name: "Simplify"
description: "Simplify implementation while preserving functionality"
backgroundColor: "#C8E6C9"
---

Simplify the current implementation while preserving functionality.

### Look for:

1. **Redundant code**: Duplicate logic that can be consolidated
2. **Over-engineering**: Abstractions that add complexity without value
3. **Deep nesting**: Flatten conditionals using early returns or guard clauses
4. **Long functions**: Break into smaller, focused functions
5. **Complex conditionals**: Simplify boolean logic, use lookup tables
6. **Unnecessary state**: Remove variables that can be computed on demand

### For each change:
- Explain what you're simplifying and why
- Show before/after comparison
- Verify behavior is preserved

