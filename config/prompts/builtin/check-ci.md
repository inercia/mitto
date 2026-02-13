---
name: "Check CI"
description: "Check CI pipeline status and report results"
backgroundColor: "#BBDEFB"
---

Check the CI pipeline status for the current branch and provide a clear report.

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

### 2. Check CLI Tool Availability

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

### 4. Present Status Report

Generate a clear status report:

```console
📊 CI Status Report for branch: <branch-name>
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

| Run | Workflow | Status | Duration | Triggered |
|-----|----------|--------|----------|-----------|
| #1  | Tests    | ✅ Pass | 3m 24s   | 2h ago    |
| #2  | Deploy   | ❌ Fail | 1m 12s   | 2h ago    |
| #3  | Tests    | ✅ Pass | 3m 18s   | 1d ago    |

Overall Status: ⚠️ Some workflows failing
```

### 5. If CI is Passing

Report success concisely:

```console
✅ CI is passing!

All workflows completed successfully on branch <branch-name>.
Latest run: <workflow-name> (#<run-id>) - <duration> ago

You're good to merge or continue development.
```

### 6. If CI is Failing

Retrieve and analyze failure details:

**GitHub Actions:**
```bash
gh run view <run-id> --log-failed
```

**GitLab CI:**
```bash
glab ci view --log
```

Then provide a failure analysis:

```console
❌ CI Failures Detected

┌─────────────────────────────────────────────────────────────┐
│ Failure #1: Test Suite                                      │
├─────────────────────────────────────────────────────────────┤
│ Type:    Test Failure                                       │
│ Job:     test-unit                                          │
│ Error:   <exact error message from logs>                    │
│ File:    <file path and line number if available>           │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Failure #2: Linting                                         │
├─────────────────────────────────────────────────────────────┤
│ Type:    Lint Error                                         │
│ Job:     lint                                               │
│ Error:   <exact error message from logs>                    │
└─────────────────────────────────────────────────────────────┘
```

Categorize failures by type:
- **Test Failures**: Unit, integration, or e2e tests failing
- **Build Errors**: Compilation or build process failures
- **Lint/Format Issues**: Code style or formatting violations
- **Dependency Issues**: Missing or incompatible dependencies
- **Infrastructure Issues**: CI service problems, timeouts, resource limits

### 7. Suggest Next Steps

Based on the status, suggest appropriate actions:

**If passing:**
- Proceed with merge/PR
- Continue development

**If failing:**
- Use "Fix CI" prompt to automatically diagnose and fix issues
- For flaky tests: Consider re-running the workflow
- For infrastructure issues: Wait and retry, or check CI service status

```console
📋 Suggested Next Steps:

1. Run "Fix CI" prompt to automatically fix the issues
2. Or manually fix: <brief description of what needs fixing>
3. Push changes and re-check with this prompt

To re-run failed jobs:
- GitHub: gh run rerun <run-id> --failed
- GitLab: glab ci retry
```

## Rules

- Report status clearly and concisely
- Categorize failures to help users understand the type of issue
- Never modify code or CI configuration - this is a read-only check
- Always suggest the "Fix CI" prompt for automated fixes
- If failures appear to be flaky or infrastructure-related, note this explicitly
- Include timing information so users know how recent the results are

