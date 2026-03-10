---
name: "Address PR Comments"
description: "Systematically address all pull request review feedback"
group: "Submission of changes"
backgroundColor: "#B2DFDB"
---

<task>
Address all review comments on the current pull request with thoughtful responses and code changes.
</task>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_ask_yes_no`
- `mitto_conversation_new`
- `mitto_children_tasks_wait`
- `mitto_children_tasks_report`
- `mitto_conversation_delete`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

<instructions>

### 1. Identify the PR/MR

```bash
git branch --show-current
gh pr status          # GitHub
glab mr view          # GitLab
```

If multiple or none found, ask the user to specify.

### 2. Retrieve All Comments

```bash
gh pr view <number> --json reviews,comments,reviewThreads   # GitHub
glab mr view <number> --comments                             # GitLab
```

Categorize: review comments, inline code comments, conversation threads, change requests, approvals with comments.

### 3. Analyze Each Comment

For each, evaluate:
- **Concern**: What issue is raised? Correctness, style, performance, security?
- **Validity**: Is it technically valid? Aligned with project conventions? Missing context?
- **Priority**: Blocking (must fix) / Important (should fix) / Optional (nice-to-have) / Discussion (needs clarification)

### 4. Formulate Responses

- **Agree**: Acknowledge, implement fix, reply with "✅ Fixed in [hash]"
- **Need clarification**: Ask specific questions, provide context
- **Disagree**: Acknowledge perspective, explain reasoning with evidence, offer alternatives
- **Already addressed**: Point to relevant code/commit

Ask me for confirmation if any question arises.

### 5. Group and Prioritize

1. Group related comments (multiple about same issue)
2. Identify dependencies (some fixes resolve multiple comments)
3. Prioritize: blocking first, related changes together, independent in parallel

<output_format>

| Comment | Type | Validity | Priority | Action |
|---------|------|----------|----------|--------|
| ...     | ...  | ...      | ...      | ...    |

</output_format>

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Does this analysis look correct?"
**Without**: Ask in conversation for confirmation.

### 6. Implement Changes

Per change:
1. Make the change
2. Run relevant tests
3. Commit: `fix: address review feedback on [topic]`
4. Reply to the comment

#### Delegating Significant Fixes to Child Conversations

For fixes requiring **significant work** (3+ files, substantial new code, risky refactors), delegate to Mitto child conversations for parallel execution.

**How to delegate (requires Mitto MCP tools):**

1. Your session ID is `@mitto:session_id`. Available ACP servers: `@mitto:available_acp_servers`
2. Select ACP server: prefer `"coding"`/`"fast"` tagged servers for implementation tasks. Fallback: current server (marked `(current)` in the list above).
3. `mitto_conversation_new(self_id: "@mitto:session_id")`:
   ```
   title: "PR fix: <description>"
   initial_prompt: |
     Addressing a PR review comment.
     **Repo/Branch/PR**: <context>
     **Review comment**: <full comment>
     **What to do**: <detailed fix description>
     **Constraints**: Only modify related files, run tests, follow project style.

     When done, report via mitto_children_tasks_report(self_id, task_id: "<task_id>", status, summary, details).
     (Get your own self_id by calling mitto_conversation_get_current(self_id: "init").)
   acp_server: <selected server>
   ```
4. `mitto_children_tasks_wait(self_id: "@mitto:session_id", children_list, task_id: "<short task description>", timeout_seconds: 600)`
5. Review results, verify changes, run tests
6. `mitto_conversation_delete` for completed children
7. Commit combined changes

**Without Mitto tools**: implement all fixes directly.

### 7. Respond to All Comments

Before pushing:
- Every comment has a response
- Mark resolved conversations as appropriate
- Leave unresolved any needing discussion

### 8. Identify Push Remote

```bash
git remote -v
git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null
```

In fork workflows: push to `origin` (your fork), not `upstream`. Verify via PR's `head.repo.full_name`.

### 9. Push and Request Re-review

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Ready to push and request re-review?"
**Without**: Ask in conversation.

```bash
git push <push-remote> <branch-name>
gh pr ready && gh pr comment --body "Ready for re-review"   # GitHub
glab mr update --ready                                       # GitLab
```

### 10. Summary Report

<output_format>

```console
✅ PR Review Comments Addressed

📊 Summary:
- Total comments: X
- Resolved with code changes: Y
- Resolved with explanation: Z
- Pending discussion: W

🔄 Changes made:
- [Brief description of key changes]

🔗 PR: <pr-url>
```

</output_format>

</instructions>

<rules>
- Consider all feedback carefully — reviewers may have context you're missing
- Respond to every comment
- Provide evidence when disagreeing (code, docs, benchmarks)
- Ask for clarification rather than guessing intent
- Group related changes in single commits
- Run tests before pushing
- Only mark conversations resolved if you authored the comment (unless asked)
- For larger refactors, discuss scope first — consider delegating to a child conversation
- When delegating, prefer `"coding"`/`"fast"` tagged ACP servers
- Max 4 parallel child conversations
- In fork workflows, push to `origin`, not `upstream`
</rules>
