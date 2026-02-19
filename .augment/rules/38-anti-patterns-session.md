---
description: Session lifecycle anti-patterns, archive/unarchive issues, ACP connection management
globs:
  - "internal/web/session_manager.go"
  - "internal/web/background_session.go"
keywords:
  - session lifecycle
  - archive anti-pattern
  - ACP connection
  - session resume
  - archived session
---

# Session Lifecycle Anti-Patterns

## Archived Session Handling

### ❌ Don't: Resume ACP for Archived Sessions

```go
// BAD: Resumes ACP without checking archived state
if bs == nil && store != nil {
    meta, err := store.GetMetadata(sessionID)
    if err == nil {
        // Missing archived check!
        bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
    }
}
```

**Problem**: Archived sessions should be read-only with no ACP connection:
- Wastes resources (ACP process running for read-only session)
- Confuses users (green "active" dot on archived session)
- Violates the archive contract (archived = no active agent)

### ✅ Do: Check Archived State Before Resuming

```go
// GOOD: Skip resume for archived sessions
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

## UI Indicators

### ❌ Don't: Show Active Indicator for Archived Sessions

```javascript
// BAD: All sessions in state are marked active
const isActiveSession = session.isActive || session.status === "active";
// Archived sessions incorrectly show green dot!
```

### ✅ Do: Check Archived State for UI Indicators

```javascript
// GOOD: Archived sessions are never "active"
const isActiveSession =
    !isArchived && (session.isActive || session.status === "active");
const isStreaming = !isArchived && (session.isStreaming || false);
```

## Archive Workflow

### Pattern: Proper Archive Sequence

When archiving a session:

1. **Wait for completion** - Let any active response complete (graceful shutdown)
2. **Close ACP connection** - Stop the agent process
3. **Stop MCP server** - Clean up per-session MCP server if running
4. **Mark as archived** - Update metadata
5. **Broadcast state change** - Notify all clients

```go
func (sm *SessionManager) ArchiveSession(sessionID string) error {
    bs := sm.getSession(sessionID)
    if bs != nil {
        // 1. Close session (handles graceful shutdown)
        bs.Close("archived")
        
        // 2. Remove from active sessions
        sm.removeSession(sessionID)
    }
    
    // 3. Mark as archived in metadata
    return sm.store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
        meta.Archived = true
    })
}
```

## Unarchive Workflow

### Pattern: Proper Unarchive Sequence

When unarchiving a session:

1. **Clear archived flag** - Update metadata first
2. **Resume ACP immediately** - Start ACP via ResumeSession
3. **Broadcast acp_started** - Notify all clients

### Pattern: WebSocket Client Attachment After Unarchive

When a WebSocket client was connected to an archived session (bgSession == nil),
and the session is later unarchived, the client's `bgSession` field is NOT automatically updated.

**Solution**: Before actions requiring bgSession, call `tryAttachToSession()`:

```go
// In handlePromptWithMeta, handleUIPromptAnswer, handleSetConfigOption:
if c.bgSession == nil {
    c.tryAttachToSession()
}

if c.bgSession == nil {
    c.sendError("Session not running")
    return
}

// tryAttachToSession checks if the session was resumed and attaches to it
func (c *SessionWSClient) tryAttachToSession() {
    bs := c.server.sessionManager.GetSession(c.sessionID)
    if bs == nil {
        return
    }
    c.bgSession = bs
    // Add as observer if initial load is done
    if c.initialLoadDone {
        bs.AddObserver(c)
    }
    // Notify client that session is now running
    c.sendMessage(WSMsgTypeACPStarted, map[string]interface{}{
        "session_id": c.sessionID,
    })
}
```

## MCP Server Lifecycle

### Pattern: MCP Server Follows Session Lifecycle

| Event | MCP Server Action |
|-------|-------------------|
| Session created | Start if flags enabled |
| Session archived | Stop |
| Session unarchived | Start (new instance) |
| Session deleted | Stop |
| Server shutdown | Stop all |

```go
// In BackgroundSession.Close()
func (bs *BackgroundSession) Close(reason string) {
    // ...
    
    // Stop MCP server (must happen before killing ACP)
    bs.stopSessionMcpServer()
    
    // Close ACP client
    if bs.acpClient != nil {
        bs.acpClient.Close()
    }
    // ...
}
```

## Lessons Learned

### 1. Archived Sessions Must Not Have Active ACP

When archiving:
- Wait for any active response to complete (graceful shutdown)
- Close the ACP connection
- Mark session as archived in metadata
- Broadcast state change to all clients

When viewing archived session:
- Load history from storage (read-only)
- Do NOT start ACP connection
- Do NOT show active indicator

### 2. Per-Session Resources Follow Session Lifecycle

Per-session resources (MCP servers, observers, etc.) must be:
- Created when session starts/unarchives
- Destroyed when session closes/archives
- Recreated as new instances on unarchive (don't reuse)

### 3. Session State Is Server-Authoritative

The server is the source of truth for:
- Archived state
- Active/streaming state
- Message history

Clients should always sync with server on connect, not trust cached state.

## Related Documentation

- `15-web-backend-session-lifecycle.md` - Complete lifecycle patterns
- `16-web-backend-settings.md` - Per-session settings
- [Session Management](../../docs/devel/session-management.md) - Architecture

