---
description: Session Store, Recorder, Player, Lock, Queue, ActionButtonsStore, Flags, and auxiliary package
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
  - flags
  - AdvancedSettings
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
| `PeriodicStore`      | Periodic prompt config per session          | Yes (mutex)             |
| `Flags`              | Available feature flags registry            | N/A (read-only)         |

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

### Event.Meta: Generic Metadata Bag

Attach optional metadata to events using `RecordOption` during persistence:

```go
// Persist with metadata
recorder.RecordEventWithSeq(event,
    session.WithMeta(session.EventMeta{
        WorkingDir: "/path/to/dir",
        TaskID:     "task-123",
    }),
)
```

**Key patterns**:
- `Event.Meta` is a generic map (`map[string]interface{}`), size-capped at 64 KB
- Metadata is **not persisted to events.jsonl** — stored separately in `event_meta.jsonl`
- On event read, metadata is attached if a corresponding entry exists
- Use `WithMeta()` `RecordOption` to inject metadata during `RecordEventWithSeq()`
- Observers notified via `EventMetaObserver` interface (see `11-web-backend-sequences.md`)

### MaxSeq Tracking

The `Metadata.MaxSeq` field tracks the highest persisted sequence number. `ACPStartFailureCount` persists cold-start failure state across app restarts — `session_manager.go` increments it on exhausted retries and auto-archives when it reaches 3:

```go
type Metadata struct {
    EventCount          int   `json:"event_count"`
    MaxSeq              int64 `json:"max_seq,omitempty"`
    ACPStartFailureCount int  `json:"acp_start_failure_count,omitempty"` // Auto-archive at 3
}
```

`MaxSeq` is used by `SessionWSClient.getServerMaxSeq()` for client synchronization.

## Lock Management

```go
// Update lock status during operations
lock.SetProcessing("Agent thinking...")  // Before prompt
lock.SetIdle()                           // After response
lock.SetWaitingPermission("File write")  // During permission request
```

## Message Queue

**Important**: Queue configuration is **global/workspace-scoped**, NOT per-session. See [docs/devel/message-queue.md](../docs/devel/message-queue.md) for config options, REST API, WebSocket notifications, and title auto-generation.

## Periodic Prompts (PeriodicStore)

Stored in `periodic.json`. Only top-level sessions may have periodic prompts (child → 400).

**Key fields**:
- `PromptName` — references workspace prompt by name (resolved at send time via cache)
- `MaxIterations` — cap on runs (0 = unlimited). Auto-disables when reached.
- `Trigger` — `schedule` (default) or `onCompletion` (event-driven)
- `DelaySeconds` — wait after agent idle before firing (onCompletion only)
- `MaxDurationSeconds` — wall-clock cap since first run

**Critical**: Changing `PeriodicStore.Update()` signature requires updating BOTH `session_periodic_api.go` (PATCH handler) AND `mcpserver/server.go` (MCP tool) — both call `Update()`.

## Auxiliary Package

The `internal/auxiliary` package provides a hidden ACP session for utility tasks. Lazy init, auto-approve permissions, file writes denied, thread-safe.

```go
auxiliary.Initialize(acpCommand, logger)
title, err := auxiliary.GenerateTitle(ctx, userMessage)
auxiliary.Shutdown()
```

## Action Buttons Store

The `ActionButtonsStore` persists follow-up suggestions to disk. See [docs/devel/follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md) for full architecture.

```go
abStore := store.ActionButtons(sessionID)
abStore.Set(buttons, eventSeq)   // after analysis
abStore.Get()                    // returns empty slice if none
abStore.Clear()                  // on new prompt
```

Stored in `action_buttons.json` (not events.jsonl). Delete on clear (vs writing empty). Two-tier cache in BackgroundSession: memory + disk.

## Feature Flags (AdvancedSettings)

Per-session feature flags stored in metadata. See `16-web-backend-settings.md` for full patterns.

- All flags default to `false` (opt-in model)
- Use `GetFlagValue()` to safely check nil maps
- Flags stored in `metadata.json` as `advanced_settings` map
