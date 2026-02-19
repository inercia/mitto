---
description: WebSocket and async anti-patterns, race conditions, deduplication issues
globs:
  - "web/static/hooks/useWebSocket.js"
  - "internal/web/session_ws_client.go"
keywords:
  - websocket anti-pattern
  - race condition
  - lastSentSeq
  - deduplication
  - zombie connection
  - keepalive
  - seq tracker
  - stale client
---

# WebSocket and Async Anti-Patterns

## Timeout Handling

### ❌ Don't: Show Timeout Warning for Synchronous Errors

```javascript
// BAD: Backend returns error immediately, but frontend still shows timeout warning
const handleSubmit = async () => {
  const timeoutId = setTimeout(() => {
    showWarning("Message delivery could not be confirmed"); // Wrong!
  }, 5000);

  try {
    await sendPrompt(message); // Backend returns error synchronously
    clearTimeout(timeoutId);
  } catch (err) {
    // Error is shown, but timeout warning ALSO shows because clearTimeout
    // wasn't called before the error handler runs
    showError(err.message);
  }
};
```

### ✅ Do: Clear Timeout on ANY Promise Settlement

```javascript
// GOOD: Clear timeout before handling result
const handleSubmit = async () => {
  const timeoutId = setTimeout(() => {
    showWarning("Message delivery could not be confirmed");
  }, 5000);

  try {
    await sendPrompt(message);
  } catch (err) {
    showError(err.message);
  } finally {
    clearTimeout(timeoutId); // Always clear, regardless of success/failure
  }
};
```

## Connection State

### ❌ Don't: Assume WebSocket State from `readyState`

```javascript
// BAD: readyState can be OPEN even for zombie connections
if (ws.readyState === WebSocket.OPEN) {
  ws.send(message); // May silently fail!
}
```

### ✅ Do: Use Application-Level Keepalive

```javascript
// GOOD: Track actual message delivery
if (ws.readyState === WebSocket.OPEN && isConnectionHealthy(sessionId)) {
  ws.send(message);
}

// isConnectionHealthy checks keepalive_ack responses
const isConnectionHealthy = (sessionId) => {
  const keepalive = keepaliveRef.current[sessionId];
  return keepalive && keepalive.missedCount === 0;
};
```

## Seq Tracker Deduplication

### ❌ Don't: Keep Stale Seq Tracker on Client Recovery

```javascript
// BAD: Seq tracker not reset when stale client detected
case "events_loaded": {
  const isStaleClient = isStaleClientState(clientLastSeq, serverLastSeq);

  // Process events without resetting tracker
  for (const event of events) {
    if (event.seq) {
      markSeqSeen(sessionId, event.seq);  // Tracker still has stale highestSeq!
    }
  }
  // Fresh events from server are rejected as "very old" duplicates!
}
```

**Problem**: With stale `highestSeq = 200` and `MAX_RECENT_SEQS = 100`, any `seq < 100` is rejected.

### ✅ Do: Reset Seq Tracker Before Processing Stale Recovery Events

```javascript
// GOOD: Reset tracker when stale client detected
case "events_loaded": {
  const isStaleClient = isStaleClientState(clientLastSeq, serverLastSeq);

  // CRITICAL: Reset tracker BEFORE processing events
  if (isStaleClient) {
    console.log(`[M1 fix] Resetting seq tracker for stale client`);
    clearSeenSeqs(sessionId);
  }

  // Now process events with fresh tracker
  for (const event of events) {
    if (event.seq) {
      markSeqSeen(sessionId, event.seq);
    }
  }
}
```

## Backend lastSentSeq

### ❌ Don't: Reset lastSentSeq on Fallback to Initial Load

```go
// BAD: Resetting lastSentSeq when falling back to initial load
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    c.seqMu.Lock()
    c.lastSentSeq = 0  // BUG: This loses track of observer-delivered events!
    c.seqMu.Unlock()
}
```

**Race condition:**
1. Agent streams seq=18, observer delivers to client (`lastSentSeq=18`)
2. Client keepalive shows mismatch (client seq=18, storage seq=10)
3. Server falls back to initial load, resets `lastSentSeq=0`
4. `replayBufferedEventsWithDedup` re-sends seq=18 → duplicate!

### ✅ Do: Preserve lastSentSeq on Fallback

```go
// GOOD: Don't reset lastSentSeq - preserve observer-delivered events
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    // NOTE: Do NOT reset lastSentSeq here.
    // The observer path may have already delivered events with higher seq.
}
```

## Frontend HTML Coalescing

### ❌ Don't: Append Duplicate HTML During Streaming

```javascript
// BAD: Blindly appending HTML without checking for duplicates
if (shouldAppend) {
    const newHtml = (last.html || "") + msg.data.html;
    messages[messages.length - 1] = { ...last, html: newHtml };
}
```

### ✅ Do: Check for Duplicate Content Before Appending

```javascript
// GOOD: Check if content is already present before appending
if (shouldAppend) {
    const existingHtml = last.html || "";
    const incomingHtml = msg.data.html;

    // Safeguard: Skip if this is duplicate content
    if (existingHtml.endsWith(incomingHtml)) {
        console.log("[DEBUG agent_message] Skipping duplicate append");
        return prev;
    }

    const newHtml = existingHtml + incomingHtml;
    messages[messages.length - 1] = { ...last, html: newHtml };
}
```

## Related Documentation

- [WebSocket Protocol](../../docs/devel/websockets/) - Full protocol spec
- `22-web-frontend-websocket.md` - Frontend WebSocket patterns
- `27-web-frontend-sync.md` - Sync and deduplication

