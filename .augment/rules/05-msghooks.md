---
description: Command processors for pre/post processing, external command execution, message transformation
globs:
  - "internal/processors/**/*"
---

# Command Processors Package

The `internal/processors` package provides external command-based processors for message transformation. Processors are loaded from YAML files in `MITTO_DIR/processors/` directory.

> **Note:** The old `hooks/` directory name is still supported for backward compatibility.

## Quick Reference

| Component    | Purpose                               |
| ------------ | ------------------------------------- |
| `Processor`  | Processor definition loaded from YAML |
| `Loader`     | Loads and validates processor files   |
| `Executor`   | Runs processors as external commands  |
| `Apply*`     | Applies processors to messages        |

## Processor Configuration

Processors are defined in YAML files in `MITTO_DIR/processors/*.yaml`:

```yaml
name: system-prompt
description: Prepends a system prompt to the first message
when: first # first, all, all-except-first
position: prepend # prepend, append
priority: 100 # Lower = earlier execution

command: ./generate-prompt.sh
args: []
input: message # message, conversation, none
output: transform # transform, prepend, append, discard

timeout: 5s
working_dir: session # session, hook
on_error: skip # skip, fail

# Optional: limit to specific workspaces
workspaces:
  - /path/to/project
```

## Input Types

| Type           | Description                            |
| -------------- | -------------------------------------- |
| `message`      | Send message with basic context (JSON) |
| `conversation` | Send full conversation history (JSON)  |
| `none`         | Send nothing to stdin                  |

## Output Types

| Type        | Description                          |
| ----------- | ------------------------------------ |
| `transform` | Replace message entirely with stdout |
| `prepend`   | Prepend stdout to message            |
| `append`    | Append stdout to message             |
| `discard`   | Ignore stdout (side-effect only)     |

## Working Directory

| Type      | Description                                    |
| --------- | ---------------------------------------------- |
| `session` | Use session's working directory                |
| `hook`    | Use processor file's directory (for relative paths) |

## Processor Application Flow

```
Message
  ↓
Load processors from MITTO_DIR/processors/*.yaml
  ↓
Filter by enabled, when, workspace
  ↓
Sort by priority (lower first)
  ↓
Execute each processor in order
  ↓
Transformed message
```

## Error Handling

| Mode   | Behavior                                          |
| ------ | ------------------------------------------------- |
| `skip` | Continue without the processor on error (default) |
| `fail` | Abort the message on error                        |

## Key Patterns

### Command Resolution

Commands are resolved as follows:

- `./ or ../` prefix → relative to processor file directory
- Absolute path → used as-is
- Otherwise → PATH lookup

```go
func (p *Processor) ResolveCommand() string {
    if strings.HasPrefix(p.Command, "./") || strings.HasPrefix(p.Command, "../") {
        return filepath.Join(p.ProcessorDir, p.Command)
    }
    return p.Command
}
```

### ShouldApply Logic

Processors check enabled, workspace filter, and when condition:

```go
func (p *Processor) ShouldApply(isFirstMessage bool, workingDir string) bool {
    if !p.IsEnabled() { return false }
    // Check workspace filter...
    // Check when condition (first, all, all-except-first)...
}
```

### Default Values

| Field         | Default   |
| ------------- | --------- |
| `enabled`     | true      |
| `timeout`     | 5s        |
| `priority`    | 100       |
| `input`       | message   |
| `output`      | transform |
| `working_dir` | session   |
| `on_error`    | skip      |
