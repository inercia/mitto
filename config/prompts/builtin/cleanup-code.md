---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
backgroundColor: "#C8E6C9"
---

Analyze the codebase for cleanup opportunities and propose a prioritized list of changes.

**Do not make changes immediately. Propose a plan first and wait for approval.**

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

Present a prioritized table of proposed cleanup items:

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

**Do not proceed until the user explicitly approves.**

### 4. Execute Approved Changes

For each approved item:
1. Make the change
2. Verify nothing breaks (run linter, tests)
3. Report the result

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

## Rules

- **Never remove code without proposing first**: Always present the plan and wait for approval
- **Never remove code without verification**: Always search for references first
- **Preserve version control history**: Don't worry about "losing" code - it's in history
- **Run tests after changes**: Catch issues early
- **Be conservative with public APIs**: They might be used by external code
- **Update related documentation**: Keep docs in sync with code changes
