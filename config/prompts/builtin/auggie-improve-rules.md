---
name: "Improve rules"
description: "Update Augment rules based on recent conversations and code changes"
group: "Agents & Mitto"
acps: auggie
backgroundColor: "#1b0bc693"
---

<task>
Review and update `.augment/rules` based on insights and lessons from recent
conversations and code changes.
</task>

<instructions>

## Update Content

1. Add new architectural patterns or components introduced
2. Document new conventions, best practices, or anti-patterns discovered
3. Update outdated or incomplete sections
4. Add sections for uncovered areas (new packages, APIs, patterns)
5. Ensure examples reflect current codebase

Focus on actionable guidance. Preserve existing valid content.

**Balance knowledge and length**: Optimize the total length of all rules files while preserving essential knowledge:
- Remove redundant or outdated information
- Consolidate overlapping sections
- Keep examples concise but illustrative
- Prioritize high-value patterns over exhaustive coverage

## Reorganize Files

Consider splitting large files into focused, scope-specific files:

- Analyze each file for sections that could be separate files
- Split by package, feature, or concern
- Update trigger descriptions to be specific and targeted
- Keep related content together; avoid excessively long files
- Follow logical numbering/naming convention

Goal: focused rules files loaded only when their topic is relevant.

</instructions>

<rules>
- Preserve existing valid content — only add or update
- Be cautious with uncertain changes
</rules>
