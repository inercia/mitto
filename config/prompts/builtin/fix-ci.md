---
name: "Fix CI"
description: "Diagnose and fix CI pipeline failures"
group: "CI"
backgroundColor: "#B2DFDB"
---

Check CI status and read failure logs before making changes. Do not speculate —
read the logs and relevant source files.

Diagnose and fix CI pipeline failures for the current branch.

Only fix CI-failing issues. Keep changes minimal.

Fix root causes, not symptoms. If a test fails, fix the code or test based on
which is actually wrong — no hard-coded workarounds. Report flawed tests/requirements
rather than working around them.

### 1. Detect CI System

| CI System | Detection Files |
|-----------|-----------------|
| GitHub Actions | `.github/workflows/*.yml` |
| GitLab CI | `.gitlab-ci.yml` |
| Jenkins | `Jenkinsfile` |
| CircleCI | `.circleci/config.yml` |
| Travis CI | `.travis.yml` |
| Azure Pipelines | `azure-pipelines.yml` |

If none found, inform user and stop.

### 2. Check CLI Tool

| CI System | CLI | Auth Check |
|-----------|-----|------------|
| GitHub Actions | `gh` | `gh auth status` |
| GitLab CI | `glab` | `glab auth status` |

If not installed/authenticated: report, provide instructions, stop.

### 3. Get Status

```bash
git branch --show-current
gh run list --branch <branch> --limit 5    # GitHub
glab ci status                              # GitLab
```

If passing, report success and stop.

### 4. Diagnose

```bash
gh run view <run-id> --log-failed    # GitHub
glab ci view --log                    # GitLab
```

Per failure:

1. Quote exact error from logs
2. Diagnose root cause: test failure, build error, lint/format, dependency, config/environment

### 5. Fix

Per issue: implement fix, explain the change, verify locally (tests, build, lint).

Fix in dependency order — causes before symptoms.

### 6. Report

```console
✅ Fixes applied. Push and monitor CI.
To check: gh run watch / glab ci status
If CI still fails, run this prompt again.
```

### 7. Commit and Push

Suggest user commit and push the changes.

## Guidelines

- Check CI status before attempting fixes
- Get user approval before modifying CI config or dependencies
- Report flaky tests as flaky rather than retrying blindly
- Note infrastructure-related failures explicitly
- Group related fixes in a single commit
