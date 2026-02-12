---
description: Using MCP tools for debugging Mitto issues
keywords:
  - mcp debug
  - mcp tools
  - events.jsonl
  - conversation inspection
  - event replay
  - troubleshoot
  - get_runtime_info
  - list_conversations
  - get_config
---

# Using MCP Tools for Debugging

Mitto includes a built-in MCP (Model Context Protocol) server that exposes debugging tools. Use these tools to inspect conversations, configuration, and runtime state when troubleshooting issues.

**For MCP server development patterns, see `42-mcpserver-development.md`.**
**For log file debugging, see `41-debugging-logs.md`.**

## Available MCP Tools

### 1. `list_conversations`

Lists all conversations with metadata:
- Session ID, title, description
- ACP server and working directory
- Created/updated timestamps
- Message count, status, archived state
- **Session folder path** (contains `events.jsonl`)
- Runtime: `is_running`, `is_prompting`, `last_seq`

### 2. `get_config`

Returns the current effective Mitto configuration (sanitized).

### 3. `get_runtime_info`

Returns runtime information:
- OS, architecture, hostname
- Process ID, executable path
- **Log file paths** (`mitto.log`, `access.log`, `webview.log`)
- Data directories, configuration file paths
- Environment variables

## Debugging Workflow

### Step 1: Get Runtime Info

First, call `get_runtime_info` to locate log files and directories:

```json
{
  "logs_dir": "~/Library/Logs/Mitto",
  "log_files": {
    "main_log": "~/Library/Logs/Mitto/mitto.log",
    "access_log": "~/Library/Logs/Mitto/access.log",
    "webview_log": "~/Library/Logs/Mitto/webview.log"
  },
  "sessions_dir": "~/Library/Application Support/Mitto/sessions"
}
```

### Step 2: List Conversations

Call `list_conversations` to find the problematic session:

```json
{
  "session_id": "20260211-143052-a1b2c3d4",
  "title": "Debug session",
  "session_folder": "~/Library/Application Support/Mitto/sessions/20260211-143052-a1b2c3d4",
  "message_count": 42,
  "is_prompting": false,
  "last_seq": 42
}
```

### Step 3: Inspect Events

The `events.jsonl` file in the session folder contains all events:

```bash
# View all events
cat "$SESSION_FOLDER/events.jsonl" | jq .

# Filter by event type
cat "$SESSION_FOLDER/events.jsonl" | jq 'select(.type == "agent_message")'

# Find events by sequence number
cat "$SESSION_FOLDER/events.jsonl" | jq 'select(.seq >= 10 and .seq <= 20)'
```

### Step 4: Check Log Files

**For message/streaming issues:**
- Check `mitto.log` for backend errors and sequence numbers
- Check `webview.log` for frontend JavaScript errors

**For authentication issues:**
- Check `access.log` for security events

```bash
# Tail logs in real-time
tail -f ~/Library/Logs/Mitto/mitto.log

# Search for errors
grep -i error ~/Library/Logs/Mitto/mitto.log

# Search by session ID
grep "20260211-143052-a1b2c3d4" ~/Library/Logs/Mitto/mitto.log
```

## Event Types in events.jsonl

| Type | Description |
|------|-------------|
| `session_start` | Session initialization |
| `user_prompt` | User message |
| `agent_message` | Agent response (HTML content) |
| `agent_thought` | Agent thinking/reasoning |
| `tool_call` | Tool invocation start |
| `tool_call_update` | Tool status update |
| `plan` | Task plan entries |
| `permission` | Permission request/response |
| `error` | Error event |
| `session_end` | Session termination |

## Replaying Events with Mock ACP Server

For reproducing issues, replay `events.jsonl` with the mock ACP server:

### 1. Build the Mock Server

```bash
make build-mock-acp
```

### 2. Create a Scenario from Events

Convert `events.jsonl` to a mock scenario:

```bash
# Extract agent messages and tool calls
cat events.jsonl | jq -s '[.[] | select(.type == "agent_message" or .type == "tool_call")]'
```

### 3. Run with Mock Server

Configure Mitto to use the mock server:

```yaml
# .mittorc
acp_servers:
  - name: mock
    command: ./tests/mocks/acp-server/mock-acp-server --verbose
```

## Common Debugging Scenarios

### Message Not Appearing

1. Check `events.jsonl` for the message event
2. Verify sequence numbers are monotonic
3. Check `mitto.log` for `event_persisted` entries
4. Check `webview.log` for WebSocket errors

### Streaming Issues

1. Look for gaps in sequence numbers in `events.jsonl`
2. Check `mitto.log` for `seq=N` entries
3. Verify `MarkdownBuffer` flush events

### Session Not Loading

1. Check `metadata.json` in session folder
2. Verify `events.jsonl` is valid JSONL (no corrupted lines)
3. Check for lock files (`lock.json`)

### WebSocket Disconnections

1. Check `webview.log` for connection errors
2. Look for `zombie connection` or `wake resync` in logs
3. Check `access.log` for authentication failures

## File Locations Summary

| File | Location | Purpose |
|------|----------|---------|
| `events.jsonl` | `$SESSIONS_DIR/$SESSION_ID/` | All session events |
| `metadata.json` | `$SESSIONS_DIR/$SESSION_ID/` | Session metadata |
| `lock.json` | `$SESSIONS_DIR/$SESSION_ID/` | Session lock info |
| `mitto.log` | `$LOGS_DIR/` | Backend application logs |
| `access.log` | `$LOGS_DIR/` | Security/auth events |
| `webview.log` | `$LOGS_DIR/` | Frontend JS console |

## Using Sequence Numbers

Sequence numbers (`seq`) are key for debugging message ordering:

```bash
# Find events around a specific sequence
cat events.jsonl | jq 'select(.seq >= 40 and .seq <= 50)'

# Check for gaps
cat events.jsonl | jq -s '[.[].seq] | sort | . as $s | range(1; length) | select($s[.] != $s[.-1] + 1) | {gap_after: $s[.-1], missing: $s[.]}'
```

In logs, search for `seq=N` to trace a message through the system.

