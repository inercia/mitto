# ACP Servers

This document covers configuration and setup for the AI coding agents that Mitto can connect to via the Agent Communication Protocol (ACP).

## Overview

ACP servers are AI coding agents that implement the Agent Communication Protocol. Mitto acts as an ACP client, connecting to these servers to provide a unified interface for interacting with different AI agents.

## Configuration

ACP servers are configured in your `~/.mittorc` (YAML) or `settings.json`:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
  - auggie:
      command: auggie --acp --allow-indexing
  - copilot:
      command: copilot --acp
```

Each entry consists of:

- **name** - A unique identifier for the server
- **command** - The shell command to start the ACP server
- **type** (optional) - A category/class for the server, used for prompt matching (see [Server Types](#server-types))
- **cwd** (optional) - The working directory for the ACP server process

The first server in the list is the default.

### Working Directory (cwd)

The `cwd` option sets the working directory in which the ACP server process will run. This is useful when the agent needs to be started from a specific directory:

```yaml
acp:
  - my-agent:
      command: my-agent --acp
      cwd: /home/user/my-project
```

This is cleaner than using shell wrappers like `sh -c 'cd /some/dir && command'`.

> **Note:** When using [restricted runners](restricted.md), the `cwd` option is not supported. A warning will be logged if `cwd` is specified with a restricted runner.

### Multiple Configurations of the Same Agent

You can define multiple ACP servers using the same executable with different arguments. This is useful for creating different "profiles" of the same agent:

```yaml
acp:
  # Auggie with full indexing (slower startup, better context)
  - auggie-indexed:
      command: auggie --allow-indexing --acp

  # Auggie without indexing (faster startup)
  - auggie-fast:
      command: auggie --acp

  # Claude Code with permission auto-approve (use with caution!)
  - claude-code-yolo:
      command: npx -y @zed-industries/claude-code-acp@latest --dangerously-skip-permissions

  # Claude Code with standard permissions
  - claude-code-safe:
      command: npx -y @zed-industries/claude-code-acp@latest
```

This allows you to:

- **Create workspaces with different agent configurations** for the same project folder
- **Use the Duplicate Workspace feature** to quickly set up the same project with different agent profiles
- **Switch between configurations** depending on the task (e.g., quick questions vs. complex refactoring)

### Server Types

When you have multiple configurations of the same agent (like `auggie-fast` and `auggie-smart`), you can assign them a shared **type** so that prompts can target all servers of that type:

```yaml
acp:
  # Both servers share type "auggie"
  - auggie-fast:
      command: auggie --acp --model fast
      type: auggie
  - auggie-smart:
      command: auggie --acp --model smart
      type: auggie
  # Server without type - uses name as type
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

Now a prompt file with `acps: auggie` will match **both** `auggie-fast` and `auggie-smart`:

```yaml
---
name: "Improve Rules"
acps: auggie
---
Please improve the Augment rules based on recent learnings.
```

If the `type` field is not specified, the server's name is used as its type. This means:

- `acps: auggie` matches a server named "auggie" (or any server with `type: auggie`)
- `acps: auggie-fast` matches only the server named "auggie-fast"

This is useful when you want to:

- **Share prompts across variants** of the same agent
- **Create agent-specific prompts** that work regardless of which variant is active
- **Organize servers by underlying agent** while keeping unique names

### Per-Server Prompts

You can configure predefined prompts specific to each ACP server:

```yaml
acp:
  - auggie:
      command: auggie --acp --allow-indexing
      prompts:
        - name: "Improve Rules"
          prompt: "Please improve the Augment rules based on recent learnings"
        - name: "Run Tests"
          prompt: "Run all tests and fix any failures"
```

---

## Supported Agents

### Claude Code

[Claude Code](https://github.com/anthropics/claude-code) is Anthropic's AI coding assistant, available as an ACP server through Zed's community package.

#### Installation

No installation required—the command uses `npx` to run the latest version:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

#### Requirements

- Node.js 18 or later
- Valid Anthropic API key or Claude Max subscription

#### Environment Variables

| Variable            | Description            |
| ------------------- | ---------------------- |
| `ANTHROPIC_API_KEY` | Your Anthropic API key |

---

### Auggie

[Auggie](https://www.augmentcode.com/) is Augment Code's AI coding assistant with deep codebase understanding.

#### Installation

Install Auggie CLI from your Augment Code dashboard, then configure:

```yaml
acp:
  - auggie:
      command: auggie --acp --allow-indexing
```

#### Requirements

- Auggie CLI installed and authenticated
- Valid Augment Code subscription

#### Features

- Codebase-wide semantic search
- Multi-file editing
- Test generation
- Code review

---

### GitHub Copilot

[GitHub Copilot CLI](https://github.com/features/copilot) supports ACP, enabling integration with Mitto.

> **Note:** ACP support in Copilot CLI is in public preview as of January 2026.

#### Installation

Install the GitHub CLI with Copilot extension, then configure:

```yaml
acp:
  - copilot:
      command: copilot --acp
```

Or connect via TCP on a specific port:

```yaml
acp:
  - copilot:
      command: copilot --acp --port 8080
```

#### Requirements

- GitHub CLI with Copilot extension
- Valid GitHub Copilot subscription

#### Features

- Isolated sessions with custom working directories
- Streaming updates as the agent works
- Permission requests for tool execution
- Support for text, images, and context resources

For more information, see the [GitHub Copilot ACP documentation](https://docs.github.com/en/copilot/acp).

---

## Selecting an ACP Server

### Command Line

```bash
# Use a specific server
mitto chat --server claude-code

# Start web interface with a specific server
mitto web --server auggie
```

### Web Interface

When creating a new conversation, the web interface shows a workspace selector where you can choose which ACP server to use. Each workspace is paired with a specific ACP server, so selecting a workspace also selects its configured agent.

You can configure multiple workspaces for the same project folder with different ACP servers using the **Duplicate Workspace** feature in Settings.

---

## Related Documentation

- [Configuration Overview](overview.md) - Main configuration documentation
- [Workspaces](web/workspace.md) - Workspace configuration and management
- [Prompts](prompts.md) - Predefined prompts and quick actions
- [Restricted Runners](restricted.md) - Sandboxing agent execution
- [Web Interface](web/README.md) - Web server settings
