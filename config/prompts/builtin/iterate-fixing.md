---
name: "Iterate fixing"
description: "Continue iterating to fix the problem we have been working on"
group: "Development"
backgroundColor: "#BBDEFB"
---

<investigate_before_answering>
Before making any changes, read the state file and the relevant source files to
understand the current state of the problem. Do not speculate about code you have
not opened — read it first.
</investigate_before_answering>

<task>
Continue fixing the problems we have been working on, using a state file to track
progress across iterations.
</task>

<scope>
Only fix the problems identified. Keep changes minimal and focused on the root cause.
Do not refactor surrounding code or add improvements beyond what is needed to resolve
the issues.
</scope>

<solution_quality>
Implement fixes that address the root cause, not just the symptoms. Ensure fixes work
correctly for all valid inputs, not just the specific case that triggered the error.
If a problem reveals a deeper design issue, report it rather than applying a narrow
workaround.
</solution_quality>

<instructions>

# Preparation

Start by defining the state file, a markdown file with name
`implement-<problem>-<date>.md` (where `<problem>` is a short description of
the problem we have been working on, and `<date>` is the current date in `YYYY-MM-DD` format).

The contents of the state file should follow this template:

```markdown
# The problem

[problem description]

# Progress

## [num] [issue title]

[issue description]

[fix applied]

# Issues remaining

## [num] [issue title]
```

# The task

Start by reading the state file to refresh your memory about the problem.
Read about the problem description and understand what it requires.
Then read about the progress and what has been done so far.

If you do not find the state file, create it following the template above.
Save the problem description, the progress made so far, and the remaining
issues that still need to be fixed.

Once you have a clear picture of the problem, what has been done, and what
is still missing, continue with the following steps:

1. Review the current state and what we have tried and what is still failing
2. Analyze all remaining issues that prevent the problem from being fully fixed
3. Prioritize the problems and investigate the root cause
4. Implement the fix
5. Verify the fix resolves the issue
6. Update the state file — report progress and update "Issues remaining"

Once you believe the problem is completely fixed, review the state file
and verify the problem is fully resolved as originally described.

</instructions>
