# Command Processors Configuration

Mitto supports external command-based processors that can dynamically transform messages
before sending them to the ACP server. Processors execute arbitrary commands that receive
message context as JSON input and produce transformed output.

> **Note:** The old `hooks/` directory name is still supported for backward compatibility.

## Overview

Processors are loaded from YAML files in the `MITTO_DIR/processors/` directory:

- **macOS**: `~/Library/Application Support/Mitto/processors/`
- **Linux**: `~/.local/share/mitto/processors/` (or `$XDG_DATA_HOME/mitto/processors/`)
- **Windows**: `%APPDATA%\Mitto\processors\`

The processors directory is created automatically when Mitto starts.

## Processor Configuration Schema

Each YAML file in the processors directory defines one processor:

```yaml
# Required fields
name: my-processor # Human-readable identifier
command: /path/to/script.sh # Command to execute (see Command Resolution)
when: first # "first", "all", or "all-except-first"

# Optional fields
description: "Adds context" # Description of what the processor does
enabled: true # Default: true
position: prepend # "prepend" or "append" (default: prepend)
priority: 100 # Execution order, lower = earlier (default: 100)

# I/O configuration
input: message # "message", "conversation", or "none" (default: message)
output: transform # "transform", "prepend", "append", "discard" (default: transform)

# Execution settings
timeout: 5s # Command timeout (default: 5s)
working_dir: session # "session" or "hook" (default: session)
on_error: skip # "skip" or "fail" (default: skip)

# Environment variables (in addition to automatic ones)
environment:
  MY_VAR: "value"

# Workspace filtering (empty = all workspaces)
workspaces:
  - /path/to/project1
  - /path/to/project2
```

## Command Resolution

The `command` field supports:

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

## Input Format (stdin)

All input is JSON. The format depends on the `input` setting:

### `input: message` (default)

```json
{
  "message": "The user's message text",
  "is_first_message": true,
  "session_id": "20260131-143052-a1b2c3d4",
  "working_dir": "/path/to/project",
  "parent_session_id": "",
  "session_name": "Fix login bug",
  "acp_server": "claude-code",
  "workspace_uuid": "d4e5f6a7-...",
  "available_acp_servers": [
    { "name": "auggie",      "type": "auggie",      "tags": ["coding"], "current": false },
    { "name": "claude-code", "type": "claude-code", "tags": ["coding"], "current": true  }
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
    { "name": "auggie",      "type": "auggie",      "tags": ["coding"], "current": false },
    { "name": "claude-code", "type": "claude-code", "tags": ["coding"], "current": true  }
  ],
  "history": [
    { "role": "user",      "content": "Previous question" },
    { "role": "assistant", "content": "Previous answer"   }
  ]
}
```

### `input: none`

No stdin is provided (useful for side-effect-only processors).

## Output Format (stdout)

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

| Variable                        | Description                                      | Example                                                  |
| ------------------------------- | ------------------------------------------------ | -------------------------------------------------------- |
| `MITTO_SESSION_ID`              | Current session ID                               | `20260131-143052-a1b2c3d4`                               |
| `MITTO_WORKING_DIR`             | Session working directory                        | `/Users/me/myproject`                                    |
| `MITTO_IS_FIRST_MESSAGE`        | Whether this is the first message                | `true` or `false`                                        |
| `MITTO_PROCESSORS_DIR`          | Path to the processors directory                 | `~/Library/Application Support/Mitto/processors`         |
| `MITTO_PROCESSOR_FILE`          | Path to the current processor's YAML file        | `.../processors/my-processor.yaml`                       |
| `MITTO_PROCESSOR_DIR`           | Directory containing the current processor file  | `.../processors`                                         |
| `MITTO_PARENT_SESSION_ID`       | Parent conversation ID (empty if root)           | `20260130-100000-aabbccdd`                               |
| `MITTO_SESSION_NAME`            | Conversation title/name                          | `Fix login bug`                                          |
| `MITTO_ACP_SERVER`              | Active ACP server name                           | `claude-code`                                            |
| `MITTO_WORKSPACE_UUID`          | Workspace identifier                             | `d4e5f6a7-b8c9-...`                                      |
| `MITTO_AVAILABLE_ACP_SERVERS`   | JSON array of servers with workspaces for this folder | `[{"name":"auggie","tags":["coding"],"current":false},…]` |

## Variable Substitution

Any text that ends up in the final outgoing message — whether it comes from the user's original message, a declarative processor `text` field, or the output of a command processor — can contain `@mitto:variable` placeholders that are replaced with live session values before the message is sent to the ACP agent.

> **Note:** Substitution runs on the **assembled result** after all processors have been applied, so variables work equally in declarative processor text, command processor output, and the user's own message text.

### Syntax

```
@mitto:variable_name
```

The `@mitto:` prefix followed by a lowercase, underscored variable name. This is consistent with the `@namespace:value` convention used by processor triggers (e.g., `@git:status`, `@file:path`). Unknown `@mitto:` placeholders are left as-is.

### Available Variables

| Placeholder                   | Replaced with                                                                 |
| ----------------------------- | ----------------------------------------------------------------------------- |
| `@mitto:session_id`              | Current session ID                                                            |
| `@mitto:parent_session_id`       | Parent conversation ID; empty string if this is a root session                |
| `@mitto:session_name`            | Conversation title/name; empty string if not yet set                          |
| `@mitto:working_dir`             | Session working directory                                                     |
| `@mitto:acp_server`              | Active ACP server name (e.g. `claude-code`)                                  |
| `@mitto:workspace_uuid`          | Workspace UUID                                                                |
| `@mitto:available_acp_servers`   | Human-readable list of ACP servers with workspaces for this folder — see below |

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
- **CLI mode** — `@mitto:session_id`, `@mitto:parent_session_id`, `@mitto:session_name`, `@mitto:acp_server`, `@mitto:workspace_uuid`, and `@mitto:available_acp_servers` all substitute to empty string; `@mitto:working_dir` substitutes to the CLI working directory

## Examples

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

Add project-specific rules for certain workspaces:

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
workspaces:
  - /path/to/my-project
```

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

## Comparison with Declarative Processors

| Feature        | Declarative Processors     | Command Processors                           |
| -------------- | -------------------------- | -------------------------------------------- |
| Configuration  | YAML in config files       | YAML files in processors directory           |
| Transformation | Static text prepend/append | Dynamic via external commands                |
| Input          | None                       | JSON via stdin                               |
| Output         | None                       | JSON via stdout                              |
| Use case       | Simple prompts, reminders  | Complex transformations, external data       |

Both declarative processors and command processors can be used together. Declarative
processors are applied first, then command processors.
