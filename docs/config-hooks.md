# Message Hooks Configuration

Mitto supports external command-based hooks that can dynamically transform messages before sending them to the ACP server. Unlike static text processors, hooks execute arbitrary commands that receive message context as JSON input and produce transformed output.

## Overview

Hooks are loaded from YAML files in the `MITTO_DIR/hooks/` directory:
- **macOS**: `~/Library/Application Support/Mitto/hooks/`
- **Linux**: `~/.local/share/mitto/hooks/` (or `$XDG_DATA_HOME/mitto/hooks/`)
- **Windows**: `%APPDATA%\Mitto\hooks\`

The hooks directory is created automatically when Mitto starts.

## Hook Configuration Schema

Each YAML file in the hooks directory defines one hook:

```yaml
# Required fields
name: my-hook                    # Human-readable identifier
command: /path/to/script.sh      # Command to execute (see Command Resolution)
when: first                      # "first", "all", or "all-except-first"

# Optional fields
description: "Adds context"      # Description of what the hook does
enabled: true                    # Default: true
position: prepend                # "prepend" or "append" (default: prepend)
priority: 100                    # Execution order, lower = earlier (default: 100)

# I/O configuration
input: message                   # "message", "conversation", or "none" (default: message)
output: transform                # "transform", "prepend", "append", "discard" (default: transform)

# Execution settings
timeout: 5s                      # Command timeout (default: 5s)
working_dir: session             # "session" or "hook" (default: session)
on_error: skip                   # "skip" or "fail" (default: skip)

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

1. **Absolute paths**: `/usr/local/bin/my-hook`
2. **Relative paths**: `./script.sh` (resolved relative to the hook file's directory)
3. **PATH lookup**: `my-hook` (must be in system PATH)

### Companion Scripts

For hooks with companion scripts, use relative paths:

```
~/Library/Application Support/Mitto/hooks/
├── git-context.yaml
├── git-context.sh          # Companion script
└── lib/
    └── helpers.sh          # Shared helpers
```

```yaml
# git-context.yaml
name: git-context
command: ./git-context.sh    # Resolved to hooks/git-context.sh
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
  "working_dir": "/path/to/project"
}
```

### `input: conversation`

```json
{
  "message": "The user's message text",
  "is_first_message": false,
  "session_id": "20260131-143052-a1b2c3d4",
  "working_dir": "/path/to/project",
  "history": [
    {"role": "user", "content": "Previous question"},
    {"role": "assistant", "content": "Previous answer"}
  ]
}
```

### `input: none`

No stdin is provided (useful for side-effect-only hooks).

## Output Format (stdout)

All output must be JSON. The format depends on the `output` setting:

### `output: transform` (default)

Replace the message entirely:
```json
{"message": "The completely transformed message"}
```

### `output: prepend` or `output: append`

Add text before/after the message:
```json
{"text": "Context to add:\n\n"}
```

### `output: discard`

Output is ignored (hook runs for side effects only).

### Error Output

Hooks can signal errors gracefully:
```json
{
  "error": "Something went wrong",
  "message": "fallback text if any"
}
```

### Attachments

Hooks can attach files (images, etc.) to the message. Attachments are sent to the ACP server as content blocks alongside the text message.

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

Attachment fields:
| Field | Description |
|-------|-------------|
| `type` | Attachment type: `"image"`, `"text"`, `"file"` |
| `path` | File path (relative to working directory or absolute) |
| `data` | Base64-encoded content (alternative to `path`) |
| `mime_type` | MIME type (auto-detected if not provided) |
| `name` | Display name for the attachment |

Either `path` or `data` must be provided. If using `path`, the file is read and base64-encoded automatically.

## Environment Variables

The following environment variables are automatically set for all hooks:

| Variable | Description | Example |
|----------|-------------|---------|
| `MITTO_SESSION_ID` | Current session ID | `20260131-143052-a1b2c3d4` |
| `MITTO_WORKING_DIR` | Session working directory | `/Users/me/myproject` |
| `MITTO_IS_FIRST_MESSAGE` | Whether this is the first message | `true` or `false` |
| `MITTO_HOOKS_DIR` | Path to the hooks directory | `~/Library/Application Support/Mitto/hooks` |
| `MITTO_HOOK_FILE` | Path to the current hook's YAML file | `.../hooks/my-hook.yaml` |
| `MITTO_HOOK_DIR` | Directory containing the current hook file | `.../hooks` |

## Examples

### Git Context Hook

Add recent git commits to the first message:

```yaml
# hooks/git-context.yaml
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
# hooks/git-context.sh

# Read JSON input
input=$(cat)
working_dir=$(echo "$input" | jq -r '.working_dir')

# Get git log
cd "$working_dir"
git_log=$(git log -5 --oneline 2>/dev/null || echo "Not a git repository")

# Output JSON
jq -n --arg text "Recent commits:\n$git_log\n\n---\n\n" '{"text": $text}'
```

### Code Formatter Hook

Transform code blocks in messages:

```yaml
# hooks/format-code.yaml
name: format-code
description: "Formats code blocks in messages"
when: all
command: /usr/local/bin/format-code-blocks
input: message
output: transform
```

### Project Rules Hook

Add project-specific rules for certain workspaces:

```yaml
# hooks/project-rules.yaml
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

## Disabling Hooks

To temporarily disable a hook:

1. **Set `enabled: false`** in the YAML file
2. **Move to `disabled/` subdirectory** - files in `hooks/disabled/` are ignored

```
hooks/
├── active-hook.yaml       # Loaded
├── disabled/
│   └── experimental.yaml  # Ignored
```

## Execution Order

Hooks are executed in priority order (lower priority number = earlier execution):

1. Hooks with `priority: 50` run first
2. Hooks with `priority: 100` (default) run next
3. Hooks with `priority: 200` run last

Within the same priority, order is undefined.

## Error Handling

| `on_error` | Behavior |
|------------|----------|
| `skip` (default) | Log warning, continue with original message |
| `fail` | Abort the message, return error to user |

Hooks that timeout or exit with non-zero status are treated as errors.

## Comparison with Processors

| Feature | Processors | Hooks |
|---------|------------|-------|
| Configuration | YAML in config files | YAML files in hooks directory |
| Transformation | Static text prepend/append | Dynamic via external commands |
| Input | None | JSON via stdin |
| Output | None | JSON via stdout |
| Use case | Simple prompts, reminders | Complex transformations, external data |

Both processors and hooks can be used together. Processors are applied first, then hooks.
