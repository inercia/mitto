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

Stored in `periodic.json` per session. API: `GET/PUT/PATCH/DELETE /api/sessions/{id}/periodic`, `POST /api/sessions/{id}/periodic/run-now`.

```go
ps := store.Periodic(sessionID)
ps.Set(&session.PeriodicPrompt{Prompt: "...", Frequency: ..., Enabled: true})
ps.Update(prompt, promptName, frequency, enabled, freshContext, maxIterations, trigger, delaySeconds, maxDurationSeconds)  // partial update (pointer args, nil = no-op)
ps.RecordSent()                         // increments iteration_count + updates last_sent_at/next_scheduled_at; sets first_run_at on the first call
ps.TriggerNow(sessionID, resetTimer)    // immediate delivery via periodicRunner
```

**Max iterations / auto-stop** (`PeriodicPrompt` fields):
- `MaxIterations` (json `max_iterations`, 0/absent = unlimited) — per-conversation cap on scheduled runs.
- `IterationCount` (json `iteration_count`) — runs delivered so far; incremented **only** by `RecordSent` (never by `Update`/`Set`, which preserve it).
- `ReachedMaxIterations()` → true when `MaxIterations > 0 && IterationCount >= MaxIterations`.
- **Auto-stop**: in `periodic_runner.go` `deliverPrompt`'s `OnComplete`, after `RecordSent` the runner compares `IterationCount` against `config.EffectiveMaxPeriodicIterations(promptMax, configMax)` (smallest positive of prompt cap, config `max_periodic_iterations` default 100, hardcoded `GlobalMaxPeriodicIterations`=1000). When reached it **disables** the periodic (`Update(enabled=false)`) — it is **not** archived/deleted — and broadcasts via the `onPeriodicAutoStopped` callback.

**Trigger / on-completion / maxDuration** (`PeriodicPrompt` fields, added by the on-completion epic):
- `Trigger` (json `trigger`, "" / `schedule` (default) / `onCompletion`). `EffectiveTrigger()` treats "" as `schedule`; `IsOnCompletion()` is the predicate.
- `DelaySeconds` (json `delay_seconds`) — for `onCompletion`, seconds to wait after the agent goes idle before firing. `ClampDelay(floor)` raises it to the global floor (`min_periodic_completion_delay_seconds`, default 5); only applied when `IsOnCompletion()`.
- `MaxDurationSeconds` (json `max_duration_seconds`) + `FirstRunAt` (json `first_run_at`, set on the **first** `RecordSent` only). `ReachedMaxDuration(now)` → true when `MaxDurationSeconds > 0 && FirstRunAt != nil && now.Sub(*FirstRunAt) >= MaxDurationSeconds`.
- **Event-driven firing** (`periodic_runner.go`): turn completes → `BackgroundSession.onTurnIdle` → `PeriodicRunner.OnConversationIdle` → `armCompletionTimer(delay)` (replaces any pending timer; at most one per session) → after delay `fireOnCompletion` re-validates, then `autoStopIfMaxDurationReached` (disable + `onPeriodicAutoStopped` broadcast if the wall-clock cap is hit) else `TriggerNow(resetTimer=true)`. The delivered run's completion re-arms the next.

**Key rules**:
- Only top-level/parent sessions may have periodic prompts (child sessions return 400)
- `PromptName` references a named workspace prompt by name instead of embedding full text. `Validate()` accepts empty `Prompt` when `PromptName` is set. The periodic runner resolves the name to text at send time via the prompts cache.
- **Caller update required**: Changing `PeriodicStore.Update()` signature requires updating **both** `internal/web/session_periodic_api.go` (PATCH handler) AND `internal/mcpserver/server.go` (MCP tool handler) — both call `Update()`.

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
