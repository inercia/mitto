---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

<investigate_before_answering>
Read relevant code and search for references before proposing cleanup.
Read multiple files in parallel. Do not speculate — verify by searching.
</investigate_before_answering>

<task>
Analyze for cleanup opportunities. Propose a plan and wait for approval.
</task>

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

<output_format>

| Priority | Category | Location | Description | Risk | Effort |
|----------|----------|----------|-------------|------|--------|
| 1 | Dead Code | `path/file` | Remove unused `oldHelper()` | Low | Small |

Priority: 1=clear dead code, 2=likely unused, 3=needs verification.
Risk: Low=clearly unused, Medium=verify first, High=public API.

</output_format>

### 3. Wait for Approval

**With Mitto UI**: `mitto_ui_options_buttons` → "Approve all / Approve selected / Investigate / Cancel"
**Without**: Ask in conversation. Wait for explicit approval.

### 4. Execute

Per item: make change, verify (linter, tests), report.

#### Delegating Significant Cleanup to Child Conversations

For cleanup spanning 3+ files, module restructuring, or multiple parallelizable items, delegate to Mitto child conversations.

**Choosing the right ACP server:**

1. `mitto_conversation_get_current(self_id: "init")` → get `available_acp_servers`
2. Match server tags to task:
   - Well-defined removals → prefer `"coding"`/`"fast"` servers
   - Complex refactors, ambiguous decisions → prefer `"reasoning"`/`"planning"` servers
   - No match → current server, then first available
3. `mitto_conversation_new` with full context, constraints, and reporting directive
4. `mitto_children_tasks_wait(timeout_seconds: 600)`
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

</instructions>

<rules>
- Propose before removing; wait for approval
- Search for references before declaring code unused
- Rely on version control for recovery
- Run tests after changes
- Be conservative with public APIs
- Update related docs when removing code
- For significant cleanup, consider delegating to child conversations
- Match ACP server to task: coding agents for clear removals, reasoning agents for complex refactors
- Max 4 parallel child conversations
</rules>
