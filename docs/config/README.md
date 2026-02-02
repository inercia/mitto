# Mitto Configuration

This directory contains configuration documentation for Mitto, organized by platform and
topic.

## Table of Contents

### Platform-Specific Documentation

| Platform             | Documentation                  | Description                                        |
| -------------------- | ------------------------------ | -------------------------------------------------- |
| 🌐 **Web Interface** | [web/README.md](web/README.md) | Web server, auth, security (works on any platform) |
| 🍎 **macOS App**     | [mac/README.md](mac/README.md) | Native macOS Desktop App features                  |

### Web Interface Topics

| Topic                  | Document                             | Description                                  |
| ---------------------- | ------------------------------------ | -------------------------------------------- |
| 🤖 **ACP Servers**     | [web/acp.md](web/acp.md)             | Claude Code, Auggie, GitHub Copilot setup    |
| 📁 **Workspace**       | [web/workspace.md](web/workspace.md) | Project-specific `.mittorc` files            |
| 🌐 **External Access** | [ext-access.md](ext-access.md)       | Tailscale, ngrok, Cloudflare tunneling setup |

### Common Configuration (All Platforms)

| Topic                | Document                             | Description                                   |
| -------------------- | ------------------------------------ | --------------------------------------------- |
| 📋 **Overview**      | [overview.md](overview.md)           | File locations, formats, complete examples    |
| ⚡ **Prompts**       | [prompts.md](prompts.md)             | Quick actions and predefined prompts          |
| 💬 **Conversations** | [conversations.md](conversations.md) | Message processors (prepend/append)           |
| 🔗 **Hooks**         | [hooks.md](hooks.md)                 | External command-based message transformation |

## Quick Reference

| Topic               | Document                          | Key Sections                   |
| ------------------- | --------------------------------- | ------------------------------ |
| File locations      | [Overview](overview.md)           | Configuration Files            |
| Claude Code setup   | [ACP Servers](web/acp.md)         | Claude Code                    |
| Auggie setup        | [ACP Servers](web/acp.md)         | Auggie                         |
| GitHub Copilot      | [ACP Servers](web/acp.md)         | GitHub Copilot                 |
| Authentication      | [Web Interface](web/README.md)    | Authentication                 |
| Lifecycle hooks     | [Web Interface](web/README.md)    | Lifecycle Hooks                |
| External access     | [External Access](ext-access.md)  | Tailscale, ngrok, Cloudflare   |
| Global hotkeys      | [macOS App](mac/README.md)        | Global Hotkeys                 |
| Notification sounds | [macOS App](mac/README.md)        | Notification Sounds            |
| Quick actions       | [Prompts](prompts.md)             | Prompt Sources, File Format    |
| Global prompts      | [Prompts](prompts.md)             | Global Prompts Directory       |
| Project prompts     | [Workspace](web/workspace.md)     | Prompts                        |
| System prompts      | [Conversations](conversations.md) | System Prompt on First Message |
| External hooks      | [Hooks](hooks.md)                 | Hook Configuration Schema      |

## Configuration File Locations

### Settings File (Recommended)

The primary configuration is stored in `settings.json`:

| Platform    | Location                                            |
| ----------- | --------------------------------------------------- |
| **macOS**   | `~/Library/Application Support/Mitto/settings.json` |
| **Linux**   | `~/.local/share/mitto/settings.json`                |
| **Windows** | `%APPDATA%\Mitto\settings.json`                     |

### YAML Configuration

You can also use a YAML configuration file (`.mittorc`):

| Source                         | Priority |
| ------------------------------ | -------- |
| `MITTORC` environment variable | Highest  |
| `~/.mittorc`                   | Default  |
| `--config` flag                | Override |

### Workspace Configuration

Project-specific settings in `<project>/.mittorc`:

```
my-project/
├── .mittorc          # Workspace-specific configuration
├── src/
└── ...
```

## Sample Configuration

See [sample.mittorc](../../sample.mittorc) for a complete configuration example.

## Related Documentation

- [Usage Guide](../usage.md) - Commands, flags, and usage examples
- [Development](../development.md) - Building, testing, and contributing
- [Architecture](../devel/README.md) - System design and internals
