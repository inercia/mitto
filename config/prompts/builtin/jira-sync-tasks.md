---
icon: "tag"
name: "JIRA: sync tasks"
menus: prompts
description: "Periodically pull JIRA tickets matching the project's saved query into local beads issues"
backgroundColor: "#D1C4E9"
group: "JIRA"
tags: ["periodic", "jira"]
enabledWhen: 'tools.hasPattern("jira_*") && commandExists("bd")'
---

Pull JIRA tickets matching this project's saved query into local beads issues,
keeping the beads copy in sync with changes made in JIRA (description, comments,
priority, status). This is a **one-way pull** (JIRA → beads) for now; two-way
sync may be added later. Run it **on demand** in a regular conversation, or
schedule it to run periodically via `mitto_conversation_set_periodic` — it
adapts its behaviour to whichever mode it is invoked in (see Interaction Mode).

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

Project user data (JSON):
@mitto:user_data

## Interaction Mode

This prompt runs in two modes. Check these variables to decide which applies:

- `@mitto:periodic` = is this a scheduled periodic execution?
- `@mitto:periodic_forced` = was a periodic run manually triggered by the user?

**Interactive mode — a regular conversation** (`@mitto:periodic` = "false") **or a force-triggered periodic run** (`@mitto:periodic_forced` = "true"):
- The user is present. Use interactive tools (`mitto_ui_options`, `mitto_ui_form`, `mitto_ui_textbox`) as well as `mitto_ui_notify`. This is the default when run on demand.

**Silent mode — a scheduled periodic run** (`@mitto:periodic` = "true" AND `@mitto:periodic_forced` = "false"):
- Use **only** `mitto_ui_notify` — non-blocking notifications only.
- Do **NOT** use `mitto_ui_options`, `mitto_ui_form`, or `mitto_ui_textbox`. The user is not watching.

## Step 1 — Read the "Jira Tasks" query from project user data

Inspect the **Project user data** JSON above (an array of `{"name", "value"}` objects). Find the attribute whose `name` is **"Jira Tasks"** (case-insensitive). Its `value` is the **JQL query** that selects the tickets to mirror.

- If **no** "Jira Tasks" attribute exists, or its value is empty:
  - Periodic run: send one `mitto_ui_notify` explaining the project has no "Jira Tasks" query configured, then **stop**.
  - Interactive run: tell the user to set a "Jira Tasks" property (a JQL query) in the conversation's user data, then **stop**.

## Step 2 — Ensure beads is ready

```bash
ls -d .beads 2>/dev/null
```

- If `.beads` exists: continue.
- If it does **not** exist: in interactive mode, run `bd init --non-interactive`; in periodic mode, notify and **stop** (do not initialise unattended).

## Step 3 — Fetch matching JIRA tickets

Run the saved JQL with `jira_search_jira`, requesting `fields="*all"` and `expand="renderedFields"` so you get the rendered description and comments. Page through all results (use `startAt`/`maxResults`) — do not truncate. For each ticket capture: key, summary, description, status, priority, issue type, labels, assignee, the `updated` timestamp, and every comment (id, author, created, body).

## Step 4 — Load existing synced beads

Synced beads are tagged with the label `jira-sync` and carry `external_ref = "jira-<KEY>"` (lowercase key). Load them in one query:

```bash
bd list --all --label jira-sync --json
```

Build a map from JIRA key → bead using each bead's `external_ref`. For each bead also note its `metadata` (`jira_updated`, `jira_synced_comments`), `priority`, `status`, `issue_type`, `title`, and `description`.

## Step 5 — Pull each ticket (create or update)

Use these mappings:

- **Priority** (JIRA → beads): Highest→0, High→1, Medium→2, Low→3, Lowest→4 (default 2).
- **Type** (JIRA → beads): Bug→bug, Story→feature, Epic→epic, Task/Sub-task/other→task.

Compose a canonical bead **body** for every ticket (write it to a temp file and pass via `--body-file` to preserve Markdown):

```
**JIRA:** <KEY> — <status> · <issue type> · assignee: <assignee>
**Link:** https://<jira-instance>/browse/<KEY>

<the ticket description (rendered Markdown)>
```

For each ticket from Step 3:

**A. No matching bead exists** → create it:

```bash
bd create "[<KEY>] <summary>" \
  --type <mapped-type> --priority <mapped-priority> \
  --labels "jira-sync" --external-ref "jira-<key-lower>" \
  --body-file /tmp/jira-<key>.md
```

Capture the new bead ID, then record sync state and mirror all comments (see C):

```bash
bd update <id> --set-metadata jira_updated="<ticket.updated>" --set-metadata jira_synced_comments="<csv of comment ids>"
```

**B. A matching bead exists** → detect changes and update:

- If the bead's `metadata.jira_updated` **equals** the ticket's `updated` timestamp **and** no new comments exist: **skip** (no change since last sync).
- Otherwise update only what changed:

```bash
bd update <id> --title "[<KEY>] <summary>" \
  --priority <mapped-priority> --type <mapped-type> \
  --body-file /tmp/jira-<key>.md \
  --set-metadata jira_updated="<ticket.updated>"
```

- **Status reflection**: if the JIRA ticket is Done/Closed and the bead is still open, close it: `bd close <id> --reason "Closed in JIRA (<status>)"`. (In interactive mode you may confirm first.)

**C. Mirror new comments** (both create and update paths):

For each JIRA comment whose id is **not** in `metadata.jira_synced_comments`, append it to the bead with a marker so it is never duplicated, then extend the metadata list:

```bash
bd comment <id> "[jira] <author> @ <created>: <comment body>"
bd update <id> --set-metadata jira_synced_comments="<previous ids + new ids>"
```

## Step 6 — Summary

Report counts: tickets matched, beads created, beads updated, comments mirrored, beads closed, unchanged (skipped).

- Silent mode (scheduled periodic): send a single `mitto_ui_notify` only if anything changed; stay silent otherwise.
- Interactive mode: print the full summary.

## Guidelines

- **Pull-only**: never write back to JIRA (no status transitions, no comments, no edits in JIRA). Two-way sync is future work.
- **Idempotent**: the `external_ref` (`jira-<key>`) is the join key; `metadata.jira_updated` skips unchanged tickets; `metadata.jira_synced_comments` prevents duplicate comments. Running twice in a row must produce no spurious changes.
- Always `--body-file` for descriptions and `--file`/inline text for comments to avoid shell-quoting issues.
- Never delete beads for tickets that fall out of the query — they may simply no longer match the JQL.
