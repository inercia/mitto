---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, graceful shutdown, crash recovery, error classification, parent-child rules, and session lifecycle anti-patterns
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
  - "internal/web/session_periodic_api.go"
  - "internal/web/acp_error_classification.go"
  - "internal/web/shared_acp_process.go"
  - "internal/mcpserver/server.go"
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
  - parent child
  - child session
  - cascade delete
  - periodic
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

## Auto-Archive of Inactive Sessions

The `PeriodicRunner` can automatically archive sessions that have been inactive for a configured period.
Sessions that are **excluded** from auto-archiving:

- **Already archived sessions** — Skipped (they're already archived)
- **Child sessions** — Handled via parent cascade deletion only
- **Periodic sessions** — Sessions with **enabled** periodic prompts are never auto-archived (they should remain active indefinitely)

To configure auto-archive:

```yaml
session:
  auto_archive_inactive_after: "1w"  # Archive after 1 week of inactivity
```

**Implementation**: See `checkAutoArchive()` in `internal/web/periodic_runner.go`

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

### Restart Rate Limiting & Circuit Breaker

Both code paths use shared constants from `acp_error_classification.go`:

| Constant               | Value   | Purpose                                                     |
| ---------------------- | ------- | ----------------------------------------------------------- |
| `MaxACPRestarts`       | 3       | Max restarts within the sliding window                      |
| `MaxACPTotalRestarts`  | 10      | **Lifetime cap** — trips the circuit breaker permanently    |
| `ACPRestartWindow`     | 5 min   | Sliding window for counting recent restarts                 |
| `ACPRestartBaseDelay`  | 3s      | Initial backoff (longer than start-retry)                   |
| `ACPRestartMaxDelay`   | 30s     | Backoff cap                                                 |

Backoff progression: 3s → 6s → 12s → 24s → 30s (capped). The longer base delay (vs 500ms for start-retries) gives the system time to recover from conditions like notification queue overflow.

### Circuit Breaker (`permanentlyFailed`)

`BackgroundSession` has a `permanentlyFailed bool` field (protected by `restartMu`) that acts as a true circuit breaker:

- Set by `canRestartACP()` when `restartCount >= MaxACPTotalRestarts`
- Set by `restartACPProcess()` when `startACPProcess` returns a non-retryable `ACPClassifiedError`
- Once `true`, `canRestartACP()` returns `false` immediately on every future call — the sliding window is never consulted

**Why the sliding window alone is insufficient:** After 3 failures in a window, `canRestartACP()` returns `false`. But 5 minutes later, old timestamps expire and it returns `true` again. Dead sessions (e.g. closed pipes) would retry every ~5 minutes forever. The `permanentlyFailed` flag prevents this.

```go
// canRestartACP checks circuit breaker FIRST, then the sliding window:
if bs.permanentlyFailed {
    return false  // Circuit breaker open — no more retries
}
if bs.restartCount >= MaxACPTotalRestarts {
    bs.permanentlyFailed = true
    return false  // Lifetime cap hit — open circuit breaker
}
// ... sliding window check (MaxACPRestarts within ACPRestartWindow)
```

```go
// restartACPProcess sets permanentlyFailed on permanent errors:
if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
    bs.permanentlyFailed = true  // Trip circuit breaker
}
```

### Permanent Error Pattern: "file already closed"

The OS error `"write |1: file already closed"` (Go pipe write-end closed) is classified as **permanent** in `permanentErrorPatterns`. This catches dead-pipe sessions where the ACP stdin pipe was permanently destroyed after subprocess exit — retrying the same process start will never succeed.

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

### Auxiliary Session Invalidation After Restart

When `SharedACPProcess.Restart()` succeeds, cached auxiliary sessions in `ACPProcessManager.auxSessions` become stale (the new process doesn't know old session IDs). The `onRestart` callback pattern handles this:

```go
// ACPProcessManager registers this when creating a process (before releasing m.mu):
wuuid := workspace.UUID
p.SetOnRestart(func() {
    m.invalidateAuxiliarySessions(wuuid)  // Acquires auxMu only — no deadlock risk
})
// SharedACPProcess.Restart() calls p.onRestart() after successful restart.
```

- Both `GetOrCreateProcess()` and `GetOrCreateAuxProcess()` register this callback
- Lock ordering is safe: `Restart()` does NOT hold `m.mu` when calling `onRestart`
- Next `PromptAuxiliary()` call will create fresh sessions on the new process

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


## Parent-Child Session Lifecycle Rules

### Rule 1: Children Cannot Be Directly Archived

A child session (one with `ParentSessionID != ""`) **cannot** be archived through the HTTP API or MCP tools. Attempting to archive a child returns HTTP 400 or an MCP error. Children are only removed when their parent is archived (see Rule 2).

Guards exist in:
- `handleUpdateSession` in `session_api.go` — checks `meta.ParentSessionID` before allowing archive
- `handleArchiveConversation` in `mcpserver/server.go` — same check

### Rule 2: Archiving a Parent Cascade-Deletes All Children

When a parent session is archived, all its direct children are **permanently deleted** (not just archived). This is handled by `DeleteChildSessions()` in `SessionManager`:

1. Lists all direct children via `store.ListChildSessions(parentID)`
2. For each child: gracefully stops ACP process (30s timeout), then force-closes if needed
3. Finds auto-grandchildren before deletion (for ACP cleanup and broadcast)
4. Calls `store.Delete(childID)` — permanently removes session data from disk
5. Broadcasts `session_deleted` for the child and any cascade-deleted grandchildren

Called asynchronously (`go sm.DeleteChildSessions(parentID)`) from both:
- `handleUpdateSession` in `session_api.go`
- `handleArchiveConversation` in `mcpserver/server.go`

### Rule 3: Children Cannot Be Made Periodic

A child session cannot have periodic scheduling configured. Only top-level (parentless) sessions can be periodic. A child **can** have a periodic parent — the restriction is only on setting periodic config directly on a child.

Guards exist in:
- `handleSessionPeriodic` in `session_periodic_api.go` — blocks PUT/PATCH/DELETE on children (GET is allowed)
- `handleSetPeriodic` in `mcpserver/server.go` — returns error for children

### Anti-Patterns

```go
// BAD: Archiving a child directly
store.UpdateMetadata(childID, func(m *session.Metadata) {
    m.Archived = true  // Wrong — children should be deleted, not archived
})

// GOOD: Archive the parent — children are cascade-deleted automatically
store.UpdateMetadata(parentID, func(m *session.Metadata) {
    m.Archived = true
})
go sm.DeleteChildSessions(parentID)
```

```go
// BAD: Allowing periodic on a child
store.SetPeriodicConfig(childID, config)  // Wrong — children can't be periodic

// GOOD: Check parent status first
if meta.ParentSessionID != "" {
    return error("cannot set periodic on a child conversation")
}
```
