---
description: Web server patterns, WebSocket handling, background sessions, and authentication
globs:
  - "internal/web/**/*"
---

# Web Interface Backend Patterns

**Architecture docs**: See [docs/devel/web-interface.md](../docs/devel/web-interface.md) and [docs/devel/websocket-messaging.md](../docs/devel/websocket-messaging.md).

## Key Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server, routing, lifecycle |
| `SessionWSClient` | `session_ws.go` | Per-session WebSocket (implements `SessionObserver`) |
| `BackgroundSession` | `background_session.go` | Long-lived ACP session with observer pattern |
| `SessionManager` | `session_manager.go` | Session registry + workspace management |
| `QueueTitleWorker` | `queue_title.go` | Auto-generates titles for queued messages |

## Critical Patterns

### Observer Cleanup

**Always** remove observers when WebSocket connections close:

```go
defer func() {
    if c.bgSession != nil {
        c.bgSession.RemoveObserver(c)  // MUST remove
    }
}()
```

### Race Condition Prevention

Check for duplicates after reacquiring lock in `SessionManager`:

```go
sm.mu.Lock()
if existing, ok := sm.sessions[id]; ok {
    sm.mu.Unlock()
    bs.Close("duplicate")
    return existing, nil
}
sm.sessions[id] = bs
sm.mu.Unlock()
```

### HTTP Response Helpers

```go
writeJSONOK(w, data)                              // 200
writeJSONCreated(w, data)                         // 201
writeErrorJSON(w, status, errorCode, message)     // Error with code
```

## WebSocket Message Types

See [docs/devel/websocket-messaging.md](../docs/devel/websocket-messaging.md) for complete list.

**Key messages:**
- `prompt` / `prompt_received` - Prompt with ACK
- `agent_message` - Streaming HTML
- `sync_session` / `session_sync` - Mobile wake resync
- `queue_message_titled` - Queue title generated

## Structured Logging

```go
// Session-scoped (auto-includes session_id, working_dir, acp_server)
bs.logger = logging.WithSessionContext(config.Logger, sessionID, workingDir, acpServer)

// Client-scoped (auto-includes client_id, session_id)
clientLogger := logging.WithClient(s.logger, clientID, sessionID)
```

## Caching

**Development mode**: No caching for static assets (HTML has injected values).

```go
w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
```

## External Access

Dual listener architecture:
1. **Localhost** (`127.0.0.1`): No auth required
2. **External** (`0.0.0.0`): Optional, requires authentication

See architecture docs for auth flow and Tailscale funnel debugging.

