---
name: "What's next?"
description: "Analyze progress and suggest next steps"
backgroundColor: "#BBDEFB"
---

Analyze our progress and suggest next steps.

### Review:

1. **Completed**: What we've accomplished so far
2. **Current state**: Where the code/project stands now
3. **Remaining work**: What's left to do for the original goal

### Suggest next steps:

Present a prioritized list:

| Priority | Task | Reason | Effort |
|----------|------|--------|--------|
| 1 | ... | ... | Small/Medium/Large |

### Consider:
- Dependencies (what must come before what)
- Risk (tackle risky items early)
- Value (high-impact items first)
- Blockers (anything preventing progress)

**Using Mitto UI tools (if available):** Use `mitto_ui_ask_yes_no` to offer proceeding:
```
Question: "Would you like me to proceed with the top priority task?"
Yes label: "Yes, proceed"
No label: "No, let me choose"
```

If the user selects "No", follow up in conversation to determine which task to tackle.

**Fallback (if Mitto UI tools are not available):**

Ask if I should proceed with the top priority item.

