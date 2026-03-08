---
name: "Improve memory"
description: "Update Claude Code memory files based on recent conversations"
group: "Agents & Mitto"
acps: claude-code
backgroundColor: "#1b0bc693"
---

<task>
Update Claude Code memory files based on insights from recent conversations and code changes.
</task>

<instructions>

## Memory File Locations

- `./CLAUDE.md` or `./.claude/CLAUDE.md` - Project memory (shared)
- `./.claude/rules/*.md` - Modular project rules
- `./CLAUDE.local.md` - Personal preferences (not committed)

## What to Update

1. Add new architectural patterns or components
2. Document new conventions, best practices, anti-patterns
3. Update outdated or incomplete sections
4. Add sections for uncovered areas
5. Ensure examples reflect current codebase

## Guidelines

- Focus on actionable guidance for future sessions
- Preserve existing valid content — only add or update
- One topic per file, bullet points, descriptive headings

</instructions>
