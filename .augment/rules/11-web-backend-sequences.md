---
description: Sequence number assignment, observer pattern, WebSocket message ordering, MarkdownBuffer flushing, and SeqProvider
globs:
  - "internal/web/client.go"
  - "internal/web/markdown.go"
  - "internal/web/background_session.go"
  - "internal/web/session_ws.go"
---

# Sequence Numbers and Observer Pattern

## Critical Patterns

### Observer Cleanup

**Always** remove observers when WebSocket connections close:

```go
defer func() {
    if c.bgSession != nil {
        c.bgSession.RemoveObserver(c)  // MUST remove
    }
}()
```

### Race Condition Prevention

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

## Sequence Number Assignment

**Critical**: Sequence numbers must be assigned at **ACP receive time**, not when content is emitted from buffers.

### SeqProvider Interface

`WebClient` uses `SeqProvider` to get sequence numbers. `BackgroundSession` implements this:

```go
// SeqProvider provides sequence numbers for event ordering
type SeqProvider interface {
    GetNextSeq() int64
}

// BackgroundSession implements SeqProvider
func (bs *BackgroundSession) GetNextSeq() int64 {
    return bs.getNextSeq()
}
```

### Why Receive-Time Assignment Matters

Without receive-time assignment, tool calls can "leapfrog" text:

```
❌ WRONG (emit-time assignment):
1. Text received → buffered in MarkdownBuffer
2. Tool call received → gets seq=N (buffer hasn't flushed yet)
3. Buffer flushes → text gets seq=N+1
Result: Tool call appears BEFORE text in UI

✅ CORRECT (receive-time assignment):
1. Text received → gets seq=N, buffered with pendingSeq=N
2. Tool call received → gets seq=N+1
3. Buffer flushes → text uses preserved seq=N
Result: Text appears BEFORE tool call in UI ✓
```

## Tool Calls Signal End of Text Block

**Critical insight**: When a tool call or thought arrives from ACP, it signals that the agent has finished its current text output. The MarkdownBuffer should be force-flushed immediately.

**Rationale**: The ACP protocol does NOT interleave tool calls in the middle of markdown blocks. The agent always completes its explanation before invoking a tool:

```
Agent → "Let me read that file..." (AgentMessageChunk)
Agent → ToolCall(read_file)        ← Signals text is complete
Agent → "I found the following..." (AgentMessageChunk after tool completes)
```

### Flushing Strategy

| Event Type | Action | Why |
|------------|--------|-----|
| `AgentMessageChunk` | Buffer (smart flush on boundaries) | Preserve markdown structure |
| `AgentThoughtChunk` | **Force flush** buffer first | Text block is complete |
| `ToolCall` | **Force flush** buffer first | Text block is complete |
| Prompt complete | **Force flush** buffer | Session is done streaming |

```go
case u.ToolCall != nil:
    seq := c.getNextSeq()
    // Force flush - tool call means agent finished its text block
    c.mdBuffer.Flush()  // Use Flush(), NOT SafeFlush()
    if c.onToolCall != nil {
        c.onToolCall(seq, ...)
    }
```

### Flush() vs SafeFlush()

| Method | Behavior | When to Use |
|--------|----------|-------------|
| `Flush()` | Force flush, ignores markdown state | Tool calls, thoughts, prompt complete |
| `SafeFlush()` | Only flush if not in table/list/code | Periodic/timeout flushes |

**Avoid SafeFlush for tool calls**: SafeFlush() can return false (not flushed) if we're mid-table. But if a tool call arrives, the agent is done with that table - flush it anyway.

### Callback Signatures

All callbacks include `seq` as first parameter:

```go
type WebClientConfig struct {
    SeqProvider    SeqProvider
    OnAgentMessage func(seq int64, html string)
    OnAgentThought func(seq int64, text string)
    OnToolCall     func(seq int64, id, title, status string)
    OnToolUpdate   func(seq int64, id string, status *string)
    OnPlan         func(seq int64)
    OnFileWrite    func(seq int64, path string, size int)
    OnFileRead     func(seq int64, path string, size int)
}
```

### MarkdownBuffer Seq Tracking

The buffer preserves seq from first chunk:

```go
func (mb *MarkdownBuffer) Write(seq int64, chunk string) {
    if mb.buffer.Len() == 0 {
        mb.pendingSeq = seq  // First chunk's seq becomes pendingSeq
    }
    // ... buffer content ...
}

func (mb *MarkdownBuffer) flushLocked() {
    seq := mb.pendingSeq  // Use preserved seq
    mb.pendingSeq = 0
    mb.onFlush(seq, htmlStr)
}
```

## WebSocket Message Types

See [docs/devel/websocket-messaging.md](../docs/devel/websocket-messaging.md) for complete list.

**Key messages:**
- `prompt` / `prompt_received` - Prompt with ACK
- `agent_message` - Streaming HTML (includes `seq` for ordering)
- `sync_session` / `session_sync` - Mobile wake resync
- `queue_message_titled` - Queue title generated

## Testing with SeqProvider

```go
type mockSeqProvider struct {
    nextSeq int64
}

func (m *mockSeqProvider) GetNextSeq() int64 {
    seq := m.nextSeq
    m.nextSeq++
    return seq
}

func TestWebClient_SeqAssignment(t *testing.T) {
    seqProvider := &mockSeqProvider{nextSeq: 1}
    var receivedSeqs []int64

    client := NewWebClient(WebClientConfig{
        SeqProvider: seqProvider,
        OnAgentMessage: func(seq int64, html string) {
            receivedSeqs = append(receivedSeqs, seq)
        },
    })
    defer client.Close()

    // Simulate: text chunk, tool call, text flush
    client.SessionUpdate(ctx, textChunkNotification)
    client.mdBuffer.Flush()

    if receivedSeqs[0] != 1 {
        t.Errorf("text seq = %d, want 1", receivedSeqs[0])
    }
}
```

