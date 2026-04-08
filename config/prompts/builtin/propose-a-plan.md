---
name: "Propose a plan"
description: "Create a detailed plan for the current task"
group: "Planning"
backgroundColor: "#BBDEFB"
---

Explore relevant codebase parts. Read affected files and check for existing
patterns and reusable utilities.

Create a detailed plan for the current task.

### Structure

1. **Goal**: What we're achieving
2. **Current state**: What exists, what's missing
3. **Steps**: Numbered concrete actions with file paths, complexity estimates, dependencies
4. **Risks**: Potential issues and mitigations
5. **Verification**: How we'll know it's complete

Present plan, wait for approval.

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Approve and execute / Modify plan"
**Without**: Ask in conversation.
