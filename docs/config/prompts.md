# Prompts and Quick Actions

Prompts (also called Quick Actions) are predefined text snippets that appear as buttons
in the chat interface. Clicking a prompt button sends its content to the AI agent,
saving you from typing common requests.

## Overview

Prompts appear in a dropdown menu above the chat input. They are organized into two
sections:

- **Workspace** (green folder icon) - Prompts from the current workspace's `.mittorc`
  file
- **Global** (gear icon) - Prompts from settings, global files, and built-in defaults

When you hover over a prompt button, a tooltip shows its description (if provided).

## Prompt Sources

Prompts can be defined in multiple locations. When prompts have the same name,
higher-priority sources override lower-priority ones.

| Priority    | Source                        | Location                         |
| ----------- | ----------------------------- | -------------------------------- |
| 1 (lowest)  | Built-in defaults             | `config/config.default.yaml`     |
| 2           | Global prompts directory      | `MITTO_DIR/prompts/*.md`         |
| 3           | Additional prompts dirs       | `prompts_dirs` in settings       |
| 4           | User settings file            | `MITTO_DIR/settings.yaml`        |
| 5           | Default workspace prompts dir | `$WORKSPACE/.mitto/prompts/*.md` |
| 6           | Workspace prompts dirs        | `prompts_dirs` in `.mittorc`     |
| 7 (highest) | Workspace `.mittorc` prompts  | `prompts:` in `.mittorc`         |

### 1. Built-in Default Prompts

Mitto includes a set of default prompts for common workflows. These are defined in
`config/config.default.yaml` and cannot be modified directly, but can be overridden by
defining a prompt with the same name in any higher-priority source.

Default prompts include:

- **Continue** - Resume the current task
- **Propose a plan** - Create a detailed plan
- **Summarize** - Summarize the conversation
- **Commit changes** - Create a git commit
- And more...

### 2. Global Prompts Directory

Store reusable prompts as markdown files in the global prompts directory:

| Platform | Location                                       |
| -------- | ---------------------------------------------- |
| macOS    | `~/Library/Application Support/Mitto/prompts/` |
| Linux    | `~/.local/share/mitto/prompts/`                |

Files must have a `.md` extension. Subdirectories are supported for organization:

```
prompts/
├── code-review.md
├── git/
│   ├── commit.md
│   └── pr-description.md
└── testing/
    └── write-tests.md
```

See [File Format](#file-format-for-global-prompts) below for the full specification.

### 3. User Settings File

Define prompts in your `settings.yaml` file under the `prompts:` key:

```yaml
# MITTO_DIR/settings.yaml
prompts:
  - name: "My Custom Prompt"
    prompt: "Do something specific..."
    backgroundColor: "#E8F5E9"
```

### 4. Default Workspace Prompts Directory

Mitto automatically searches for prompts in the `.mitto/prompts/` directory at the root
of each workspace. This allows you to store project-specific prompts directly in your
repository without any additional configuration.

```
my-project/
├── .mitto/
│   └── prompts/
│       ├── code-review.md
│       ├── deploy.md
│       └── run-tests.md
├── src/
└── package.json
```

This directory is automatically searched when you open the workspace - no `.mittorc`
configuration is required. The prompts use the same markdown format as global prompts
(see [File Format](#file-format-for-global-prompts)).

**Benefits:**

- **Zero configuration** - Just create the directory and add prompts
- **Version controlled** - Commit prompts alongside your code
- **Team sharing** - Share project-specific prompts with your team
- **Portable** - Prompts travel with the repository

**Priority:** Default workspace prompts are searched after global prompts but before
`prompts_dirs` configured in `.mittorc`. Prompts with the same name in higher-priority
sources will override those in `.mitto/prompts/`.

### 5. Workspace `.mittorc` File

Define workspace-specific prompts in a `.mittorc` file at the root of your project:

```yaml
# my-project/.mittorc
prompts:
  - name: "Run Tests"
    prompt: "Run the test suite with: npm test"
    backgroundColor: "#BBDEFB"

  - name: "Build Project"
    prompt: "Build the project with: npm run build"
    backgroundColor: "#E8F5E9"
```

Workspace prompts have the highest priority and appear in a separate "Workspace" section
in the UI.

## Additional Prompts Directories

You can configure additional directories to search for prompt files using the
`prompts_dirs` option. This allows you to:

- Share prompts across multiple projects
- Organize prompts in team-shared directories
- Keep project-specific prompts in custom locations

### Global `prompts_dirs` (in settings)

Add additional directories to search after the default `MITTO_DIR/prompts/`:

```yaml
# ~/.mittorc or MITTO_DIR/settings.yaml
prompts_dirs:
  - "/shared/team/prompts"
  - "/Users/me/my-prompts"
```

These directories are searched in order, with later directories overriding earlier ones
when prompts have the same name. All paths should be absolute.

### Workspace `prompts_dirs` (in `.mittorc`)

Add workspace-specific prompt directories:

```yaml
# my-project/.mittorc
prompts_dirs:
  - ".prompts" # Relative to workspace root
  - "/shared/team/prompts" # Absolute path

prompts:
  - name: "Inline Prompt"
    prompt: "This has highest priority"
```

**Path resolution:**

- Relative paths are resolved against the workspace root directory
- Absolute paths are used as-is
- Non-existent directories are silently ignored

**Priority order within workspace:**

1. Default `.mitto/prompts/` directory (lowest priority, automatically searched)
2. Prompts from `prompts_dirs` (in order listed)
3. Inline `prompts:` entries (highest priority)

### Example: Team Shared Prompts

```yaml
# ~/.mittorc (global)
prompts_dirs:
  - "/Users/Shared/team-prompts"

# my-project/.mittorc (workspace)
prompts_dirs:
  - ".prompts"  # Project-specific prompts

prompts:
  - name: "Deploy"
    prompt: "Deploy to staging environment"
```

In this setup:

1. `MITTO_DIR/prompts/` is always searched first
2. `/Users/Shared/team-prompts/` is searched next (from global config)
3. `my-project/.mitto/prompts/` is searched (default workspace prompts)
4. `my-project/.prompts/` is searched (from workspace `prompts_dirs`)
5. Inline prompts from `.mittorc` have highest priority

## File Format for Global Prompts

Global prompt files use markdown with YAML front-matter:

```markdown
---
name: "Code Review"
description: "Review code for bugs and improvements"
backgroundColor: "#E8F5E9"
icon: "code"
tags: ["review", "quality"]
enabled: true
---

Please review the following code for:

- Bugs and potential issues
- Performance improvements
- Code style and best practices
- Security vulnerabilities

Provide specific suggestions with code examples where applicable.
```

### Front-matter Fields

| Field             | Required | Type     | Description                                                     |
| ----------------- | -------- | -------- | --------------------------------------------------------------- |
| `name`            | No\*     | string   | Display name for the button. If omitted, derived from filename. |
| `description`     | No       | string   | Tooltip text shown on hover                                     |
| `backgroundColor` | No       | string   | Hex color for the button (e.g., `"#E8F5E9"`)                    |
| `icon`            | No       | string   | Icon identifier (reserved for future use)                       |
| `tags`            | No       | string[] | Categorization tags (reserved for future use)                   |
| `enabled`         | No       | bool     | Set to `false` to disable the prompt. Default: `true`           |

\*If `name` is not specified, it's derived from the filename (e.g., `code-review.md` →
"code-review").

### Multi-line Prompts

The prompt content (after the front-matter) can span multiple lines and supports full
markdown:

```markdown
---
name: "Detailed Analysis"
---

Please analyze the code with the following criteria:

## Performance

- Identify bottlenecks
- Suggest optimizations

## Security

- Check for vulnerabilities
- Review input validation

## Maintainability

- Assess code clarity
- Suggest refactoring opportunities
```

### Minimal Example

Front-matter is optional. A file with just content uses the filename as the name:

```markdown
Fix any linting errors in the current file.
```

If saved as `fix-lint.md`, this creates a prompt named "fix-lint".

## Priority and Override Behavior

When multiple sources define prompts with the same name, the higher-priority source
wins:

1. **Workspace `.mittorc`** overrides everything
2. **User settings** overrides global files and defaults
3. **Global prompts directory** overrides built-in defaults
4. **Built-in defaults** are used if no override exists

### Disabling a Built-in Prompt

To disable a built-in prompt, create a prompt with the same name and set
`enabled: false`:

**Option 1: In global prompts directory**

```markdown
---
name: "Continue"
enabled: false
---
```

**Option 2: In settings.yaml**

```yaml
prompts:
  - name: "Continue"
    prompt: "" # Empty prompt effectively disables it
```

### Overriding a Built-in Prompt

To customize a built-in prompt, create one with the same name:

```markdown
---
name: "Continue"
description: "Resume work with my custom workflow"
backgroundColor: "#FFF3E0"
---

Continue with the current task. Before proceeding:

1. Run `git status` to check for uncommitted changes
2. Review the task list
3. Pick up where we left off
```

## Examples

### Code Review Prompt

```markdown
---
name: "Code Review"
description: "Thorough code review with actionable feedback"
backgroundColor: "#E8F5E9"
tags: ["review", "quality"]
---

Please review the code I'm about to share. Focus on:

## Correctness

- Logic errors and edge cases
- Proper error handling
- Type safety issues

## Performance

- Unnecessary computations
- Memory leaks
- N+1 queries

## Maintainability

- Code clarity and naming
- DRY violations
- Missing documentation

## Security

- Input validation
- Authentication/authorization
- Data exposure

Provide specific, actionable feedback with code examples.
```

### Git Workflow Prompt

```markdown
---
name: "Git Commit"
description: "Generate a conventional commit message"
backgroundColor: "#FFF3E0"
tags: ["git", "workflow"]
---

Generate a commit message for the staged changes.

Follow the Conventional Commits format:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation
- `refactor:` for code refactoring
- `test:` for adding tests
- `chore:` for maintenance tasks

Include a brief description and bullet points for details if needed.
```

### Testing Prompt

```markdown
---
name: "Write Tests"
description: "Generate comprehensive tests for the current code"
backgroundColor: "#FCE4EC"
tags: ["testing"]
---

Write comprehensive tests for the code I'll share.

Requirements:

1. Cover happy path and edge cases
2. Include error scenarios
3. Use descriptive test names
4. Follow existing test patterns in the codebase
5. Aim for high coverage of critical paths

Use the project's existing test framework and conventions.
```

## CLI Commands

List all global prompts:

```bash
mitto prompts list
```

Output:

```
Prompts directory: /Users/me/Library/Application Support/Mitto/prompts

NAME         DESCRIPTION                               FILE
----         -----------                               ----
Code Review  Thorough code review with actionable...   code-review.md
Git Commit   Generate a conventional commit message    git/commit.md
Write Tests  Generate comprehensive tests for the...   testing/write-tests.md

Total: 3 prompt(s)
```

## Hot Reload

Global prompts are automatically reloaded when the prompts dropdown is opened and the
directory has changed. You don't need to restart Mitto after adding or modifying prompt
files.

## Related Documentation

- [Workspace Configuration](web/workspace.md) - Workspace-specific prompts in `.mittorc`
- [Configuration Overview](overview.md) - Global settings including prompts
- [Message Hooks](hooks.md) - Dynamic message transformation
- [Web Interface Configuration](web/README.md) - Web interface settings
- [macOS App Configuration](mac/README.md) - Desktop app settings
