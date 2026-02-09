---
description: WebSocket connection management, message handling, reconnection, promise-based sending, keepalive mechanism, and API URL helpers
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/hooks/index.js"
  - "web/static/utils/api.js"
  - "web/static/utils/csrf.js"
keywords:
  - keepalive
  - websocket
  - reconnect
  - zombie connection
  - prompt_received
  - ACK
---

# WebSocket and Message Handling

## Hooks Directory Structure

| File | Purpose |
|------|---------|
| `hooks/useWebSocket.js` | WebSocket connections, session management, keepalive, message handling |
| `hooks/useSwipeNavigation.js` | Mobile swipe gestures for navigation |
| `hooks/index.js` | Re-exports for clean imports |

## Promise-Based Message Sending with ACK

Messages are sent with delivery confirmation and retry capability:

```
User clicks Send → onSend() returns Promise → Wait for ACK → Resolve/Reject
```

### Send Button States

| State | Button Appearance | Text Area |
|-------|-------------------|-----------|
| Normal | "Send" button enabled | Editable |
| Sending | Spinner + "Sending..." | Disabled |
| Error | "Send" enabled | Editable (text preserved) |
| Streaming | "Stop" button | Disabled |

```javascript
const handleSubmit = async (e) => {
    e.preventDefault();
    if (!hasContent || isSending) return;

    setIsSending(true);
    setSendError(null);

    try {
        await onSend(text, images);  // Returns Promise that resolves on ACK
        setText('');  // Only clear on success
    } catch (err) {
        setSendError(err.message);
        // Text is preserved for retry
    } finally {
        setIsSending(false);
    }
};
```

### Pending Prompt Persistence

For mobile reliability (phone sleep, network loss), prompts are saved to localStorage before sending:

```javascript
export function savePendingPrompt(sessionId, promptId, message, imageIds = []) {
    const pending = getPendingPrompts();
    pending[promptId] = { sessionId, message, imageIds, timestamp: Date.now() };
    localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
}
```

## WebSocket Connection Management

### Preventing Reference Leaks on Close

When a WebSocket closes, only delete its reference if it's still the current one:

```javascript
ws.onclose = () => {
    // Only delete ref if it still points to THIS WebSocket
    if (sessionWsRefs.current[sessionId] === ws) {
        delete sessionWsRefs.current[sessionId];
    }
    // Only schedule reconnect if no newer connection exists
    if (activeSessionIdRef.current === sessionId && !sessionWsRefs.current[sessionId]) {
        // Schedule reconnect...
    }
};
```

### Force Reconnect Pattern

When forcing a reconnection (e.g., on visibility change), delete the ref BEFORE closing:

```javascript
const forceReconnectActiveSession = useCallback(() => {
    const currentSessionId = activeSessionIdRef.current;
    if (!currentSessionId) return;

    // Delete ref FIRST so onclose doesn't schedule another reconnect
    const existingWs = sessionWsRefs.current[currentSessionId];
    if (existingWs) {
        delete sessionWsRefs.current[currentSessionId];
        existingWs.close();
    }

    // Create fresh connection
    connectToSession(currentSessionId);
}, [connectToSession]);
```

## Message Ordering and Deduplication

### Two-Tier Deduplication Strategy

The system uses both server-side and client-side deduplication:

1. **Server-side** (`lastSentSeq` tracking): Prevents duplicates during normal streaming
2. **Client-side** (`mergeMessagesWithSync`): Handles sync after reconnect when `lastSeenSeq` may be stale

### When Client-Side Deduplication is Needed

The `lastSeenSeq` in localStorage is only updated at specific points:
- When `prompt_complete` is received
- When `events_loaded` is received

If a visibility change (phone wake) triggers a reconnect **during streaming** (before `prompt_complete`), the `lastSeenSeq` may be stale. The server will return events that are already displayed in the UI.

### Sequence Number Based Ordering

All streaming events include a sequence number (`seq`) assigned when the event is received from the ACP:
- Assigned immediately when event is received (not at persistence time)
- Included in all WebSocket messages (`agent_message`, `tool_call`, etc.)
- Sorting by `seq` gives correct chronological order

### events_loaded Handler

```javascript
case "events_loaded": {
  if (isPrepend) {
    // Load more (older events) - no dedup needed
    messages = [...newMessages, ...session.messages];
  } else if (session.messages.length === 0) {
    // Initial load - just use the new messages
    messages = newMessages;
  } else {
    // Sync after reconnect - merge with deduplication
    messages = mergeMessagesWithSync(session.messages, newMessages);
  }
}
```

### Deduplication in mergeMessagesWithSync

```javascript
export function mergeMessagesWithSync(existingMessages, newMessages) {
  // Create a map of existing messages by seq for fast lookup
  const existingBySeq = new Map();
  const existingHashes = new Set();
  for (const m of existingMessages) {
    if (m.seq) existingBySeq.set(m.seq, m);
    existingHashes.add(getMessageHash(m));
  }

  // Deduplicate by seq (preferred) or content hash (fallback)
  const filteredNewMessages = newMessages.filter((m) => {
    if (m.seq && existingBySeq.has(m.seq)) return false;
    return !existingHashes.has(getMessageHash(m));
  });

  // Combine and sort by seq for correct ordering
  const allMessages = [...existingMessages, ...filteredNewMessages];
  allMessages.sort((a, b) => {
    if (a.seq && b.seq) return a.seq - b.seq;
    return 0;
  });
  return allMessages;
}
```

## Keepalive Mechanism for Zombie Connection Detection

Mobile browsers can leave WebSocket connections in a "zombie" state - appearing open but actually dead. The keepalive mechanism detects and recovers from this.

### Configuration Constants

```javascript
const KEEPALIVE_INTERVAL_MS = 25000;  // Send keepalive every 25 seconds
const KEEPALIVE_TIMEOUT_MS = 10000;   // Response timeout (unused, tracked by missed count)
const KEEPALIVE_MAX_MISSED = 2;       // Force reconnect after 2 missed keepalives
```

### Keepalive Flow

```
1. WebSocket connects → Start keepalive interval
2. Every 25s: Send keepalive {client_time} → Set pendingKeepalive = true
3. Server responds → keepalive_ack {client_time, server_time}
4. Frontend receives → Reset missedCount, set pendingKeepalive = false

On missed keepalive:
1. Interval fires, pendingKeepalive is still true → missedCount++
2. If missedCount >= 2 → Force close WebSocket → Triggers reconnect
```

### Keepalive State Tracking

```javascript
// Track per-session keepalive state
const keepaliveRef = useRef({});

// Structure: { sessionId: { intervalId, lastAckTime, missedCount, pendingKeepalive } }

// On WebSocket open - start keepalive interval
keepaliveRef.current[sessionId] = {
    intervalId,
    lastAckTime: Date.now(),
    missedCount: 0,
    pendingKeepalive: false,
};

// On keepalive_ack received - connection is healthy
case "keepalive_ack": {
    const keepalive = keepaliveRef.current[sessionId];
    if (keepalive) {
        keepalive.lastAckTime = Date.now();
        keepalive.missedCount = 0;
        keepalive.pendingKeepalive = false;
    }
    break;
}
```

### Connection Health Check

Before sending critical messages, check if the connection is healthy:

```javascript
const isConnectionHealthy = (sessionId) => {
    const keepalive = keepaliveRef.current[sessionId];
    if (!keepalive) return true; // No tracking yet, assume healthy

    const timeSinceLastAck = Date.now() - (keepalive.lastAckTime || 0);
    return timeSinceLastAck < KEEPALIVE_INTERVAL_MS * 2 &&
           (keepalive.missedCount || 0) === 0;
};
```

### Cleanup on WebSocket Close

Always clean up keepalive interval when WebSocket closes:

```javascript
ws.onclose = () => {
    if (keepaliveRef.current[sessionId]?.intervalId) {
        clearInterval(keepaliveRef.current[sessionId].intervalId);
        delete keepaliveRef.current[sessionId];
    }
    // Schedule reconnect...
};
```

## API Prefix Handling

The API prefix is injected by the server into the HTML page:

```javascript
// In utils/api.js
export function getApiPrefix() {
    return window.mittoApiPrefix || '';
}

export function apiUrl(path) {
    return getApiPrefix() + path;  // e.g., "/mitto" + "/api/sessions"
}

export function wsUrl(path) {
    const prefix = getApiPrefix();
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${protocol}//${window.location.host}${prefix}${path}`;
}
```

## Sequence Number Contract

See [docs/devel/websocket-messaging.md](../docs/devel/websocket-messaging.md#sequence-number-contract) for the complete formal specification.

### Frontend Responsibilities

1. **Update lastSeenSeq immediately during streaming** (H1 fix):
   ```javascript
   // Called for agent_message, agent_thought, tool_call, user_prompt
   updateLastSeenSeqIfHigher(sessionId, msg.data.seq);
   ```

2. **Client-side deduplication by seq** (M1 fix):
   ```javascript
   // Check before processing
   if (isSeqDuplicate(sessionId, msgSeq, lastMessageSeq)) {
     return; // Skip duplicate
   }
   // Mark as seen after processing
   markSeqSeen(sessionId, msgSeq);
   ```

3. **Allow same-seq for coalescing**:
   ```javascript
   // Allow same seq as last message (streaming continuation)
   if (lastMessageSeq && seq === lastMessageSeq) return false;
   ```

### Exponential Backoff (M2 fix)

WebSocket reconnection uses exponential backoff with jitter:

```javascript
const RECONNECT_BASE_DELAY_MS = 1000;   // 1 second initial
const RECONNECT_MAX_DELAY_MS = 30000;   // 30 second max
const RECONNECT_JITTER_FACTOR = 0.3;    // 30% random jitter

function calculateReconnectDelay(attempt) {
  const exponentialDelay = Math.min(
    RECONNECT_BASE_DELAY_MS * Math.pow(2, attempt),
    RECONNECT_MAX_DELAY_MS
  );
  const jitter = exponentialDelay * RECONNECT_JITTER_FACTOR * Math.random();
  return Math.floor(exponentialDelay + jitter);
}
```

