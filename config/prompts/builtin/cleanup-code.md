---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

Read relevant code and search for references before proposing cleanup.
Read multiple files in parallel. Do not speculate — verify by searching.

Analyze for cleanup opportunities. Propose a plan and wait for approval.

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_options_buttons`
- `mitto_conversation_new`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_conversation_delete`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

### 1. Analyze

**Modularity**: Duplicated code, oversized modules, extractable function groups.

**Unused Imports** (use project tools):

| Language | Tools |
|----------|-------|
| Go | `goimports`, `gopls` |
| JS/TS | ESLint `no-unused-vars` |
| Python | `autoflake`, `pylint` |
| Rust | `cargo clippy` |

**Dead Code** (use static analysis):

| Language | Tools |
|----------|-------|
| Go | `golangci-lint` (unused, deadcode) |
| JS/TS | ESLint, `ts-prune` |
| Python | `vulture`, `pylint` |
| Rust | `cargo clippy` |

Look for: unexported functions never called, exported functions with no references, unused constants/variables/fields, unused test helpers.

**Also check**: commented-out code blocks, outdated documentation, obsolete test code.

### 2. Propose Plan



| Priority | Category | Location | Description | Risk | Effort |
|----------|----------|----------|-------------|------|--------|
| 1 | Dead Code | `path/file` | Remove unused `oldHelper()` | Low | Small |

Priority: 1=clear dead code, 2=likely unused, 3=needs verification.
Risk: Low=clearly unused, Medium=verify first, High=public API.



### 3. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Approve selected / Investigate / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 4. Execute

Per item: make change, verify (linter, tests), report.

#### Delegating Significant Cleanup to Child Conversations

For cleanup spanning 3+ files, module restructuring, or multiple parallelizable items, delegate to Mitto child conversations.

**Session context for delegation:**

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.
Available ACP servers: `@mitto:available_acp_servers`
Existing children: `@mitto:children`

**Choosing the right ACP server:**

1. Match server tags to task:
   - Well-defined removals → prefer `"coding"`/`"fast"` servers
   - Complex refactors, ambiguous decisions → prefer `"reasoning"`/`"planning"` servers
   - No match → server marked `(current)`, then first available
2. If relevant children already exist, consider sending work to them via `mitto_conversation_send_prompt` instead of creating new ones
3. `mitto_conversation_new(self_id: "@mitto:session_id")` with full context, constraints, and reporting directive
4. `mitto_children_tasks_wait(self_id: "@mitto:session_id", task_id: "<short task description>", timeout_seconds: 600)`
5. Review results, verify changes, run tests
6. `mitto_conversation_delete` for completed children

**Without Mitto tools**: execute directly.

### 5. Summary

```markdown
## Cleanup Summary
### Changes Made
- `path/file`: Removed unused function
### Verification
- ✅ Tests passing / ✅ Linter passing / ✅ Formatted
### Skipped Items
- Item #N: Skipped per user request
```

## Guidelines

- Propose before removing; wait for approval
- Search for references before declaring code unused
- Rely on version control for recovery
- Run tests after changes
- Be conservative with public APIs
- Update related docs when removing code
- For significant cleanup, consider delegating to child conversations
- Match ACP server to task: coding agents for clear removals, reasoning agents for complex refactors
- Max 4 parallel child conversations
