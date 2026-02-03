---
name: "Generate memory"
description: "Analyze workspace and generate comprehensive Claude Code memory files"
acps: claude-code
backgroundColor: "#1b0bc693"
---

Analyze this workspace and generate comprehensive Claude Code memory files that will help
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

## Step 2: Generate Memory Files

Create Claude Code memory files following this structure:

### Option A: Single CLAUDE.md (for smaller projects)

Create `./CLAUDE.md` with all project instructions:

```markdown
# Project Name

Brief description of what this project does.

## Build & Test Commands
- `npm install` - Install dependencies
- `npm test` - Run tests
- `npm run build` - Build for production

## Code Style
- Use 2-space indentation
- Prefer const over let
- ...

## Architecture
- ...
```

### Option B: Modular Rules (for larger projects)

Create `.claude/CLAUDE.md` for overview and `.claude/rules/` for topic-specific rules:

```
.claude/
├── CLAUDE.md           # Main project overview and common commands
└── rules/
    ├── code-style.md   # Code style guidelines
    ├── testing.md      # Testing conventions
    ├── api.md          # API development rules
    └── security.md     # Security requirements
```

### File Format

For `.claude/rules/*.md` files, use YAML frontmatter with `paths` for conditional rules:

```markdown
---
paths:
  - "src/api/**/*.ts"
  - "src/services/**/*.ts"
---

# API Development Rules

- All API endpoints must include input validation
- Use the standard error response format
- Include OpenAPI documentation comments
```

Rules without `paths` frontmatter are loaded unconditionally.

### Glob Patterns for `paths`

| Pattern | Matches |
|---------|---------|
| `**/*.ts` | All TypeScript files in any directory |
| `src/**/*` | All files under `src/` directory |
| `*.md` | Markdown files in the project root |
| `src/**/*.{ts,tsx}` | TypeScript and TSX files in src |

### Content Guidelines

- **Be specific**: "Use 2-space indentation" not "Format code properly"
- **Use bullet points**: Format each instruction as a bullet point
- **Group related items**: Use markdown headings to organize
- **Include commands**: Document build, test, lint commands
- **Show examples**: Code snippets demonstrating correct usage
- **Explain why**: Document rationale for conventions

## Step 3: Create the Files

1. Create `.claude/` directory if using modular rules
2. Generate the main `CLAUDE.md` file
3. Generate topic-specific rules in `.claude/rules/` if needed
4. Ensure files are well-organized and not too long (aim for <200 lines each)

## Important Notes

- Focus on patterns that will help AI assistants write better code
- Prioritize actionable guidance over exhaustive documentation
- Include "don't do this" examples for common mistakes
- Reference existing documentation in the repo rather than duplicating it
- If the project already has `CLAUDE.md`, enhance rather than replace
- Use `CLAUDE.local.md` for personal preferences that shouldn't be committed
