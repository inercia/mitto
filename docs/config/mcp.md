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
| `mitto_conversation_history`     | Search and retrieve conversation history events with filtering by type, text content, tool name, and sequence range. Supports pagination. |
| `mitto_workspace_list`           | List all configured workspaces with their settings, metadata, and activity status. |

### UI Prompt Tools

These tools require the **"Can prompt user"** flag to be enabled:

| Tool                       | Description                                                            |
| -------------------------- | ---------------------------------------------------------------------- |
| `mitto_ui_options`         | Present an options menu with optional descriptions and free text input |
| `mitto_ui_textbox`         | Present a text editing dialog to the user and wait for their changes. Returns the edited text or a diff. |
| `mitto_ui_form`            | Present a sanitized HTML form to the user. Returns submitted field values as key-value pairs. |
| `mitto_ui_notify`          | Send a non-blocking notification to the user. Supports styles: 'info', 'success', 'warning', 'error'. Can optionally play a sound or trigger a native OS notification. |

### Cross-Conversation Tools

These tools require the **"Can Send Prompt"** or **"Can start conversation"** flags:

| Tool                             | Description                                     |
| -------------------------------- | ----------------------------------------------- |
| `mitto_conversation_send_prompt` | Send a prompt to another conversation's queue   |
| `mitto_conversation_new`         | Create a new conversation in the same workspace |

### Session Lifecycle Tools

These tools require the **"Can Send Prompt"** flag or appropriate permissions:

| Tool                              | Description                                                                            |
| --------------------------------- | -------------------------------------------------------------------------------------- |
| `mitto_conversation_set_periodic` | Configure a conversation to run periodically with a scheduled prompt                   |
| `mitto_conversation_archive`      | Archive or unarchive a conversation                                                    |
| `mitto_conversation_delete`       | Delete a child conversation (caller must be parent)                                    |
| `mitto_conversation_wait`         | Wait until an event occurs in a conversation (e.g., agent finishes responding)         |
| `mitto_conversation_update`       | Update conversation properties such as title and user-defined metadata attributes.     |

### Parent-Child Task Coordination Tools

These tools require the **"Can Send Prompt"** flag:

| Tool                         | Description                                                                    |
| ---------------------------- | ------------------------------------------------------------------------------ |
| `mitto_children_tasks_wait`  | Send a progress inquiry to child conversations and block until they all report back |
| `mitto_children_tasks_report`| Report task completion/progress back to a waiting parent conversation          |

## Enabling Permissions

UI prompt tools require permissions to be enabled per-conversation:

1. Open the conversation in Mitto
2. Click on the conversation title/properties panel
3. In **Advanced Settings**, enable:
   - **"Can prompt user"** - For UI prompt tools
   - **"Can Send Prompt"** - For cross-conversation prompts
   - **"Can start conversation"** - For creating new conversations

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
