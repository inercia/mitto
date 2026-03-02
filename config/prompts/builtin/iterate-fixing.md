---
name: "Iterate fixing"
description: "Continue iterating to fix the problem we have been working on"
backgroundColor: "#BBDEFB"
---

# Preparation

Let's start by defining the state file, a markdown file with name
`implement-<problem>-<date>.md` (where <problem> is a short description of
the problem we have been working on, and <date> is the current date in `YYYY-MM-DD` format)

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

The main task is to continue fixing the problems we have been working on.

Start by reading the state file to refresh your memory about the problem.
Read about the problem in case you forgot.
Then read about the progress and what has been done so far.

In case you do not find the state file, please create it following the template above.
Save the problem description as well as the progress you have made so far, and
the remaining issues that still need to be implemented.

Once you have a clear idea of the problem and what has been done so far
and what is missing.
Continue with the following steps:

1. Review the current state and what we have tried and what is still failing.
2. Analyze ALL remaining issues that prevent the problem from being fully fixed
3. Prioritize the problems and investigate the problem.
4. Implement the fix
5. Verify the feature(s) have been fully implemented
6. Report progress in the state file, and also update the "Issues remaining" there.

Once you think the problem is completely fixed, review the state file
and make sure the problem is completely resolved as it as originally described.
