---
name: "Address PR Comments"
description: "Systematically address all pull request review feedback"
backgroundColor: "#B2DFDB"
---

Address all review comments on the current pull request with thoughtful responses and appropriate code changes.

### 1. Identify the Pull/Merge Request

**Detect the active PR/MR:**

```bash
# Get current branch
git branch --show-current

# GitHub: Check for associated PR
gh pr status

# GitLab: Check for associated MR
glab mr view
```

If multiple PRs/MRs or none found, list recent ones or ask the user to specify which to work on (by number or URL).

### 2. Retrieve All Review Comments

**Fetch comprehensive feedback:**

```bash
# GitHub: Get PR details and all comments
gh pr view <pr-number> --json reviews,comments,reviewThreads

# GitLab: Get MR details and discussions
glab mr view <mr-number> --comments
```

**Categorize comments by type:**
- **Review comments**: General feedback on the PR
- **Inline code comments**: Specific line-by-line suggestions
- **Conversation threads**: Multi-comment discussions
- **Change requests**: Blocking issues requiring resolution
- **Approvals with comments**: Non-blocking suggestions

### 3. Analyze Each Comment

For every comment, carefully evaluate:

**Understanding the concern:**
- What specific issue is being raised?
- What is the underlying motivation or principle?
- Is this about correctness, style, performance, maintainability, or security?

**Assessing validity:**
- Is the concern technically valid?
- Does it align with project conventions and best practices?
- Is there context the reviewer might be missing?

**Determining priority:**
- **Blocking**: Must be addressed before merge (change requests, critical bugs)
- **Important**: Should be addressed (valid concerns, best practices)
- **Optional**: Nice-to-have improvements (minor style, suggestions)
- **Discussion**: Requires clarification or debate

### 4. Formulate Responses

For each comment, determine the appropriate action:

**If the concern is valid and you agree:**
- Acknowledge: "Good catch! This is indeed problematic because..."
- Commit to action: "I'll update this to..."
- Implement the fix or improvement
- Reply with: "✅ Fixed in [commit hash]. Now it..."

**If clarification is needed:**
- Ask specific questions: "Could you clarify whether you mean X or Y?"
- Provide context: "I implemented it this way because... Does that address your concern?"
- Wait for response before making changes

**If you respectfully disagree:**
- Acknowledge the perspective: "I understand the concern about..."
- Explain your reasoning: "However, I chose this approach because..."
- Provide supporting evidence: code examples, documentation, performance data
- Offer alternatives: "Would you prefer if we... instead?"
- Be open to discussion

**If it's already addressed:**
- Point to the relevant code or commit
- Explain: "This was addressed in [commit/line], where..."
- Ask if the resolution is satisfactory

If any question arises, please ask me for confirmation before proceeding.

### 5. Group and Prioritize

**Organize comments strategically:**

1. **Group related comments**: Multiple comments about the same issue
2. **Identify dependencies**: Some fixes may resolve multiple comments
3. **Prioritize execution**:
   - Critical/blocking issues first
   - Related changes together (avoid multiple commits for same area)
   - Independent changes can be done in parallel

Summarize the analysis in a table, formatted as Markdown:

| Comment | Type | Validity | Priority | Action |
|---------|------|----------|----------|--------|
| ...     | ...  | ...      | ...      | ...    |

Show it to the user. Ask for confirmation before proceeding
(the user could have some comments about this analysis).

### 6. Implement Changes

For each required code change:

1. **Make the change** with careful attention to the feedback
2. **Verify the fix**: Run relevant tests, check for side effects
3. **Commit with clear message**:
   ```
   fix: address review feedback on [topic]
   
   - [Specific change 1] (addresses @reviewer's comment)
   - [Specific change 2] (addresses @reviewer's comment)
   ```
4. **Update or reply to the comment** explaining what was done

### 7. Respond to All Comments

**Before pushing changes:**

- Ensure every comment has a response (even if just "Acknowledged, will fix")
- Mark conversations as resolved when appropriate
- Leave unresolved any that need further discussion

**Response best practices:**
- Be professional and appreciative
- Be specific about what changed
- Include commit references when applicable
- Ask for re-review if significant changes were made

### 8. Identify Push Remote

Before pushing, ensure you're pushing to the correct remote:

**Check where to push:**

```bash
# List all configured remotes
git remote -v

# Check upstream tracking for current branch
git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null
```

**In fork workflows:**
- `origin` typically points to your fork (push here)
- `upstream` points to the main repository (PR target)
- Always push to the remote where your PR source branch lives (usually `origin`)

**Verify by checking the PR:**

Use the GitHub API (via `github-api` tool) to confirm:
```
GET /repos/{owner}/{repo}/pulls?head={username}:{current-branch}&state=open
```

The PR's `head.repo.full_name` tells you which repository your branch lives in - push to that remote.

### 9. Push Changes and Request Re-review

After all changes are implemented and responses are ready and
the user has approved, do:

```bash
# Ensure code is formatted (run project's formatters)

# Run tests to verify nothing broke

# Push changes to the correct remote (your fork in fork workflows)
git push <push-remote> <branch-name>

# Request re-review
# GitHub: gh pr ready && gh pr comment --body "Ready for re-review"
# GitLab: glab mr update --ready
```

### 10. Summary Report

Prepare a clear summary:

```console
✅ PR Review Comments Addressed

📊 Summary:
- Total comments: X
- Resolved with code changes: Y
- Resolved with explanation: Z
- Pending discussion: W

🔄 Changes made:
- [Brief description of key changes]

🔗 PR: <pr-url>

All comments have been responded to. The PR is ready for re-review.
```

## Rules

- **Never dismiss feedback without careful consideration**
- **Always respond to every comment** (even if just to acknowledge)
- **Be respectful and professional** in all responses
- **Provide evidence** when disagreeing (code, docs, benchmarks)
- **Ask for clarification** rather than guessing intent
- **Group related changes** in single commits when logical
- **Run tests** before pushing to avoid introducing new issues
- **Don't mark conversations as resolved** if you're not the author of the comment (unless explicitly asked)
- **If a comment suggests a larger refactor**, discuss scope before implementing
- **If feedback conflicts**, ask reviewers to align before proceeding
- **Never assume which remote to push to** - in fork workflows, push to `origin` (your fork), not `upstream`
