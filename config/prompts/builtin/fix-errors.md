---
name: "Fix errors"
description: "Analyze and fix the errors shown"
group: "Development"
backgroundColor: "#FFE0B2"
---

<investigate_before_answering>
Read relevant source files to understand code context around each error.
Do not speculate about code you haven't opened.
</investigate_before_answering>

<task>
Analyze and fix the errors shown.
</task>

<scope>
Only fix the identified errors. Keep changes minimal and focused on root causes.
</scope>

<solution_quality>
Fix root causes, not symptoms. Ensure fixes work for all valid inputs.
Report deeper design issues rather than applying narrow workarounds.
</solution_quality>

<instructions>

### Per error:

1. **Identify**: Quote exact error message
2. **Diagnose**: Root cause — what triggered it, why it failed
3. **Fix**: Implement and explain the change
4. **Verify**: Run code/tests, check for related issues

### Multiple errors:

- Fix in dependency order (causes before symptoms)
- Group errors sharing a root cause
- Final verification after all fixes

</instructions>
