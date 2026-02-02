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

## Testing the Web Server

### Handler() Method for httptest.Server

The `Server` exposes a `Handler()` method for use with `httptest.Server`:

```go
srv, err := web.NewServer(config)
if err != nil {
    t.Fatalf("NewServer failed: %v", err)
}

// Use httptest.Server for in-process testing
httpServer := httptest.NewServer(srv.Handler())
defer httpServer.Close()

// Create client pointing to test server
client := client.New(httpServer.URL)
```

This enables fast in-process integration tests without spawning external processes.

### Testing Auth Changes

When testing authentication configuration changes, set `externalPort: -1` to prevent the server from trying to start an external listener:

```go
server := &Server{
    config: Config{
        MittoConfig: &config.Config{
            Web: config.WebConfig{
                ExternalPort: -1, // Disabled
            },
        },
    },
    externalPort: -1, // Also set server's port
}

// Now test auth changes without starting listener
server.applyAuthChanges(false, true, authConfig)
```

### Testing Image Upload Security

The `handleUploadImageFromPath` endpoint only allows localhost access:

```go
func TestHandleUploadImageFromPath_NonLocalhost(t *testing.T) {
    // Simulate request from non-localhost IP
    req := httptest.NewRequest(http.MethodPost, "/api/sessions/id/images/from-path", nil)
    req.RemoteAddr = "192.168.1.100:12345" // Non-localhost
    w := httptest.NewRecorder()

    server.handleUploadImageFromPath(w, req, store, sessionID)

    // Should be forbidden
    if w.Code != http.StatusForbidden {
        t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
    }
}
```

