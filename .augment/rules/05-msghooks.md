---
description: Message hooks for pre/post processing, external command execution, message transformation
globs:
  - "internal/msghooks/**/*"
---

# Message Hooks Package

The `internal/msghooks` package provides external command-based hooks for message transformation. Hooks are loaded from YAML files in `MITTO_DIR/hooks/` directory.

## Quick Reference

| Component | Purpose |
|-----------|---------|
| `Hook` | Hook definition loaded from YAML |
| `Loader` | Loads and validates hook files |
| `Executor` | Runs hooks as external commands |
| `Apply*` | Applies hooks to messages |

## Hook Configuration

Hooks are defined in YAML files in `MITTO_DIR/hooks/*.yaml`:

```yaml
name: system-prompt
description: Prepends a system prompt to the first message
when: first  # first, all, all-except-first
position: prepend  # prepend, append
priority: 100  # Lower = earlier execution

command: ./generate-prompt.sh
args: []
input: message  # message, conversation, none
output: transform  # transform, prepend, append, discard

timeout: 5s
working_dir: session  # session, hook
on_error: skip  # skip, fail

# Optional: limit to specific workspaces
workspaces:
  - /path/to/project
```

## Input Types

| Type | Description |
|------|-------------|
| `message` | Send message with basic context (JSON) |
| `conversation` | Send full conversation history (JSON) |
| `none` | Send nothing to stdin |

## Output Types

| Type | Description |
|------|-------------|
| `transform` | Replace message entirely with stdout |
| `prepend` | Prepend stdout to message |
| `append` | Append stdout to message |
| `discard` | Ignore stdout (side-effect only) |

## Working Directory

| Type | Description |
|------|-------------|
| `session` | Use session's working directory |
| `hook` | Use hook file's directory (for relative paths) |

## Hook Application Flow

```
Message
  ↓
Load hooks from MITTO_DIR/hooks/*.yaml
  ↓
Filter by enabled, when, workspace
  ↓
Sort by priority (lower first)
  ↓
Execute each hook in order
  ↓
Transformed message
```

## Error Handling

| Mode | Behavior |
|------|----------|
| `skip` | Continue without the hook on error (default) |
| `fail` | Abort the message on error |

## Key Patterns

### Command Resolution

Commands are resolved as follows:
- `./ or ../` prefix → relative to hook file directory
- Absolute path → used as-is
- Otherwise → PATH lookup

```go
func (h *Hook) ResolveCommand() string {
    if strings.HasPrefix(h.Command, "./") || strings.HasPrefix(h.Command, "../") {
        return filepath.Join(h.HookDir, h.Command)
    }
    return h.Command
}
```

### ShouldApply Logic

Hooks check enabled, workspace filter, and when condition:

```go
func (h *Hook) ShouldApply(isFirstMessage bool, workingDir string) bool {
    if !h.IsEnabled() { return false }
    // Check workspace filter...
    // Check when condition (first, all, all-except-first)...
}
```

### Default Values

| Field | Default |
|-------|---------|
| `enabled` | true |
| `timeout` | 5s |
| `priority` | 100 |
| `input` | message |
| `output` | transform |
| `working_dir` | session |
| `on_error` | skip |

