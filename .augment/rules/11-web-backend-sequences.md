---
description: Backend sequence number assignment, observer pattern, MarkdownBuffer flushing, prompt ACK, SeqProvider, WritePump close frames
globs:
  - "internal/web/client.go"
  - "internal/web/markdown.go"
  - "internal/web/session_ws*.go"
  - "internal/web/ws_conn.go"
  - "internal/web/background_session.go"
keywords:
  - sequence number
  - SeqProvider
  - MarkdownBuffer
  - observer pattern
  - prompt ACK
  - max_seq
  - Flush SafeFlush
  - WritePump
  - close frame
  - CloseMessage
  - session_gone
  - NegativeSessionCache
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

## Observer Patterns

### Multiple Observer Interfaces

`BackgroundSession` supports multiple observer types:

| Observer | Purpose | Implements |
|----------|---------|------------|
| `SessionObserver` | Session events (msg, error, close) | in `observer.go` |
| `EventMetaObserver` | Event metadata propagation | in `observer.go` |

### EventMetaObserver

Propagates `Event.Meta` (generic metadata bag) to interested parties:

```go
type EventMetaObserver interface {
    OnEventMeta(sessionID string, eventMeta *session.EventMeta)
}
```

Register via `AddMetaObserver(ctx, observer)`. Notified after metadata is persisted to `events.jsonl`. Use for:
- Streaming metadata to WebSocket clients
- Analytics/logging enrichment
- Cross-session metadata aggregation

### Observer Cleanup

**Always** remove observers when WebSocket connections close:

```go
defer func() {
    if c.bgSession != nil {
        c.bgSession.RemoveObserver(c)      // Remove SessionObserver
        c.bgSession.RemoveMetaObserver(c)  // Remove EventMetaObserver if registered
    }
}()
```

## Race Condition Prevention

Duplicate sessions: check after reacquiring lock in `SessionManager`:

```go
sm.mu.Lock()
if existing, ok := sm.sessions[id]; ok {
    sm.mu.Unlock()
    bs.Close("duplicate")
    return existing, nil  // Return existing, don't create new
}
sm.sessions[id] = bs
sm.mu.Unlock()
```

## Key Patterns (Abbreviated)

| Pattern | Rule |
|---------|------|
| Prompt ACK | `connected` includes `last_user_prompt_id` for delivery verification |
| max_seq | All messages include it; `GetMaxAssignedSeq()` prevents false stale detection |
| Backpressure | Wait 100ms on full buffer, then close (never drop) |
| Close codes | 1000=clean, 1001=shutdown/error, 1006=backpressure timeout |
| Anti-pattern | Never reset `lastSentSeq` in fallback paths (observer already sent higher seqs) |
