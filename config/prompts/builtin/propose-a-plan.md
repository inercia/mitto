---
name: "Propose a plan"
description: "Create a detailed plan for the current task"
group: "Planning"
backgroundColor: "#BBDEFB"
---

Create a detailed plan for the current task.

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: This prompt can work without Mitto's MCP server, but provides a better user experience with it.

**Optional tools:**
- `mitto_ui_ask_yes_no`

**Check availability:**
1. Look for these tools in your available tools list
2. If ANY of these tools are missing, inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
💡 This prompt works better with Mitto's MCP server for interactive prompts. To enable interactive UI features, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, proceed with the sections below using text-based conversation instead.**

---

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

