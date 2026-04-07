# ACP Process Garbage Collection

## Problem Statement

When Mitto starts, any interaction with a workspace triggers creation of a shared ACP
process (e.g., Claude Code subprocess). Once created, this process **never shuts down**
— it lives until the Mitto server exits. This wastes resources (CPU, memory, battery)
when no conversations are actively using the workspace.

### How It Happens Today

1. **`ProcessPendingQueues()` on startup** — If any persisted session has queued
   messages, Mitto calls `ResumeSession()`, which calls `getSharedProcess()` →
   `GetOrCreateProcess()`, starting the shared ACP process. After the queue is drained,
   both the `BackgroundSession` and the shared process remain alive indefinitely.

2. **`PeriodicRunner` auto-resume** — When a periodic prompt is due, the runner calls
   `ResumeSession()` to deliver the prompt. Same result: session and process stay alive
   forever after delivery.

3. **Opening any conversation** — Even briefly opening a conversation in the UI starts
   the shared process, which is never stopped.

4. **Auxiliary pre-warming** — When `GetOrCreateProcess()` creates a new process, it
   immediately spawns 4 auxiliary sessions (mcp-check, mcp-tools, title-gen, follow-up)
   via `prewarmAuxiliarySessions()`, regardless of whether any user work will follow.

### Root Causes

| # | Root Cause | Location |
|---|------------|----------|
| 1 | `StopProcess()` is dead code — never called except during full server shutdown (`CloseAll()`) | `session_manager.go` |
| 2 | `BackgroundSession` objects stay in `SessionManager.sessions` map after their work completes | `session_manager.go` |
| 3 | No mechanism to detect and clean up idle sessions or idle processes | — |
| 4 | `ProcessPendingQueues()` resumes sessions even when the queue is empty by the time `ResumeSession` runs | `session_manager.go` |

## Solution: Two-Tier Periodic Garbage Collection

Instead of reference counting (error-prone, requires wiring into every lifecycle path),
use a periodic GC loop that is self-healing: even if something goes wrong, the next
cycle cleans up.

### Tier 1 — Idle Session Cleanup

A session is considered **idle** when ALL of the following are true:

- Zero WebSocket observers (`!bs.HasObservers()`)
- Not currently prompting (`!bs.IsPrompting()`)
- Queue is empty (no pending messages)
- No periodic prompt due within the next GC interval
- Not closed (not already cleaned up)

When a session is idle, the GC calls `CloseSession()`, which:
- Removes it from `SessionManager.sessions`
- Calls `bs.Close()` (unregisters from shared process, stops recorder)

**Important**: Sessions with active periodic prompts should NOT be closed if their
next scheduled delivery is within 2× the GC interval. This avoids the overhead of
repeatedly closing and re-creating sessions that will be needed again shortly.

### Tier 2 — Idle Process Cleanup

After tier 1 runs, check each shared process in `ACPProcessManager.processes`:

- Query `SessionManager`: are there any running sessions for this workspace UUID?
- If **no sessions** AND the process has been sessionless for longer than
  `gracePeriod` → call `StopProcess(workspaceUUID)`

The grace period (default: 60 seconds) prevents process thrashing when quickly
switching between conversations. A `lastSessionSeen` timestamp per workspace
tracks when sessions were last present.

### Avoiding Unnecessary Process Creation

#### `ProcessPendingQueues()` — Already Safe

`ProcessPendingQueues()` already checks `queue.Len()` **before** calling
`ResumeSession()` (line ~1890 in `session_manager.go`):

```go
queue := store.Queue(meta.SessionID)
queueLen, err := queue.Len()
if err != nil || queueLen == 0 {
    continue  // Skip — no queued messages
}
```

So it does NOT start a process for sessions with empty queues. The problem is that
after the queue is processed, the session (and its process) remain alive. The GC
fixes this.

#### `PeriodicRunner` — Already Safe

`PeriodicRunner.checkSession()` only calls `ResumeSession()` when a periodic prompt
is actually due (line ~329 in `periodic_runner.go`). It correctly skips archived
sessions and sessions that aren't due yet. Again, the problem is cleanup after
delivery — which the GC handles.

#### Auxiliary Pre-warming — Deferred

Currently, `GetOrCreateProcess()` eagerly pre-warms 4 auxiliary sessions. With the
GC in place, this should be **deferred**: pre-warm only when the process is created
for an actual user conversation, not for transient queue/periodic work.

Change `GetOrCreateProcess()` to accept a `prewarm bool` parameter:
- `true` when called from `CreateSession`/`ResumeSession` for user conversations
- `false` when called from `ProcessPendingQueues` or `PeriodicRunner` paths

Alternatively, keep pre-warming always-on and let the GC clean up the process
shortly after — simpler but wastes ~5 seconds of Claude startup for no reason.

## Implementation Plan

### 1. Add GC Loop to `ACPProcessManager`

```go
// GCConfig configures the garbage collection loop.
type GCConfig struct {
    Interval    time.Duration // How often to run GC (default: 30s)
    GracePeriod time.Duration // How long a process must be sessionless before stopping (default: 60s)
}
```

New fields on `ACPProcessManager`:
- `lastSessionSeen map[string]time.Time` — per workspace, when sessions were last present
- `gcStop chan struct{}` / `gcDone chan struct{}` — lifecycle management

New methods:
- `StartGC(config GCConfig, sessionQuery SessionQueryFunc)` — starts the GC goroutine
- `StopGC()` — stops the GC goroutine
- `RunGCOnce(sessionQuery SessionQueryFunc)` — single GC iteration (exported for testing)

The `SessionQueryFunc` is a callback to query `SessionManager` without creating a
circular dependency:

```go
// SessionQueryFunc returns running sessions grouped by workspace UUID.
// Used by the GC to determine which processes still have active sessions.
type SessionQueryFunc func() map[string][]SessionInfo

// SessionInfo contains the minimum information the GC needs about a session.
type SessionInfo struct {
    SessionID    string
    IsPrompting  bool
    HasObservers bool
    QueueLength  int
    // NextPeriodicAt is when the next periodic prompt is due (nil = no periodic config)
    NextPeriodicAt *time.Time
}
```

### 2. Provide Session Info from `SessionManager`

Add a method to `SessionManager` that the GC can call:

```go
// GetSessionInfoByWorkspace returns session info grouped by workspace UUID.
// Used by the ACP process GC to determine which processes are still needed.
func (sm *SessionManager) GetSessionInfoByWorkspace() map[string][]SessionInfo {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    result := make(map[string][]SessionInfo)
    for _, bs := range sm.sessions {
        uuid := bs.GetWorkspaceUUID()
        if uuid == "" {
            continue
        }

        var nextPeriodic *time.Time
        if sm.store != nil {
            if p, err := sm.store.Periodic(bs.GetSessionID()).Get(); err == nil && p.Enabled {
                nextPeriodic = p.NextScheduledAt
            }
        }

        var queueLen int
        if sm.store != nil {
            queueLen, _ = sm.store.Queue(bs.GetSessionID()).Len()
        }

        result[uuid] = append(result[uuid], SessionInfo{
            SessionID:      bs.GetSessionID(),
            IsPrompting:    bs.IsPrompting(),
            HasObservers:   bs.HasObservers(),
            QueueLength:    queueLen,
            NextPeriodicAt: nextPeriodic,
        })
    }
    return result
}
```

### 3. Wire Up in `server.go`

After creating the `ACPProcessManager` and `SessionManager`:

```go
acpProcessMgr.StartGC(GCConfig{
    Interval:    30 * time.Second,
    GracePeriod: 60 * time.Second,
}, func() map[string][]SessionInfo {
    return sessionMgr.GetSessionInfoByWorkspace()
})
```

Add `acpProcessMgr.StopGC()` to the server shutdown path.

### 4. Defer Auxiliary Pre-warming (Optional Enhancement)

Modify `GetOrCreateProcess()` signature:

```go
func (m *ACPProcessManager) GetOrCreateProcess(
    workspace *config.WorkspaceSettings,
    r *runner.Runner,
    prewarm bool,  // New parameter
) (*SharedACPProcess, error) {
    // ... existing logic ...
    if !m.DisableAuxiliary && prewarm {
        go m.prewarmAuxiliarySessions(workspace.UUID, processLogger)
    }
}
```

Update callers:
- `SessionManager.getSharedProcess()` → pass `true` (user conversations)
- `SessionManager.EnsureWorkspaceAuxiliary()` → pass `false`
- Any transient/background path → pass `false`

## GC Algorithm (Pseudocode)

```
every Interval:
    sessionsByWorkspace = sessionQuery()

    // Tier 1: Close idle sessions
    for each workspace, sessions in sessionsByWorkspace:
        for each session in sessions:
            if session.IsPrompting:
                continue  // Active work
            if session.HasObservers:
                continue  // UI connected
            if session.QueueLength > 0:
                continue  // Pending work
            if session.NextPeriodicAt != nil &&
               session.NextPeriodicAt < now + 2*Interval:
                continue  // Periodic prompt due soon
            sessionManager.CloseSession(session.SessionID, "gc_idle")

    // Tier 2: Stop idle processes
    for each workspaceUUID, process in acpProcessManager.processes:
        runningSessions = sessionQuery()[workspaceUUID]
        if len(runningSessions) > 0:
            lastSessionSeen[workspaceUUID] = now
            continue
        if lastSessionSeen[workspaceUUID] is zero:
            lastSessionSeen[workspaceUUID] = now  // First time seeing it empty
            continue
        if now - lastSessionSeen[workspaceUUID] < GracePeriod:
            continue  // Within grace period
        acpProcessManager.StopProcess(workspaceUUID)
        delete(lastSessionSeen, workspaceUUID)
```

## Edge Cases

### Session closed during active auxiliary prompt
Auxiliary prompts (title-gen, follow-up) run asynchronously. If the GC closes a
session while an aux prompt is in-flight, the aux prompt will fail with "no shared
process" on the next attempt. This is acceptable — the failure is logged and the
aux result is simply lost (title generation, follow-up suggestions are non-critical).

### Process stopped while PeriodicRunner is about to deliver
If the GC stops a process and the PeriodicRunner immediately tries to deliver,
`ResumeSession()` will call `GetOrCreateProcess()` and restart the process. This is
the correct behavior — the process is started on demand.

### Rapid open/close of conversations
The 60-second grace period prevents the process from being stopped and immediately
restarted. The user can open and close several conversations within 60 seconds
without triggering process restarts.

### Multiple workspaces
Each workspace has its own independent GC tracking. Closing all sessions in
workspace A does not affect workspace B's process.

### Server shutdown
`StopGC()` is called during shutdown. The existing `CloseAll()` → `pm.Close()`
path handles killing all processes. The GC does not interfere.

## Testing Strategy

1. **Unit test for GC algorithm**: Create mock `SessionQueryFunc` returning various
   states. Verify that `RunGCOnce()` correctly identifies idle sessions and idle
   processes.

2. **Grace period test**: Verify that a process is NOT stopped within the grace
   period, and IS stopped after it expires.

3. **Integration test**: Start a session, close it, wait for GC, verify the shared
   process is stopped.

4. **Periodic session preservation**: Verify that sessions with upcoming periodic
   prompts are NOT closed by the GC.

## Configuration

The GC intervals could be made configurable via `config.yaml` under a new section:

```yaml
process:
  gc_interval: 30s        # How often to check for idle processes
  gc_grace_period: 60s    # How long to wait before stopping an idle process
```

For the initial implementation, hardcoded defaults are sufficient.

## Impact Summary

| Component | Change |
|-----------|--------|
| `ACPProcessManager` | New GC loop, `lastSessionSeen` tracking, `StartGC`/`StopGC`/`RunGCOnce` |
| `SessionManager` | New `GetSessionInfoByWorkspace()` method |
| `server.go` | Wire up GC start/stop |
| `GetOrCreateProcess` | Optional: add `prewarm` parameter |
| Existing session lifecycle | **No changes** — GC is purely additive |
| Tests | New unit tests for GC; existing tests unaffected |
