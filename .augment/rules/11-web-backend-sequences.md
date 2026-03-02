---
description: Backend sequence number assignment, observer pattern, MarkdownBuffer flushing, prompt ACK, and SeqProvider
globs:
  - "internal/web/client.go"
  - "internal/web/markdown.go"
  - "internal/web/session_ws*.go"
  - "internal/web/background_session.go"
keywords:
  - sequence number
  - SeqProvider
  - MarkdownBuffer
  - observer pattern
  - prompt ACK
  - max_seq
  - Flush SafeFlush
---

# Sequence Numbers and Observer Pattern (Backend)

> **Full Protocol**: See [docs/devel/websockets/](../../docs/devel/websockets/) for complete spec.

## Critical: Receive-Time Seq Assignment

Sequence numbers must be assigned at **ACP receive time**, not when content is emitted from buffers:

```
WRONG: Text buffered -> Tool call gets seq=N -> Buffer flushes gets seq=N+1 (leapfrog!)
RIGHT: Text gets seq=N at receive -> Tool call gets seq=N+1 -> Buffer flushes with preserved seq=N
```

### SeqProvider Interface

```go
type SeqProvider interface {
    GetNextSeq() int64
}

func (bs *BackgroundSession) GetNextSeq() int64 {
    return bs.getNextSeq()
}
```

## Tool Calls Signal End of Text Block

When a tool call or thought arrives from ACP, force-flush the MarkdownBuffer:

| Event Type          | Action                           | Why                         |
| ------------------- | -------------------------------- | --------------------------- |
| `AgentMessageChunk` | Buffer (smart flush)             | Preserve markdown structure |
| `AgentThoughtChunk` | **Force flush** buffer first     | Text block is complete      |
| `ToolCall`          | **Force flush** buffer first     | Text block is complete      |
| Prompt complete     | **Force flush** buffer           | Session is done streaming   |

### Flush() vs SafeFlush()

| Method        | Behavior                             | When to Use                           |
| ------------- | ------------------------------------ | ------------------------------------- |
| `Flush()`     | Force flush, ignores markdown state  | Tool calls, thoughts, prompt complete |
| `SafeFlush()` | Only flush if not in table/list/code | Periodic/timeout flushes              |

## Observer Cleanup

**Always** remove observers when WebSocket connections close:

```go
defer func() {
    if c.bgSession != nil {
        c.bgSession.RemoveObserver(c)  // MUST remove
    }
}()
```

## Race Condition Prevention

Check for duplicates after reacquiring lock in `SessionManager`:

```go
sm.mu.Lock()
if existing, ok := sm.sessions[id]; ok {
    sm.mu.Unlock()
    bs.Close("duplicate")
    return existing, nil
}
sm.sessions[id] = bs
sm.mu.Unlock()
```

## Prompt ACK Flow

```
Frontend --- prompt {prompt_id} --> Backend
                                    Validate & persist
Frontend <-- prompt_received ------ (or error if rejected)
Frontend <-- agent_message ---------
Frontend <-- prompt_complete -------
```

The `connected` message includes `last_user_prompt_id` for delivery verification after reconnect.

## max_seq Piggybacking

All streaming messages include `max_seq` for immediate gap detection:

```go
func (c *SessionWSClient) getServerMaxSeq() int64 {
    // Check persisted events AND assigned seq (includes unpersisted)
    maxSeq := metadata.EventCount
    if assignedSeq := bs.GetMaxAssignedSeq(); assignedSeq > maxSeq {
        maxSeq = assignedSeq
    }
    return maxSeq
}
```

`GetMaxAssignedSeq()` returns `nextSeq - 1` (highest ever assigned), preventing false stale detection during streaming.

## Backend Anti-Pattern: lastSentSeq Reset

```go
// BAD: Resetting lastSentSeq on fallback loses observer-delivered events
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    c.lastSentSeq = 0  // BUG: observer already delivered higher seq!
}

// GOOD: Preserve lastSentSeq
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    // Do NOT reset lastSentSeq
}
```
