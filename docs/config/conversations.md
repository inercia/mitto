# Conversation Processing Configuration

Mitto supports configurable message processing that transforms user messages before
sending them to the ACP server. This allows you to automatically prepend system prompts,
append reminders, or add project-specific context to your conversations.

## Overview

Message processors are applied in order to transform user messages. Each processor
specifies:

- **When** to apply: first message only, all messages, or all except first
- **Where** to insert: prepend (before) or append (after) the message
- **What** text to insert

## Configuration Locations

Processors can be configured at two levels:

| Level         | File                            | Scope                          |
| ------------- | ------------------------------- | ------------------------------ |
| **Global**    | `~/.mittorc` or `settings.json` | Applies to all workspaces      |
| **Workspace** | `<project>/.mittorc`            | Applies only to that workspace |

## Configuration Schema

```yaml
conversations:
  processing:
    # Optional: if true, workspace processors replace global processors entirely
    # Default: false (merge with global)
    override: false

    processors:
      - when: first # "first", "all", or "all-except-first"
        position: prepend # "prepend" or "append"
        text: |
          Your text here
```

### Processor Fields

| Field      | Values             | Description                                                                                        |
| ---------- | ------------------ | -------------------------------------------------------------------------------------------------- |
| `when`     | `first`            | Apply only to the first message in a conversation                                                  |
|            | `all`              | Apply to every message                                                                             |
|            | `all-except-first` | Apply to all messages except the first                                                             |
| `position` | `prepend`          | Insert text before the user's message                                                              |
|            | `append`           | Insert text after the user's message                                                               |
| `text`     | string             | The text to insert (supports multi-line YAML); supports `@mitto:variable` substitution (see below) |

## Variable Substitution in Processor Text

The `text` field of a declarative processor (and any text produced by [command processors](processors.md)) can include `@mitto:variable` placeholders. After all processors have run, Mitto replaces each placeholder with the corresponding session value before the message is sent to the ACP agent.

> The user's **original** message is still what gets recorded in session history — only the text forwarded to the agent is expanded.

### Available Variables

| Placeholder                    | Value                                                                           |
| ------------------------------ | ------------------------------------------------------------------------------- |
| `@mitto:session_id`            | Current session ID                                                              |
| `@mitto:parent_session_id`     | Parent conversation ID; empty string if this is a root session                  |
| `@mitto:session_name`          | Conversation title/name; empty string if not yet set                            |
| `@mitto:working_dir`           | Session working directory                                                       |
| `@mitto:acp_server`            | Active ACP server name (e.g. `claude-code`)                                     |
| `@mitto:workspace_uuid`        | Workspace UUID                                                                  |
| `@mitto:available_acp_servers` | Comma-separated list of ACP servers with workspaces for this folder (see below) |
| `@mitto:periodic`              | `"true"` if this prompt was triggered by the periodic runner, `"false"` otherwise |

### `@mitto:available_acp_servers` format

Produces a comma-separated list of the ACP servers that have workspaces configured for the session's working directory, for example:

```
auggie [coding, ai-assistant] (current), claude-code [coding, fast-model]
```

Each entry contains the server name, optional tags in brackets, and `(current)` for the active server.

### Behaviour

- **Unknown placeholders** — `@mitto:unknown` is left verbatim
- **Empty values** — e.g. `@mitto:parent_session_id` when there is no parent → replaced with empty string
- **Fast path** — if the assembled message contains no `@mitto:`, the substitution pass is skipped entirely

### Examples

Inject the session ID and working directory into a first-message system prompt:

```yaml
conversations:
  processing:
    processors:
      - when: first
        position: prepend
        text: |
          Session: @mitto:session_id
          Project: @mitto:working_dir
          Agent: @mitto:acp_server

          ---
```

Show the user which other agents are available for the same project:

```yaml
conversations:
  processing:
    processors:
      - when: first
        position: prepend
        text: |
          You are connected via @mitto:acp_server.
          Other agents available for this project: @mitto:available_acp_servers

          ---
```

## Examples

### System Prompt on First Message

Add a system prompt that only appears at the start of each conversation:

```yaml
conversations:
  processing:
    - when: first
      position: prepend
      text: |
        You are a helpful AI coding assistant.
        Please follow best practices and be concise.

        ---
```

### Reminder on All Messages

Add a reminder that appears on every message:

```yaml
conversations:
  processing:
    - when: all
      position: append
      text: "\n\n[Remember: Provide working code examples]"
```

### Project-Specific Context (Workspace)

In your project's `.mittorc` file:

```yaml
# Project-specific prompts
prompts:
  - name: "Run Tests"
    prompt: "Run all tests with: go test ./..."

# Project-specific conversation processing
conversations:
  processing:
    - when: first
      position: prepend
      text: |
        This is the Mitto project - a CLI client for ACP.

        Key packages:
        - internal/acp: ACP protocol client
        - internal/web: Web interface server
        - internal/config: Configuration loading

        Follow Go conventions and existing patterns.

        ---
```

### Combining Multiple Processors

Processors are applied in order:

```yaml
conversations:
  processing:
    # First: Add system context (first message only)
    - when: first
      position: prepend
      text: "SYSTEM: You are a senior developer.\n\nUSER: "

    # Second: Add format marker (all messages)
    - when: all
      position: append
      text: "\n\n---END---"

    # Third: Add continuation hint (after first message)
    - when: all-except-first
      position: prepend
      text: "[Continuing...]\n\n"
```

**First message result:**

```
SYSTEM: You are a senior developer.

USER: Help me fix this bug

---END---
```

**Second message result:**

```
[Continuing...]

Add some tests

---END---
```

## Merge Behavior

When both global and workspace configurations exist:

1. **Default (merge)**: Global processors run first, then workspace processors
2. **Override mode**: Only workspace processors run (set `override: true`)

### Example: Override Global Config

```yaml
# In workspace .mittorc
conversations:
  override: true # Ignore global processors
  processing:
    - when: first
      position: prepend
      text: "Custom workspace-only context"
```

## Processing Flow

```
User types: "Help me fix this bug"

┌─────────────────────────────────────────────────────────┐
│ 1. Check if first message → Apply "first" processors   │
│ 2. Apply "all" processors                              │
│ 3. If not first → Apply "all-except-first" processors  │
└─────────────────────────────────────────────────────────┘

Result sent to ACP server (original message recorded in history)
```

## Notes

- **Recording**: The original user message is recorded in session history, not the
  transformed version
- **Resumed sessions**: When resuming a session, `isFirstMessage` is `false`, so "first"
  processors won't apply
- **Empty processors**: If no processors are configured, messages are sent unchanged
- **CLI and Web**: Processors work identically in both the CLI (`mitto cli`) and web
  interface

## External Images

By default, Mitto blocks external images in AI responses for privacy reasons. When an AI response includes an image from an external URL like `![photo](https://example.com/image.png)`, the browser's Content Security Policy (CSP) prevents loading it.

This is intentional because external images can be used for tracking:

- When you view a message containing an external image, your browser requests that image from the external server
- This reveals your IP address and when you viewed the message

### Enabling External Images

If you want to allow external images (e.g., when working with AI that generates image links), you can enable them:

**Via Settings UI:**

1. Open Settings (⚙️ button)
2. Go to the **UI** tab
3. Under **Advanced**, enable **Allow External Images**
4. Save your settings and restart Mitto for the change to take effect

**Via Configuration:**

```yaml
conversations:
  external_images:
    enabled: true # Allow external HTTPS images (default: false)
```

### Security Considerations

When external images are enabled:

- Only HTTPS images are allowed (not HTTP)
- Data URLs and same-origin images are always allowed regardless of this setting
- Your IP address may be exposed to external image servers
- External servers can track when you view messages

**Recommendation:** Keep external images disabled unless you specifically need them.

## Related Documentation

- [User Data](user-data.md) - Custom metadata for conversations
- [Workspace Configuration](web/workspace.md) - Project-specific `.mittorc` files
- [Configuration Overview](overview.md) - Global configuration options
- [Message Processors](processors.md) - External command-based processors
