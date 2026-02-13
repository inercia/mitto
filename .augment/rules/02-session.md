---
description: Session Store, Recorder, Player, Lock, Queue, ActionButtonsStore, and auxiliary package for title generation
globs:
  - "internal/session/**/*"
  - "internal/auxiliary/**/*"
keywords:
  - session
  - recorder
  - player
  - store
  - lock
  - queue
  - action buttons
  - title generation
---

# Session Package Patterns

**Architecture docs**: See [docs/devel/session-management.md](../docs/devel/session-management.md) and [docs/devel/message-queue.md](../docs/devel/message-queue.md).

## Quick Reference

| Component            | Responsibility                              | Thread-Safe             |
| -------------------- | ------------------------------------------- | ----------------------- |
| `Store`              | Low-level file I/O, CRUD operations         | Yes (mutex)             |
| `Recorder`           | High-level recording API, session lifecycle | Yes (mutex)             |
| `Player`             | Read-only playback, navigation              | No (single-user)        |
| `Lock`               | Session locking, heartbeat, cleanup         | Yes (mutex + goroutine) |
| `Queue`              | Message queue for busy agent                | Yes (mutex)             |
| `ActionButtonsStore` | Follow-up suggestions persistence           | Yes (mutex)             |

## Immediate Persistence (Web Interface)

Events are persisted **immediately** when received from ACP, preserving pre-assigned sequence numbers.

### Key Methods

| Method | Purpose | Seq Handling |
|--------|---------|--------------|
| `Store.AppendEvent()` | CLI recording | Assigns seq = EventCount + 1 |
| `Store.RecordEvent()` | Web immediate persistence | Preserves pre-assigned seq |
| `Recorder.RecordEventWithSeq()` | Web recording wrapper | Delegates to `Store.RecordEvent()` |

### Pattern: Immediate Persistence in BackgroundSession

```go
func (bs *BackgroundSession) onAgentMessage(seq int64, html string) {
    // Persist immediately with pre-assigned seq
    if bs.recorder != nil {
        event := session.Event{
            Seq:       seq,  // Pre-assigned by WebClient
            Type:      session.EventTypeAgentMessage,
            Timestamp: time.Now(),
            Data:      session.AgentMessageData{Text: html},
        }
        if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
            bs.logger.Error("Failed to persist agent message", "seq", seq, "error", err)
        }
    }

    // Notify all observers
    bs.notifyObservers(func(o SessionObserver) {
        o.OnAgentMessage(seq, html)
    })
}
```

### MaxSeq Tracking

The `Metadata.MaxSeq` field tracks the highest persisted sequence number:

```go
type Metadata struct {
    EventCount int   `json:"event_count"`
    MaxSeq     int64 `json:"max_seq,omitempty"` // Highest seq persisted
}
```

This is used by `SessionWSClient.getServerMaxSeq()` for client synchronization.

## Lock Management

```go
// Update lock status during operations
lock.SetProcessing("Agent thinking...")  // Before prompt
lock.SetIdle()                           // After response
lock.SetWaitingPermission("File write")  // During permission request
```

## Message Queue

**Important**: Queue configuration is **global/workspace-scoped**, NOT per-session.

See [docs/devel/message-queue.md](../docs/devel/message-queue.md) for:

- Configuration options and scope rationale
- REST API endpoints
- WebSocket notifications
- Title auto-generation

## Auxiliary Package

The `internal/auxiliary` package provides a hidden ACP session for utility tasks.

```go
auxiliary.Initialize(acpCommand, logger)  // Once at startup
title, err := auxiliary.GenerateTitle(ctx, userMessage)
auxiliary.Shutdown()  // On exit
```

**Key characteristics**: Lazy init, auto-approve permissions, file writes denied, thread-safe.

## Action Buttons Store

The `ActionButtonsStore` persists follow-up suggestions to disk. See [docs/devel/follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md) for full architecture.

```go
// Get action buttons store for a session
abStore := store.ActionButtons(sessionID)

// Store suggestions after analysis
abStore.Set(buttons, eventSeq)

// Read suggestions (returns empty slice if none)
buttons, err := abStore.Get()

// Clear when user sends new prompt
abStore.Clear()
```

**Key patterns**:

- Separate file (`action_buttons.json`) - not in events.jsonl (transient UI state, not history)
- Delete file on clear (vs writing empty) - reduces disk clutter
- Two-tier cache in BackgroundSession: memory (fast) + disk (persistent)
