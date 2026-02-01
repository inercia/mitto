# Mitto Configuration

Mitto uses a YAML configuration file (`.mittorc`) or JSON settings file (`settings.json`) to configure its behavior. This document provides an overview of the configuration options.

## Configuration Files

Mitto looks for configuration in the following locations:

### Settings File (Recommended)

The primary configuration is stored in `settings.json` in your Mitto data directory:

- **macOS**: `~/Library/Application Support/Mitto/settings.json`
- **Linux**: `~/.local/share/mitto/settings.json`
- **Windows**: `%APPDATA%\Mitto\settings.json`

This file is automatically created on first run with default settings and can be edited via the Settings dialog in the web interface.

### YAML Configuration File

You can also use a YAML configuration file:

- **Environment variable**: `MITTORC` (highest priority)
- **macOS**: `~/.mittorc`
- **Linux**: `~/.mittorc` or `$XDG_CONFIG_HOME/.mittorc`
- **Windows**: `%APPDATA%\.mittorc`

Use the `--config` flag to specify a custom configuration file:

```bash
mitto web --config /path/to/config.yaml
```

## Configuration Sections

### ACP Servers

Configure the AI coding agents that Mitto can connect to:

```yaml
acp:
  - auggie:
      command: auggie --acp
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
  - copilot:
      command: copilot --acp
```

Each server has:
- **name** (key): Identifier used to reference the server
- **command**: Shell command to start the ACP server

The first server in the list is the default.

See [ACP Servers Configuration](acp.md) for detailed setup instructions for each supported agent (Claude, Auggie, Copilot).

### Web Interface

Configure the web server, authentication, hooks, and more. See [Web Configuration](web.md) for details.

```yaml
web:
  host: 127.0.0.1
  port: 8080
  theme: v2
  # ... see web.md for full options
```

### UI Settings

Configure UI behavior including confirmation dialogs and macOS-specific settings.

```yaml
ui:
  # Confirmation dialogs
  confirmations:
    delete_session: true  # Show confirmation when deleting conversations (default: true)

  # macOS desktop app settings (see mac.md for details)
  mac:
    hotkeys:
      show_hide:
        key: "cmd+shift+m"
    notifications:
      sounds:
        agent_completed: true
```

See [macOS Configuration](mac.md) for macOS-specific settings like hotkeys and notifications.

## Complete Example

Here's a complete configuration example:

```yaml
# ACP Servers
acp:
  - auggie:
      command: auggie --acp
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest

# Global Prompts (quick-action buttons in the chat interface)
prompts:
  - name: "Continue"
    prompt: "Please continue with the current task."
  - name: "Propose a plan"
    prompt: "Please propose a plan for the current task."

# Web Interface
web:
  host: 127.0.0.1
  port: 8080
  theme: v2

# UI Settings
ui:
  # Confirmation dialogs
  confirmations:
    delete_session: true  # Set to false to skip delete confirmations

  # macOS desktop app settings (optional)
  mac:
    hotkeys:
      show_hide:
        enabled: true
        key: "cmd+shift+m"
    notifications:
      sounds:
        agent_completed: true
```

## Related Documentation

- [ACP Servers](acp.md) - Setup instructions for Claude, Auggie, Copilot
- [Workspace Configuration](workspace.md) - Project-specific `.mittorc` files
- [Conversation Processing](conversations.md) - Message processing rules
- [Web Configuration](web.md) - Web server, authentication, hooks, security
- [macOS Configuration](mac.md) - Hotkeys, notifications, desktop app settings
- [Architecture](../devel/README.md) - System design and internals

## JSON Format

When using `settings.json`, the format is slightly different:

```json
{
  "acp_servers": [
    {"name": "auggie", "command": "auggie --acp"},
    {"name": "claude-code", "command": "npx -y @zed-industries/claude-code-acp@latest"}
  ],
  "web": {
    "host": "127.0.0.1",
    "port": 8080,
    "theme": "v2"
  },
  "ui": {
    "confirmations": {
      "delete_session": true
    },
    "mac": {
      "notifications": {
        "sounds": {
          "agent_completed": true
        }
      }
    }
  }
}
```

