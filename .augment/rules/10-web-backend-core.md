---
description: Web server core patterns, HTTP handlers, routing, and server lifecycle
globs:
  - "internal/web/server.go"
  - "internal/web/routes.go"
  - "internal/web/http_*.go"
  - "internal/web/session_manager.go"
keywords:
  - HTTP handler
  - routing
  - web server
  - httptest
  - writeJSON
---

# Web Backend Core Patterns

**Architecture docs**: See [docs/devel/web-interface.md](../../docs/devel/web-interface.md) and [docs/devel/websockets/](../../docs/devel/websockets/).

## Key Components

| Component           | File                    | Purpose                                              |
| ------------------- | ----------------------- | ---------------------------------------------------- |
| `Server`            | `server.go`             | HTTP server, routing, lifecycle                      |
| `SessionWSClient`   | `session_ws.go`         | Per-session WebSocket (implements `SessionObserver`) |
| `BackgroundSession` | `background_session.go` | Long-lived ACP session with observer pattern         |
| `SessionManager`    | `session_manager.go`    | Session registry + workspace management              |
| `QueueTitleWorker`  | `queue_title.go`        | Auto-generates titles for queued messages            |
| `WebClient`         | `client.go`             | ACP client for web (implements `acp.Client`)         |
| `MarkdownBuffer`    | `markdown.go`           | Streaming markdown to HTML with smart flushing       |

## HTTP Response Helpers

```go
writeJSONOK(w, data)                              // 200
writeJSONCreated(w, data)                         // 201
writeErrorJSON(w, status, errorCode, message)     // Error with code
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

### Testing Auth Changes

When testing authentication configuration changes, set `externalPort: -1`:

```go
server := &Server{
    config: Config{
        MittoConfig: &config.Config{
            Web: config.WebConfig{
                ExternalPort: -1, // Disabled
            },
        },
    },
    externalPort: -1,
}
server.applyAuthChanges(false, true, authConfig)
```

### Testing Image Upload Security

The `handleUploadImageFromPath` endpoint only allows localhost access:

```go
func TestHandleUploadImageFromPath_NonLocalhost(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/api/sessions/id/images/from-path", nil)
    req.RemoteAddr = "192.168.1.100:12345" // Non-localhost
    w := httptest.NewRecorder()

    server.handleUploadImageFromPath(w, req, store, sessionID)

    if w.Code != http.StatusForbidden {
        t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
    }
}
```

## Structured Logging

```go
// Session-scoped (auto-includes session_id, working_dir, acp_server)
bs.logger = logging.WithSessionContext(config.Logger, sessionID, workingDir, acpServer)

// Client-scoped (auto-includes client_id, session_id)
clientLogger := logging.WithClient(s.logger, clientID, sessionID)
```
