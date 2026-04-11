---
name: "JIRA: start work"
description: "Pick a JIRA ticket from the active sprint and spawn parallel Mitto conversations to implement it"
backgroundColor: "#BBDEFB"
group: "JIRA"
enabledWhen: "!session.isChild"
enabledWhenMCP: jira_*, mitto_conversation_*
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.
Available ACP servers: `@mitto:available_acp_servers`
Existing children: `@mitto:children`

# JIRA: Start Work on a Ticket

## Step 1 — Find tickets to work on

1. Use `jira_get_agile_boards_jira` to find the relevant board (ask the user if there are multiple boards and you're not sure which one to use).
2. Use `jira_get_sprints_from_board_jira` with `state=active` to get the active sprint.
3. Use `jira_search_jira` with a JQL query like:
   ```
   sprint = <sprint_id> AND assignee = currentUser() AND status in ("To Do", "In Progress")
   ```
   to retrieve all tickets assigned to you in the active sprint that are not yet done.

## Step 2 — Let the user choose a ticket

- If **multiple tickets** are found: use `mitto_ui_options_buttons_mitto(self_id: "@mitto:session_id")` to present the ticket list and ask the user which one to work on. Include the ticket key and summary in each option label.
- If **exactly one ticket** is found: use `mitto_ui_ask_yes_no_mitto(self_id: "@mitto:session_id")` to confirm with the user before proceeding.
- If **no tickets** are found: inform the user and stop.

## Step 3 — Fetch full ticket details

Using the selected ticket key, call all of the following in parallel:
- `jira_get_issue_jira` with `fields="*all"` to get description, acceptance criteria, priority, labels, components, fix version, sprint, and any custom fields.
- `jira_get_issue_jira` with `expand="renderedFields"` to get rendered (HTML/Markdown) description and comments.
- `jira_download_attachments_jira` to retrieve any attachments.
- `jira_get_issue_development_info_jira` to check linked PRs, branches, and commits.
- Follow any linked issues (`issuelinks`) and fetch their summaries.

Analyze all gathered context thoroughly: understand the problem statement, scope, constraints, acceptance criteria, and any prior discussion in comments.

## Step 4 — Produce an implementation plan

Create a structured plan with the following sections:

### Goal
One-paragraph summary of what needs to be built or fixed, and why.

### Approach
High-level technical approach: which components are affected, what design decisions are involved, and why this approach was chosen.

### Work Items
A numbered list of concrete, independently executable tasks. Each task must have:
- **Title**: short action-oriented name (e.g., "Add database migration for new column")
- **What to do**: a focused description of the work
- **Inputs / context needed**: what the task needs to know or have access to
- **Definition of done**: how to verify the task is complete

### Open Questions & Risks
- Any ambiguities in the ticket that need clarification
- Technical risks or unknowns
- Dependencies on other teams or systems

## Step 5 — Present the plan and iterate

Use `mitto_ui_ask_yes_no_mitto(self_id: "@mitto:session_id")` to show the plan summary to the user and ask: "Does this plan look correct? Shall I proceed with spawning work conversations?"

- If the user says **No** or provides feedback: revise the plan accordingly and present it again. Repeat until the user explicitly approves.
- If the user says **Yes**: proceed to Step 6.

## Step 6 — Spawn one Mitto conversation per work item

For each work item in the approved plan:

1. Call `mitto_conversation_new_mitto` with `self_id: "@mitto:session_id"` and:
   - `title`: the work item title prefixed with the JIRA ticket key (e.g., `"CGW-1234 · Add database migration"`)
   - `acp_server`: use `"Auggie (Sonnet 4.6)"` unless the task is particularly complex, in which case use `"Auggie (Opus 4.6)"`
   - `initial_prompt`: a **self-contained** prompt that includes:
     - The full JIRA ticket key, title, and description
     - The acceptance criteria from the ticket
     - The specific work item title and description
     - The definition of done for this task
     - Any relevant context from linked issues or comments
     - Instruction to report back using `mitto_children_tasks_report_mitto` when done

2. Do **not** wait for each conversation to complete before spawning the next — spawn all conversations in parallel.

3. After all conversations are spawned, use `mitto_children_tasks_wait_mitto(self_id: "@mitto:session_id", children_list: [...], task_id: "<ticket-key>", timeout_seconds: 600)` to wait for all of them to report back, then summarise results to the user.

