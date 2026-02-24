# MCP Server Configuration

Mitto exposes an MCP (Model Context Protocol) server that allows AI agents to:

1. **Introspect** - Query conversations, configuration, and runtime information
2. **Interact with users** - Display UI prompts (buttons, dropdowns) for user input

This enables powerful workflows where AI agents can debug issues, manage conversations,
and request user decisions through interactive UI elements.

## Quick Start

1. Ensure Mitto is running (`mitto web` or the macOS app)
2. Configure your AI agent to use the MCP server at `http://127.0.0.1:5757/mcp`
3. Enable the **"Can prompt user"** flag in the conversation's Advanced Settings if you want UI prompts

## Available Tools

### Introspection Tools

These tools are **always available** and don't require special permissions:

| Tool                             | Description                                                               |
| -------------------------------- | ------------------------------------------------------------------------- |
| `mitto_conversation_list`        | List all conversations with metadata (title, status, message count, etc.) |
| `mitto_get_config`               | Get the current Mitto configuration (sanitized)                           |
| `mitto_get_runtime_info`         | Get runtime info (OS, log paths, data directories, process info)          |
| `mitto_conversation_get_current` | Get details about the current conversation                                |
| `mitto_conversation_get`         | Get details about a specific conversation by ID                           |
| `mitto_conversation_get_summary` | Generate an AI summary of a conversation                                  |

### UI Prompt Tools

These tools require the **"Can prompt user"** flag to be enabled:

| Tool                       | Description                                             |
| -------------------------- | ------------------------------------------------------- |
| `mitto_ui_ask_yes_no`      | Display a yes/no dialog with customizable button labels |
| `mitto_ui_options_buttons` | Display up to 4 buttons for user selection              |
| `mitto_ui_options_combo`   | Display a dropdown with up to 10 options                |

### Cross-Conversation Tools

These tools require the **"Can Send Prompt"** or **"Can start conversation"** flags:

| Tool                             | Description                                     |
| -------------------------------- | ----------------------------------------------- |
| `mitto_conversation_send_prompt` | Send a prompt to another conversation's queue   |
| `mitto_conversation_start`       | Create a new conversation in the same workspace |

## Enabling Permissions

UI prompt tools require permissions to be enabled per-conversation:

1. Open the conversation in Mitto
2. Click on the conversation title/properties panel
3. In **Advanced Settings**, enable:
   - **"Can prompt user"** - For UI prompt tools
   - **"Can Send Prompt"** - For cross-conversation prompts

## Configuration Examples

### Augment Code (Auggie)

Add to `~/.augment/settings.json`:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Claude Desktop

Add to your Claude Desktop configuration:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Claude Code (CLI)

Add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Gemini CLI

Add to your Gemini CLI settings file:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

## Use Cases

### Debugging Conversations

Ask your AI agent:

> "Use Mitto tools to list all conversations and find any that are stuck"

> "Get the Mitto runtime info and tell me where the log files are"

> "Check which conversations have the most messages"

### Interactive Workflows

Enable the "Can prompt user" flag, then ask:

> "Before making changes, ask me to confirm using a yes/no dialog"

> "Present me with options for how to proceed using buttons"

The agent will display UI prompts directly in the Mitto conversation interface,
and the user's selection is returned to the agent.

### Cross-Conversation Automation

Enable the "Can Send Prompt" flag, then:

> "Send the commit message to the other conversation that's waiting for review"

This allows orchestrating work across multiple AI conversations.

## Security Notes

- The MCP server binds to `127.0.0.1` only (localhost)
- UI prompt tools require explicit permission flags per-conversation
- Sensitive configuration data is sanitized in responses
- Cross-conversation prompts require explicit opt-in

## Troubleshooting

### Tools Not Appearing

1. Verify Mitto is running (`mitto web` or macOS app)
2. Check the URL is correct: `http://127.0.0.1:5757/mcp`
3. Restart your AI agent after adding the MCP configuration

### Permission Denied Errors

If you see "requires flag to be enabled":

1. Open the conversation's properties panel
2. Enable the required flag in Advanced Settings
3. The change takes effect immediately

### UI Prompts Not Showing

1. Ensure the "Can prompt user" flag is enabled
2. The conversation must be open in the Mitto UI
3. Check browser console for WebSocket connection errors

## Related Documentation

- [Developer MCP Documentation](../devel/mcp.md) - Full technical details
- [Session Management](../devel/session-management.md) - How sessions work
- [Architecture](../devel/architecture.md) - System overview
