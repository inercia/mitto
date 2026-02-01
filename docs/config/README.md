# Mitto Configuration

This directory contains configuration documentation for Mitto.

## Table of Contents

### Getting Started

- **[Configuration Overview](overview.md)** - Main configuration file locations, formats, and complete examples

### Configuration Topics

- **[ACP Servers](acp.md)** - Setup instructions for AI coding agents (Claude Code, Auggie, GitHub Copilot)

- **[Web Interface](web.md)** - Web server settings, authentication, security, themes, and reverse proxy setup

- **[macOS Desktop App](mac.md)** - Building, hotkeys, notifications, and macOS-specific settings

- **[Workspace Configuration](workspace.md)** - Project-specific `.mittorc` files and workspace prompts

- **[Conversation Processing](conversations.md)** - Message processors for prepending/appending text to messages

- **[Message Hooks](hooks.md)** - External command-based hooks for dynamic message transformation

## Quick Reference

| Topic | Document | Key Sections |
|-------|----------|--------------|
| File locations | [Overview](overview.md) | Configuration Files |
| Claude Code setup | [ACP Servers](acp.md) | Claude Code |
| Auggie setup | [ACP Servers](acp.md) | Auggie |
| GitHub Copilot | [ACP Servers](acp.md) | GitHub Copilot |
| Authentication | [Web](web.md) | Authentication |
| Lifecycle hooks | [Web](web.md) | Lifecycle Hooks |
| ngrok/Tailscale | [Web](web.md) | Using ngrok, Using Tailscale Funnel |
| Global hotkeys | [macOS](mac.md) | Global Hotkeys |
| Notification sounds | [macOS](mac.md) | Notification Sounds |
| Project prompts | [Workspace](workspace.md) | Prompts |
| System prompts | [Conversations](conversations.md) | System Prompt on First Message |
| External hooks | [Hooks](hooks.md) | Hook Configuration Schema |

## Configuration File Locations

### Settings File (Recommended)

The primary configuration is stored in `settings.json`:

| Platform | Location |
|----------|----------|
| **macOS** | `~/Library/Application Support/Mitto/settings.json` |
| **Linux** | `~/.local/share/mitto/settings.json` |
| **Windows** | `%APPDATA%\Mitto\settings.json` |

### YAML Configuration

You can also use a YAML configuration file (`.mittorc`):

| Source | Priority |
|--------|----------|
| `MITTORC` environment variable | Highest |
| `~/.mittorc` | Default |
| `--config` flag | Override |

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

