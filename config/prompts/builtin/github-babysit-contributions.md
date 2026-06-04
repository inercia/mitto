---
name: "GitHub: babysit contributions"
menus: prompts
description: "Periodically check for pending review requests, bot dependency PRs ready to merge, and stale remote branches from merged PRs"
group: "CI"
backgroundColor: "#C8E6C9"
tags: ["periodic", "github"]
enabledWhen: 'fileExists(".git/config") && (tools.hasPattern("github_*") || commandExists("gh"))'
---

Monitor community and repo-wide contributions for the current repository:
pending review requests addressed to you, bot dependency PRs (Dependabot,
Renovate) ready to merge, and stale remote branches from merged PRs. This
prompt does **not** touch your own PRs — use "GitHub: babysit my PRs" for that.
Designed to be run periodically via `mitto_conversation_set_periodic`.

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all `mitto_*` MCP tool calls.

Available ACP servers:
@mitto:available_acp_servers

## Interaction Mode

- **Periodic run**: `@mitto:periodic` = is this a scheduled periodic execution?
- **Force-triggered**: `@mitto:periodic_forced` = was this periodic run manually triggered by the user?

**If this is a scheduled periodic run** (`@mitto:periodic` = "true" AND `@mitto:periodic_forced` = "false"):
- Use **only** `mitto_ui_notify` for all communication — non-blocking notifications only.
- Do **NOT** use `mitto_ui_options`, `mitto_ui_form`, `mitto_ui_textbox`, or any
  interactive/blocking UI tool. The user is not watching.

**If this is a force-triggered run** (`@mitto:periodic_forced` = "true") **or a
non-periodic conversation** (`@mitto:periodic` = "false"):
- You may freely interact with the user using `mitto_ui_options`, `mitto_ui_form`,
  and other interactive tools in addition to `mitto_ui_notify`.

## Step 1 — Identify the repository

```bash
git remote -v
git rev-parse --show-toplevel
gh repo view --json nameWithOwner,defaultBranchRef -q '.nameWithOwner + " (default: " + .defaultBranchRef.name + ")"'
```

If `gh` is not authenticated (`gh auth status` fails), inform the user and stop.

Once you have the `nameWithOwner` (e.g., `some-org/some-repo`), rename this
conversation so it's easy to identify — but only if the current name
(`@mitto:session_name`) doesn't already start with "Babysit contributions":

```
mitto_conversation_update(self_id: "@mitto:session_id",
  conversation_id: "@mitto:session_id",
  name: "Babysit contributions in <nameWithOwner>")
```

## Step 2 — Pending review requests

Check if the current user has been requested to review any PRs:

```bash
gh pr list --search "review-requested:@me" --state open --json number,title,author,createdAt --limit 20
```

If there are pending review requests, **batch into a single notification:**

```
mitto_ui_notify(self_id: "@mitto:session_id",
  title: "👀 <count> PRs awaiting your review",
  message: "You've been requested to review:\n• #<N> <title> by <author> (<days> days ago)\n• #<M> <title> by <author> (<days> days ago)\n...",
  style: "info")
```

## Step 3 — Dependabot / Renovate PRs with passing CI

List open bot dependency PRs:

```bash
gh pr list --state open --author "app/dependabot" --json number,title,statusCheckRollup,author --limit 20
gh pr list --state open --author "app/renovate" --json number,title,statusCheckRollup,author --limit 20
```

If a bot PR has all CI checks passing:

**In interactive mode**, offer to approve and merge:
```
mitto_ui_options(self_id: "@mitto:session_id",
  question: "🤖 Bot PR #<number> (<title>) has passing CI. Approve and merge?",
  options: [
    { label: "Yes, approve and merge" },
    { label: "No, just notify" }
  ])
```
If the user selects "Yes": `gh pr review <number> --approve` then
`gh pr merge <number> --merge`. Then notify success.

**In scheduled mode**, just notify:
```
mitto_ui_notify(self_id: "@mitto:session_id",
  title: "🤖 Bot PR #<number> ready to merge",
  message: "<title> — dependency update from <author> with passing CI. Consider merging.",
  style: "info")
```

## Step 4 — Merged branch cleanup

Check for remote branches from merged PRs that were not deleted:

```bash
gh pr list --state merged --json headRefName,number,title --limit 30
```

Cross-reference against existing remote branches:

```bash
git ls-remote --heads origin
```

If any merged-PR branches still exist on the remote:

**In interactive mode**, offer to delete them:
```
mitto_ui_options(self_id: "@mitto:session_id",
  question: "🧹 <count> merged branches still on remote:\n<branch1> (PR #N)\n<branch2> (PR #M)\n\nDelete them?",
  options: [
    { label: "Yes, delete all" },
    { label: "No, leave them" }
  ])
```
If the user selects "Yes", delete via remote API (safe — does not affect local):
```bash
git push origin --delete <branch1> <branch2> ...
```

**In scheduled mode**, just notify:
```
mitto_ui_notify(self_id: "@mitto:session_id",
  title: "🧹 <count> merged branches can be cleaned up",
  message: "Branches from merged PRs still on remote:\n<branch1> (PR #N)\n<branch2> (PR #M)\n...",
  style: "info")
```

## Step 5 — Summary

After processing everything, produce a brief summary:

```console
📊 Contributions Summary

Pending reviews for you: <count>
Bot PRs ready to merge: <count>
Merged branches to clean up: <count>
Next check: <if periodic, mention the schedule>
```

## Guidelines

- If `gh` authentication fails, stop immediately and inform the user.
- **Interaction mode** (see "Interaction Mode" section above):
  - **Scheduled periodic** (`@mitto:periodic` = "true", `@mitto:periodic_forced` = "false"):
    Use only `mitto_ui_notify`. No interactive UI. Skip the summary — only
    send notifications for actionable items.
  - **Force-triggered or non-periodic**: You may use `mitto_ui_options`,
    `mitto_ui_form`, and other interactive tools. Ask the user before risky
    actions (merges, branch deletions). Show the full summary at the end.
- **Notification batching**: batch repetitive items (pending reviews, bot PRs)
  into a single notification per category to avoid spamming the user —
  especially important in periodic mode.
- In **scheduled mode**: do not merge PRs or delete branches automatically —
  only notify. In **interactive mode**: offer to merge or delete with user
  confirmation.
- This prompt does **not** act on the user's own PRs. Use "GitHub: babysit my
  PRs" for that. The two prompts are complementary with no overlap.
