---
name: "JIRA: decompose"
description: "Break a JIRA ticket into sub-tickets and create them automatically"
backgroundColor: "#E1BEE7"
group: "JIRA"
enabledWhen: '!session.isChild && tools.hasPattern("jira_*")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# JIRA: Decompose a Ticket into Sub-Tickets

## Step 1 — Find tickets to decompose

1. Use `jira_get_agile_boards_jira` to find the relevant board (ask the user if there are multiple boards and you're not sure which one to use).
2. Use `jira_get_sprints_from_board_jira` with `state=active` to get the active sprint.
3. Use `jira_search_jira` with a JQL query like:
   ```
   sprint = <sprint_id> AND assignee = currentUser() AND status in ("To Do", "In Progress")
   ```
   to retrieve all tickets assigned to you in the active sprint that are not yet done.

## Step 2 — Let the user choose a ticket

- If **multiple tickets** are found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to present the ticket list and ask the user which one to decompose. Include the ticket key and summary in each option label.
- If **exactly one ticket** is found: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to confirm with the user before proceeding.
- If **no tickets** are found: inform the user and stop.

## Step 3 — Fetch full ticket details

Using the selected ticket key, call all of the following in parallel:
- `jira_get_issue_jira` with `fields="*all"` to get description, acceptance criteria, priority, labels, components, fix version, sprint, assignee, and any custom fields.
- `jira_get_issue_jira` with `expand="renderedFields"` for rendered description and comments.
- `jira_get_issue_development_info_jira` to check if work has already started (existing branches or PRs).
- Follow any linked issues (`issuelinks`) and fetch their summaries to understand existing relationships.

Analyse all gathered context thoroughly: understand the full scope, acceptance criteria, constraints, and any prior discussion.

## Step 4 — Critically evaluate whether decomposition is warranted

Before proposing sub-tickets, reason carefully:

**Do NOT decompose if:**
- The ticket describes a single, atomic change (e.g., "Update config value X", "Fix typo in error message")
- The work is tightly coupled and cannot be delivered or reviewed independently in parts
- The ticket already has sub-tasks or child issues linked
- The ticket is estimated as small (e.g., ≤ 1–2 days of work) and has clear, narrow acceptance criteria

**Decompose if:**
- The ticket spans multiple independent concerns (e.g., backend + frontend + docs)
- Different parts can be parallelised across team members
- The ticket is large enough that a single PR would be difficult to review
- Multiple distinct acceptance criteria map cleanly to separate deliverables

If decomposition is **not** recommended: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to inform the user of your reasoning and ask if they want to proceed anyway. If they say No, stop here.

## Step 5 — Produce a decomposition plan

Create a breakdown with:

### Parent Ticket Summary
Brief restatement of what the parent ticket is about.

### Decomposition Rationale
Why splitting this ticket makes sense: what the independent concerns are and how parallelism or reviewability is improved.

### Proposed Sub-Tickets
For each proposed sub-ticket, provide:
- **Title**: concise, action-oriented (will become the JIRA summary)
- **Description**: what needs to be done and why, written as if it were a standalone ticket
- **Acceptance Criteria**: specific, testable conditions for "done"
- **Estimated Scope**: Small / Medium / Large, with a brief justification
- **Dependencies**: list any other sub-tickets that must be completed first

### What Stays in the Parent
Describe what (if anything) remains in the parent ticket after sub-tickets are created — e.g., coordination, final integration testing, or documentation.

## Step 6 — Present the plan and iterate

Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to show the decomposition plan to the user and ask: "Does this breakdown look correct? Shall I create these sub-tickets in JIRA?"

- If the user says **No** or provides feedback: revise the breakdown and present it again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 7.

## Step 7 — Create sub-tickets in JIRA

For each approved sub-ticket:

1. Call `jira_create_issue_jira` with:
   - `project_key`: same as the parent ticket
   - `summary`: the sub-ticket title
   - `issue_type`: use `"Story"` or `"Task"` (match the parent's issue type if appropriate, unless it's an Epic)
   - `description`: the sub-ticket description with acceptance criteria in Markdown
   - `assignee`: current user (inherit from parent)
   - `additional_fields` as a JSON object including:
     - `"labels"`: inherited from parent
     - `"components"`: inherited from parent
     - `"fixVersions"`: inherited from parent (use version IDs from `jira_get_project_versions_jira` if needed)
     - `"priority"`: inherited from parent
     - `"parent"`: the parent ticket key (for sub-task hierarchy if the project supports it; otherwise use issue links)

2. After creating each sub-ticket, call `jira_create_issue_link_jira` to link it to the parent with link type `"is child of"` or `"Relates to"` (use `jira_get_link_types_jira` to confirm the correct link type name).

3. Add the sub-ticket to the same sprint as the parent using `jira_add_issues_to_sprint_jira`.

4. Create all sub-tickets sequentially (to avoid race conditions with sprint/board state) but report progress after each creation.

## Step 8 — Confirm results

After all sub-tickets are created, present a summary to the user listing:
- Each created sub-ticket key and title with a link
- Any failures or warnings from the JIRA API
- Reminder to review and adjust estimates or dependencies in JIRA if needed

