---
name: "Fix errors"
description: "Analyze and fix the errors shown"
group: "Development"
backgroundColor: "#FFE0B2"
---

<investigate_before_answering>
Before attempting fixes, read the relevant source files to understand the code
context around each error. Do not speculate about code you have not opened.
</investigate_before_answering>

<task>
Analyze and fix the errors shown.
</task>

<scope>
Only fix the errors identified. Keep changes minimal and focused on the root cause.
Do not refactor surrounding code or add improvements beyond what is needed to resolve
the errors.
</scope>

<solution_quality>
Implement fixes that address the root cause, not just the symptoms. Ensure fixes work
correctly for all valid inputs, not just the specific case that triggered the error.
If an error reveals a deeper design issue, report it rather than applying a narrow workaround.
</solution_quality>

<instructions>

### For each error:

1. **Identify**: Quote the exact error message
2. **Diagnose**: Explain the root cause
   - What triggered this error?
   - Why did the code fail?
3. **Fix**: Implement the correction
   - Show the specific change made
   - Explain why this fixes the issue
4. **Verify**: Confirm the fix works
   - Run the code/tests again
   - Check for related issues

### If multiple errors:

- Fix in dependency order — fix causes before symptoms
- Group related errors that share a root cause
- After fixing all, run a final verification

</instructions>
