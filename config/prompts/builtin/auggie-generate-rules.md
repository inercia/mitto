---
name: "Generate rules"
description: "Analyze workspace and generate comprehensive Augment rules files"
acps: auggie
backgroundColor: "#1b0bc693"
---

Analyze this workspace and generate comprehensive `.augment/rules` files that will help
future AI interactions understand and work effectively with this codebase.

## Step 1: Explore the Project

Thoroughly examine the codebase to understand:

### Project Overview
- What does this project do? What problem does it solve?
- What is the main functionality and key features?
- Who are the intended users?

### Directory Structure
- What are the key directories and their purposes?
- How are files organized (by feature, by layer, by type)?
- Where are entry points, configurations, tests, and documentation?

### Technology Stack
- Programming languages used (primary and secondary)
- Frameworks and major libraries
- Build tools, package managers, task runners
- Testing frameworks and tools
- Linting, formatting, and code quality tools

### Architecture (for software projects)
- Main modules/packages and their responsibilities
- Dependencies and relationships between components
- Entry points and data flow patterns
- Design patterns used (MVC, Repository, Factory, etc.)
- API structure (REST, GraphQL, RPC, etc.)

### Code Conventions
- Naming conventions (files, functions, classes, variables)
- Import organization and grouping
- Comment styles and documentation patterns
- Formatting rules (indentation, line length, etc.)
- Error handling patterns

### Development Patterns
- How tests are organized and written
- Configuration management approach
- Environment handling (dev, staging, prod)
- Logging and debugging patterns
- Common idioms specific to this codebase

## Step 2: Generate Rules Files

Create `.augment/rules/` files following this format:

### File Structure

Each file should have YAML frontmatter:

```markdown
---
description: Brief description of what this rule covers
globs:
  - "**/*.ext"  # File patterns this rule applies to
alwaysApply: true  # Optional: if this rule should always be loaded
---

# Rule Title

Content goes here...
```

### Recommended Files

Create files numbered for logical ordering:

1. **00-overview.md** - Project overview and architecture
   - Set `alwaysApply: true` and `globs: **/*`
   - Include project purpose, package structure, key concepts

2. **01-[language]-conventions.md** - Primary language conventions
   - Naming, formatting, idioms, error handling
   - Code examples showing correct patterns

3. **02-[major-component].md** - Rules for major components
   - One file per significant subsystem
   - Architecture, patterns, common operations

4. **0N-testing.md** - Testing conventions (if relevant)
   - Test organization, naming, patterns
   - How to run tests, coverage targets

5. **0N-config.md** - Configuration patterns (if relevant)
   - Config file formats, environment variables
   - How settings are loaded and used

### Content Guidelines

- **Be specific**: Include file paths, function names, exact patterns
- **Show examples**: Code snippets demonstrating correct usage
- **Explain why**: Document rationale for conventions
- **Make it actionable**: Rules should guide actual coding decisions
- **Keep focused**: Each file covers one cohesive topic
- **Use tables**: For quick reference (command lists, pattern summaries)
- **Include common pitfalls**: What to avoid and why

## Step 3: Create the Files

1. Create the `.augment/rules/` directory if it doesn't exist
2. Generate each rules file with appropriate content
3. Ensure files are well-organized and not too long (aim for <200 lines each)
4. Cross-reference between files where relevant

## Important Notes

- Focus on patterns that will help AI assistants write better code
- Prioritize actionable guidance over exhaustive documentation
- Include "don't do this" examples for common mistakes
- Reference existing documentation in the repo rather than duplicating it
- If the project already has `.augment/rules/`, enhance rather than replace
