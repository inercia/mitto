---
description: Follow-up suggestions (action buttons), async analysis, caching patterns, and persistence
globs:
  - "internal/web/background_session.go"
  - "internal/session/action_buttons.go"
---

# Follow-up Suggestions (Action Buttons)

See [docs/devel/follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md) for full architecture.

## Overview

Action buttons are AI-generated response options shown after the agent completes a response. They help users quickly continue conversations without typing.

## Key Patterns

### Two-tier Cache in BackgroundSession

```go
// Memory cache for fast access
bs.actionButtonsMu.RLock()
if bs.cachedActionButtons != nil {
    // Return from memory
}
bs.actionButtonsMu.RUnlock()

// Fall back to disk
abStore := bs.store.ActionButtons(bs.persistedID)
buttons, _ := abStore.Get()
```

### Async Analysis

Non-blocking after prompt completes:

```go
// In handlePromptComplete
if agentMessage != "" {
    go bs.analyzeFollowUpQuestions(agentMessage)  // Non-blocking
}
```

### Clear on New Activity

Suggestions become stale when user sends new prompt:

```go
func (bs *BackgroundSession) clearActionButtons() {
    // Clear memory
    bs.cachedActionButtons = nil
    // Clear disk
    abStore.Clear()
    // Notify observers (empty array = hide buttons)
    bs.notifyObservers(func(o SessionObserver) {
        o.OnActionButtons([]ActionButton{})
    })
}
```

### Send to New Observers

Clients connecting after generation still see suggestions:

```go
func (bs *BackgroundSession) AddObserver(observer SessionObserver) {
    // ... add to observers map ...
    if !isPrompting {
        bs.sendCachedActionButtonsTo(observer)  // Send cached buttons
    }
}
```

## ActionButtonsStore

The `ActionButtonsStore` persists follow-up suggestions to disk:

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

## WebSocket Message

```go
// Backend sends action_buttons message
case 'action_buttons':
    // data: { session_id, buttons: [{label, response}, ...] }
```

## Key Behaviors

| Event | Action |
|-------|--------|
| `action_buttons` received | Update session state with new buttons |
| User clicks button | Send `btn.response` as prompt, buttons auto-clear |
| User types and sends | Buttons cleared by server (new prompt = stale suggestions) |
| Session switch | New session's buttons loaded from server |

