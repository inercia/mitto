# Processors Configuration

Mitto supports processors that transform messages before sending them to the ACP
server. There are three modes:

- **Text-mode** — Inject static text (with optional variable substitution) into messages. No external commands needed.
- **Command-mode** — Execute external commands that receive message context as JSON and produce transformed output.
- **Prompt-mode** — Send a prompt (with conversation history) to a workspace-scoped auxiliary AI agent. Fire-and-forget: the pipeline continues immediately without waiting for the agent's response.

All modes are configured via YAML files and share the same triggering, priority, and conditional enablement features.

> **Note:** The old `hooks/` directory name is still supported for backward compatibility.

## Overview

Processors are loaded from YAML files in the `MITTO_DIR/processors/` directory:

- **macOS**: `~/Library/Application Support/Mitto/processors/`
- **Linux**: `~/.local/share/mitto/processors/` (or `$XDG_DATA_HOME/mitto/processors/`)
- **Windows**: `%APPDATA%\Mitto\processors\`

The processors directory is created automatically when Mitto starts.

### Workspace-Local Processors

In addition to the global processors directory, Mitto automatically loads processors
from the workspace directory at `$MITTO_WORKING_DIR/.mitto/processors/`. This allows
per-project processor configuration that travels with the repository.

Additional workspace processor directories can be configured via `.mittorc`:

```yaml
processors_dirs:
  - ".processors"
  - "team/shared-processors"
```

Paths are relative to the workspace root. Absolute paths are also supported.

**Merge behavior:**

1. Global processors from `MITTO_DIR/processors/` are loaded first
2. Inline text-mode processors from `.mittorc` `conversations.processing.processors` are merged
3. Workspace processors from `.mitto/processors/` are merged
4. Additional directories from `processors_dirs` are merged last (highest override priority)

When a workspace processor has the **same `name`** as a global processor, the workspace
version overrides the global one. All processors are sorted by priority after merging.

Use `mitto processors list --dir .mitto/processors` to preview how workspace processors
merge with global ones.

### Enabling and Disabling Processors per Workspace

Processors can be enabled or disabled per workspace through the Web UI or the `.mittorc` file.

**Web UI:** Open the Workspaces dialog (folder icon in sidebar footer), select a folder, and click the **Processors** tab. Each processor shows its source (workspace, global, or built-in) and has a toggle to enable or disable it.

**`.mittorc` file:** To override the enabled state of a global or built-in processor for a specific workspace, add entries to the `processors` list:

```yaml
processors:
  - name: delegate-to-coder
    enabled: false
  - name: memorize-preferences
    enabled: true
```

This mirrors the same `{name, enabled}` pattern used by the `prompts` section. When re-enabling a processor that is enabled by default, you can remove the entry entirely — missing entries use the processor's default state.

For workspace-local processors (from `.mitto/processors/`), the toggle updates the `enabled` field directly in the processor's YAML file.

> **Backward compatibility:** The legacy `disabled_processors` list format is still read and automatically migrated to the new `processors` format when saving.

**Source precedence:**

| Source      | Badge Color | Description                                               |
| ----------- | ----------- | --------------------------------------------------------- |
| `workspace` | Green       | From `.mitto/processors/` in the workspace                |
| `built-in`  | Blue        | Shipped with Mitto, in `MITTO_DIR/processors/builtin/`    |
| `global`    | Orange      | User-created in `MITTO_DIR/processors/`                   |

## Builtin Processors

Mitto ships with builtin processors that are automatically deployed to `MITTO_DIR/processors/builtin/` on first run. Like builtin prompts, they are embedded in the binary and kept in sync — if a new version of Mitto ships updated builtins, they are automatically updated on startup (content-based comparison).

### Included Builtin Processors

| Processor             | Description                                                                                              | When    | Mode   | Enabled                                            |
| --------------------- | -------------------------------------------------------------------------------------------------------- | ------- | ------ | -------------------------------------------------- |
| `session-context`     | Injects session identity, parent/child relationships, and available agents into the first message        | `first` | text   | Yes                                                |
| `check-mcp-tools`     | Checks if Mitto MCP tools are available and suggests installation if missing                             | `first` | text   | Yes                                                |
| `delegate-to-coder`   | Suggests delegating coding tasks to a faster model when using a premium reasoning model (Opus, o3, etc.) | `first` | text   | Yes (only activates for matching ACP servers)      |
| `delegate-playwright` | Delegates Playwright browser automation to a faster model when using a premium reasoning model           | `first` | text   | Yes (requires smart model + `browser_*` MCP tools) |
| `cleanup-children`    | Reminds the agent to clean up child conversations it no longer needs                                     | `first` | text   | Yes (requires ≥2 MCP-created children + delete tool) |
| `memorize-preferences`| Extracts user preferences from conversations and saves them to AGENTS.md                                 | `first` | prompt | **No** (opt-in; enable in Workspaces dialog or `.mittorc`) |
| `auggie-manage-rules` | Generates and maintains `.augment/rules/` from workspace analysis and conversations (every 15 messages)   | `first` | prompt | **No** (opt-in; Auggie only)                       |
| `claude-manage-memory`| Generates and maintains Claude Code memory files from workspace analysis and conversations (every 15 msgs)| `first` | prompt | **No** (opt-in; Claude Code only)                  |
| `identify-user-data`  | Detects user data values from conversations and sets them via MCP (every 5 messages)                      | `first` | prompt | **No** (opt-in; requires `user_data` schema in `.mittorc`) |

### Managing Builtin Processors

- **Disable**: Edit the YAML file and set `enabled: false`, or move it to `processors/builtin/disabled/`
- **Override**: Create a processor with higher priority in `processors/` (outside `builtin/`)
- **Force update**: Run `mitto processors update-builtin` to overwrite local modifications with the embedded versions
- **Dry run**: Run `mitto processors update-builtin --dry-run` to preview changes

> **Note:** User-created processors in `MITTO_DIR/processors/` (outside `builtin/`) are never modified by automatic updates.

## Text-Mode Processors (Static Content)

Text-mode processors inject static text into messages without executing any external command.
They use the `text` field instead of `command`. This is the simplest way to add context,
reminders, or instructions to conversations.

### Basic Structure

```yaml
name: my-reminder
description: "Adds a coding reminder to every message"
when: all
position: append
priority: 100
text: |
  ---
  Remember: always write tests for new code.
  Follow the project's existing patterns and conventions.
```

### Examples

#### Inject project context on first message

```yaml
name: project-context
description: "Adds project context to the first message"
when: first
position: prepend
priority: 20
text: |
  [Project Context]
  This is the Acme API project. It uses Go 1.22, PostgreSQL, and follows
  clean architecture patterns. All handlers are in internal/api/.
  Run tests with: make test
  ---
```

#### Add safety reminders to every message

```yaml
name: safety-reminder
description: "Reminds the agent about safe practices"
when: all
position: append
priority: 200
text: |
  ---
  IMPORTANT: Do not modify files outside the project directory.
  Do not commit or push without explicit user approval.
```

#### Inject session identity with variable substitution

Text-mode processors support `@mitto:variable` placeholders that are replaced with
live session values (see [Variable Substitution](#variable-substitution) below).

```yaml
name: session-context
description: "Injects session identity and context"
when: first
position: prepend
priority: 10
rerun:
  afterTime: 30m
  afterSentMsgs: 20
text: |
  [Session Context]
  Session: @mitto:session_id (@mitto:session_name)
  Agent: @mitto:acp_server
  Working Directory: @mitto:working_dir
  Parent: @mitto:parent
  Children: @mitto:children
  Available Agents: @mitto:available_acp_servers
  ---
```

#### Conditional text for specific models

```yaml
name: reasoning-guidance
description: "Delegation guidance for premium reasoning models"
when: first
position: append
priority: 90
enabledWhen: 'acp.tags.exists(t, t == "reasoning")'
text: |
  ---
  You are running on a premium reasoning model. For tasks that involve
  extensive coding changes, consider delegating to a faster model.
```

## Command-Mode Processors (Dynamic Content)

Command-mode processors execute external commands to dynamically generate or transform
message content. They use the `command` field and communicate via JSON on stdin/stdout.

## Prompt-Mode Processors (Auxiliary AI Agent)

Prompt-mode processors send a prompt to a workspace-scoped auxiliary AI agent. They are
**fire-and-forget**: the prompt is dispatched asynchronously and the processor pipeline
continues immediately without waiting for the agent's response. This makes them ideal
for background tasks like extracting insights, updating documentation, or tracking
preferences.

Prompt-mode processors use the `prompt` field (mutually exclusive with `text` and
`command`). The prompt template supports all standard `@mitto:variable` placeholders,
plus `@mitto:messages` for injecting filtered conversation history.

### Basic Structure

```yaml
name: my-analyzer
description: "Analyzes conversations in the background"
when: first
priority: 200
timeout: 120s
on_error: skip
rerun:
  afterSentMsgs: 10

prompt: |
  Analyze the conversation messages below and extract key insights.
  Save your findings to a file in the workspace.

  Session: @mitto:session_name
  Working directory: @mitto:working_dir

  === Messages ===
  @mitto:messages

messages:
  scope: since-last-run
  roles: [user, agent]
  max_messages: 30
  max_tokens: 8000
```

### Messages Configuration

The `messages` block controls which conversation messages are injected at the
`@mitto:messages` placeholder. All fields are optional with sensible defaults.

| Field          | Type       | Default          | Description                                           |
| -------------- | ---------- | ---------------- | ----------------------------------------------------- |
| `scope`        | string     | `since-last-run` | Which messages to include (see scopes below)          |
| `roles`        | string[]   | `[user, agent]`  | Include only these roles (`user`, `agent`/`assistant`) |
| `max_messages` | int        | `50`             | Maximum number of messages to include                 |
| `max_tokens`   | int        | _(unlimited)_    | Approximate token cap (4 chars ≈ 1 token)             |

#### Message Scopes

| Scope            | Description                                                    |
| ---------------- | -------------------------------------------------------------- |
| `since-last-run` | Messages since this processor last ran (default). Avoids processing the same messages twice. |
| `last-message`   | Only the most recent message                                   |
| `last-n`         | The last N messages (controlled by `max_messages`)             |
| `all`            | Full conversation history                                      |

#### Role Filtering

Roles accept `user` and `agent` (or `assistant` — both are equivalent). For example,
`roles: [user]` includes only the user's messages, filtering out agent responses. This
is useful for processors that analyze what the user says rather than the full conversation.

### Key Differences from Text/Command Mode

- **No `position` or `output` fields** — prompt-mode processors don't modify the outgoing message
- **Always asynchronous** — the pipeline never blocks waiting for the auxiliary agent
- **Requires a workspace** — the auxiliary session is scoped to the workspace
- **Requires an auxiliary ACP server** — configured in the workspace settings
- **The `messages` block is only valid with `prompt`** — loader rejects it otherwise

### Examples

#### Track user preferences automatically

The built-in `memorize-preferences` processor (disabled by default) demonstrates this
pattern — see [Builtin Processors](#builtin-processors).

#### Summarize progress every 15 messages

```yaml
name: progress-summary
description: "Summarizes session progress periodically"
when: first
priority: 200
timeout: 120s
on_error: skip
rerun:
  afterSentMsgs: 15

prompt: |
  Review the recent conversation and write a brief progress summary.
  Append it to .mitto/progress.md in the workspace.

  @mitto:messages

messages:
  scope: since-last-run
  roles: [user, agent]
  max_messages: 40
  max_tokens: 6000
```

#### Extract action items from agent responses

```yaml
name: action-items
description: "Extracts TODO items from agent responses"
when: first
priority: 200
timeout: 60s
on_error: skip
rerun:
  afterSentMsgs: 10

prompt: |
  Look through the agent's responses below for any TODO items, action items,
  or follow-up tasks mentioned. Add new ones to .mitto/todos.md.

  @mitto:messages

messages:
  scope: since-last-run
  roles: [agent]
  max_messages: 20
```

## Full Configuration Schema

Each YAML file in the processors directory defines one processor. Use **either** `text` (text-mode),
`command` (command-mode), or `prompt` (prompt-mode) — not more than one.

```yaml
# Required fields
name: my-processor # Human-readable identifier
when: first # "first", "all", or "all-except-first"

# --- Text-mode (use ONE of the three modes) ---
text: | # Static text to inject (no command needed)
  Your static content here.

# --- Command-mode (use ONE of the three modes) ---
command: /path/to/script.sh # Command to execute (see Command Resolution)

# --- Prompt-mode (use ONE of the three modes) ---
prompt: | # Prompt template for auxiliary AI agent (fire-and-forget)
  Analyze these messages: @mitto:messages

# Messages configuration (prompt-mode only)
messages:
  scope: since-last-run   # "since-last-run", "last-message", "last-n", or "all"
  roles: [user, agent]    # Which roles to include (default: both)
  max_messages: 50         # Max messages to include (default: 50)
  max_tokens: 8000         # Approximate token cap (optional)

# Optional fields
description: "Adds context" # Description of what the processor does
enabled: true # Default: true
position: prepend # "prepend" or "append" (default: prepend; text/command-mode only)
priority: 100 # Execution order, lower = earlier (default: 100)

# I/O configuration (text/command-mode only; ignored for prompt-mode)
input: message # "message", "conversation", or "none" (default: message)
output: transform # "transform", "prepend", "append", "discard" (default: transform)

# Execution settings
timeout: 5s # Command timeout (default: 5s); also caps auxiliary agent time in prompt-mode
working_dir: session # "session" or "hook" (default: session)
on_error: skip # "skip" or "fail" (default: skip)

# Environment variables (in addition to automatic ones; text/command-mode only)
environment:
  MY_VAR: "value"

# CEL expression for conditional activation (empty = always apply)
# Same context as prompt enabledWhen: acp.*, session.*, parent.*, children.*, workspace.*, tools.*
enabledWhen: 'acp.tags.exists(t, t == "reasoning") && tools.hasAllPatterns(["mitto_conversation_*", "jira_*"])'

# Automatic re-run for "when: first" processors (refreshes context periodically)
rerun:
  afterTime: 30m # re-run after 30 minutes since last run
  afterSentMsgs: 20 # re-run after 20 user messages since last run
```

### Conditional Enablement

Processors support the same fields as prompts:

| Field         | Type | Use Case                                    |
| ------------- | ---- | ------------------------------------------- |
| `enabled`     | bool | Permanently disable a processor             |
| `enabledWhen` | CEL  | Dynamic conditions based on session context |

If `enabled: false`, the processor is never loaded. Otherwise, the `enabledWhen` CEL
expression must evaluate to `true`.

**CEL context** — Same variables and functions as prompt `enabledWhen`:

- `acp.name`, `acp.type`, `acp.tags`, `acp.autoApprove`
- `acp.matchesServer("name")`, `acp.matchesServer(["a", "b"])`
- `session.id`, `session.name`, `session.isChild`, `session.isAutoChild`, `session.parentId`, `session.isPeriodic`
- `parent.exists`, `parent.name`, `parent.acpServer`
- `children.count`, `children.exists`, `children.mcpCount`, `children.names`, `children.acpServers`
- `workspace.uuid`, `workspace.folder`, `workspace.name`
- `tools.available`, `tools.names`
- `tools.hasPattern("glob_*")`, `tools.hasAllPatterns(["g1", "g2"])`, `tools.hasAnyPattern(["g1", "g2"])`
- `permissions.canDoIntrospection`, `permissions.canSendPrompt`, `permissions.canPromptUser`, `permissions.canStartConversation`, `permissions.canInteractOtherWorkspaces`, `permissions.autoApprovePermissions`

### Automatic Re-run (`rerun`)

Processors with `when: first` normally fire only once (on the first message after session
start or resume). The `rerun` field allows them to fire again periodically, refreshing
context for the LLM.

```yaml
when: first
rerun:
  afterTime: 30m # re-run after 30 minutes since last run
  afterSentMsgs: 20 # re-run after 20 user messages since last run
```

| Field           | Type     | Description                                               |
| --------------- | -------- | --------------------------------------------------------- |
| `afterTime`     | duration | Time since last run (`"10m"`, `"1h"`, `"30s"`, `"2h30m"`) |
| `afterSentMsgs` | int      | Number of user messages sent since last run               |

If both are set, whichever threshold is reached first triggers the re-run.

Rerun state is tracked **in memory only** — not persisted across restarts. This is
correct because `isFirstPrompt = true` on session resume already handles the restart case.

> **Note:** `rerun` is only valid with `when: first`. The loader rejects processors that
> combine `rerun` with other `when` values.

## Command Resolution (Command-Mode Only)

The `command` field in command-mode processors supports:

1. **Absolute paths**: `/usr/local/bin/my-processor`
2. **Relative paths**: `./script.sh` (resolved relative to the processor file's directory)
3. **PATH lookup**: `my-processor` (must be in system PATH)

### Companion Scripts

For processors with companion scripts, use relative paths:

```
~/Library/Application Support/Mitto/processors/
├── git-context.yaml
├── git-context.sh          # Companion script
└── lib/
    └── helpers.sh          # Shared helpers
```

```yaml
# git-context.yaml
name: git-context
command: ./git-context.sh # Resolved to processors/git-context.sh
when: first
```

## Input Format — Command-Mode (stdin)

All input is JSON. The format depends on the `input` setting:

### `input: message` (default)

```json
{
  "message": "The user's message text",
  "is_first_message": true,
  "session_id": "20260131-143052-a1b2c3d4",
  "working_dir": "/path/to/project",
  "parent_session_id": "",
  "parent_session_name": "",
  "session_name": "Fix login bug",
  "acp_server": "claude-code",
  "workspace_uuid": "d4e5f6a7-...",
  "available_acp_servers": [
    {
      "name": "auggie",
      "type": "auggie",
      "tags": ["coding"],
      "current": false
    },
    {
      "name": "claude-code",
      "type": "claude-code",
      "tags": ["coding"],
      "current": true
    }
  ],
  "child_sessions": [
    {
      "id": "20260131-143100-e5f6a7b8",
      "name": "Sub task",
      "acp_server": "auggie"
    }
  ]
}
```

### `input: conversation`

```json
{
  "message": "The user's message text",
  "is_first_message": false,
  "session_id": "20260131-143052-a1b2c3d4",
  "working_dir": "/path/to/project",
  "parent_session_id": "20260130-100000-aabbccdd",
  "session_name": "Fix login bug",
  "acp_server": "claude-code",
  "workspace_uuid": "d4e5f6a7-...",
  "available_acp_servers": [
    {
      "name": "auggie",
      "type": "auggie",
      "tags": ["coding"],
      "current": false
    },
    {
      "name": "claude-code",
      "type": "claude-code",
      "tags": ["coding"],
      "current": true
    }
  ],
  "history": [
    { "role": "user", "content": "Previous question" },
    { "role": "assistant", "content": "Previous answer" }
  ]
}
```

### `input: none`

No stdin is provided (useful for side-effect-only processors).

## Output Format — Command-Mode (stdout)

All output must be JSON. The format depends on the `output` setting:

### `output: transform` (default)

Replace the message entirely:

```json
{ "message": "The completely transformed message" }
```

### `output: prepend` or `output: append`

Add text before/after the message:

```json
{ "text": "Context to add:\n\n" }
```

### `output: discard`

Output is ignored (processor runs for side effects only).

### Error Output

Processors can signal errors gracefully:

```json
{
  "error": "Something went wrong",
  "message": "fallback text if any"
}
```

### Attachments

Processors can attach files (images, etc.) to the message. Attachments are sent to the ACP
server as content blocks alongside the text message.

```json
{
  "message": "Here's the screenshot you asked about",
  "attachments": [
    {
      "type": "image",
      "path": "screenshot.png",
      "name": "Screenshot"
    }
  ]
}
```

Attachment fields: | Field | Description | |-------|-------------| | `type` | Attachment
type: `"image"`, `"text"`, `"file"` | | `path` | File path (relative to working
directory or absolute) | | `data` | Base64-encoded content (alternative to `path`) | |
`mime_type` | MIME type (auto-detected if not provided) | | `name` | Display name for
the attachment |

Either `path` or `data` must be provided. If using `path`, the file is read and
base64-encoded automatically.

## Environment Variables

The following environment variables are automatically set for all processors:

| Variable                      | Description                                           | Example                                                                  |
| ----------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------ |
| `MITTO_SESSION_ID`            | Current session ID                                    | `20260131-143052-a1b2c3d4`                                               |
| `MITTO_WORKING_DIR`           | Session working directory                             | `/Users/me/myproject`                                                    |
| `MITTO_IS_FIRST_MESSAGE`      | Whether this is the first message                     | `true` or `false`                                                        |
| `MITTO_PROCESSORS_DIR`        | Path to the processors directory                      | `~/Library/Application Support/Mitto/processors`                         |
| `MITTO_PROCESSOR_FILE`        | Path to the current processor's YAML file             | `.../processors/my-processor.yaml`                                       |
| `MITTO_PROCESSOR_DIR`         | Directory containing the current processor file       | `.../processors`                                                         |
| `MITTO_PARENT_SESSION_ID`     | Parent conversation ID (empty if root)                | `20260130-100000-aabbccdd`                                               |
| `MITTO_PARENT_SESSION_NAME`   | Parent conversation title/name (empty if no parent)   | `Fix login bug`                                                          |
| `MITTO_SESSION_NAME`          | Conversation title/name                               | `Fix login bug`                                                          |
| `MITTO_ACP_SERVER`            | Active ACP server name                                | `claude-code`                                                            |
| `MITTO_WORKSPACE_UUID`        | Workspace identifier                                  | `d4e5f6a7-b8c9-...`                                                      |
| `MITTO_AVAILABLE_ACP_SERVERS` | JSON array of servers with workspaces for this folder | `[{"name":"auggie","tags":["coding"],"current":false},…]`                |
| `MITTO_CHILD_SESSIONS`        | JSON array of child sessions                          | `[{"id":"20260131-...","name":"Sub task","acp_server":"claude-code"},…]` |

## Variable Substitution

Any text that ends up in the final outgoing message — whether it comes from the user's original message, a declarative processor `text` field, or the output of a command processor — can contain `@mitto:variable` placeholders that are replaced with live session values before the message is sent to the ACP agent.

> **Note:** Substitution runs on the **assembled result** after all processors have been applied, so variables work equally in declarative processor text, command processor output, and the user's own message text.

### Syntax

```
@mitto:variable_name
```

The `@mitto:` prefix followed by a lowercase, underscored variable name. This is consistent with the `@namespace:value` convention used by processor triggers (e.g., `@git:status`, `@file:path`). Unknown `@mitto:` placeholders are left as-is.

### Available Variables

| Placeholder                    | Replaced with                                                                  |
| ------------------------------ | ------------------------------------------------------------------------------ |
| `@mitto:session_id`            | Current session ID                                                             |
| `@mitto:parent_session_id`     | Parent conversation ID; empty string if this is a root session                 |
| `@mitto:parent`                | Parent session formatted as `id (name)` or just `id` if unnamed; empty if root |
| `@mitto:session_name`          | Conversation title/name; empty string if not yet set                           |
| `@mitto:working_dir`           | Session working directory                                                      |
| `@mitto:acp_server`            | Active ACP server name (e.g. `claude-code`)                                    |
| `@mitto:workspace_uuid`        | Workspace UUID                                                                 |
| `@mitto:available_acp_servers` | Human-readable list of ACP servers with workspaces for this folder — see below |
| `@mitto:children`              | Human-readable list of child sessions — see below                              |
| `@mitto:periodic`              | `"true"` if this prompt was triggered by the periodic runner, `"false"` otherwise |
| `@mitto:messages`              | Filtered conversation history (prompt-mode only). Controlled by the `messages` block — see [Messages Configuration](#messages-configuration). |

### `@mitto:available_acp_servers` format

Produces a comma-separated list of every ACP server that has a workspace configured for the session's working directory. Each entry follows the pattern:

```
name [tag1, tag2] (current)
```

- **`[tags]`** — omitted when the server has no tags
- **`(current)`** — appended only to the active server

Example with two servers:

```
auggie [coding, ai-assistant] (current), claude-code [coding, fast-model]
```

The same data is also available as a structured JSON array via the `available_acp_servers` field in stdin and the `MITTO_AVAILABLE_ACP_SERVERS` environment variable (see above).

### `@mitto:children` format

Produces a comma-separated list of direct child sessions. Each entry follows the pattern:

```
id (name) [acp-server]
```

- **`(name)`** — omitted when the child session has no name/title yet
- **`[acp-server]`** — omitted when the child has no ACP server set

Example with two children:

```
20260407-120000-a1b2c3d4 (Research task) [claude-code], 20260407-120100-e5f6a7b8 (Test runner) [auggie]
```

The same data is also available as a structured JSON array via the `child_sessions` field in stdin and the `MITTO_CHILD_SESSIONS` environment variable (see above).

### `@mitto:parent` format

Produces a formatted reference to the parent session:

```
id (name)
```

- If the parent has a name: `20260407-100000-aabbccdd (Main session)`
- If the parent has no name: `20260407-100000-aabbccdd`
- If there is no parent (root session): empty string

### Example: inject session context into a prepended text

A command processor that dynamically includes the session ID and active server:

```bash
#!/bin/bash
# Output a preamble that tells the agent which session and server it's in.
# Variables are substituted *after* this text is merged into the message.
jq -n '{
  "text": "Session: @mitto:session_id\nAgent: @mitto:acp_server\nProject: @mitto:working_dir\n\nAvailable agents for this project: @mitto:available_acp_servers\n\n"
}'
```

```yaml
name: session-context
when: first
command: ./session-context.sh
output: prepend
```

### Behaviour notes

- **Unknown variables** — `@mitto:unknown` is left verbatim in the message
- **Empty values** — e.g. `@mitto:parent_session_id` when there is no parent → replaced with empty string
- **Fast path** — if the assembled message contains no `@mitto:`, the substitution pass is skipped entirely
- **CLI mode** — `@mitto:session_id`, `@mitto:parent_session_id`, `@mitto:parent`, `@mitto:session_name`, `@mitto:acp_server`, `@mitto:workspace_uuid`, `@mitto:available_acp_servers`, and `@mitto:children` all substitute to empty string; `@mitto:working_dir` substitutes to the CLI working directory
- **Escaping** — to include a literal `@mitto:variable` without substitution, prefix it with a backslash: `\@mitto:variable`. The backslash is stripped and the variable name is passed through as-is (e.g. `\@mitto:session_id` → `@mitto:session_id`)

## Command-Mode Examples

### Git Context Processor

Add recent git commits to the first message:

```yaml
# processors/git-context.yaml
name: git-context
description: "Adds recent git commits to context"
when: first
command: ./git-context.sh
input: message
output: prepend
timeout: 5s
```

```bash
#!/bin/bash
# processors/git-context.sh

# Read JSON input
input=$(cat)
working_dir=$(echo "$input" | jq -r '.working_dir')

# Get git log
cd "$working_dir"
git_log=$(git log -5 --oneline 2>/dev/null || echo "Not a git repository")

# Output JSON
jq -n --arg text "Recent commits:\n$git_log\n\n---\n\n" '{"text": $text}'
```

### Code Formatter Processor

Transform code blocks in messages:

```yaml
# processors/format-code.yaml
name: format-code
description: "Formats code blocks in messages"
when: all
command: /usr/local/bin/format-code-blocks
input: message
output: transform
```

### Project Rules Processor

> **Tip:** The examples below use command-mode. For simpler static content injection,
> consider using [text-mode processors](#text-mode-processors-static-content) instead.

Add project-specific rules for certain workspaces using CEL or workspace-local processors:

```yaml
# processors/project-rules.yaml
name: project-rules
description: "Adds project-specific coding rules"
when: first
command: /bin/cat
args:
  - "${MITTO_WORKING_DIR}/.ai-rules"
input: none
output: prepend
on_error: skip
enabledWhen: 'workspace.folder.startsWith("/path/to/my-project")'
```

Alternatively, place the processor in `$workspace/.mitto/processors/` to scope it
automatically to that workspace.

## Disabling Processors

To temporarily disable a processor:

1. **Set `enabled: false`** in the YAML file
2. **Move to `disabled/` subdirectory** - files in `processors/disabled/` are ignored

```
processors/
├── active-processor.yaml  # Loaded
├── disabled/
│   └── experimental.yaml  # Ignored
```

## Execution Order

Processors are executed in priority order (lower priority number = earlier execution):

1. Processors with `priority: 50` run first
2. Processors with `priority: 100` (default) run next
3. Processors with `priority: 200` run last

Within the same priority, order is undefined.

## Error Handling

| `on_error`       | Behavior                                    |
| ---------------- | ------------------------------------------- |
| `skip` (default) | Log warning, continue with original message |
| `fail`           | Abort the message, return error to user     |

Processors that timeout or exit with non-zero status are treated as errors.

## Mode Comparison

| Feature       | Text-Mode                    | Command-Mode                           | Prompt-Mode                                  |
| ------------- | ---------------------------- | -------------------------------------- | -------------------------------------------- |
| Configuration | `text` field in YAML         | `command` field + external script      | `prompt` field + optional `messages` block   |
| Content       | Static text (with variables) | Dynamic via external commands          | Prompt template with `@mitto:messages`       |
| Input         | None (text is inline)        | JSON via stdin                         | Conversation history via `messages` config   |
| Output        | Modifies outgoing message    | Modifies outgoing message              | None (fire-and-forget to auxiliary agent)    |
| Execution     | Synchronous                  | Synchronous                            | Asynchronous (pipeline continues immediately)|
| Use case      | Context, reminders, rules    | Complex transformations, external data | Background analysis, preference tracking     |
| Dependencies  | None                         | External script or binary              | Workspace with auxiliary ACP server          |

All modes share the same triggering (`when`), priority, conditional enablement
(`enabledWhen`), re-run, and error handling features. The `position`, `input`, and
`output` fields are only applicable to text-mode and command-mode processors.

## Processor Statistics

The conversation properties panel displays real-time processor statistics:

- **Processors** — Number of active processors for the current conversation
- **Activations** — Total number of times the processor pipeline has run
- **Last activation** — Relative time since the last processor execution (e.g., "2m ago")

These statistics are updated after each prompt completes and during periodic keepalive messages. They are tracked in-memory and reset when the session restarts.
