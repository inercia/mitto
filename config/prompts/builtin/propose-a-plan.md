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

### Present Plan for Review

Generate the plan, then present it for review and editing.

**With Mitto UI**: Present the plan in a textbox so the user can edit it directly:

```
mitto_ui_textbox(self_id: "@mitto:session_id",
  title: "Review Plan — edit before approving",
  text: "<generated-plan-markdown>",
  result: "edited_text")
```

- If `changed == true`: use the edited plan as the approved version.
- If `aborted == true`: ask the user what they'd like to change, revise, and present again.

Then confirm execution:

```
mitto_ui_options(self_id: "@mitto:session_id",
  question: "Plan reviewed. Proceed?",
  options: [{label: "Execute the plan"}, {label: "Revise further"}, {label: "Cancel"}])
```

**Without**: Present plan in conversation and ask for approval.
