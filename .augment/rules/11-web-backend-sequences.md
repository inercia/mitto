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

## Terminal Session Messages (`session_gone`)

When sending `session_gone` for a deleted session, the write pump must have time to flush the message before the connection closes:

```go
// Start pumps so the message can be written
go client.writePump()
go client.readPump()

// Send terminal message
client.sendMessage(WSMsgTypeSessionGone, map[string]interface{}{
    "session_id": sessionID,
    "reason":     "session not found",
})

// Close after flush delay (100ms lets writePump deliver the message)
go func() {
    time.Sleep(100 * time.Millisecond)
    client.wsConn.Close()
}()
```

This pattern ensures the client receives the `session_gone` message before the WebSocket close frame.

## Send Buffer Backpressure (`ws_conn.go`)

`WSConn.SendMessage` and `SendRaw` use **backpressure** instead of silently dropping messages when the send buffer is full:

```go
func (w *WSConn) sendWithBackpressure(data []byte, msgType string) {
    // Fast path: non-blocking send
    select {
    case w.send <- data:
        return
    default:
    }
    // Slow path: wait up to 100ms, then close connection
    timer := time.NewTimer(sendBackpressureTimeout) // 100ms
    defer timer.Stop()
    select {
    case w.send <- data:
        // Buffer drained, message sent
    case <-timer.C:
        w.conn.Close() // Force reconnect — client syncs via load_events
    }
}
```

**Why not drop?** Silent drops cause unrecoverable sequence gaps — the client has no way to detect the loss. Closing the connection triggers the client's reconnection flow, which syncs from persisted events.

**Why 100ms timeout?** Observer notifications call `SendMessage` on all observers sequentially. A long wait would delay healthy clients. The 100ms absorbs short bursts without blocking.

## WritePump Close Frames (`ws_conn.go`)

The `WritePump` goroutine sends proper WebSocket close frames before exiting, so clients receive a clean close code instead of an abrupt TCP teardown (code 1006):

| Exit Path | Close Code | Reason | Notes |
|-----------|-----------|--------|-------|
| Send channel closed (`!ok`) | 1000 (NormalClosure) | `""` | Clean shutdown |
| Ping write failed | 1001 (GoingAway) | `"ping failed"` | Best-effort: write may fail |
| Context cancelled | 1001 (GoingAway) | `"server shutdown"` | Best-effort: write may fail |
| Backpressure timeout | 1006 (Abnormal) | _(no close frame)_ | `conn.Close()` from `sendWithBackpressure` |

> For the complete close code table, backpressure flow, and client-side handling, see [synchronization.md — WebSocket Close Codes](../../docs/devel/websockets/synchronization.md#websocket-close-codes).

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
