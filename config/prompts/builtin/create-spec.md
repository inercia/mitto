---
name: "Create spec"
description: "Interactively build a developer-ready specification through guided questions"
group: "Planning"
backgroundColor: "#FFECB3"
---

<task>
Act as a technical product interviewer. Extract every detail needed for a
developer-ready specification through guided Q&A.
</task>

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: Works without Mitto's MCP server, but provides a better experience with it.

**Optional tools:**
- `mitto_ui_ask_yes_no`
- `mitto_ui_options_combo`

If missing, show instructions for adding Mitto's MCP server at http://127.0.0.1:5757/mcp, then proceed without interactive features.

---

<instructions>

## Process

Begin: **"What do you want to build?"**

Then follow-up based on answers:
1. One clear question per turn
2. Each question builds on what's established
3. Ask rather than assume
4. After each answer, briefly summarize (1-2 sentences), then next question
5. Cover: functional requirements, non-functional (performance, security), data models, edge cases, error handling, constraints, dependencies

Use the `think` tool for deep reasoning. Use the `todo` tool to track what's established.

## Output

Create a spec file:

```markdown
# Requirements Document

## Introduction
[2-3 sentences: what and why]

## Requirements

### Requirement 1
**User Story:** As a [role], I want [goal], so that [benefit].
#### Acceptance Criteria
1. GIVEN [context] WHEN [action] THEN [result]

## Non-Functional Requirements
### Performance
### Security
### Other Constraints

## Edge Cases

## Open Questions
```

### File Location

1. Check for `specs/` or `spec/` folder
2. Multiple candidates: **With Mitto UI**: `mitto_ui_options_combo` to select. **Without**: list and ask.
3. No folder exists: **With Mitto UI**: `mitto_ui_ask_yes_no` to create `specs/`. **Without**: ask permission.
4. Create file with short descriptive name (e.g., `user-auth.md`)

</instructions>
