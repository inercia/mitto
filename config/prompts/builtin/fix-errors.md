---
name: "Fix errors"
description: "Analyze and fix the errors shown"
group: "Development"
backgroundColor: "#FFE0B2"
---

Read relevant source files to understand code context around each error.
Do not speculate about code you haven't opened.

Analyze and fix the errors shown.

Only fix the identified errors. Keep changes minimal and focused on root causes.

Fix root causes, not symptoms. Ensure fixes work for all valid inputs.
Report deeper design issues rather than applying narrow workarounds.

### Per error:

1. **Identify**: Quote exact error message
2. **Diagnose**: Root cause — what triggered it, why it failed
3. **Fix**: Implement and explain the change
4. **Verify**: Run code/tests, check for related issues

### Multiple errors:

- Fix in dependency order (causes before symptoms)
- Group errors sharing a root cause
- Final verification after all fixes

#### Delegating Complex Error Fixes to Child Conversations

When there are **3+ unrelated errors** that can be fixed independently (different files, no shared root cause), delegate fixes to parallel child conversations.

**Do NOT delegate** for: a single error, errors sharing a root cause, cascading errors from one issue, or trivial one-line fixes.

**Session context for delegation:**

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

Available ACP servers:
@mitto:available_acp_servers

Existing children:
@mitto:children

**How to delegate:**

1. Group errors by root cause — only independent groups get separate children
2. Choose ACP server: clear fix path → prefer `"coding"`/`"fast"` servers; requires investigation/design decisions → prefer `"reasoning"`/`"planning"` servers; no match → server marked `(current)`, then first available
3. If relevant children already exist, consider sending work to them via `mitto_conversation_send_prompt` instead of creating new ones
4. `mitto_conversation_new(self_id: "@mitto:session_id")` per task — include: exact error message(s), relevant file paths and context, what to fix, constraints (minimal changes, root cause fixes only), and reporting directive
5. `mitto_children_tasks_wait(self_id: "@mitto:session_id", task_id: "<error-fix-description>", timeout_seconds: 600)`
6. Review all results — check for conflicts between fixes
7. Run full verification after combining all changes
8. `mitto_conversation_delete` for completed children

**Without Mitto tools**: fix all issues directly in dependency order.
