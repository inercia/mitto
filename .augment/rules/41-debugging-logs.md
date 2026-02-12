---
description: Log file debugging for Mitto application issues
keywords:
  - log file
  - log files
  - mitto.log
  - access.log
  - webview.log
  - debug logs
  - debugging
  - console log
  - UI crash
  - UI freeze
  - app crash
  - rendering issue
  - Library/Logs/Mitto
  - troubleshoot
---

# Log File Debugging

Mitto writes to multiple log files for comprehensive debugging. **Always check these logs when investigating issues.**

## Log File Locations

All logs are stored in `~/Library/Logs/Mitto/` (macOS):

| Log File | Purpose | Rotation |
|----------|---------|----------|
| `mitto.log` | Go application logs (server, ACP, sessions) | 10MB, 3 backups |
| `access.log` | Security events (auth, unauthorized access) | 10MB, 1 backup |
| `webview.log` | JavaScript console output from WKWebView | 10MB, 3 backups |

**Tip:** Use `get_runtime_info_mitto-debug` MCP tool to get exact paths.

## Quick Commands

```bash
# View all log files
ls -la ~/Library/Logs/Mitto/

# Tail all logs simultaneously
tail -f ~/Library/Logs/Mitto/mitto.log ~/Library/Logs/Mitto/webview.log

# Search across all logs
grep -r "error" ~/Library/Logs/Mitto/

# View recent entries from all logs
tail -50 ~/Library/Logs/Mitto/*.log
```

## Which Log to Check

| Issue Type | Primary Log | Secondary Log |
|------------|-------------|---------------|
| **App startup failures** | `mitto.log` | - |
| **Server/API errors** | `mitto.log` | `access.log` |
| **Authentication issues** | `access.log` | `mitto.log` |
| **UI rendering problems** | `webview.log` | - |
| **JavaScript errors** | `webview.log` | - |
| **WebSocket connection issues** | `mitto.log` | `webview.log` |
| **Session loading/saving** | `mitto.log` | - |
| **ACP communication** | `mitto.log` | - |
| **Message ordering issues** | `mitto.log` | `webview.log` |

## mitto.log - Go Application Logs

Contains all Go-level logging from the server, ACP client, session management.

```bash
# View recent application logs
tail -100 ~/Library/Logs/Mitto/mitto.log

# Search for errors
grep -i "level=ERROR" ~/Library/Logs/Mitto/mitto.log

# Search for specific session
grep "session_id=SESSION_ID" ~/Library/Logs/Mitto/mitto.log

# Search for ACP-related logs
grep "component=acp" ~/Library/Logs/Mitto/mitto.log

# Search for WebSocket events
grep -i "websocket\|ws\|client" ~/Library/Logs/Mitto/mitto.log
```

**Key patterns:**

| Pattern | Indicates |
|---------|-----------|
| `level=ERROR` | Application errors |
| `level=WARN` | Warnings that may indicate issues |
| `component=web` | Web server events |
| `component=session` | Session management |
| `session_id=` | Session-specific events |
| `client_id=` | WebSocket client events |
| `seq=` | Message sequence numbers (DEBUG level) |

## webview.log - JavaScript Console

Contains JavaScript console output from the macOS app's WKWebView.

```bash
# View recent entries
tail -100 ~/Library/Logs/Mitto/webview.log

# Search for errors
grep '\[ERROR\]' ~/Library/Logs/Mitto/webview.log

# Track WebSocket messages
grep "\[WS\] event:" ~/Library/Logs/Mitto/webview.log | tail -50
```

**Log format:**
```
[2024-01-15T10:30:45.123-08:00] [LEVEL] message content
```

**Sequence number logging:**
```
[LOG] [WS] event: seq=42 type=text kind=assistant_text
[LOG] [WS] event: seq=43 type=agent_thought title="Analyzing code"
[LOG] [WS] event: seq=44 type=tool_call id=call_123 name=codebase-retrieval
```

## access.log - Security Events

Contains security-focused events for auditing and debugging auth issues.

```bash
# View recent security events
tail -50 ~/Library/Logs/Mitto/access.log

# Find failed login attempts
grep "login_failed\|unauthorized" ~/Library/Logs/Mitto/access.log

# Find rate limiting events
grep "rate_limit" ~/Library/Logs/Mitto/access.log
```

## Sequence Number Debugging

Sequence numbers (`seq`) are key for debugging message ordering:

```bash
# Find events around a specific sequence (in webview.log)
grep "seq=42" ~/Library/Logs/Mitto/webview.log

# Enable DEBUG logging to see sequences in mitto.log
MITTO_LOG_LEVEL=debug /Applications/Mitto.app/Contents/MacOS/mitto-app

# Cross-reference frontend and backend
grep "seq=42" ~/Library/Logs/Mitto/webview.log ~/Library/Logs/Mitto/mitto.log
```

## Debugging Workflow

1. **Reproduce the issue** while tailing logs:
   ```bash
   tail -f ~/Library/Logs/Mitto/mitto.log ~/Library/Logs/Mitto/webview.log
   ```

2. **Identify the timeframe** of the issue

3. **Search for errors**:
   ```bash
   grep -i "error" ~/Library/Logs/Mitto/*.log | tail -50
   ```

4. **Correlate across logs** using session ID or sequence numbers

5. **Check rotated logs** if issue occurred earlier:
   ```bash
   grep -r "pattern" ~/Library/Logs/Mitto/
   ```

## Enabling Debug Logging

```bash
# Set environment variable before launching
export MITTO_LOG_LEVEL=debug
open /Applications/Mitto.app

# Or launch from terminal
MITTO_LOG_LEVEL=debug /Applications/Mitto.app/Contents/MacOS/mitto-app
```

## CLI vs macOS App Logging

| Feature | `mitto web` CLI | macOS App |
|---------|-----------------|-----------|
| Console output | ✅ Always | ✅ Always |
| `mitto.log` file | ❌ Disabled by default | ✅ Enabled |
| `access.log` file | ❌ Disabled by default | ✅ Enabled |
| `webview.log` file | N/A | ✅ Enabled |

To enable file logging for CLI:
```bash
mitto web --access-log ~/Library/Logs/Mitto/access.log
```

