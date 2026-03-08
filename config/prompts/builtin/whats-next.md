---
name: "What's next?"
description: "Analyze progress and suggest next steps"
group: "Work flow"
backgroundColor: "#BBDEFB"
---

<investigate_before_answering>
Review current state: read relevant files, check git status and recent changes.
</investigate_before_answering>

<task>
Analyze progress and suggest next steps.
</task>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Optional tools:** `mitto_ui_ask_yes_no`

If missing, show instructions for Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

<instructions>

### Review

1. **Completed**: What we've accomplished
2. **Current state**: Where things stand
3. **Remaining**: What's left for the original goal

### Suggest Next Steps

<output_format>

| Priority | Task | Reason | Effort |
|----------|------|--------|--------|
| 1 | ... | ... | Small/Medium/Large |

</output_format>

Consider: dependencies, risk (tackle risky items early), value (high-impact first), blockers.

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Proceed with top priority task?"
**Without**: Ask in conversation.

</instructions>
