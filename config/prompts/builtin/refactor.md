---
name: "Refactor"
description: "Propose refactoring improvements for better code quality"
backgroundColor: "#C8E6C9"
---

Analyze the code and propose a prioritized list of refactoring improvements.

**Do not make changes immediately. Propose a plan first and wait for approval.**

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

Present a prioritized table of proposed refactorings:

| Priority | Category | Location | Issue | Proposed Change | Benefit | Effort |
|----------|----------|----------|-------|-----------------|---------|--------|
| 1 | Structure | `path/to/file` | Related functions scattered | Group into module | Better organization | Medium |
| 2 | DRY | `path/to/files` | Duplicated validation logic | Extract to helper | Less duplication | Small |
| 3 | Naming | `path/to/file:fn()` | Unclear function name | Rename to `descriptiveName()` | Clarity | Small |
| ... | ... | ... | ... | ... | ... | ... |

**Priority levels:**
- **1 (High)**: Significantly improves maintainability or readability
- **2 (Medium)**: Noticeable improvement to code quality
- **3 (Low)**: Minor improvement, nice-to-have

**Effort levels:**
- **Small**: Quick change, low risk
- **Medium**: Moderate change, some risk
- **Large**: Significant change, higher risk

### 3. Wait for Approval

Ask the user to:
- **Approve all** - proceed with all refactorings
- **Approve selected** - specify which items to proceed with (by priority number)
- **Investigate** - get more details on specific items before deciding
- **Cancel** - abort without making changes

**Do not proceed until the user explicitly approves.**

### 4. Execute Approved Refactorings

For each approved item:
1. Make one type of change at a time
2. Run tests after each change to catch regressions
3. Preserve external behavior (this is refactoring, not rewriting)
4. Report the result

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

## Rules

- **Never refactor without proposing first**: Always present the plan and wait for approval
- **Preserve external behavior**: This is refactoring, not rewriting
- **Make one type of change at a time**: Easier to review and revert if needed
- **Run tests after each change**: Catch regressions early
- **Document the benefit**: Explain why each change improves the code
