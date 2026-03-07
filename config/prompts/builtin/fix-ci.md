---
name: "Fix CI"
description: "Diagnose and fix CI pipeline failures"
group: "CI"
backgroundColor: "#B2DFDB"
---

<investigate_before_answering>
Before attempting any fixes, check the CI status and read the failure logs first.
Understand the actual error messages and the code involved before making changes.
Do not speculate about what might be wrong — read the logs and the relevant source files.
</investigate_before_answering>

<task>
Diagnose and fix CI pipeline failures for the current branch.
</task>

<scope>
Only fix the issues causing CI failures. Keep changes minimal and focused on resolving
the pipeline errors. Do not refactor surrounding code or add improvements beyond what
is needed to make CI pass.
</scope>

<solution_quality>
Implement fixes that address the root cause. If a test is failing, fix the code or
the test based on which is actually wrong — do not just make the test pass with
hard-coded values or narrow workarounds. If a failure reveals a flawed test or
requirement, report it rather than working around it.
</solution_quality>

<instructions>

### 1. Detect CI System

Check for CI configuration files to identify the CI system in use:

| CI System | Detection Files |
|-----------|-----------------|
| GitHub Actions | `.github/workflows/*.yml` or `.github/workflows/*.yaml` |
| GitLab CI | `.gitlab-ci.yml` |
| Jenkins | `Jenkinsfile` |
| CircleCI | `.circleci/config.yml` |
| Travis CI | `.travis.yml` |
| Azure Pipelines | `azure-pipelines.yml` |

If no CI configuration is found, inform the user and stop.

### 2. Check CI Tool Availability

Verify the appropriate CLI tool is installed and authenticated:

| CI System | CLI Tool | Auth Check Command |
|-----------|----------|-------------------|
| GitHub Actions | `gh` | `gh auth status` |
| GitLab CI | `glab` | `glab auth status` |

If the CLI tool is not installed or not authenticated:
- Report the issue clearly
- Provide installation/authentication instructions
- Stop and wait for user to resolve

### 3. Get Current Branch and CI Status

```bash
# Get current branch
git branch --show-current

# GitHub Actions: Check workflow runs
gh run list --branch <current-branch> --limit 5

# GitLab CI: Check pipeline status
glab ci status
```

If CI is passing, report success and stop.

### 4. Diagnose Failures

If CI is failing, retrieve and analyze the failure logs:

**GitHub Actions:**
```bash
# Get the failed run ID from the list
gh run view <run-id> --log-failed
```

**GitLab CI:**
```bash
glab ci view --log
```

For each failure:
1. **Identify**: Quote the exact error message from the logs
2. **Diagnose**: Explain the root cause
   - Is it a test failure?
   - Is it a build/compilation error?
   - Is it a linting/formatting issue?
   - Is it a dependency issue?
   - Is it a configuration/environment issue?

### 5. Fix Issues

For each identified issue:

1. **Implement the fix** in the codebase
2. **Explain the change** and why it resolves the issue
3. **Verify locally** if possible (run tests, build, lint)

Fix issues in dependency order — fix causes before symptoms.

### 6. Report and Guide User

<output_format>

```console
✅ Fixes pushed successfully!

CI has been triggered. The pipeline typically takes X minutes to complete.

To check the status:
- GitHub: gh run watch
- GitLab: glab ci status

Check back in a few minutes to verify the fixes resolved the issues.
If CI still fails, run this prompt again to diagnose any remaining issues.
```

</output_format>

### 7. Commit and Push

After all fixes are implemented, suggest the user to commit
and push the changes.

</instructions>

<rules>
- Check CI status before attempting fixes, because understanding the actual failures prevents wasted effort
- Get explicit user approval before modifying CI configuration files or changing dependencies
- Report flaky tests as flaky rather than retrying blindly, so the user can address the root cause
- Inform the user when failures are infrastructure-related (CI service issues, not code issues)
- Group related fixes in a single commit when possible for cleaner history
</rules>
