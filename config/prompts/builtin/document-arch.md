---
name: "Document Architecture"
description: "Update developer/architecture documentation for the changes we just made"
group: "Documentation"
backgroundColor: "#CE93D8"
---

<investigate_before_answering>
Before updating any documentation, read the code changes we made and any existing
architecture documentation. Understand what changed and how it affects the system's
design before writing or updating docs.
</investigate_before_answering>

<task>
Update the developer and architecture documentation to reflect the changes we just made.
</task>

<instructions>

### Find and update:

1. **New packages/modules**: Add to architecture docs, create dedicated doc if complex
2. **API changes**: Update relevant docs with new interfaces, methods, types
3. **Flow changes**: Update diagrams if the project uses them
4. **Design decisions**: Document rationale for non-obvious choices
5. **New patterns**: Add examples and guidelines

### Guidelines:

- Write for developers contributing to the codebase
- Include code examples showing patterns to follow when relevant
- Document design decisions and trade-offs, because future contributors need to understand the reasoning behind architectural choices
- Follow the existing documentation style and structure
- Cross-reference related documentation
- Use mermaid diagrams when helpful

</instructions>
