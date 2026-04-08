---
name: "Generate memory"
description: "Analyze workspace and generate comprehensive Claude Code memory files"
group: "Agents & Mitto"
enabledWhenACP: claude-code
backgroundColor: "#1b0bc693"
---


Analyze this workspace and generate Claude Code memory files for effective future AI interactions.


<efficiency>
Read multiple files in parallel (configs, entry points, tests).
</efficiency>



## Step 1: Explore the Project

Examine:
- **Overview**: Purpose, functionality, users
- **Directory structure**: Key directories, file organization, entry points
- **Tech stack**: Languages, frameworks, build tools, test tools, linters
- **Architecture**: Modules, dependencies, data flow, design patterns, API structure
- **Conventions**: Naming, imports, comments, formatting, error handling
- **Dev patterns**: Test organization, config management, environments, logging

## Step 2: Generate Memory Files

### Option A: Single `./CLAUDE.md` (smaller projects)

```markdown
# Project Name
Brief description.
## Build & Test Commands
- `<command>` - description
## Code Style
- 
## Architecture
- <structure>
```

### Option B: Modular (larger projects)

```
.claude/
├── CLAUDE.md           # Overview and common commands
└── rules/
    ├── code-style.md   # Style guidelines
    ├── testing.md      # Testing conventions
    └── api.md          # API rules
```

### File Format

For `.claude/rules/*.md`, use YAML frontmatter with `paths` for conditional rules:

```markdown
---
paths:
  - "src/api/**/*"
---
# API Rules
- All endpoints must include input validation
```

Rules without `paths` load unconditionally.

### Content Guidelines

- Be specific: "Use 2-space indentation" not "Format properly"
- Use bullet points
- Group with headings
- Include commands, examples, rationale

## Step 3: Create Files

1. Create `.claude/` if using modular rules
2. Generate main `CLAUDE.md`
3. Generate topic-specific rules if needed
4. Keep files <200 lines each

## Guidelines

- Focus on patterns that help AI assistants write better code
- Prioritize actionable guidance over exhaustive documentation
- Include anti-pattern examples
- Reference existing repo docs rather than duplicating
- If `CLAUDE.md` exists, enhance rather than replace
- Use `CLAUDE.local.md` for personal preferences (not committed)
