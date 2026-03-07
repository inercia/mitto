---
name: "Iterate implementing"
description: "Continue iterating to implement the feature we have been working on"
group: "Development"
backgroundColor: "#BBDEFB"
---

<investigate_before_answering>
Before making any changes, read the state file and the relevant source files to
understand the current state of the implementation. Do not speculate about code
you have not opened — read it first.
</investigate_before_answering>

<task>
Continue implementing the features we have been working on, using a state file to
track progress across iterations.
</task>

<scope>
Only implement what is described in the problem specification. Keep changes focused
and do not add features, abstractions, or improvements beyond what was specified.
</scope>

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
issues that still need to be implemented.

Once you have a clear picture of the problem, what has been done, and what
is still missing, continue with the following steps:

1. Review the current state and what is still missing
2. Analyze all remaining issues that prevent the feature from being fully implemented
3. Prioritize the issues and identify the next work item
4. Implement that work item
5. Verify the implementation works correctly
6. Update the state file — report progress and update "Issues remaining"

Once you believe the implementation is complete, review the state file
and verify the problem is fully resolved as originally described.

</instructions>
