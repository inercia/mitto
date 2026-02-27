---
name: "Improve rules"
description: "Update Augment rules based on recent conversations and code changes"
acps: auggie
backgroundColor: "#1b0bc693"
---

# General instructions

Review and update the Augment rules in `.augment/rules` based on all insights,
patterns, and lessons learned from our recent conversations and code changes.
Specifically:

1. Add any new architectural patterns or components that have been introduced
2. Document new conventions, best practices, or anti-patterns discovered during implementation
3. Update existing sections if they are outdated or incomplete
4. Add new sections for areas not currently covered (e.g., new packages, APIs, frontend patterns)
5. Ensure examples reflect the current codebase state

Focus on actionable guidance that will help future development sessions.
Do not remove existing valid content - only add or update information.

# Reorganize rules files

Once you have a good understanding of the existing rules files, consider if it
would make sense to reorganize the .augment/rules/ files in order to optimize
automatic context inclusion.

Specifically:

* Analyze each existing rules file and identify sections that could
  be split into separate, more focused files
* Split large rules files into smaller, scope-specific files
  (e.g., separate files for different packages, features, or concerns)
* Update each file's trigger description (the "If the user prompt matches..."
  condition) to be specific and targeted, ensuring rules are only included when truly relevant
* Keep related content together but ensure no single file is excessively long
* Update the global rules file to reflect any new file structure
* Ensure file naming follows a logical numbering/naming convention

The goal is to have focused rules files that are automatically loaded
only when their specific topic is being worked on, reducing context noise
and improving relevance.

Do not touch things if you are not sure.
