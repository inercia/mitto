---
name: "What's next?"
description: "Analyze progress and suggest next steps"
group: "Work flow"
backgroundColor: "#BBDEFB"
---

<investigate_before_answering>
Before suggesting next steps, review the current state of the work by reading relevant
files, checking git status and recent changes, and understanding what has been accomplished
so far.
</investigate_before_answering>

<task>
Analyze our progress and suggest next steps.
</task>

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

<instructions>

### Review:

1. **Completed**: What we've accomplished so far
2. **Current state**: Where the code/project stands now
3. **Remaining work**: What's left to do for the original goal

### Suggest next steps:

<output_format>

| Priority | Task | Reason | Effort |
|----------|------|--------|--------|
| 1 | ... | ... | Small/Medium/Large |

</output_format>

### Consider:

- Dependencies — what must come before what
- Risk — tackle risky items early to surface problems sooner
- Value — high-impact items first
- Blockers — anything preventing progress

**Using Mitto UI tools (if available):** Use `mitto_ui_ask_yes_no` to offer proceeding:
```
Question: "Would you like me to proceed with the top priority task?"
Yes label: "Yes, proceed"
No label: "No, let me choose"
```

If the user selects "No", follow up in conversation to determine which task to tackle.

**Fallback (if Mitto UI tools are not available):**

Ask if I should proceed with the top priority item.

</instructions>
