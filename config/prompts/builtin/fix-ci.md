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

#### Delegating Complex CI Fixes to Child Conversations

When there are **3+ independent CI failures** in different areas (e.g., test failures in separate packages, a lint error AND a build error AND a test failure), delegate fixes to parallel child conversations.

**Do NOT delegate** for: a single failure, cascading failures from one root cause, or simple lint/format fixes.

**Session context for delegation:**

Your session ID is `@mitto:session_id` — use as `self_id` for all `mitto_*` tool calls.

Available ACP servers:
@mitto:available_acp_servers

Existing children:
@mitto:children

**How to delegate:**

1. Group failures into independent fix tasks (no shared root cause, no overlapping files)
2. Choose ACP server: straightforward fixes → prefer `"coding"`/`"fast"` servers; ambiguous failures needing investigation → prefer `"reasoning"`/`"planning"` servers; no match → server marked `(current)`, then first available
3. If relevant children already exist, consider sending work to them via `mitto_conversation_send_prompt` instead of creating new ones
4. `mitto_conversation_new(self_id: "@mitto:session_id")` per task — include: the exact error log, relevant file paths, what to fix, constraints (minimal changes, fix root cause not symptoms), and reporting directive
5. `mitto_children_tasks_wait(self_id: "@mitto:session_id", task_id: "<ci-fix-description>", timeout_seconds: 600)`
6. Review all results together — check for conflicts between fixes
7. Verify combined changes locally: run full CI check (`make test` or equivalent)
8. `mitto_conversation_delete` for completed children

**Without Mitto tools**: fix all issues directly in sequence.

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
