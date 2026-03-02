---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, graceful shutdown, and session lifecycle anti-patterns
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
keywords:
  - session lifecycle
  - archive
  - unarchive
  - ACP connection
  - graceful shutdown
  - session resume
  - archived session
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

## MCP Server Lifecycle

| Event               | MCP Server Action          |
| ------------------- | -------------------------- |
| Session created     | Start if flags enabled     |
| Session archived    | Stop                       |
| Session unarchived  | Start (new instance)       |
| Session deleted     | Stop                       |
| Server shutdown     | Stop all                   |

Per-session resources must be destroyed on archive and recreated (new instances) on unarchive.

## WebSocket Messages

| Message Type        | Direction       | When                          |
| ------------------- | --------------- | ----------------------------- |
| `session_archived`  | Server->Client  | Session archived/unarchived   |
| `acp_stopped`       | Server->Client  | ACP connection closed         |
| `acp_started`       | Server->Client  | ACP connection started        |
| `acp_start_failed`  | Server->Client  | ACP failed to start           |
