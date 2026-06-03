---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, graceful shutdown, crash recovery, error classification, parent-child rules, session suspension, staggered resumption
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
  - "internal/web/session_periodic_api.go"
  - "internal/web/acp_error_classification.go"
  - "internal/web/shared_acp_process.go"
  - "internal/web/acp_process_gc.go"
  - "internal/mcpserver/server.go"
keywords:
  - session lifecycle
  - archive
  - unarchive
  - suspend
  - ensure_resumed
  - stagger
  - ACP connection
  - graceful shutdown
  - session resume
  - session_gone
  - error classification
  - crash recovery
  - parent child
  - cascade delete
  - periodic
---

# Session Lifecycle Management

## Session States

| State         | ACP Connection | User Can Send | Visible In List |
| ------------- | -------------- | ------------- | --------------- |
| **Active**    | Running        | Yes           | Yes (green dot) |
| **Suspended** | Stopped (GC)   | Yes (resumes) | Yes (yellow dot)|
| **Archived**  | Stopped        | No (read-only)| Archived section|
| **Deleted**   | N/A            | N/A           | No              |

## Archive / Unarchive Flow

- **Archive**: `PATCH /api/sessions/{id} archived=true` → `CloseSessionGracefully()` (waits for response) → stops ACP → broadcasts `acp_stopped` + `session_archived`
- **Unarchive**: `PATCH archived=false` → broadcasts `session_archived(false)` → `ResumeSession()` → `acp_started`

**Critical**: Always check `meta.Archived` before calling `ResumeSession()` on WebSocket connect — never resume an archived session automatically.

## Session Suspension (GC Periodic Suspend)

The GC suspends idle periodic sessions whose next prompt is far away, saving ACP resources. Sessions resume transparently when the user focuses them.

- **Config**: `PeriodicSuspendThreshold` (default 30m) in `acp_process_gc.go`. Settings UI: `periodic_suspend_timeout` (`"disabled"`, `"15m"`, `"30m"`, `"1h"`, `"2h"`).
- **Eligibility**: Periodic session + next prompt > threshold from now. Applies even if user has it open (resumes instantly).
- **Grace window**: `PeriodicSuspendGracePeriod` (default 10m) — a session is NOT suspended while its most recent turn completion (`SessionInfo.LastResponseCompleteAt`) or activity (`LastActivityAt`) is within this window. Prevents reclaiming a conversation that just ended a turn and may continue. Use `LastResponseCompleteAt` (turn END) as the signal — `LastActivityAt` is set at prompt START and is stale after long tasks. GC always skips actively-prompting sessions first (`IsPrompting`), so this only matters once the turn ends.
- **Tracking**: `ACPProcessManager.gcSuspendedSessions` map. `SetGCSuspended()` / `IsGCSuspended()` / `ClearGCSuspended()`.
- **Resume**: `ensure_resumed` WebSocket message (sent on user focus) → `handleEnsureResumed()` in `session_ws.go`. Also clears GC-suspended flag on any explicit resume (periodic runner, prompt send).
- **UI**: Suspended sessions show a friendly "Session suspended" balloon (not error), yellow dot in sidebar tooltip.

## Staggered Session Resumption

`reconnectAllSessionsStaggered()` in `session_manager.go` prevents thundering herd on startup. Sessions sharing the same ACP process are staggered by `startup_stagger_ms` (default 300ms). Non-active sessions are deferred — resumed on first user focus via `ensure_resumed`.

## Archive Reasons

`Metadata.ArchiveReason` (`ArchiveReason` type in `session/types.go`) tracks why a session was archived. Cleared on unarchive.

| Reason              | Constant                      | Trigger                                      |
| ------------------- | ----------------------------- | -------------------------------------------- |
| `manual`            | `ArchiveReasonManual`         | User/MCP archive action                      |
| `inactivity`        | `ArchiveReasonInactivity`     | Auto-archive after configured inactive period|
| `acp_start_failures`| `ArchiveReasonACPFailures`    | `ACPStartFailureCount` ≥ threshold (3)       |

Broadcast in `session_archived` WebSocket message as `archive_reason` field.

## Auto-Archive

Config: `session.auto_archive_inactive_after: "1w"` (in `checkAutoArchive()`). Excluded: already-archived, child sessions, sessions with periodic prompts (enabled or paused).

## ACP Process Crash Recovery

Both `BackgroundSession` and `SharedACPProcess` attempt automatic restart with error classification and telemetry.

`classifyACPError(err, stderr)` → `ACPClassifiedError{Class, UserMessage, UserGuidance}`: **Permanent** (`command not found`, `MODULE_NOT_FOUND`, `EACCES`, `SyntaxError`) → stop, show guidance. **Transient** (network, port conflict, crash) → retry with backoff.

`isACPConnectionError(err)` detects: `broken pipe`, `file already closed`, `connection reset`, `peer disconnected`, `shared ACP process has exited/not running`.

**Restart constants** (`acp_error_classification.go`): `MaxACPRestarts`=3 per window, `MaxACPTotalRestarts`=10 lifetime cap, `ACPRestartWindow`=5min, `ACPRestartBaseDelay`=3s→30s (exponential). Circuit breaker: `permanentlyFailed bool` (in `restartMu`) — set on lifetime cap or permanent error; `canRestartACP()` checks it first, never resets.

### Auxiliary Session Invalidation

- **On `SharedACPProcess.Restart()`**: `onRestart` → `invalidateAuxiliarySessions(wuuid)` (removes ALL aux sessions).
- **On connection error in `PromptAuxiliary()`**: `invalidateAuxSession(wuuid, purpose)` (removes one, retries once).
- Lock ordering: both called WITHOUT holding `m.mu`; they acquire `auxMu` internally.

### Auxiliary Session GC (Tier 3)

Tier 3 cleans up auxiliary sessions idle longer than `AuxIdleTimeout` (default: 10m) via `CleanupStaleAuxiliarySessions()`. Lazily re-created on next use.

### ACP Process Death Detection (Three-Layer)

| Layer | Mechanism | Detection Time | File |
| ----- | --------- | -------------- | ---- |
| **Fix A** | OS liveness polling (`kill(pid, 0)`) | ~2s | `shared_acp_process.go` |
| **Fix B** | `conn.Done()` pipe EOF | ~seconds | SDK level |
| **Fix C** | Stderr crash pattern matching | Immediate | `background_session.go`, `shared_acp_process.go` |

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

Send `session_gone` (NOT generic error — clients stop reconnecting on `session_gone`). `NegativeSessionCache` (30s TTL) prevents repeated FS lookups. **Critical**: Archived sessions still exist — do NOT cache them as "not found".

## WebSocket Messages

| Message Type        | Direction       | When                          |
| ------------------- | --------------- | ----------------------------- |
| `session_archived`  | Server→Client   | Session archived/unarchived   |
| `acp_stopped`       | Server→Client   | ACP connection closed         |
| `acp_started`       | Server→Client   | ACP connection started        |
| `acp_start_failed`  | Server→Client   | ACP failed to start           |
| `session_gone`      | Server→Client   | Session deleted/not found (terminal) |
| `ensure_resumed`    | Client→Server   | Request ACP resume on user focus |


## Parent-Child Session Lifecycle Rules

| Rule | Constraint | Guards |
| ---- | ---------- | ------ |
| **1** | Children (`ParentSessionID != ""`) cannot be directly archived — HTTP 400 | `session_api.go`, `mcpserver/server.go` |
| **2** | Archiving a parent **cascade-deletes** all children permanently (`store.Delete`, not archive) | `go sm.DeleteChildSessions(parentID)` |
| **3** | Children cannot be made periodic | `session_periodic_api.go`, `mcpserver/server.go` |

`DeleteChildSessions`: lists children → gracefully stops each (30s timeout) → `store.Delete` → broadcasts `session_deleted`.

**Anti-patterns**: Never archive a child directly. Never allow periodic config on a child.

## Periodic Prompt Name Resolution

`PromptName` field selects a named workspace prompt instead of inline text. Resolved at send time via `PromptResolverFunc`. Either `Prompt` or `PromptName` must be set.

## Auto-Resume Guard (Race Condition)

GC-closed sessions become `SessionStatusCompleted` but are NOT archived. Always check BOTH conditions before auto-resume:
```go
if bs == nil && !meta.Archived && meta.Status != session.SessionStatusCompleted {
    // safe to auto-resume
}
```
