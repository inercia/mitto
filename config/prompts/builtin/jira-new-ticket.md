---
name: "JIRA: new ticket"
description: "Create a JIRA ticket — from the current conversation context or from scratch"
backgroundColor: "#C8E6C9"
group: "JIRA"
enabledWhen: 'tools.hasPattern("jira_*")'
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

# JIRA: Create a New Ticket

## Step 1 — Determine ticket source

First, check the conversation history for meaningful prior work context (investigation, debugging, feature discussion, research findings, etc.).

- If the conversation contains **prior work context**: use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to ask: "What should the new ticket be about?" with the following options:
  - "Based on what we've been working on" (with a short description of the detected topic, e.g., "Based on what we've been working on — the auth timeout bug in the login flow")
  - "Something entirely new — I'll describe it"

- If the conversation contains **no meaningful prior context** (fresh session, only greetings, etc.): skip this question and proceed directly as if the user chose "something new".

## Step 2 — Analyse conversation context (if applicable)

If the user chose to base the ticket on the current conversation, review the full history to extract:

- **Problem statement**: What bug, task, or feature is being discussed?
- **Root cause analysis**: Any findings from investigation or debugging
- **Reproduction steps**: If a bug, how to reproduce it
- **Proposed solution**: Any solution discussed or partially implemented
- **Affected components**: Files, services, or systems involved
- **Priority signals**: Severity, user impact, frequency

If the user chose "something new", proceed to Step 3 with empty defaults — the user will fill in all details manually via the form and description editor.

## Step 2 — Gather project metadata and current assignments from JIRA

Run the following in parallel to prepare form defaults:

1. `jira_get_agile_boards_jira` — to identify available projects and boards
2. `jira_search_jira` with JQL `assignee = currentUser() ORDER BY updated DESC` and `max_results=10` — to learn the user's recently used projects, issue types, labels, and components
3. `jira_get_server_info_jira` — to confirm the JIRA instance
4. After obtaining the board, use `jira_get_sprints_from_board_jira` with `state=active` to find the active sprint, then `jira_search_jira` with JQL:
   ```
   sprint = <sprint_id> AND assignee = currentUser()
   ```
   to fetch all tickets currently assigned to the user in the active sprint. These provide the **strongest signal** for attribute defaults since they reflect the user's current working context.

From both the recent tickets and the active sprint tickets, extract commonly used values for:
- **Project key** (prefer the project used by active sprint tickets; fall back to most frequently used)
- **Issue type** (most common: Story, Bug, Task)
- **Labels** (union of labels from active sprint tickets, supplemented by recent tickets)
- **Components** (union of components from active sprint tickets, supplemented by recent tickets)
- **Priority** (most common priority across active sprint tickets)
- **Fix version** (if active sprint tickets share a common fix version)
- **Assignee** (current user)

## Step 3 — Collect ticket fields via form

Use `mitto_ui_form_mitto(self_id: "@mitto:session_id")` to present a form with the following fields, pre-filled with intelligent defaults derived from Steps 1 and 2:

```html
<label>Project Key</label>
<input type="text" name="project_key" value="<default_project>" />

<label>Issue Type</label>
<select name="issue_type">
  <option value="Bug">Bug</option>
  <option value="Task">Task</option>
  <option value="Story">Story</option>
  <option value="Sub-task">Sub-task</option>
</select>

<label>Summary</label>
<input type="text" name="summary" value="<generated_summary>" />

<label>Priority</label>
<select name="priority">
  <option value="Highest">Highest</option>
  <option value="High">High</option>
  <option value="Medium" selected>Medium</option>
  <option value="Low">Low</option>
  <option value="Lowest">Lowest</option>
</select>

<label>Labels (comma-separated)</label>
<input type="text" name="labels" value="<default_labels>" />

<label>Components (comma-separated)</label>
<input type="text" name="components" value="<default_components>" />

<label>Assignee (username)</label>
<input type="text" name="assignee" value="<current_user>" />
```

Set the `title` to "New JIRA Ticket" and use the `selected` attribute on the issue type option that best matches the conversation context (e.g., select "Bug" if debugging was discussed).

## Step 4 — Compose and review the description

Based on the conversation context, compose a well-structured ticket description in Markdown including:

- **Summary / Background**: What the issue is about
- **Details / Findings**: Key information from the conversation (root cause, investigation results, relevant code references)
- **Steps to Reproduce** (if applicable): Numbered reproduction steps
- **Expected vs Actual Behaviour** (if applicable)
- **Proposed Solution** (if discussed): What approach was identified
- **Acceptance Criteria**: Concrete, testable conditions for "done"

Present the description using `mitto_ui_textbox_mitto(self_id: "@mitto:session_id")` with:
- `title`: "Ticket Description — Review & Edit"
- `text`: the composed description
- `result`: "full"

This allows the user to review and freely edit the description before submission.

## Step 5 — Create the ticket in JIRA

After the user confirms (submits both the form and the description), create the ticket:

1. Parse the labels and components from the comma-separated form values into arrays.
2. Call `jira_create_issue_jira` with:
   - `project_key`: from form
   - `summary`: from form
   - `issue_type`: from form
   - `description`: the final (possibly user-edited) description text
   - `assignee`: from form
   - `additional_fields`: a JSON object with:
     - `"labels"`: array of label strings
     - `"components"`: array of `{"name": "<component>"}` objects
     - `"priority"`: `{"name": "<priority>"}`

3. If creation fails, report the error to the user and suggest corrections (e.g., invalid project key, unknown component).

## Step 6 — Sprint and status

Immediately after creation, use `mitto_ui_form_mitto(self_id: "@mitto:session_id")` to ask:

```html
<label>Assign to me?</label>
<select name="assign_to_me">
  <option value="yes" selected>Yes</option>
  <option value="no">No — leave unassigned</option>
</select>

<label>Add to the active sprint?</label>
<select name="add_to_sprint">
  <option value="yes" selected>Yes</option>
  <option value="no">No</option>
</select>

<label>Set status to "In Progress"?</label>
<select name="set_in_progress">
  <option value="yes">Yes — I'm starting work now</option>
  <option value="no" selected>No — leave as To Do</option>
</select>
```

Set the `title` to "Sprint, Assignment & Status".

- If the user chooses to **assign to me**: call `jira_assign_issue_jira` to assign the ticket to the current user.
- If the user chooses to **add to sprint**: use the active sprint ID already obtained in Step 2 (or fetch it now if not available) and call `jira_add_issues_to_sprint_jira`.
- If the user chooses to **set in progress**: call `jira_transition_issue_jira` to move the ticket to "In Progress" (use `jira_get_transitions_jira` first to find the correct transition ID).

## Step 7 — Confirm and offer follow-up actions

After completing sprint/status changes:

1. Report the new ticket key and confirm what was done (created, added to sprint, transitioned).
2. Use `mitto_ui_options_mitto(self_id: "@mitto:session_id")` to ask: "Ticket `<KEY>` is ready. Would you like to:"
   - "Link it to another ticket"
   - "Start working on it now"
   - "Done — no further action"

3. If the user chooses to **link**: ask for the target ticket key, use `jira_get_link_types_jira` to confirm available link types, then call `jira_create_issue_link_jira`.
4. If the user chooses to **start working**: inform them to use the "JIRA: start work" prompt with the new ticket key.

Always end by displaying the full URL to the newly created ticket (e.g., `https://<jira-instance>/browse/<KEY>`) so the user can open it directly in their browser.
