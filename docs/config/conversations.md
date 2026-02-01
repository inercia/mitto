# Conversation Processing Configuration

Mitto supports configurable message processing that transforms user messages before sending them to the ACP server. This allows you to automatically prepend system prompts, append reminders, or add project-specific context to your conversations.

## Overview

Message processors are applied in order to transform user messages. Each processor specifies:
- **When** to apply: first message only, all messages, or all except first
- **Where** to insert: prepend (before) or append (after) the message
- **What** text to insert

## Configuration Locations

Processors can be configured at two levels:

| Level | File | Scope |
|-------|------|-------|
| **Global** | `~/.mittorc` or `settings.json` | Applies to all workspaces |
| **Workspace** | `<project>/.mittorc` | Applies only to that workspace |

## Configuration Schema

```yaml
conversations:
  processing:
    # Optional: if true, workspace processors replace global processors entirely
    # Default: false (merge with global)
    override: false
    
    processors:
      - when: first          # "first", "all", or "all-except-first"
        position: prepend    # "prepend" or "append"
        text: |
          Your text here
```

### Processor Fields

| Field | Values | Description |
|-------|--------|-------------|
| `when` | `first` | Apply only to the first message in a conversation |
| | `all` | Apply to every message |
| | `all-except-first` | Apply to all messages except the first |
| `position` | `prepend` | Insert text before the user's message |
| | `append` | Insert text after the user's message |
| `text` | string | The text to insert (supports multi-line YAML) |

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
  override: true  # Ignore global processors
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

- **Recording**: The original user message is recorded in session history, not the transformed version
- **Resumed sessions**: When resuming a session, `isFirstMessage` is `false`, so "first" processors won't apply
- **Empty processors**: If no processors are configured, messages are sent unchanged
- **CLI and Web**: Processors work identically in both the CLI (`mitto cli`) and web interface

