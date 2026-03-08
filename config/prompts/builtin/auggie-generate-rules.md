---
name: "Generate rules"
description: "Analyze workspace and generate comprehensive Augment rules files"
group: "Agents & Mitto"
acps: auggie
backgroundColor: "#1b0bc693"
---

<task>
Analyze this workspace and generate `.augment/rules` files for effective future AI interactions.
</task>

<efficiency>
Read multiple files in parallel (configs, entry points, tests).
</efficiency>

<instructions>

## Step 1: Explore the Project

Examine:
- **Overview**: Purpose, functionality, users
- **Directory structure**: Key directories, file organization, entry points
- **Tech stack**: Languages, frameworks, build tools, test tools, linters
- **Architecture**: Modules, dependencies, data flow, design patterns, API structure
- **Conventions**: Naming, imports, comments, formatting, error handling
- **Dev patterns**: Test organization, config management, environments, logging

## Step 2: Generate Rules Files

### File Structure

```markdown
---
description: Brief description
globs:
  - "**/*.ext"
alwaysApply: true  # Optional
---
# Rule Title
Content...
```

### Recommended Files

1. **00-overview.md** - Project overview (`alwaysApply: true`, `globs: **/*`)
2. **01-[language]-conventions.md** - Naming, formatting, idioms, error handling with examples
3. **02-[component].md** - Per major subsystem: architecture, patterns, operations
4. **0N-testing.md** - Test organization, naming, patterns, how to run
5. **0N-config.md** - Config formats, env variables, settings loading

### Content Guidelines

- Be specific: include file paths, function names, exact patterns
- Show code examples demonstrating correct usage
- Explain rationale for conventions
- Make rules actionable for coding decisions
- One cohesive topic per file
- Use tables for quick reference
- Include common pitfalls

## Step 3: Create Files

1. Create `.augment/rules/` if needed
2. Generate each file (<200 lines)
3. Cross-reference between files

</instructions>

<rules>
- Focus on patterns that help AI assistants write better code
- Prioritize actionable guidance over exhaustive documentation
- Include anti-pattern examples
- Reference existing repo docs rather than duplicating
- If `.augment/rules/` exists, enhance rather than replace
</rules>
