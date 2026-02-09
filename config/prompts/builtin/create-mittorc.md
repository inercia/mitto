---
name: "Create mittorc"
description: "Set up .mitto directory structure and generate project-specific prompts"
backgroundColor: "#B3E5FC"
---

Set up the Mitto configuration for this project by creating the `.mitto` directory structure
and analyzing the project to generate useful, project-specific prompts.

## Step 1: Create Directory Structure

Create the following directory structure in the project root:

```
.mitto/
└── prompts/
```

1. Create the `.mitto` directory if it doesn't exist
2. Create the `.mitto/prompts` subdirectory for project-specific prompts

## Step 2: Create the `.mittorc` File

Create a `.mittorc` file in the project root with the following content:

```yaml
# Mitto workspace configuration
# See: https://github.com/inercia/mitto/blob/main/docs/config/prompts.md

prompts_dirs:
  - ".mitto/prompts"
```

This tells Mitto to look for prompt files in the `.mitto/prompts` directory.

## Step 3: Analyze the Project

Before creating any prompts, analyze the project to understand:

### Build & Development
- What build system or package manager is used? (npm, yarn, cargo, go, make, etc.)
- What are the common development commands? (build, dev server, watch mode)
- Are there any custom scripts defined?

### Testing
- What testing framework is used?
- How are tests run? (unit tests, integration tests, e2e tests)
- Are there specific test patterns or commands?

### Code Quality
- What linters or formatters are configured?
- How is code quality checked? (lint, format, typecheck)

### Deployment & CI/CD
- Are there deployment scripts or commands?
- Is there a CI/CD configuration?

### Project-Specific Workflows
- Are there any domain-specific commands or workflows?
- Are there common tasks specific to this project type?

## Step 4: Create Project-Specific Prompts (If Needed)

**IMPORTANT**: Only create prompts if they would be genuinely useful for this specific project.
Do NOT create generic prompts that would apply to any project.

### Criteria for Creating a Prompt

Create a prompt only if:
- The project has specific, non-obvious commands or workflows
- The project uses custom tooling that needs explanation
- There are project-specific patterns that an AI assistant should follow
- The workflow is complex enough that a prompt would save time

### Do NOT Create Prompts If

- The project uses standard tooling with obvious commands
- A built-in prompt already covers the use case
- The prompt would be too generic (e.g., "Run tests" for a standard npm project)

### Prompt File Format

If you determine that project-specific prompts are warranted, create them in
`.mitto/prompts/` using this format:

```markdown
---
name: "Descriptive Name"
description: "What this prompt does"
backgroundColor: "#E8F5E9"
---

The actual prompt content goes here.
Explain what the AI should do and include any project-specific details.
```

### Example Good Prompts

- **Project with custom build pipeline**: Prompt explaining the multi-stage build process
- **Monorepo**: Prompt for running tests in a specific package
- **Project with database migrations**: Prompt for creating and running migrations
- **Project with specific deployment process**: Prompt for deployment steps

### Example Bad Prompts (Don't Create)

- "Run npm test" for a standard npm project
- "Build the project" with no special instructions
- Generic "fix bugs" or "add tests" prompts

## Summary

After completing this setup:

1. ✅ `.mitto/` directory created
2. ✅ `.mitto/prompts/` directory created
3. ✅ `.mittorc` file created with `prompts_dirs` configuration
4. ✅ Project analyzed for specific needs
5. ✅ Project-specific prompts created (only if genuinely useful)

If no project-specific prompts were needed, that's perfectly fine! The directory
structure is ready for adding prompts later when the need arises.

