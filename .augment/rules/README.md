# Augment Rules - Quick Reference

## What Are Rules Files?

Rules files provide context-specific guidance to AI assistants working on the Mitto codebase. They are automatically loaded based on file paths, keywords, or always applied.

## How Rules Are Triggered

### 1. File Path Patterns (globs)

Rules are loaded when working on files matching the glob pattern:

```yaml
---
description: Markdown-to-HTML conversion
globs:
  - "internal/conversion/**/*"
---
```

### 2. Keywords

Rules are loaded when specific terms appear in prompts:

```yaml
---
description: Regular expression patterns
keywords:
  - regex
  - pattern
  - URL detection
---
```

### 3. Always Apply

Some rules are always loaded:

```yaml
---
description: Project overview
alwaysApply: true
---
```

## Rules Organization

### File Numbering

- **00-09**: Core concepts (overview, Go conventions, core packages, macOS app)
- **10-19**: Backend (web server, sequences, actions, macOS keyboard/gestures)
- **20-29**: Frontend (components, state, hooks)
- **30-39**: Testing (unit, integration, UI, anti-patterns)
- **40-49**: Debugging (MCP tools, log files, MCP server development)
- **98-99**: Local/private (not committed)

### Quick Index

| Range | Topic           | Example Files                                                                          |
| ----- | --------------- | -------------------------------------------------------------------------------------- |
| 00-09 | Core            | `00-overview.md`, `01-go-conventions.md`, `09-macos-app.md`                            |
| 10-19 | Backend + macOS | `10-web-backend-core.md`, `13-macos-keyboard-gestures.md`, `15-web-backend-session-lifecycle.md` |
| 20-29 | Frontend        | `20-web-frontend-core.md`, `21-web-frontend-state.md`, `27-web-frontend-sync.md`       |
| 30-39 | Testing         | `30-testing-unit.md`, `34-anti-patterns.md`                                            |
| 40-49 | Debugging       | `40-mcp-debugging.md`, `41-debugging-logs.md`, `42-mcpserver-development.md`           |
| 98-99 | Private         | `98-release.md`, `99-local.md` *(not committed)*                                       |

## When to Update Rules

### Add to Existing File

When:

- Adding examples to existing patterns
- Documenting edge cases
- Updating existing features
- Adding related content

### Create New File

When:

- Topic is distinct and self-contained
- Existing file would become too large (>300 lines)
- Topic has specific trigger conditions
- Multiple packages share the pattern

## Rules File Template

```markdown
---
description: Brief description of what this file covers
globs:
  - "path/to/files/**/*"
  - "another/path/*.go"
keywords:
  - keyword1
  - keyword2
---

# Title

## Section 1

Content with examples...

\`\`\`go
// Code example
\`\`\`

## Section 2

More content...
```

## Best Practices

### Writing Rules

1. **Be specific**: Target exact scenarios, not general advice
2. **Use examples**: Show code, not just descriptions
3. **Show anti-patterns**: Document what NOT to do
4. **Keep focused**: One topic per file
5. **Update regularly**: Keep examples current

### Organizing Content

1. **Start with overview**: What is this about?
2. **Show patterns**: How to do it right
3. **Show anti-patterns**: How NOT to do it
4. **Provide examples**: Real code snippets
5. **Link to docs**: Reference detailed documentation

### Triggers

1. **Use specific globs**: Target exact files/directories
2. **Use relevant keywords**: Terms developers actually use
3. **Avoid over-triggering**: Don't make files too broad
4. **Test triggers**: Verify rules load when expected

## Common Patterns

### Code Example Format

```markdown
### ❌ Don't: Description

\`\`\`go
// BAD: Explanation
func badExample() {
// Wrong approach
}
\`\`\`

### ✅ Do: Description

\`\`\`go
// GOOD: Explanation
func goodExample() {
// Right approach
}
\`\`\`
```

### Table Format

```markdown
| Component | Purpose       |
| --------- | ------------- |
| Item 1    | Description 1 |
| Item 2    | Description 2 |
```

### Section Organization

```markdown
## Main Topic

### Subtopic 1

Content...

### Subtopic 2

Content...

## Related Topic

Content...
```

## Maintenance

### Quarterly Review

- Check for outdated examples
- Update code snippets
- Add new patterns discovered
- Remove obsolete content

### After Major Features

- Document new patterns
- Add anti-patterns discovered
- Update coverage targets
- Add test strategies

### When Code Changes

- Update examples to match current code
- Fix broken references
- Update package names/paths
- Verify globs still match

## Getting Help

- See `00-overview.md` for project structure
- See `34-anti-patterns.md` for common mistakes
- See specific topic files for detailed guidance
- Check `docs/devel/` for architecture details

## Quick Links

- [Overview](.augment/rules/00-overview.md)
- [Go Conventions](.augment/rules/01-go-conventions.md)
- [Testing](.augment/rules/30-testing-unit.md)
- [Anti-Patterns](.augment/rules/34-anti-patterns.md)
- [MCP Debugging](.augment/rules/40-mcp-debugging.md)
- [Log File Debugging](.augment/rules/41-debugging-logs.md)
