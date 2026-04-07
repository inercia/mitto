---
name: "What's next?"
description: "Analyze progress and suggest next steps"
group: "Work flow"
backgroundColor: "#BBDEFB"
---

Review current state: read relevant files, check git status and recent changes.

Analyze progress and suggest next steps.

## Prerequisites: Check for Mitto MCP Server (Optional)

**Optional tools:** `mitto_ui_ask_yes_no`

If missing, show instructions for Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

### Review

1. **Completed**: What we've accomplished
2. **Current state**: Where things stand
3. **Remaining**: What's left for the original goal

### Suggest Next Steps

| Priority | Task | Reason | Effort |
|----------|------|--------|--------|
| 1 | ... | ... | Small/Medium/Large |

Consider: dependencies, risk (tackle risky items early), value (high-impact first), blockers.

**With Mitto UI**: `mitto_ui_ask_yes_no` → "Proceed with top priority task?"
**Without**: Ask in conversation.
