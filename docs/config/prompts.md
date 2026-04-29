# Prompts and Quick Actions

Prompts (also called Quick Actions) are predefined text snippets that appear as buttons
in the chat interface. Clicking a prompt button sends its content to the AI agent,
saving you from typing common requests.

![](prompts-1.png)

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
| 5           | Default workspace prompts dir | `$MITTO_WORKING_DIR/.mitto/prompts/*.md` |
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

| Field             | Required | Type     | Description                                                                                  |
| ----------------- | -------- | -------- | -------------------------------------------------------------------------------------------- |
| `name`            | No\*     | string   | Display name for the button. If omitted, derived from filename.                              |
| `description`     | No       | string   | Tooltip text shown on hover                                                                  |
| `backgroundColor` | No       | string   | Hex color for the button (e.g., `"#E8F5E9"`)                                                 |
| `icon`            | No       | string   | Icon identifier (reserved for future use)                                                    |
| `tags`            | No       | string[] | Categorization tags (reserved for future use)                                                |
| `enabled`         | No       | bool     | Set to `false` to disable the prompt. Default: `true`                                        |
| `enabledWhen`     | No       | string   | CEL expression for conditional enablement. See [below](#enabledwhen-conditional-enablement). |
| `enabledWhenACP`  | No       | string   | Comma-separated ACP server names where this prompt appears                                   |
| `enabledWhenMCP`  | No       | string   | Glob pattern for MCP tools required for this prompt to appear                                |

\*If `name` is not specified, it's derived from the filename (e.g., `code-review.md` →
"code-review").

### Conditional Enablement Overview

Mitto provides a family of `enabled*` fields for controlling when prompts appear:

| Field            | Type   | Evaluated  | Use Case                                    |
| ---------------- | ------ | ---------- | ------------------------------------------- |
| `enabled`        | bool   | At load    | Permanently disable a prompt                |
| `enabledWhen`    | CEL    | At display | Dynamic conditions based on session context |
| `enabledWhenACP` | string | At display | Restrict to specific AI agents              |
| `enabledWhenMCP` | string | At display | Require specific MCP tools to be available  |

**Evaluation order:** If `enabled: false`, the prompt is never loaded. Otherwise, all
three `enabledWhen*` conditions must be satisfied for the prompt to appear.

**Example: Comprehensive prompt configuration**

```yaml
---
name: "JIRA: start work"
description: "Pick a JIRA ticket and spawn parallel conversations"
group: "JIRA"
backgroundColor: "#BBDEFB"
enabled: true
enabledWhen: "!session.isChild"
enabledWhenACP: "auggie, claude-code"
enabledWhenMCP: "jira_*, mitto_conversation_*"
---
```

This prompt:

- Is enabled (not permanently disabled)
- Only appears in parent conversations (not children)
- Only appears when using Auggie or Claude Code
- Only appears when both JIRA and Mitto MCP tools are available

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

## Variable Substitution in Prompts

Prompt text supports `@mitto:variable` placeholders that are automatically replaced with
live session values before the prompt is sent to the AI agent. This is the same variable
substitution system used by [message processors](processors.md#variable-substitution).

### Available Variables

| Variable                       | Description                                                                  |
| ------------------------------ | ---------------------------------------------------------------------------- |
| `@mitto:session_id`            | Current session/conversation ID                                              |
| `@mitto:parent_session_id`     | Parent conversation ID (empty if root session)                               |
| `@mitto:parent`                | Parent session as "id (name)" or empty                                       |
| `@mitto:session_name`          | Conversation title/name                                                      |
| `@mitto:working_dir`           | Session working directory                                                    |
| `@mitto:acp_server`            | ACP server name (e.g., "claude-code")                                        |
| `@mitto:workspace_uuid`        | Workspace identifier                                                         |
| `@mitto:available_acp_servers` | ACP servers for this workspace, comma-separated with tags and current marker |
| `@mitto:children`              | Child sessions, comma-separated with names and ACP servers                   |
| `@mitto:periodic`              | `"true"` if this prompt was triggered by the periodic runner, `"false"` otherwise |

### Behavior

- **Automatic**: Substitution happens after all processors run, on the final assembled
  message — no configuration needed.
- **Unknown variables**: `@mitto:unknown` is left verbatim in the text.
- **Empty values**: e.g., `@mitto:parent_session_id` when there is no parent → replaced
  with empty string.
- **Fast path**: If the prompt text contains no `@mitto:`, the substitution pass is
  skipped entirely.

### Why Use Variables in Prompts?

Variables are especially useful for prompts that instruct the AI agent to call MCP tools.
Many Mitto MCP tools (like `mitto_conversation_new`, `mitto_ui_options`, etc.) require
a `self_id` parameter. By providing `@mitto:session_id` directly in the prompt text, you
eliminate the need for the agent to first call `mitto_conversation_get_current` to discover
its session ID — saving a tool call round-trip.

Similarly, `@mitto:available_acp_servers` and `@mitto:children` provide information that
would otherwise require additional tool calls to retrieve.

### Example

A prompt that helps the agent use Mitto MCP tools efficiently:

```markdown
---
name: "Spawn Workers"
enabledWhenMCP: mitto_conversation_*
---

## Session Context

Your session ID is `@mitto:session_id` — use this as `self_id` for all MCP tool calls.
Available ACP servers: `@mitto:available_acp_servers`
Existing children: `@mitto:children`

## Instructions

Create child conversations to work on subtasks...
```

### Important: Child Session Limitation

When a prompt instructs the agent to create child conversations via
`mitto_conversation_new`, the `initial_prompt` text passed to the child **will not**
benefit from the parent's `@mitto:` substitution for the child's own context. The parent's
`@mitto:session_id` resolves to the parent's ID, not the child's.

Children that need their own session ID (e.g., for `mitto_children_tasks_report`) must
call `mitto_conversation_get_current(self_id: "init")` to discover it. This is the one
case where the tool call cannot be avoided.

### Minimal Example

Front-matter is optional. A file with just content uses the filename as the name:

```markdown
Fix any linting errors in the current file.
```

If saved as `fix-lint.md`, this creates a prompt named "fix-lint".

## enabledWhen: Conditional Enablement

The `enabledWhen` field allows you to conditionally show or hide prompts based on the
current conversation context using [CEL (Common Expression Language)](https://github.com/google/cel-go)
expressions.

### Basic Syntax

```yaml
---
name: "Create Minions"
description: "Break work into parallel tasks"
enabledWhen: "!session.isChild"
---
```

When `enabledWhen` evaluates to `true`, the prompt is visible. When it evaluates to
`false`, the prompt is hidden. If the expression is invalid or evaluation fails, the
prompt is shown (fail-open behavior for safety).

### Available Context Variables

#### ACP Server Context (`acp.*`)

Information about the AI agent (ACP server) used in the current conversation.

| Variable          | Type      | Description                                |
| ----------------- | --------- | ------------------------------------------ |
| `acp.name`        | string    | ACP server name (e.g., `"Claude Code"`)    |
| `acp.type`        | string    | Server type (e.g., `"claude"`, `"auggie"`) |
| `acp.tags`        | list[str] | Server tags (e.g., `["coding", "fast"]`)   |
| `acp.autoApprove` | bool      | Whether auto-approve is enabled            |

#### Workspace Context (`workspace.*`)

Information about the current workspace.

| Variable           | Type   | Description                  |
| ------------------ | ------ | ---------------------------- |
| `workspace.uuid`   | string | Unique workspace identifier  |
| `workspace.folder` | string | Workspace directory path     |
| `workspace.name`   | string | Display name (if configured) |

#### Session Context (`session.*`)

Information about the current conversation/session.

| Variable              | Type   | Description                               |
| --------------------- | ------ | ----------------------------------------- |
| `session.id`          | string | Session identifier                        |
| `session.name`        | string | Session display name                      |
| `session.isChild`     | bool   | `true` if this is a child conversation    |
| `session.isAutoChild` | bool   | `true` if created automatically by parent |
| `session.parentId`    | string | Parent session ID (empty if not a child)  |

#### Parent Context (`parent.*`)

Information about the parent conversation (only meaningful for child sessions).

| Variable           | Type   | Description                     |
| ------------------ | ------ | ------------------------------- |
| `parent.exists`    | bool   | `true` if parent session exists |
| `parent.name`      | string | Parent session name             |
| `parent.acpServer` | string | ACP server used by parent       |

#### Children Context (`children.*`)

Information about child conversations spawned from this session.

| Variable              | Type      | Description                          |
| --------------------- | --------- | ------------------------------------ |
| `children.count`      | int       | Number of direct child sessions      |
| `children.exists`     | bool      | `true` if has at least one child     |
| `children.names`      | list[str] | List of child session names          |
| `children.acpServers` | list[str] | List of ACP servers used by children |

#### MCP Tools Context (`tools.*`)

Information about available MCP tools. Note: Tool information may not be available
immediately when a session starts.

| Variable          | Type      | Description                         |
| ----------------- | --------- | ----------------------------------- |
| `tools.available` | bool      | `true` if tool list has been loaded |
| `tools.names`     | list[str] | List of available tool names        |

**Custom function:**

| Function                 | Returns | Description                                 |
| ------------------------ | ------- | ------------------------------------------- |
| `tools.hasPattern(glob)` | bool    | `true` if any tool matches the glob pattern |

The glob pattern supports `*` (any characters) and `?` (single character).

### CEL Expression Examples

#### Session Hierarchy

```yaml
# Only show in parent conversations (not in children)
enabledWhen: "!session.isChild"

# Only show in child conversations
enabledWhen: "session.isChild"

# Only show in manually-created child conversations
enabledWhen: "session.isChild && !session.isAutoChild"

# Show only if this session has spawned children
enabledWhen: "children.exists"

# Show only if this session has no children
enabledWhen: "children.count == 0"
```

#### ACP Server Filtering

```yaml
# Only for Claude-based servers
enabledWhen: 'acp.name.startsWith("Claude")'

# Only for servers tagged with "coding"
enabledWhen: '"coding" in acp.tags'

# Only for fast models
enabledWhen: '"fast" in acp.tags || "quick" in acp.tags'

# Only when auto-approve is disabled
enabledWhen: "!acp.autoApprove"
```

#### MCP Tool Requirements

```yaml
# Only show if GitHub tools are available
enabledWhen: 'tools.hasPattern("github_*")'

# Only show if Jira tools are available
enabledWhen: 'tools.hasPattern("jira_*")'

# Only show if any database tool is available
enabledWhen: 'tools.hasPattern("*_database_*") || tools.hasPattern("*_sql_*")'

# Only when tools have been loaded
enabledWhen: "tools.available"
```

#### Combined Conditions

```yaml
# Coordinator prompt: only in parent sessions with coding servers
enabledWhen: '!session.isChild && "coding" in acp.tags'

# Report-to-parent prompt: only in children with existing parent
enabledWhen: "session.isChild && parent.exists"

# GitHub PR prompt: only with GitHub tools and not in child sessions
enabledWhen: '!session.isChild && tools.hasPattern("github_*")'

# Complex workspace check
enabledWhen: 'workspace.folder.contains("my-project") && "fast" in acp.tags'
```

#### Real-World Examples from Builtin Prompts

These examples are from Mitto's built-in prompts:

```yaml
# "Create minions" - Spawn parallel worker conversations
# Only in parent conversations, requires Mitto MCP tools
enabledWhen: "!session.isChild"
enabledWhenMCP: mitto_conversation_*

# "Report to parent" - Send status back to parent
# Only in child conversations that have a parent
enabledWhen: "session.isChild && parent.exists"

# "Continue work in child" - Resume work in existing child
# Only when the session has spawned children
enabledWhen: "children.exists"

# "JIRA: start work" - Pick a ticket and spawn workers
# Only in parent conversations, requires both JIRA and Mitto tools
enabledWhen: "!session.isChild"
enabledWhenMCP: jira_*, mitto_conversation_*

# "Improve Augment rules" - Update .augment/rules
# Only when using Auggie (not Claude Code or other agents)
enabledWhenACP: auggie

# "Handoff to new conversation" - Continue in a new session
# Only in parent conversations, requires Mitto tools
enabledWhen: "!session.isChild"
enabledWhenMCP: mitto_conversation_*
```

### CEL Language Reference

CEL is a simple expression language designed for evaluation. Key features:

**Operators:**

- Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`
- Logical: `&&` (and), `||` (or), `!` (not)
- Membership: `in` (e.g., `"tag" in acp.tags`)
- Ternary: `condition ? value_if_true : value_if_false`

**String functions:**

- `str.startsWith(prefix)` - Check prefix
- `str.endsWith(suffix)` - Check suffix
- `str.contains(substring)` - Check substring
- `str.matches(regex)` - Regex matching
- `str.size()` - String length

**List functions:**

- `list.size()` - List length
- `value in list` - Membership check
- `list.exists(x, condition)` - Any element matches
- `list.all(x, condition)` - All elements match

**Examples:**

```cel
// String operations
acp.name.startsWith("Claude")
workspace.folder.contains("/projects/")

// List operations
acp.tags.size() > 0
acp.tags.exists(t, t == "coding")
children.names.all(n, n.startsWith("Worker"))

// Ternary
children.count > 5 ? true : acp.autoApprove
```

For full CEL documentation, see the [CEL Language Definition](https://github.com/google/cel-spec/blob/master/doc/langdef.md).

### Error Handling

- **Invalid expression syntax**: Prompt is shown (fail-open), warning logged
- **Evaluation error**: Prompt is shown (fail-open), warning logged
- **Missing context**: Default values used (empty strings, false booleans, zero counts)
- **Tools not yet loaded**: `tools.available` is `false`, `tools.names` is empty

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
- [Message Processors](processors.md) - Dynamic message transformation
- [Web Interface Configuration](web/README.md) - Web interface settings
- [macOS App Configuration](mac/README.md) - Desktop app settings
