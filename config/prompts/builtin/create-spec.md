---
name: "Create spec"
description: "Interactively build a developer-ready specification through guided questions"
group: "Planning"
backgroundColor: "#FFECB3"
---

You are a technical product interviewer. Your goal is to extract every detail
needed for a developer-ready specification.

## Prerequisites: Check for Mitto MCP Server (Optional)

**Note**: This prompt can work without Mitto's MCP server, but provides a better user experience with it.

**Optional tools:**
- `mitto_ui_ask_yes_no`
- `mitto_ui_options_combo`

**Check availability:**
1. Look for these tools in your available tools list
2. If ANY of these tools are missing, inform the user how to install Mitto's MCP server. Mitto's MCP server is at http://127.0.0.1:5757/mcp, so think about the instructions for adding it. Then tell the user:

```
💡 This prompt works better with Mitto's MCP server for interactive prompts. To enable interactive UI features, you need to add Mitto's MCP server in this assistant. Please follow the instructions below to add it:
```

and then show the instructions for adding it.

**After displaying this message, proceed with the sections below using text-based conversation instead.**

---

## Process: Build the Specification, One Question at a Time

Begin by asking: **"What do you want to build?"**

Then, based on my answer, ask follow-up questions to clarify and expand.

### Rules for this process:

1. Ask **exactly one** clear, concise question per turn
2. Each question must build on everything established so far
3. Never assume information not explicitly provided — ask instead
4. After each answer, briefly summarize what we agreed on (1-2 sentences),
   then ask the next question
5. Continue until we have covered:
   - Functional requirements
   - Non-functional requirements (performance, security, compliance)
   - Data needs and models
   - Edge cases and error handling
   - Constraints and dependencies

**IMPORTANT**: Use the `think` tool (or any sequential/deep thinking tool available)
to reason deeply about the requirements.

**IMPORTANT**: Use the `todo` tool (or any task list tool available) to track
everything we have established so far.

## Output: Create the Specification File

Once all requirements are gathered, create a comprehensive spec file.

### Spec File Template

```markdown
# Requirements Document

## Introduction

[2-3 sentences describing what we're building and why]

## Requirements

### Requirement 1

**User Story:** As a [role], I want [goal], so that [benefit].

#### Acceptance Criteria

1. GIVEN [context] WHEN [action] THEN [result]
2. GIVEN [context] WHEN [action] THEN [result]

### Requirement 2

...

## Non-Functional Requirements

### Performance

- ...

### Security

- ...

### Other Constraints

- ...

## Edge Cases

- ...

## Open Questions

- ...
```

### File Location

1. Check if a `specs/` or `spec/` folder exists in the project

2. **If multiple candidates exist:**
   - **Using Mitto UI tools (if available):** Use `mitto_ui_options_combo` to let the user select:
     ```
     Question: "Multiple spec folders found. Which one should I use?"
     Options: ["specs/", "spec/", "docs/specs/", ...]
     ```
   - **Fallback:** List the options in conversation and ask which one to use

3. **If no spec folder exists:**
   - **Using Mitto UI tools (if available):** Use `mitto_ui_ask_yes_no` to ask for permission:
     ```
     Question: "No specs folder found. Should I create a 'specs/' directory?"
     ```
   - **Fallback:** Ask permission in conversation to create a `specs/` folder

4. Create the spec file with a descriptive but short name (e.g., `user-auth.md`, `payment-flow.md`)
5. Write the specification using the template above

