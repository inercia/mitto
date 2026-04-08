---
name: "JIRA: status ALL in-progress"
description: "Fact-check implementation status for all in-progress sprint tickets relevant to this repo"
backgroundColor: "#FFE0B2"
group: "JIRA"
enabledWhenMCP: jira_*
---

# JIRA: Status Check — All In-Progress Tickets

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

Discard tickets that show no connection to this repository. Keep track of discarded tickets and list them briefly at the end so the user can verify the filter was correct.

If **no tickets** survive the filter: inform the user, list the discarded ones with the reason for exclusion, and stop.

## Step 4 — Fetch full details for all relevant tickets (in parallel)

For **each** relevant ticket, fetch the following in parallel:

- `jira_get_issue_jira` with `fields="*all"` — description, acceptance criteria, labels, components, fix version, sprint, priority
- `jira_get_issue_jira` with `expand="renderedFields,changelog"` — rendered content and full change history
- `jira_get_issue_development_info_jira` — linked PRs, branches, and commits
- Follow any linked issues (`issuelinks`) and fetch their summaries

Also run locally (once, shared across all tickets):
```bash
# Retrieve recent git log for cross-referencing ticket keys
git log --oneline --all -200
git branch -a
```

## Step 5 — Fact-check each ticket

For **each** relevant ticket, produce a **Status Report** section. Apply the same analysis as you would for a single ticket:

1. **Parse acceptance criteria** from the ticket description (or infer them if not explicit)
2. **Cross-reference** each criterion against: linked PRs (status, title, description), linked commits (message, files changed), local git log entries mentioning the ticket key, and comments on the ticket
3. **Assess overall completion** based on evidence

Use this structure for each ticket:

---

### `<KEY>` — `<Summary>`

**Goal**: one sentence restating what this ticket is supposed to deliver.

#### Acceptance Criteria — Status

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

- **Completion estimate**: `<Not started / Early / Midway / Nearly done / Done>`
- **What appears to be done**: bullet list
- **What appears to be missing**: bullet list
- **Blockers or risks**: anything preventing completion

---

## Step 6 — Aggregate summary

After all individual ticket sections, produce a **Sprint Overview** table:

| Ticket | Summary | Completion | Blockers? |
|--------|---------|------------|-----------|
| `KEY-1` | `<summary>` | Midway | Yes — `<short description>` |
| `KEY-2` | `<summary>` | Nearly done | No |

Then list tickets that were **excluded** from the analysis (filtered out in Step 3) with a brief reason, so the user can verify no relevant ticket was accidentally dropped:

| Ticket | Summary | Reason excluded |
|--------|---------|-----------------|
| `KEY-3` | `<summary>` | No linked PRs or commits in this repo; no mention of repo name |

> ⚠️ **This report is read-only.** No code changes, no JIRA updates, and no further work will be performed. Use the "JIRA: start work" prompt to continue implementation on a specific ticket.

