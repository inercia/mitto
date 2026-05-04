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
# mitto.log
grep -i "level=ERROR\|level=WARN" ~/Library/Logs/Mitto/mitto.log | tail -100
grep -i "panic\|fatal\|crash" ~/Library/Logs/Mitto/mitto.log
grep "session_id=SESSION_ID" ~/Library/Logs/Mitto/mitto.log
# webview.log (frontend / WKWebView)
grep '\[ERROR\]' ~/Library/Logs/Mitto/webview.log | tail -50
grep '\[WS\]' ~/Library/Logs/Mitto/webview.log | tail -50
grep -i 'sync\|resync\|behind' ~/Library/Logs/Mitto/webview.log | tail -30
# access.log (security / auth) — also check rotated: access.log.1
grep -i 'fail\|invalid\|unauthorized' ~/Library/Logs/Mitto/access.log
grep -i 'rate_limit' ~/Library/Logs/Mitto/access.log
grep -v '127\.0\.0\.1\|::1' ~/Library/Logs/Mitto/access.log   # non-localhost
```

### Processor Log Patterns

```bash
grep 'processor pipeline starting\|processor pipeline complete' ~/Library/Logs/Mitto/mitto.log | tail -20
grep 'applying processor\|processor applied\|processor executed' ~/Library/Logs/Mitto/mitto.log | tail -30
grep 'processor skipped\|processor rerun triggered' ~/Library/Logs/Mitto/mitto.log | tail -20
grep 'processor execution failed\|processor returned error\|processor failed' ~/Library/Logs/Mitto/mitto.log
```

### Anomaly Detection Patterns

| Anomaly                        | Log            | grep / indicator                                    |
| ------------------------------ | -------------- | --------------------------------------------------- |
| Sync loops                     | `mitto.log`    | `keepalive.*behind\|sync_request`                   |
| Reconnection storms            | `webview.log`  | Multiple `[WS] connected` in short time             |
| Stale client                   | `mitto.log`    | `client_max_seq` >> `server_max_seq`                |
| Events after session end       | `mitto.log`    | `session_end` then more events same session         |
| Duplicate session starts       | `mitto.log`    | Multiple `session_start` same `session_id`          |
| Empty event loads              | `webview.log`  | `events_loaded.*eventCount=0` repeated              |
| Orphan prompt_ack              | `webview.log`  | `prompt_ack` without preceding `prompt_request`     |

### Enable Debug Logging

```bash
MITTO_LOG_LEVEL=debug /Applications/Mitto.app/Contents/MacOS/mitto-app
# Note: mitto.log is disabled by default in CLI; webview.log is macOS app only
```

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

`session_start` · `user_prompt` · `agent_message` · `agent_thought` · `tool_call` · `tool_call_update` · `error` · `session_end`

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
| ACP process crash loop   | mitto.log: `restart_count`, `error_class`, `reason` (see `15-web-backend-session-lifecycle.md`) |
| WebSocket backpressure   | mitto.log: `"applying backpressure"`, `"client too slow"` (see `11-web-backend-sequences.md`)   |

## Replaying Events

Build mock server (`make build-mock-acp`), extract events from `events.jsonl`, configure Mitto to use mock server.
