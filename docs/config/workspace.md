# Workspace Configuration

Mitto supports workspace-specific configuration through `.mittorc` files placed in project directories. These settings apply only when working in that specific workspace.

## Overview

| Level | File | Scope |
|-------|------|-------|
| **Global** | `~/.mittorc` or `settings.json` | Applies to all workspaces |
| **Workspace** | `<project>/.mittorc` | Applies only to that workspace |

Workspace configuration is automatically loaded when you open a workspace in Mitto. The file is cached and reloaded every 30 seconds to pick up changes.

## File Location

Place a `.mittorc` file in the root of your project directory:

```
my-project/
├── .mittorc          # Workspace-specific configuration
├── src/
├── tests/
└── ...
```

## Supported Sections

Workspace `.mittorc` files support the following sections:

| Section | Description |
|---------|-------------|
| `prompts` | Quick-action prompts shown in the chat interface |
| `conversations` | Message processing rules (prepend/append text) |

> **Note**: Other sections like `acp`, `web`, and `ui` are ignored in workspace files. These can only be configured globally.

## Prompts

Define workspace-specific prompts that appear as quick-action buttons in the chat interface:

```yaml
prompts:
  - name: "Run Tests"
    prompt: "Run all tests with: npm test"
    backgroundColor: "#BBDEFB"  # Optional: custom button color

  - name: "Build Project"
    prompt: "Build the project with: npm run build"
    backgroundColor: "#E8F5E9"
```

### Prompt Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Button label shown in the UI |
| `prompt` | Yes | Text sent to the AI agent when clicked |
| `backgroundColor` | No | Hex color for the button (e.g., `#E8F5E9`) |

### Prompt Merging

Workspace prompts are **merged** with global prompts:
- Global prompts appear first
- Workspace prompts appear after global prompts
- Both are available in the chat interface

## Conversation Processing

Add text to messages automatically. Useful for project-specific context or reminders:

```yaml
conversations:
  processing:
    # Add project context to the first message
    - when: first
      position: prepend
      text: |
        This is the MyProject codebase.
        
        Key directories:
        - src/: Source code
        - tests/: Test files
        
        Follow the existing code style.
        
        ---

    # Add a reminder to all messages
    - when: all
      position: append
      text: "\n\n[Remember: Run tests before committing]"
```

### Processor Fields

| Field | Values | Description |
|-------|--------|-------------|
| `when` | `first` | Apply only to the first message |
| | `all` | Apply to every message |
| | `all-except-first` | Apply to all messages except the first |
| `position` | `prepend` | Insert text before the user's message |
| | `append` | Insert text after the user's message |
| `text` | string | The text to insert (supports multi-line YAML) |

### Override Mode

By default, workspace processors are merged with global processors. To replace global processors entirely:

```yaml
conversations:
  processing:
    override: true  # Ignore global processors
    processors:
      - when: first
        position: prepend
        text: "Custom workspace-only context..."
```

## Complete Example

```yaml
# Workspace-specific configuration for MyProject
# Place this file at: my-project/.mittorc

# Quick-action prompts for this project
prompts:
  - name: "Run Tests"
    backgroundColor: "#BBDEFB"
    prompt: |
      Run the test suite:
      ```bash
      npm test
      ```
      Fix any failures and report the results.

  - name: "Build & Deploy"
    backgroundColor: "#E8F5E9"
    prompt: |
      Build and deploy the project:
      1. Run `npm run build`
      2. Run `npm run deploy`
      Report any errors.

# Conversation processing
conversations:
  processing:
    - when: first
      position: prepend
      text: |
        You are working on MyProject, a Node.js application.
        
        Project structure:
        - src/: TypeScript source files
        - tests/: Jest test files
        - docs/: Documentation
        
        Conventions:
        - Use TypeScript strict mode
        - Follow ESLint rules
        - Write tests for new features
        
        ---
```

## Related Documentation

- [Configuration Overview](overview.md) - Global configuration options
- [Conversation Processing](conversations.md) - Detailed processor documentation
- [Web Configuration](web.md) - Global prompts and web settings
