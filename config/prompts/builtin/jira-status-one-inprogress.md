---
name: "JIRA: status ONE in-progress"
description: "Pick one in-progress ticket relevant to this repo and fact-check its implementation status"
backgroundColor: "#FFF9C4"
group: "JIRA"
enabledWhen: 'tools.hasPattern("jira_*")'
---

# JIRA: Status Check — One In-Progress Ticket

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

## Step 1 — Identify the current repository

Run the following to determine the current repo's name and remote URL:

```bash
git remote -v
git rev-parse --show-toplevel
```

Extract the repository name (e.g., `cgw-managed-tools`) and the GitHub organisation/owner from the remote URL. You will use these to filter JIRA tickets.

## Step 2 — Fetch in-progress tickets from the active sprint

1. Use `jira_get_agile_boards_jira` to find the relevant board.
2. Use `jira_get_sprints_from_board_jira` with `state=active` to get the active sprint ID.
3. Use `jira_search_jira` with:
   ```
   sprint = <sprint_id> AND assignee = currentUser() AND status = "In Progress"
   ```

## Step 3 — Filter tickets relevant to this repository

For each ticket returned, determine whether it is related to the current repository. A ticket is considered relevant if **any** of the following is true:

- Its summary, description, or comments mention the repository name or a closely related alias
- `jira_get_issue_development_info_jira` shows linked branches, commits, or PRs in this repository
- Its labels or components correspond to the service or team that owns this repository

Discard tickets that show no connection to this repository.

If **no tickets** survive the filter: inform the user and stop.

## Step 4 — Let the user choose one ticket

Present the filtered tickets using `mitto_ui_options_mitto(self_id: "@mitto:session_id")`, showing each ticket as `KEY - Summary` in the dropdown. Ask: "Which in-progress ticket would you like to check status for?"

## Step 5 — Fetch full ticket details

For the selected ticket, run the following in parallel:

- `jira_get_issue_jira` with `fields="*all"` — description, acceptance criteria, labels, components, fix version, sprint, priority
- `jira_get_issue_jira` with `expand="renderedFields,changelog"` — rendered content and full change history
- `jira_get_issue_development_info_jira` — linked PRs, branches, and commits
- Follow any linked issues (`issuelinks`) and fetch their summaries

Also run locally:
```bash
# Find commits that reference this ticket key
git log --oneline --all | grep -i "<TICKET_KEY>"

# Check open/merged PRs by branch names containing the ticket key
git branch -a | grep -i "<TICKET_KEY>"
```

## Step 6 — Fact-check implementation status

Analyse all gathered evidence and produce a **Status Report** for the ticket. Structure it as follows:

### Ticket: `<KEY>` — `<Summary>`

**Goal** (one sentence restating what this ticket is supposed to deliver)

#### Acceptance Criteria — Status

For each acceptance criterion listed in the ticket (or inferred from the description if not explicitly listed):

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| 1 | `<criterion text>` | ✅ Done / ⚠️ Partial / ❌ Not done / ❓ Unknown | `<commit, PR, or code reference>` |

#### Linked Development Work

| Type | Reference | Status |
|------|-----------|--------|
| PR | `<PR title / URL>` | Open / Merged / Draft / Closed |
| Branch | `<branch name>` | Active / Stale |
| Commit | `<sha> — message` | — |

#### Overall Assessment

- **Completion estimate**: `<percentage or qualitative: Not started / Early / Midway / Nearly done / Done>`
- **What appears to be done**: bullet list of concrete evidence of completed work
- **What appears to be missing**: bullet list of acceptance criteria or known tasks with no evidence of completion
- **Blockers or risks**: anything preventing completion (e.g., a dependent ticket still open, a failing test, an unanswered question in comments)

> ⚠️ **This report is read-only.** No code changes, no JIRA updates, and no further work will be performed. Use the "JIRA: start work" prompt to continue implementation.

