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

## Archive / Unarchive Flow

- **Archive**: `PATCH /api/sessions/{id} archived=true` → `CloseSessionGracefully()` (waits for response) → stops ACP → broadcasts `acp_stopped` + `session_archived`
- **Unarchive**: `PATCH archived=false` → broadcasts `session_archived(false)` → `ResumeSession()` → `acp_started`

**Critical**: Always check `meta.Archived` before calling `ResumeSession()` on WebSocket connect — never resume an archived session automatically.

## Auto-Archive

```yaml
session:
  auto_archive_inactive_after: "1w"  # implemented in checkAutoArchive()
```

Excluded from auto-archive: already-archived sessions, child sessions, sessions with enabled periodic prompts.

## ACP Process Crash Recovery

Both `BackgroundSession` and `SharedACPProcess` attempt automatic restart with error classification and telemetry.

`classifyACPError(err, stderr)` → `ACPClassifiedError{Class, UserMessage, UserGuidance}`: **Permanent** (`command not found`, `MODULE_NOT_FOUND`, `EACCES`, `SyntaxError`) → stop, show guidance. **Transient** (network, port conflict, crash) → retry with backoff.

`isACPConnectionError(err)` detects: `broken pipe`, `file already closed`, `connection reset`, `peer disconnected`, `shared ACP process has exited/not running`.

**Restart constants** (`acp_error_classification.go`): `MaxACPRestarts`=3 per window, `MaxACPTotalRestarts`=10 lifetime cap, `ACPRestartWindow`=5min, `ACPRestartBaseDelay`=3s→30s (exponential). Circuit breaker: `permanentlyFailed bool` (in `restartMu`) — set on lifetime cap or permanent error; `canRestartACP()` checks it first, never resets.

**Telemetry**: `bs.recordRestart(reason)` — reasons: `crash_during_prompt`, `crash_during_stream`, `unexpected_exit`. `bs.GetRestartStats()` → stats struct.

### Auxiliary Session Invalidation

Two mechanisms keep auxiliary sessions fresh after process crashes:

**1. On `SharedACPProcess.Restart()`** — `onRestart` callback (workspace-wide invalidation):
```go
p.SetOnRestart(func() {
    m.invalidateAuxiliarySessions(wuuid)  // removes ALL aux sessions for workspace
})
```

**2. On connection error in `PromptAuxiliary()`** — surgical single-session invalidation + retry:
```go
if isACPConnectionError(err) {
    m.invalidateAuxSession(workspaceUUID, purpose)  // removes just this purpose's session
    // wait 1s for process restart, then retry once via getOrCreateAuxiliarySession
}
```

- `invalidateAuxiliarySessions(uuid)` — removes all aux sessions for a workspace (called on restart)
- `invalidateAuxSession(uuid, purpose)` — removes single aux session entry (called on connection error)
- Lock ordering: both must be called WITHOUT holding `m.mu`; they acquire `auxMu` internally

### ACP Process Death Detection (Three-Layer)

Fast crash detection avoids waiting for the ACP SDK's 60-second control request timeout:

| Layer | Mechanism | Detection Time | File |
| ----- | --------- | -------------- | ---- |
| **Fix A** | OS liveness polling (`kill(pid, 0)`) | ~2s | `shared_acp_process.go` |
| **Fix B** | `conn.Done()` pipe EOF | ~seconds | SDK level |
| **Fix C** | Stderr crash pattern matching | Immediate | `background_session.go`, `shared_acp_process.go` |

Key stderr crash patterns: `stream ended unexpectedly`, `EOF received from CLI stdout`, `connection reset by peer`, `broken pipe`, `failed to queue notification; closing connection`.

All three layers signal via `processDone` channel (closed once via `sync.Once`).

## MCP Server Lifecycle

| Event               | MCP Server Action          |
| ------------------- | -------------------------- |
| Session created     | Start if flags enabled     |
| Session archived    | Stop                       |
| Session unarchived  | Start (new instance)       |
| Session deleted     | Stop                       |
| Server shutdown     | Stop all                   |

Per-session resources must be destroyed on archive and recreated (new instances) on unarchive.

## Deleted Sessions

When a client connects to a deleted session, send `session_gone` (NOT a generic error — clients retry generic errors 15× but stop on `session_gone`).

`NegativeSessionCache` (30s TTL) prevents repeated FS lookups: check `IsNotFound()` → populate on `ErrSessionNotFound` → invalidate on `handleCreateSession`/`ResumeSession`.

**Critical**: Archived sessions still exist — do NOT cache them as "not found".

## WebSocket Messages

| Message Type        | Direction       | When                          |
| ------------------- | --------------- | ----------------------------- |
| `session_archived`  | Server->Client  | Session archived/unarchived   |
| `acp_stopped`       | Server->Client  | ACP connection closed         |
| `acp_started`       | Server->Client  | ACP connection started        |
| `acp_start_failed`  | Server->Client  | ACP failed to start           |
| `session_gone`      | Server->Client  | Session deleted/not found (terminal — client must stop reconnecting) |


## Parent-Child Session Lifecycle Rules

| Rule | Constraint | Guards |
| ---- | ---------- | ------ |
| **1** | Children (`ParentSessionID != ""`) cannot be directly archived — HTTP 400 | `session_api.go`, `mcpserver/server.go` |
| **2** | Archiving a parent **cascade-deletes** all children permanently (`store.Delete`, not archive) | `go sm.DeleteChildSessions(parentID)` |
| **3** | Children cannot be made periodic | `session_periodic_api.go`, `mcpserver/server.go` |

`DeleteChildSessions`: lists children → gracefully stops each (30s timeout) → `store.Delete` → broadcasts `session_deleted`.

**Anti-patterns**: Never archive a child directly. Never allow periodic config on a child.
