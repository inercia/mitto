---
name: "Review"
description: "Review changes for quality and correctness"
backgroundColor: "#C8E6C9"
---

Review the changes we made for quality and correctness.

### Check for:

1. **Bugs**: Logic errors, off-by-one, null/nil handling, race conditions
2. **Security**: Input validation, injection risks, auth/authz issues, secrets exposure
3. **Performance**: Unnecessary allocations, N+1 queries, blocking calls, memory leaks
4. **Error handling**: Missing error checks, unhelpful messages, swallowed errors
5. **Style**: Consistency with codebase, naming conventions, formatting
6. **Tests**: Adequate coverage, meaningful assertions, edge cases

### Report format:

For each issue found:
- **Severity**: Critical / High / Medium / Low
- **Location**: File and line number
- **Issue**: What's wrong
- **Suggestion**: How to fix it

Summarize: total issues by severity, overall assessment, recommended actions.

