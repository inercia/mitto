# ACP Servers

This document covers configuration and setup for the AI coding agents that Mitto can connect to via the Agent Communication Protocol (ACP).

## Overview

ACP servers are AI coding agents that implement the Agent Communication Protocol. Mitto acts as an ACP client, connecting to these servers to provide a unified interface for interacting with different AI agents.

### Configuration

ACP servers are configured in your `~/.mittorc` (YAML) or `settings.json`:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
  - auggie:
      command: auggie --acp
  - copilot:
      command: copilot --acp
```

Each entry consists of:
- **name** - A unique identifier for the server
- **command** - The shell command to start the ACP server

### Per-Server Prompts

You can configure predefined prompts specific to each ACP server:

```yaml
acp:
  - auggie:
      command: auggie --acp
      prompts:
        - name: "Improve Rules"
          prompt: "Please improve the Augment rules based on recent learnings"
        - name: "Run Tests"
          prompt: "Run all tests and fix any failures"
```

---

## Claude Code

[Claude Code](https://github.com/anthropics/claude-code) is Anthropic's AI coding assistant, available as an ACP server through Zed's community package.

### Installation

No installation required—the command uses `npx` to run the latest version:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

### Requirements

- Node.js 18 or later
- Valid Anthropic API key or Claude Max subscription

### Environment Variables

Claude Code uses the following environment variables:

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Your Anthropic API key |

---

## Auggie

[Auggie](https://www.augmentcode.com/) is Augment Code's AI coding assistant with deep codebase understanding.

### Installation

Install Auggie CLI from your Augment Code dashboard, then configure:

```yaml
acp:
  - auggie:
      command: auggie --acp
```

### Requirements

- Auggie CLI installed and authenticated
- Valid Augment Code subscription

### Features

Auggie provides:
- Codebase-wide semantic search
- Multi-file editing
- Test generation
- Code review

---

## GitHub Copilot

[GitHub Copilot CLI](https://github.com/features/copilot) now supports ACP, enabling integration with Mitto and other ACP clients.

> **Note:** ACP support in Copilot CLI is in public preview as of January 2026.

### Installation

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

### Requirements

- GitHub CLI with Copilot extension
- Valid GitHub Copilot subscription

### Features

Copilot ACP enables:
- Isolated sessions with custom working directories
- Streaming updates as the agent works
- Permission requests for tool execution
- Support for text, images, and context resources

### Use Cases

- **IDE integrations** - Build Copilot support into any editor
- **CI/CD pipelines** - Orchestrate agentic coding tasks in automated workflows
- **Custom frontends** - Create specialized interfaces for specific workflows
- **Multi-agent systems** - Coordinate Copilot with other AI agents

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

The web interface shows a dropdown to select from configured ACP servers. You can switch servers between conversations.

## Related Documentation

- [Configuration Overview](config.md) - Main configuration documentation
- [Web Configuration](web.md) - Web server settings
- [Architecture](architecture.md) - System design and internals

