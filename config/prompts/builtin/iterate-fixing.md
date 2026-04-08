---
name: "Iterate fixing"
description: "Continue iterating to fix the problem we have been working on"
group: "Development"
backgroundColor: "#BBDEFB"
---

Read the state file and relevant source files first. Do not speculate about
code you haven't opened.

Continue fixing problems using a state file to track progress across iterations.

Only fix identified problems. Keep changes minimal and focused on root causes.

Fix root causes, not symptoms. Ensure fixes work for all valid inputs. Report
deeper design issues rather than applying narrow workarounds.



# Preparation

State file: `implement-<problem>-<date>.md` (date as `YYYY-MM-DD`).

Template:
```markdown
# The problem
[description]
# Progress
## [num] [issue title]
[description] [fix applied]
# Issues remaining
## [num] [issue title]
```

# The Task

1. Read state file (create if missing)
2. Review current state, what's been tried, what's still failing
3. Analyze remaining issues and root causes
4. Prioritize and implement fix
5. Verify the fix
6. Update state file

Once fully fixed, verify against original problem description.
