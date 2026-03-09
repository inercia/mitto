---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, graceful shutdown, crash recovery, error classification, and session lifecycle anti-patterns
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
  - "internal/web/acp_error_classification.go"
  - "internal/web/shared_acp_process.go"
keywords:
  - session lifecycle
  - archive
  - unarchive
  - ACP connection
  - graceful shutdown
  - session resume
  - archived session
  - session_gone
  - negative cache
  - circuit breaker
  - error classification
  - crash recovery
  - restart
  - backoff
---

# Session Lifecycle Management

## Session States

| State        | ACP Connection | User Can Send | Visible In List |
| ------------ | -------------- | ------------- | --------------- |
| **Active**   | Running        | Yes           | Yes (green dot) |
| **Archived** | Stopped        | No (read-only)| Archived section|
| **Deleted**  | N/A            | N/A           | No              |

## Archive Flow

```
User clicks Archive → PATCH /api/sessions/{id} archived=true
→ CloseSessionGracefully() (wait for active response with timeout)
→ Close ACP connection → Broadcast "acp_stopped"
→ Update metadata → Broadcast "session_archived"
```

```go
func (sm *SessionManager) CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool {
    bs := sm.GetSession(sessionID)
    if bs == nil { return true }
    if bs.IsPrompting() {
        if !bs.WaitForResponseComplete(timeout) { return false }
    }
    sm.CloseSession(sessionID, reason)
    return true
}
```

## Unarchive Flow

```
User clicks Unarchive → PATCH /api/sessions/{id} archived=false
→ Update metadata → Broadcast "session_archived" (archived=false)
→ ResumeSession() → Broadcast "acp_started" (or "acp_start_failed")
```

## Critical: Don't Resume Archived Sessions on WebSocket Connect

```go
// BAD: Resumes ACP without checking archived state
if bs == nil && store != nil {
    meta, _ := store.GetMetadata(sessionID)
    bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd) // Wrong!
}

// GOOD: Check archived state before resuming
if bs == nil && store != nil {
    meta, _ := store.GetMetadata(sessionID)
    if meta.Archived {
        clientLogger.Debug("Session is archived, not resuming ACP")
    } else {
        bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
    }
}
```

## Frontend: Archived Sessions Don't Show Active Indicator

```javascript
// BAD: All sessions marked active
return { status: "active", isActive: true };

// GOOD: Check archived state
const isArchived = data.info?.archived || false;
return {
    status: isArchived ? "archived" : "active",
    isActive: !isArchived,
    isStreaming: !isArchived && (data.isStreaming || false),
};
```

## WebSocket Client Attachment After Unarchive

When a WS client was connected to an archived session and it's later unarchived, `bgSession` is NOT auto-updated:

```go
// Before actions requiring bgSession:
if c.bgSession == nil {
    c.tryAttachToSession()  // Check if session was resumed
}
```

## ACP Process Crash Recovery

When an ACP process dies unexpectedly, both `BackgroundSession` and `SharedACPProcess` attempt automatic restart with error classification and telemetry.

### Error Classification (`acp_error_classification.go`)

Errors are classified as **transient** (retryable) or **permanent** (fatal) to avoid wasting retries on unrecoverable failures:

```go
classified := classifyACPError(err, stderrOutput)
if !classified.IsRetryable() {
    // Permanent: missing binary, syntax error, permission denied
    // → Stop retrying, show actionable guidance to user
}
// Transient: network timeout, port conflict, crash
// → Retry with exponential backoff
```

| Error Class  | Examples                                                | Action                          |
| ------------ | ------------------------------------------------------- | ------------------------------- |
| **Permanent** | `command not found`, `MODULE_NOT_FOUND`, `EACCES`, `SyntaxError` | Stop retrying, show user guidance |
| **Transient** | Network timeout, port conflict, unexpected crash        | Retry with backoff              |

The `ACPClassifiedError` type implements `error` and carries user-facing messages:

```go
type ACPClassifiedError struct {
    Class         ACPErrorClass  // Transient or Permanent
    OriginalError error
    Stderr        string         // Captured stderr for diagnostics
    UserMessage   string         // "The ACP command was not found"
    UserGuidance  string         // "Check that the ACP command is installed..."
}
```

### Restart Rate Limiting

Both code paths use shared constants from `acp_error_classification.go`:

| Constant            | Value   | Purpose                                    |
| ------------------- | ------- | ------------------------------------------ |
| `MaxACPRestarts`    | 3       | Max restarts within the window             |
| `ACPRestartWindow`  | 5 min   | Sliding window for counting restarts       |
| `ACPRestartBaseDelay` | 3s    | Initial backoff (longer than start-retry)  |
| `ACPRestartMaxDelay`  | 30s   | Backoff cap                                |

Backoff progression: 3s → 6s → 12s → 24s → 30s (capped). The longer base delay (vs 500ms for start-retries) gives the system time to recover from conditions like notification queue overflow.

### Restart Telemetry

Each restart records its reason for diagnostics:

```go
type RestartReason string
const (
    RestartReasonCrashDuringPrompt  = "crash_during_prompt"
    RestartReasonCrashDuringStream  = "crash_during_stream"
    RestartReasonUnexpectedExit     = "unexpected_exit"
)

bs.recordRestart(RestartReasonCrashDuringPrompt)
stats := bs.GetRestartStats() // TotalRestarts, RecentRestarts, ReasonCounts, LastReason
```

### ACP Process Death Detection (Three-Layer)

Fast crash detection avoids waiting for the ACP SDK's 60-second control request timeout:

| Layer | Mechanism | Detection Time | File |
| ----- | --------- | -------------- | ---- |
| **Fix A** | OS liveness polling (`kill(pid, 0)`) | ~2s | `shared_acp_process.go` |
| **Fix B** | `conn.Done()` pipe EOF | ~seconds | SDK level |
| **Fix C** | Stderr crash pattern matching | Immediate | `background_session.go`, `shared_acp_process.go` |

Stderr patterns that trigger immediate crash detection:

```go
var stderrCrashPatterns = []string{
    "stream ended unexpectedly",
    "EOF received from CLI stdout",
    "background reader: stream ended",
    "connection reset by peer",
    "broken pipe",
    "received message with neither id nor method",
    "failed to queue notification; closing connection",  // SDK notification queue overflow
}
```

All three layers signal via the `processDone` channel (closed exactly once via `sync.Once`).

## MCP Server Lifecycle

| Event               | MCP Server Action          |
| ------------------- | -------------------------- |
| Session created     | Start if flags enabled     |
| Session archived    | Stop                       |
| Session unarchived  | Start (new instance)       |
| Session deleted     | Stop                       |
| Server shutdown     | Stop all                   |

Per-session resources must be destroyed on archive and recreated (new instances) on unarchive.

## Connecting to Deleted Sessions (Circuit Breaker)

When a client connects to a session that no longer exists, send `session_gone` (NOT a generic `error`):

```go
// GOOD: Send terminal signal for deleted sessions
if err == session.ErrSessionNotFound {
    if s.negativeSessionCache != nil {
        s.negativeSessionCache.MarkNotFound(sessionID)
    }
    client.sendMessage(WSMsgTypeSessionGone, map[string]interface{}{
        "session_id": sessionID,
        "reason":     "session not found",
    })
    // Close after flush delay
    go func() {
        time.Sleep(100 * time.Millisecond)
        client.wsConn.Close()
    }()
    return
}

// BAD: Generic error that clients don't treat as terminal
client.sendError("Session not found")  // Client retries 15 times!
```

### Negative Session Cache

The `NegativeSessionCache` prevents repeated filesystem lookups for deleted sessions (30s TTL):

- **Check cache** before hitting the store: `s.negativeSessionCache.IsNotFound(sessionID)`
- **Populate cache** when `store.GetMetadata()` returns `ErrSessionNotFound`
- **Invalidate cache** on `handleCreateSession` and `ResumeSession`

### Important: Archived ≠ Deleted

Archived sessions still exist in the store — they must NOT be cached as "not found":

```go
meta, err := store.GetMetadata(sessionID)
if err == session.ErrSessionNotFound {
    // Session truly gone → cache + send session_gone
} else if err == nil && meta.Archived {
    // Archived session → load in read-only mode (do NOT cache)
}
```

## WebSocket Messages

| Message Type        | Direction       | When                          |
| ------------------- | --------------- | ----------------------------- |
| `session_archived`  | Server->Client  | Session archived/unarchived   |
| `acp_stopped`       | Server->Client  | ACP connection closed         |
| `acp_started`       | Server->Client  | ACP connection started        |
| `acp_start_failed`  | Server->Client  | ACP failed to start           |
| `session_gone`      | Server->Client  | Session deleted/not found (terminal — client must stop reconnecting) |
