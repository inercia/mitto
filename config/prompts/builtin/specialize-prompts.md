---
name: "Specialize prompts"
description: "Analyze and specialize workspace prompts for this project"
group: "Agents & Mitto"
backgroundColor: "#B3E5FC"
---

Specialize the available prompts for this workspace by analyzing the project and tailoring
generic prompts to its specific technologies, commands, and workflows.

Use Mitto's MCP tools to list, inspect, and update prompts interactively.

## Step 1: List all prompts

Use `mitto_prompt_list` to retrieve all prompts available in this workspace.

## Step 2: Analyze the project

Before proposing specializations, understand the project:

- Read build configuration files (Makefile, package.json, go.mod, Cargo.toml, pyproject.toml, etc.)
- Identify test frameworks, test commands, and CI/CD setup
- Note linters, formatters, and code quality tools
- Identify project-specific workflows and domain conventions

Read multiple files in parallel to gather this context quickly.

## Step 3: Identify candidates for specialization

Review the prompt list from Step 1. For each prompt, consider whether it could benefit
from project-specific knowledge. Good candidates are prompts that:

- Reference generic commands that could be replaced with project-specific ones (e.g., "run tests" → specific test commands)
- Describe generic workflows that could include project-specific steps
- Could include project conventions, file paths, or tool configurations

Skip prompts that are already workspace-specific (source: "workspace") or that are
inherently generic (e.g., "Continue", "Explain").

## Step 4: Interactive specialization

For each candidate prompt identified in Step 3:

1. Use `mitto_prompt_get` to retrieve the full current prompt text.

2. Present to the user:
   - The prompt **name** and current **description**
   - A brief summary of the current prompt text
   - Your **proposed specialization**: explain *what* you would change and *why*,
     based on what you learned about the project in Step 2

3. Use `mitto_ui_options` to ask the user what to do:
   - **"Specialize as proposed"** — Apply the suggested specialization
   - **"Skip this prompt"** — Leave it unchanged and move to the next
   - **"Let me provide instructions"** — Allow the user to type custom instructions
     (set `allow_free_text: true` and `free_text_placeholder: "Describe how to specialize this prompt..."`)

4. If the user confirms:
   - Use `mitto_prompt_update` to save the specialized version.
     Set the `prompt` field with the new text, and optionally update `description`.
     Keep the same `name` so it overrides the original.

5. If the user provides custom instructions:
   - Incorporate their feedback into the specialization, then use `mitto_prompt_update`.

6. Continue to the next candidate until all have been processed.

## Step 5: Summary

After all candidates have been processed, present a summary:

| Prompt | Action | Details |
|--------|--------|---------|
| Run tests | ✅ Specialized | Added project-specific test commands |
| Add tests | ⏭️ Skipped | User chose to skip |
| Fix CI | ✅ Specialized | Added CI workflow details |
| ... | ... | ... |

Report the total number specialized vs. skipped.
