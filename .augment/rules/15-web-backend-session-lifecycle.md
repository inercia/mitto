---
description: Session lifecycle management, archive/unarchive flows, ACP connection lifecycle, and graceful shutdown
globs:
  - "internal/web/session_api.go"
  - "internal/web/session_manager.go"
  - "internal/web/session_ws.go"
keywords:
  - archive
  - unarchive
  - session lifecycle
  - graceful shutdown
  - ACP lifecycle
  - CloseSessionGracefully
  - ResumeSession
---

# Session Lifecycle Management

## Overview

Sessions have a lifecycle that includes creation, active use, archiving, and deletion. The ACP (Agent Communication Protocol) connection lifecycle is tightly coupled with session state.

## Session States

| State        | ACP Connection | User Can Send | Visible In List |
| ------------ | -------------- | ------------- | --------------- |
| **Active**   | Running        | Yes           | Yes (green dot) |
| **Archived** | Stopped        | No (read-only)| Archived section|
| **Deleted**  | N/A            | N/A           | No              |

## Archive Flow

When a session is archived, the ACP server must be properly shut down:

```
User clicks Archive
    ↓
Backend receives PATCH /api/sessions/{id} with archived=true
    ↓
CloseSessionGracefully() called
    ↓
If agent is responding:
    Wait for response to complete (with timeout)
    ↓
Close ACP connection
    ↓
Broadcast "acp_stopped" to all clients
    ↓
Update metadata (archived=true, archived_at=now)
    ↓
Broadcast "session_archived" to all clients
```

### Backend Implementation

```go
// session_api.go - handleUpdateSession
if req.Archived != nil && *req.Archived {
    if s.sessionManager != nil {
        // Wait for any active response to complete before archiving
        reason := "archived"
        if !s.sessionManager.CloseSessionGracefully(sessionID, reason, archiveWaitTimeout) {
            // Timeout - force close
            reason = "archived_timeout"
            s.sessionManager.CloseSession(sessionID, reason)
        }
        s.BroadcastACPStopped(sessionID, reason)
    }
}
```

### CloseSessionGracefully

Waits for active response before closing:

```go
func (sm *SessionManager) CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool {
    bs := sm.GetSession(sessionID)
    if bs == nil {
        return true  // Nothing to close
    }

    // Wait for response to complete
    if bs.IsPrompting() {
        if !bs.WaitForResponseComplete(timeout) {
            return false  // Timeout
        }
    }

    sm.CloseSession(sessionID, reason)
    return true
}
```

## Unarchive Flow

When a session is unarchived, a new ACP connection is started:

```
User clicks Unarchive
    ↓
Backend receives PATCH /api/sessions/{id} with archived=false
    ↓
Update metadata (archived=false, archived_at cleared)
    ↓
Broadcast "session_archived" (with archived=false)
    ↓
ResumeSession() called to start new ACP
    ↓
If successful: Broadcast "acp_started"
If failed: Broadcast "acp_start_failed"
```

### Backend Implementation

```go
// session_api.go - handleUpdateSession (after metadata update)
if req.Archived != nil && !*req.Archived {
    if s.sessionManager != nil {
        _, err := s.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
        if err != nil {
            s.BroadcastACPStartFailed(sessionID, err.Error())
        } else {
            s.BroadcastACPStarted(sessionID)
        }
    }
}
```

## Critical: Don't Resume Archived Sessions on WebSocket Connect

When a WebSocket connects to a session, the backend must check if the session is archived before attempting to resume:

### ❌ Wrong: Resume without checking archived state

```go
// BAD: Resumes ACP for archived sessions
if bs == nil && store != nil {
    meta, err := store.GetMetadata(sessionID)
    if err == nil {
        bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
    }
}
```

### ✅ Correct: Skip resume for archived sessions

```go
// GOOD: Check archived state before resuming
if bs == nil && store != nil {
    meta, err := store.GetMetadata(sessionID)
    if err == nil {
        if meta.Archived {
            // Don't resume - archived sessions are read-only
            if clientLogger != nil {
                clientLogger.Debug("Session is archived, not resuming ACP")
            }
        } else {
            bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
        }
    }
}
```

## Frontend: Archived Sessions Don't Show Active Indicator

Archived sessions should never show the green "active" dot since they have no ACP connection:

### ❌ Wrong: Show active for all loaded sessions

```javascript
// BAD: All sessions in state are marked active
return {
    status: "active",
    isActive: true,
    isStreaming: data.isStreaming || false,
};
```

### ✅ Correct: Check archived state

```javascript
// GOOD: Archived sessions are not active
const isArchived = data.info?.archived || storedSession?.archived || false;
return {
    status: isArchived ? "archived" : "active",
    isActive: !isArchived,
    isStreaming: !isArchived && (data.isStreaming || false),
    archived: isArchived,
};
```

## WebSocket Messages for Lifecycle Events

| Message Type        | Direction | When                          |
| ------------------- | --------- | ----------------------------- |
| `session_archived`  | Server→Client | Session archived/unarchived |
| `acp_stopped`       | Server→Client | ACP connection closed       |
| `acp_started`       | Server→Client | ACP connection started      |
| `acp_start_failed`  | Server→Client | ACP failed to start         |

## Testing Session Lifecycle

```go
func TestArchiveStopsACP(t *testing.T) {
    // Create active session
    sessionID := createSession(t)
    
    // Verify ACP is running
    bs := sessionManager.GetSession(sessionID)
    require.NotNil(t, bs)
    require.False(t, bs.IsClosed())
    
    // Archive session
    archiveSession(t, sessionID)
    
    // Verify ACP is stopped
    bs = sessionManager.GetSession(sessionID)
    require.Nil(t, bs)  // Session removed from manager
}

func TestArchivedSessionNoResume(t *testing.T) {
    // Create and archive session
    sessionID := createSession(t)
    archiveSession(t, sessionID)
    
    // Connect WebSocket to archived session
    ws := connectToSession(t, sessionID)
    
    // Verify no ACP was started
    bs := sessionManager.GetSession(sessionID)
    require.Nil(t, bs)
    
    // Verify can still read history
    events := loadEvents(t, ws)
    require.NotEmpty(t, events)
}
```

