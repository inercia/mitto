---
name: "Report to parent"
description: "Send a status report to the parent conversation"
group: "Work flow"
backgroundColor: "#FFF9C4"
enabledWhen: "session.isChild && parent.exists"
enabledWhenMCP: mitto_conversation_*
---

Report the current status and findings to the parent conversation that spawned this one.

## Phase 1: Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.
Your parent conversation ID is `@mitto:parent_session_id` — use this as `conversation_id` when sending to the parent.

## Phase 2: Analyze Current Work

Review everything accomplished in this conversation:

- What was the original task/assignment?
- What has been completed?
- What is still in progress?
- What issues or blockers were encountered?
- What decisions were made?
- What files were modified?

Think about what the parent conversation could be interested in knowing.

## Phase 3: Prepare Report

Create a structured summary:

```markdown
## Status Report

### Task
<original assignment from parent>

### Status: <completed | partial | in_progress | blocked | failed>

### Completed
- <list of completed items>

### In Progress
- <list of items still being worked on>

### Files Modified
- <list of files changed>

### Findings & Decisions
- <important discoveries or choices made>

### Issues / Blockers (if any)
- <problems encountered>

### Recommendations / Next Steps
- <suggestions for follow-up work>
```

## Phase 4: Send Report

Use `mitto_conversation_send_prompt` to deliver the report to the parent conversation:

```
self_id: "@mitto:session_id"
conversation_id: "@mitto:parent_session_id"
prompt: <the full structured report from Phase 3>
```

## Phase 5: Confirm

Inform the user:

```markdown
✅ Report Sent to Parent

**Status:** <status>
**Summary:** <summary>

The parent conversation will receive this report and can review the findings.
```

## Guidelines

- Be concise but comprehensive
- Include specific file paths for any changes
- Highlight important discoveries prominently
- Be honest about incomplete work or failures
- Provide actionable next steps
- Use the appropriate status: `completed` (done), `partial` (some done, more needed), `in_progress` (still working), `failed` (encountered blocking issues)
