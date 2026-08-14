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
| `Store`              | Low-level file I/O, CRUD operations         | Yes (global gate + keyed session locks) |
| `Recorder`           | High-level recording API, session lifecycle | Yes (mutex)             |
| `Player`             | Read-only playback, navigation              | No (single-user)        |
| `Lock`               | Session locking, heartbeat, cleanup         | Yes (mutex + goroutine) |
| `Queue`              | Message queue for busy agent                | Yes (mutex)             |
| `ActionButtonsStore` | Follow-up suggestions persistence           | Yes (mutex)             |
| `LoopStore`      | Loop prompt config per session          | Yes (mutex)             |
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

### Prune hysteresis (`PruneConfig.Slack`, mitto-9wwj)

`Recorder.recordEvent` / `RecordEventWithSeq` call `PruneIfNeeded` after **every** append. Historically `pruneInternal` triggered and targeted the same `MaxMessages`, so any session past the cap did a full `events.jsonl` parse + rewrite + fsync per single append under `s.mu`. Fix (`internal/session/prune.go`):

- `PruneConfig.Slack` — hysteresis on `MaxMessages`. Default `MaxMessages/5` (min 1). **Negative = exact trim** (no hysteresis).
- Triggers only when `len(events) > MaxMessages + slack`; still trims down to `MaxMessages`.
- Pre-checks `meta.EventCount` to **skip the full parse** on the no-op append-path call.
- `Store.PruneKeepLast` (REST "keep exactly N") passes `Slack: -1` to preserve exact-trim semantics.
- Seq / `MaxSeq` semantics untouched. Regression: `TestStore_PruneIfNeeded_WriteAmplificationAtCap`.

### Store two-level locking (`mitto-pkeh`)

`Store.mu` is a lifecycle and all-store-operation gate, not the mutex for every
session write. Ordinary session-scoped methods hold `Store.mu.RLock()` plus a
ref-counted keyed session `RWMutex`, so unrelated sessions can perform disk I/O
concurrently while same-session metadata/events/prune operations remain ordered.

Global snapshot or destructive operations retain `Store.mu.Lock()`: `List`,
child scans/counts, `Delete`/cascade, cleanup passes, observer mutation, and
`Close`. This keeps their prior correctness semantics and makes `Close` wait for
all in-flight session operations.

**Lock order**: Store lifecycle gate → keyed-lock registry → session mutex;
release in reverse order. A keyed entry's reference count includes waiters, and
the entry is deleted only after its mutex is unlocked. Never call a public
session-locking method from code already holding the exclusive Store gate; use
the corresponding internal no-lock helper.

## Lock Management

```go
// Update lock status during operations
lock.SetProcessing("Agent thinking...")  // Before prompt
lock.SetIdle()                           // After response
lock.SetWaitingPermission("File write")  // During permission request
```

## Message Queue

**Important**: Queue configuration is **global/workspace-scoped**, NOT per-session. See [docs/devel/message-queue.md](../docs/devel/message-queue.md) for config options, REST API, WebSocket notifications, and title auto-generation.

## Loop Prompts (LoopStore)

Stored in `loop.json`. Only top-level sessions may have loop prompts (child → 400). Field semantics documented in [docs/devel/message-queue.md](../docs/devel/message-queue.md).

**Critical**: Changing `LoopStore.Update()` signature requires updating BOTH `session_loop_api.go` (PATCH handler) AND `mcpserver/server.go` (MCP tool) — both call `Update()`.

**Un-loop/re-loop persistence symmetry**: `Detach()` saves settings to a slot and clears the active config (un-loop); `GetSaved()`/restore reads it back. A fresh loop `Set()` (not a restore) must be followed by `ClearSaved()` so a stale saved slot doesn't leak in later — required in both `session_loop_write.go` (`handleSetLoop`) and `mcpserver/server.go` (MCP create-loop path).

## Auxiliary Package

`internal/auxiliary` — hidden ACP session for utility tasks (title gen, follow-ups). Lazy init, auto-approve permissions, file writes denied, thread-safe. Entry points: `Initialize` / `GenerateTitle` / `Shutdown`.

## Action Buttons Store

`ActionButtonsStore` persists follow-up suggestions to `action_buttons.json` (not `events.jsonl`). Two-tier cache in BackgroundSession (memory + disk); `Clear()` deletes the file rather than writing empty. See [docs/devel/follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md).

## Feature Flags (AdvancedSettings)

Per-session feature flags stored in metadata. See `16-web-backend-settings.md` for full patterns.

- All flags default to `false` (opt-in model)
- Use `GetFlagValue()` to safely check nil maps
- Flags stored in `metadata.json` as `advanced_settings` map
