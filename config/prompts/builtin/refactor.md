---
name: "Refactor"
description: "Propose refactoring improvements for better code quality"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

Analyze the code and propose a prioritized list of refactoring improvements.

**Do not make changes immediately. Propose a plan first and wait for approval.**

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
