---
name: "Improve memory"
description: "Update Claude Code memory files based on recent conversations"
acps: claude-code
backgroundColor: "#1b0bc693"
---

Review and update the Claude Code memory files based on all insights,
patterns, and lessons learned from our recent conversations and code changes.

## Memory File Locations

Claude Code uses these memory files (check which exist):
- `./CLAUDE.md` or `./.claude/CLAUDE.md` - Project memory (shared with team)
- `./.claude/rules/*.md` - Modular project rules
- `./CLAUDE.local.md` - Personal project preferences (not committed)

## What to Update

1. Add any new architectural patterns or components that have been introduced
2. Document new conventions, best practices, or anti-patterns discovered during implementation
3. Update existing sections if they are outdated or incomplete
4. Add new sections for areas not currently covered (e.g., new packages, APIs, patterns)
5. Ensure examples reflect the current codebase state

## Guidelines

- Focus on actionable guidance that will help future development sessions
- Do not remove existing valid content - only add or update information
- Keep rules focused: each file should cover one topic
- Use bullet points for individual instructions
- Group related memories under descriptive markdown headings
