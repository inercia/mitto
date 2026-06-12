---
description: Frontend sequence sync, deduplication, stale client detection, server authority, and error handling anti-patterns
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/lib.js"
  - "web/static/utils/websocket.js"
keywords:
  - sequence sync
  - message deduplication
  - server authority
  - lastKnownSeqRef
  - mergeMessagesWithSync
  - getMaxSeq
  - seenSeqsRef
  - clearSeenSeqs
  - isSeqDuplicate
  - stale client detection
  - isStaleClientState
  - staleRecoveryCooldownRef
  - STALE_RECOVERY_COOLDOWN_MS
  - session_gone
  - circuit breaker
  - isTerminalSessionError
  - handleSessionGone
---

# Frontend Sync, Deduplication, and Error Handling

> **Full Protocol**: See [docs/devel/websockets/](../../docs/devel/websockets/) for spec.

## Server Authority

**The server is always right.** When client and server disagree, server wins.

### Sequence Tracking

Highest received seq tracked via `lastKnownSeqRef` (primary) with React state fallback:

```javascript
const lastSeq = Math.max(
  lastKnownSeqRef.current[sessionId] || 0,
  getMaxSeq(session?.messages || []),
  session?.lastLoadedSeq || 0,
);
```

Update `lastKnownSeqRef` via `updateLastKnownSeq(sessionId, seq)` in every event handler.

### Keepalive Sync Check

```javascript
case "keepalive_ack": {
    keepalive.missedCount = 0;
    const maxSeq = msg.data?.max_seq || 0;
    const clientMaxSeq = Math.max(
      lastKnownSeqRef.current[sessionId] || 0,
      getMaxSeq(sessions[sessionId]?.messages || [])
    );
    if (maxSeq > clientMaxSeq + KEEPALIVE_SYNC_TOLERANCE) {
        ws.send(JSON.stringify({ 
          type: "load_events", 
          data: { after_seq: clientMaxSeq } 
        }));
    }
}
```

## Stale Client Detection

When `clientSeq > serverSeq`: full reload, reset tracker AND `lastKnownSeqRef`.

**Cooldown** (`staleRecoveryCooldownRef`, 30s): Prevents feedback loops from React state batching. Checked in keepalive_ack before stale check. Cleared on WebSocket close.

## Three-Tier Deduplication

> Canonical reference: [synchronization.md — Deduplication Strategy](../../docs/devel/websockets/synchronization.md#deduplication-strategy)

1. **Server-side** (`lastSentSeq`): Prevents duplicates during streaming
2. **M1 seq tracker** (`seenSeqsRef`): Skips duplicates during streaming
3. **Client-side merge** (`mergeMessagesWithSync`): Handles reconnect overlap

### Critical: Reset Seq Tracker on Stale Recovery

```javascript
if (isStaleClient) {
    clearSeenSeqs(sessionId);  // MUST reset before processing events
    delete lastKnownSeqRef.current[sessionId];  // Reset reconnection watermark
}
```

Without reset: stale `highestSeq` rejects fresh events as "very old" duplicates.

### events_loaded Handler

```javascript
case "events_loaded": {
    if (isPrepend) {
        messages = [...newMessages, ...session.messages];
    } else if (session.messages.length === 0 || isStaleClient) {
        messages = newMessages;  // Initial or stale: server wins
    } else {
        messages = mergeMessagesWithSync(session.messages, newMessages);
    }
}
```

## Immediate Gap Detection (max_seq)

All streaming messages include `max_seq`. Call `checkAndFillGap` in **all** message handlers to detect missed events immediately, not waiting for keepalive.

## Circuit Breaker: Terminal Session Errors

Three layers to stop reconnecting when session is deleted:

1. **Explicit `session_gone` handler** → `handleSessionGone(sessionId, reason)`
2. **Error handler checks** `isTerminalSessionError(message)` (checks for "session not found")
3. **REST check on reconnect**: Verify `checkSessionExists()` before retrying

## Error Handling Anti-Patterns

- **Timeout**: Clear in `finally` block, not just after success
- **Connection state**: Check `isConnectionHealthy()`, not just `readyState === OPEN`
- **Generic error**: Always check `isTerminalSessionError()` before treating as recoverable
