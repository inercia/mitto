# MCP Debug Server

Mitto includes a built-in MCP (Model Context Protocol) server for debugging. This server exposes tools that allow AI agents to inspect Mitto's internal state, making it easier to diagnose issues.

## Overview

The MCP debug server:

- **Binds to `127.0.0.1:5757`** (localhost only for security) in HTTP mode
- **Starts automatically** with the web server (HTTP mode)
- **Supports two transport modes**: HTTP (Streamable HTTP) and STDIO (subprocess)
- **Exposes debugging tools** for conversation inspection and runtime info

## Transport Modes

### HTTP Mode (Default)

Streamable HTTP transport (MCP spec 2025-03-26). The server listens on a TCP port and clients connect via HTTP.

- **URL**: `http://127.0.0.1:5757/mcp`
- **Use case**: When Mitto is running as a web server
- **Protocol**: MCP Streamable HTTP (supports both JSON and SSE responses)

### STDIO Mode

Standard input/output for communication. The MCP server reads JSON-RPC messages from stdin and writes responses to stdout.

- **Use case**: Running the MCP server as a subprocess
- **Configuration**: Set `Mode: "stdio"` in the MCP server config

STDIO mode is useful for:

- Integration with AI agents that spawn MCP servers as subprocesses
- Testing and debugging without network dependencies
- Environments where HTTP is not available

## Available Tools

### `list_conversations`

Lists all conversations with detailed metadata:

| Field            | Description                               |
| ---------------- | ----------------------------------------- |
| `session_id`     | Unique session identifier                 |
| `title`          | User-friendly session name                |
| `description`    | Session description                       |
| `acp_server`     | ACP server name                           |
| `working_dir`    | Working directory                         |
| `created_at`     | Creation timestamp                        |
| `updated_at`     | Last update timestamp                     |
| `message_count`  | Number of events                          |
| `status`         | Session status (active, completed, error) |
| `archived`       | Whether session is archived               |
| `session_folder` | Full path to session directory            |
| `is_running`     | Whether session is currently active       |
| `is_prompting`   | Whether agent is processing a prompt      |
| `is_locked`      | Whether session is locked                 |
| `lock_status`    | Lock status (idle, processing)            |
| `last_seq`       | Last sequence number                      |

### `get_config`

Returns the current effective Mitto configuration (sanitized to exclude sensitive data):

| Field           | Description                           |
| --------------- | ------------------------------------- |
| `acp_servers`   | List of configured ACP servers        |
| `web`           | Web server configuration              |
| `has_prompts`   | Whether global prompts are configured |
| `prompts_count` | Number of global prompts              |
| `session`       | Session storage configuration         |

### `get_runtime_info`

Returns runtime information about the Mitto instance:

| Field           | Description                               |
| --------------- | ----------------------------------------- |
| `os`            | Operating system (darwin, linux, windows) |
| `arch`          | CPU architecture                          |
| `num_cpu`       | Number of CPUs                            |
| `hostname`      | Machine hostname                          |
| `pid`           | Process ID                                |
| `executable`    | Path to Mitto executable                  |
| `working_dir`   | Current working directory                 |
| `go_version`    | Go runtime version                        |
| `num_goroutine` | Number of goroutines                      |
| `data_dir`      | Mitto data directory                      |
| `sessions_dir`  | Sessions directory                        |
| `logs_dir`      | Logs directory                            |
| `log_files`     | Paths to log files                        |
| `config_files`  | Paths to configuration files              |

## Configuring AI Agents

### Augment Code (Auggie)

Add to your Augment settings (`.augment/config.json` or VS Code settings):

```json
{
  "augment.mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

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

Add to your Claude Code configuration:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Cursor

Add to Cursor settings (`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### Generic MCP Client (HTTP Mode)

For any MCP-compatible client using Streamable HTTP transport:

- **URL**: `http://127.0.0.1:5757/mcp`
- **Transport**: Streamable HTTP (MCP spec 2025-03-26)

### STDIO Mode Configuration

For agents that spawn MCP servers as subprocesses, you can run Mitto's MCP server in STDIO mode. This requires a separate command that starts only the MCP server.

**Claude Desktop (STDIO)**:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "command": "mitto",
      "args": ["mcp", "--stdio"]
    }
  }
}
```

**Cursor (STDIO)**:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "command": "mitto",
      "args": ["mcp", "--stdio"]
    }
  }
}
```

> **Note**: The `mitto mcp` command is a standalone MCP server mode. See the CLI documentation for details.

## Example Usage

Once configured, you can ask the AI agent to debug Mitto issues:

> "Use the mitto-debug MCP server to list all conversations and find any that are stuck in prompting state"

> "Get the runtime info from Mitto and tell me where the log files are located"

> "Check the Mitto configuration and verify the ACP servers are properly configured"

## Debugging Workflow

### 1. Get Runtime Information

Start by calling `get_runtime_info` to locate important files:

```
Log files:
- mitto.log: ~/Library/Logs/Mitto/mitto.log
- access.log: ~/Library/Logs/Mitto/access.log
- webview.log: ~/Library/Logs/Mitto/webview.log

Sessions: ~/Library/Application Support/Mitto/sessions/
```

### 2. List Conversations

Call `list_conversations` to find the session you're debugging:

```
Session: 20260211-143052-a1b2c3d4
  Title: "Debug session"
  Folder: ~/Library/Application Support/Mitto/sessions/20260211-143052-a1b2c3d4
  Messages: 42
  Status: active
  Is Prompting: false
```

### 3. Inspect Session Files

Each session folder contains:

| File            | Description                       |
| --------------- | --------------------------------- |
| `events.jsonl`  | All session events (JSONL format) |
| `metadata.json` | Session metadata                  |
| `lock.json`     | Lock information (if locked)      |
| `queue.json`    | Message queue state               |

### 4. Analyze Events

The `events.jsonl` file contains all events with sequence numbers:

```jsonl
{"seq":1,"type":"session_start","timestamp":"2026-02-11T14:30:52Z","data":{...}}
{"seq":2,"type":"user_prompt","timestamp":"2026-02-11T14:30:55Z","data":{"message":"Hello"}}
{"seq":3,"type":"agent_message","timestamp":"2026-02-11T14:30:57Z","data":{"html":"<p>Hi!</p>"}}
```

### 5. Check Logs

Use the log file paths from `get_runtime_info`:

- **mitto.log**: Backend errors, sequence numbers, event persistence
- **access.log**: Authentication, security events
- **webview.log**: Frontend JavaScript errors, WebSocket issues

## Security

The MCP server binds only to `127.0.0.1` (localhost) and cannot be accessed from other machines. This is intentional for security:

- No authentication required (localhost only)
- Exposes internal state for debugging
- Should not be exposed to the network

## Implementation

The MCP server is implemented in `internal/mcpserver/`:

- `server.go`: Server implementation using the official MCP Go SDK
- `types.go`: Response types and helper functions

The server uses the [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) with SSE transport.
