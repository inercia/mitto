---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, graceful shutdown, crash recovery, error classification, parent-child rules, session suspension, staggered resumption
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
  - "internal/web/session_loop_api.go"
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
  - loop
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

## Session Suspension (GC Loop Suspend)

The GC suspends idle loop sessions whose next prompt is far away, saving ACP resources. Sessions resume transparently when the user focuses them.

- **Config**: `LoopSuspendThreshold` (default 30m) in `acp_process_gc.go`. Settings UI: `loop_suspend_timeout` (`"disabled"`, `"15m"`, `"30m"`, `"1h"`, `"2h"`).
- **Eligibility**: Loop session + next prompt > threshold from now. Applies even if user has it open (resumes instantly).
- **Grace window**: `LoopSuspendGracePeriod` (default 10m) — a session is NOT suspended while its most recent turn completion (`SessionInfo.LastResponseCompleteAt`) or activity (`LastActivityAt`) is within this window. Prevents reclaiming a conversation that just ended a turn and may continue. Use `LastResponseCompleteAt` (turn END) as the signal — `LastActivityAt` is set at prompt START and is stale after long tasks. GC always skips actively-prompting sessions first (`IsPrompting`), so this only matters once the turn ends.
- **Tracking**: `ACPProcessManager.gcSuspendedSessions` map. `SetGCSuspended()` / `IsGCSuspended()` / `ClearGCSuspended()`.
- **Resume**: `ensure_resumed` WebSocket message (sent on user focus) → `handleEnsureResumed()` in `session_ws.go`. Also clears GC-suspended flag on any explicit resume (loop runner, prompt send).
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

Broadcast in `session_archived` WebSocket message as `archive_reason` field. `acp_start_failures` on a loop conversation is the only reason eligible for Auto-Unarchive Recovery (below).

## Auto-Archive

Config: `session.auto_archive_inactive_after: "1w"` (in `checkAutoArchive()`). Excluded: already-archived, child sessions, sessions with loop prompts (enabled or paused).

## Auto-Unarchive Recovery

Loop conversations auto-archived due to broken ACP (`ArchiveReasonACPFailures`) are retried automatically by `LoopRunner.checkAutoUnarchiveRecovery()`, called from `RunOnce()` right after `checkAutoArchive()`.

- **Eligibility**: `meta.Archived && meta.ArchiveReason == ArchiveReasonACPFailures` + loop config exists. Excludes `Manual`/`Inactivity` and non-loop ACP-failure archives.
- **Retry cadence**: 1h per conversation (`DefaultAutoUnarchiveRetryInterval`), anchored on `Metadata.AutoUnarchiveLastAttemptAt` else `ArchivedAt`. Retries indefinitely — the resume-failure path re-archives on continued outage, restarting cadence from the new `ArchivedAt`.
- **Anti-storm stagger**: 10m global minimum gap (`DefaultAutoUnarchiveStaggerInterval`), tracked in-memory (`LoopRunner.lastAutoUnarchiveAttempt`, `r.mu`); each poll attempts only the most-overdue eligible session. **Never `time.Sleep`** in this path — it stalls loop delivery.
- **On-by-default**: `autoUnarchiveEnabled=true`; `SetAutoUnarchiveRecovery(enabled, retryInterval, stagger)` — duration `<= 0` keeps current value (lets tests override one).
- **Callback** (`onAutoUnarchive`, wired in `server.go`): mirrors manual unarchive — clear archive fields → `ResumeSession()` → broadcast → `handlers.RestoreLoopOnUnarchive()`.
- **Cadence persistence**: `attemptAutoUnarchive()` persists `AutoUnarchiveLastAttemptAt` (outside `r.mu`) *before* the callback so it survives a crash; cleared only on `nil` return. Also cleared on any successful manual unarchive (`session_update.go`).

## ACP Process Crash Recovery

`classifyACPError()` → **Permanent** (command not found, syntax error) = stop + guidance. **Transient** (network, crash) = retry with backoff.

Limits: 3 restarts/5min, 10 lifetime cap. Circuit breaker (`permanentlyFailed`) blocks forever on permanent error or lifetime cap.

Death detection (three layers): OS polling (~2s), `conn.Done()` EOF (~seconds), stderr pattern match (immediate). All signal via `processDone` channel.

## Deferred Handshake Retry

When ACP handshake times out transiently, `BackgroundSession.InitializeWithACP()` defers retry up to 3 attempts with exponential backoff. The error event is persisted in the session event log (viewable in UI). Retries happen deferred in a separate goroutine to avoid blocking session creation or WebSocket initialization. After 3 attempts, the session enters error state with guidance.

## Shared Handshake Budget: Stale vs. Cold-Timeout (anti-regression)

`internal/conversation/shared_session_handshaker.go` bounds concurrent `session/load` + `session/new` handshakes with **one shared deadline** to prevent stacking (`mitto-1ut`). The budget cap MUST be **released** for the `session/new` fallback when the `session/load` probe **timed out** (process genuinely cold) — otherwise `session/new` inherits only `budget − probeTimeout` (e.g. `240s − 45s = 195s`), less than a single `MCPInitTimeout` attempt (240s), guaranteeing starvation. Only cap the fallback when the probe **fast-failed** (JSON-RPC `-32602` stale). Track this via `probeTimedOut`. Test seam: `loadBlocksUntilCtxDone` in `fakeSharedProcess` (`TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_NoNewDeadline`). Pre-attempt cancellations in `acpproc/shared_acp_process.go` emit a self-diagnosing error (elapsed vs. per-attempt budget) instead of a raw deadline.

**Clear persisted `acp_session_id` on load failure (`mitto-y1g`)**: In `resumeSharedACPSession`, both load-failure branches (`-32602 Session not found` **and** probe timeout) must call `hsClearPersistedACPSessionID()` before falling back to `session/new`. Otherwise the stale ID stays on disk and every subsequent cold-start / process recycle re-triggers the same doomed `session/load` — catastrophic when an always-active loop session is one of the offenders (each recycle burns the probe cap before the `session/new` fallback). Mirror `hsPersistACPSessionID`; assert both branches clear in the regression test.

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
| **3** | Children cannot be made loop | `session_loop_api.go`, `mcpserver/server.go` |

`DeleteChildSessions`: lists children → gracefully stops each (30s timeout) → `store.Delete` → broadcasts `session_deleted`.

**Anti-patterns**: Never archive a child directly. Never allow loop config on a child.

## Loop Prompt Name Resolution

`PromptName` field selects a named workspace prompt instead of inline text. Resolved at send time via `PromptResolverFunc`. Either `Prompt` or `PromptName` must be set.

### Title Generation from Loop Prompts

`TriggerTitleGenerationFromLoop()` in `BackgroundSession` generates session titles from loop prompts, skipping the "(pending)" placeholder. Named prompts are resolved to full text before title generation. This applies on first run and on subsequent runs when the prompt changes.

### Auto-Pause on Prompt Resolve Failures

Loop runner (`LoopRunner.maybeRunPrompt()`) auto-pauses the loop session after `MaxPromptResolveFailures` (default 5) consecutive resolution errors. This prevents endless retry loops when a prompt cannot be resolved (e.g., missing variable, invalid prompt name). The session can be manually resumed via UI.

### Saturation-Aware Resume Carve-Out (mitto-hjx facet D)

`LoopRunner.checkSession` classifies `ResumeSession` errors before incrementing `r.consecutiveFailures[sessionID]`. Transient shared-ACP-process saturation shapes MUST be excluded from the counter so a healthy loop is not auto-archived by a self-healing burst:

- `mittoAcp.IsMCPInitTimeout(err)` — cold-start MCP-init handshake timed out under load
- `errors.Is(err, acperrors.ErrSharedProcessSaturated)` — aux-session saturation bail (mitto-z70)
- `errors.Is(err, acperrors.ErrProcessClosedConcurrently)` — Tier-5 recycle vs. Restart race (mitto-ei81)

Mirrors the identical carve-out in `session_manager.go`'s `resumeSessionWithConstraint` for `ACPStartFailureCount`. Any new auto-archive-on-failure counter added to the loop runner or session manager MUST classify errors the same way — an unclassified counter re-introduces the "healthy loop archived by transient saturation" bug.

## Auto-Resume Guard (Race Condition)

GC-closed sessions become `SessionStatusCompleted` but are NOT archived. Always check BOTH conditions before auto-resume:
```go
if bs == nil && !meta.Archived && meta.Status != session.SessionStatusCompleted {
    // safe to auto-resume
}
```
