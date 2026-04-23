---
name: "Simplify"
description: "Simplify implementation while preserving functionality"
group: "Code Quality"
backgroundColor: "#C8E6C9"
---

Read the current implementation thoroughly. Understand what it does and why.
Check callers and dependents to ensure changes preserve external behavior.

Simplify the current implementation while preserving functionality.

### 1. Look for Simplification Opportunities

1. **Redundant code**: Duplicate logic to consolidate
2. **Over-engineering**: Abstractions adding complexity without value
3. **Deep nesting**: Flatten with early returns or guard clauses
4. **Long functions**: Break into smaller, focused functions
5. **Complex conditionals**: Simplify boolean logic, use lookup tables
6. **Unnecessary state**: Remove variables computable on demand

### 2. For Each Change

- Explain what you're simplifying and why
- Show before/after comparison
- Verify behavior preserved by running tests

### 3. Execute

Per item: simplify, verify (tests), report.

#### Delegating Significant Simplifications to Child Conversations

For simplifications spanning 3+ files, removing abstraction layers, or multiple parallelizable items, delegate to Mitto child conversations.

**Session context for delegation:**

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

Available ACP servers:
@mitto:available_acp_servers

Existing children:
@mitto:children

**Choosing the right ACP server:**

1. Match server tags to task:
   - Mechanical simplifications (consolidating duplicates, flattening conditionals) → prefer `"coding"`/`"fast"` servers
   - Judgment-heavy simplifications (which abstractions to remove, non-obvious decompositions) → prefer `"reasoning"`/`"planning"` servers
   - No match → server marked `(current)`, then first available
2. If relevant children already exist, consider sending work to them via `mitto_conversation_send_prompt` instead of creating new ones
3. `mitto_conversation_new(self_id: "@mitto:session_id")` with full context, constraints (preserve behavior), and reporting directive
4. `mitto_children_tasks_wait(self_id: "@mitto:session_id", task_id: "<short task description>", timeout_seconds: 600)`
5. Review: verify behavior preserved, code genuinely simpler (not just different)
6. `mitto_conversation_delete` for completed children

**Without Mitto tools**: execute directly.

## Guidelines

- Preserve external behavior
- Explain what you're simplifying and why
- Verify with tests after each change
- Show before/after comparisons
- For significant simplifications, consider delegating to child conversations
- Match ACP server to task: coding agents for mechanical simplifications, reasoning agents for complex restructurings
- Max 4 parallel child conversations
