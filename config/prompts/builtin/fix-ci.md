---
name: "Fix CI"
description: "Diagnose and fix CI pipeline failures"
backgroundColor: "#B2DFDB"
---

Diagnose and fix CI pipeline failures for the current branch.

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

Fix issues in dependency order (fix causes before symptoms).

### 6. Commit and Push

After all fixes are implemented:

```bash
# Stage changes
git add <fixed-files>

# Commit with descriptive message
git commit -m "fix: resolve CI failures

- <brief description of each fix>"

# Push to trigger CI
git push
```

### 7. Report and Guide User

After pushing:

```console
✅ Fixes pushed successfully!

CI has been triggered. The pipeline typically takes X minutes to complete.

To check the status:
- GitHub: gh run watch
- GitLab: glab ci status

Check back in a few minutes to verify the fixes resolved the issues.
If CI still fails, run this prompt again to diagnose any remaining issues.
```

## Rules

- Always check CI status before attempting fixes
- Never modify CI configuration files without explicit user approval
- If fixes require dependency changes, ask user for approval first
- If the failure is in a flaky test, report it rather than retrying blindly
- If the failure is infrastructure-related (CI service issues), inform the user
- Group related fixes in a single commit when possible
