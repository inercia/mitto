---
name: "Check CI"
description: "Check CI pipeline status and report results"
group: "CI"
backgroundColor: "#BBDEFB"
---

<task>
Check CI pipeline status for the current branch and report.
</task>

<instructions>

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

### 4. Report

<output_format>

```console
📊 CI Status Report for branch: <branch>

| Run | Workflow | Status | Duration | Triggered |
|-----|----------|--------|----------|-----------|
| #1  | Tests    | ✅ Pass | 3m 24s   | 2h ago    |

Overall Status: ✅ All passing / ⚠️ Some failing
```

</output_format>

### 5. If Passing

Report success concisely with latest run info.

### 6. If Failing

Retrieve logs (`gh run view <id> --log-failed` / `glab ci view --log`).

Per failure: type (test/build/lint/dependency/infrastructure), job, error, file/line.

### 7. Suggest Next Steps

- Passing: proceed with merge/development
- Failing: use "Fix CI" prompt, re-run for flaky tests, wait for infra issues

</instructions>

<rules>
- Report clearly and concisely
- Categorize failures by type
- This is read-only — do not modify code or CI config
- Suggest "Fix CI" prompt for automated fixes
- Note flaky or infrastructure-related failures explicitly
- Include timing information
</rules>
