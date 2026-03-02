---
description: Debugging with MCP tools, log files, events.jsonl inspection, conversation inspection, event replay
keywords:
  - mcp debug
  - mcp tools
  - events.jsonl
  - conversation inspection
  - event replay
  - troubleshoot
  - mitto_get_runtime_info
  - mitto_conversation_list
  - mitto_get_config
  - log file
  - mitto.log
  - access.log
  - webview.log
  - debug logs
  - Library/Logs/Mitto
---

# Debugging

**For MCP server development patterns, see `42-mcpserver-development.md`.**

## MCP Debugging Tools

| Tool                       | Purpose                                             |
| -------------------------- | --------------------------------------------------- |
| `mitto_conversation_list`  | Sessions with metadata, folder paths, runtime state |
| `mitto_get_config`         | Current effective config (sanitized)                |
| `mitto_get_runtime_info`   | OS, log paths, data dirs, environment               |

## Log Files

All logs in `~/Library/Logs/Mitto/` (macOS):

| Log File      | Purpose                                     | Rotation        |
| ------------- | ------------------------------------------- | --------------- |
| `mitto.log`   | Go application logs (server, ACP, sessions) | 10MB, 3 backups |
| `access.log`  | Security events (auth, unauthorized access) | 10MB, 1 backup  |
| `webview.log` | JavaScript console output from WKWebView    | 10MB, 3 backups |

### Which Log to Check

| Issue Type              | Primary       | Secondary     |
| ----------------------- | ------------- | ------------- |
| Startup failures        | `mitto.log`   | -             |
| Server/API errors       | `mitto.log`   | `access.log`  |
| Authentication issues   | `access.log`  | `mitto.log`   |
| UI/JS errors            | `webview.log` | -             |
| WebSocket issues        | `mitto.log`   | `webview.log` |
| Message ordering        | `mitto.log`   | `webview.log` |

### Key Log Patterns

| Pattern             | Indicates                |
| ------------------- | ------------------------ |
| `level=ERROR`       | Application errors       |
| `component=web`     | Web server events        |
| `session_id=`       | Session-specific events  |
| `client_id=`        | WebSocket client events  |
| `seq=`              | Sequence numbers (DEBUG) |

### Quick Commands

```bash
tail -f ~/Library/Logs/Mitto/mitto.log ~/Library/Logs/Mitto/webview.log
grep -i "level=ERROR" ~/Library/Logs/Mitto/mitto.log
grep "session_id=SESSION_ID" ~/Library/Logs/Mitto/mitto.log
```

### Enable Debug Logging

```bash
MITTO_LOG_LEVEL=debug /Applications/Mitto.app/Contents/MacOS/mitto-app
```

### CLI vs macOS App

| Feature            | CLI                    | macOS App  |
| ------------------ | ---------------------- | ---------- |
| Console output     | Always                 | Always     |
| `mitto.log`        | Disabled by default    | Enabled    |
| `webview.log`      | N/A                    | Enabled    |

## Debugging Workflow

1. Get runtime info via MCP tool to locate files
2. List conversations to find problematic session
3. Inspect `events.jsonl` in session folder:
   ```bash
   cat "$SESSION_FOLDER/events.jsonl" | jq 'select(.type == "agent_message")'
   cat "$SESSION_FOLDER/events.jsonl" | jq 'select(.seq >= 10 and .seq <= 20)'
   ```
4. Check logs, correlate using session ID or `seq=N`

## Event Types in events.jsonl

| Type               | Description             |
| ------------------ | ----------------------- |
| `session_start`    | Session initialization  |
| `user_prompt`      | User message            |
| `agent_message`    | Agent response (HTML)   |
| `agent_thought`    | Agent reasoning         |
| `tool_call`        | Tool invocation         |
| `tool_call_update` | Tool status update      |
| `error`            | Error event             |
| `session_end`      | Session termination     |

## Sequence Number Debugging

```bash
# Check for gaps in events.jsonl
cat events.jsonl | jq -s '[.[].seq] | sort | . as $s | range(1; length) | select($s[.] != $s[.-1] + 1) | {gap_after: $s[.-1], missing: $s[.]}'

# Cross-reference frontend and backend
grep "seq=42" ~/Library/Logs/Mitto/webview.log ~/Library/Logs/Mitto/mitto.log
```

## Common Scenarios

| Issue                    | Check                                          |
| ------------------------ | ---------------------------------------------- |
| Message not appearing    | events.jsonl for event, mitto.log for errors   |
| Streaming issues         | Seq gaps in events.jsonl, MarkdownBuffer flush |
| Session not loading      | metadata.json validity, lock.json              |
| WebSocket disconnections | webview.log for connection errors, access.log  |

## Replaying Events

Build mock server (`make build-mock-acp`), extract events from `events.jsonl`, configure Mitto to use mock server.
