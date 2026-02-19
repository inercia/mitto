---
name: "Propose a plan"
description: "Create a detailed plan for the current task"
backgroundColor: "#BBDEFB"
---

Create a detailed plan for the current task.

### Structure your plan as:

1. **Goal**: What we're trying to achieve
2. **Current state**: What exists now, what's missing
3. **Steps**: Numbered list of concrete actions
   - Include file paths and function names where applicable
   - Estimate complexity (simple/medium/complex) for each step
   - Note dependencies between steps
4. **Risks**: Potential issues and how to mitigate them
5. **Verification**: How we'll know the task is complete

Present the plan and wait for approval before executing.

**Using Mitto UI tools (if available):** Use `mitto_ui_ask_yes_no` to get approval:
```
Question: "Plan is ready. Would you like me to proceed with execution?"
Yes label: "Approve and execute"
No label: "Modify plan"
```

If the user selects "Modify plan", discuss the changes in conversation before proceeding.

**Fallback (if Mitto UI tools are not available):**

Ask in conversation: "Does this plan look good? Should I proceed, or would you like to modify anything?"

