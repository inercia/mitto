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
  - delivery verification
  - last_user_prompt_id
---

# WebSocket and Message Handling

> **📖 Full Protocol Documentation**: See [docs/devel/websocket-messaging.md](../../docs/devel/websocket-messaging.md) for complete WebSocket protocol specification, message formats, sequence number contract, and communication flows.

This file covers **frontend implementation patterns** for WebSocket handling. For protocol details, message formats, and architecture, refer to the main documentation.

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

### Delivery Verification on Timeout

When ACK timeout occurs, the frontend verifies delivery by reconnecting and checking `last_user_prompt_id`:

```javascript
// On timeout, instead of immediately rejecting:
// 1. Force close zombie WebSocket
// 2. Wait for fresh connection
// 3. Check if last_user_prompt_id matches our pending promptId
// 4. If match → resolve success (message was delivered!)
// 5. If no match → reject with error

const confirmed = lastConfirmedPromptRef.current[sessionId];
if (confirmed && confirmed.promptId === promptId) {
    resolve({ success: true, promptId, verifiedOnReconnect: true });
} else {
    reject(new Error("Message delivery could not be confirmed"));
}
```

> **📖 See**: [Send Timeout with Delivery Verification](../../docs/devel/websocket-messaging.md#corner-case-send-timeout-with-delivery-verification) for the complete flow diagram.

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

> **📖 See**: [27-web-frontend-sync.md](./27-web-frontend-sync.md) for detailed sequence sync, stale client detection, and deduplication patterns.

### Key Points

- **Dynamic sequence calculation**: `lastSeenSeq` is calculated from messages in state, NOT localStorage
- **Two-tier deduplication**: Server-side (`lastSentSeq`) + client-side (`mergeMessagesWithSync`)
- **Server authority**: When client and server disagree, server always wins

### Sequence Number Based Ordering

All streaming events include a sequence number (`seq`) assigned when the event is received from the ACP:
- Assigned immediately when event is received (not at persistence time)
- Included in all WebSocket messages (`agent_message`, `tool_call`, etc.)
- Sorting by `seq` gives correct chronological order

## Keepalive Mechanism with Sequence Sync

Mobile browsers can leave WebSocket connections in a "zombie" state - appearing open but actually dead. The keepalive mechanism detects and recovers from this, and also detects out-of-sync situations.

### Dual Purpose

1. **Zombie Connection Detection**: Force reconnect if keepalives aren't acknowledged
2. **Sequence Sync**: Compare client's `last_seen_seq` with server's `server_max_seq` to detect missed messages

### Configuration Constants

```javascript
// Native macOS app uses shorter interval (5s) since it's local with no network latency
// Browser uses longer interval (10s) to reduce network overhead
const KEEPALIVE_INTERVAL_NATIVE_MS = 5000;   // Native macOS app: every 5 seconds
const KEEPALIVE_INTERVAL_BROWSER_MS = 10000; // Browser: every 10 seconds
const KEEPALIVE_TIMEOUT_MS = 10000;          // Response timeout (unused, tracked by missed count)
const KEEPALIVE_MAX_MISSED = 2;              // Force reconnect after 2 missed keepalives

// Dynamic interval based on runtime environment
function getKeepaliveInterval() {
  return isNativeApp() ? KEEPALIVE_INTERVAL_NATIVE_MS : KEEPALIVE_INTERVAL_BROWSER_MS;
}
```

### Message Format

```javascript
// Client → Server (keepalive)
{
  type: "keepalive",
  data: {
    client_time: Date.now(),
    last_seen_seq: getMaxSeq(sessionMessages)  // Calculated from messages in state
  }
}

// Server → Client (keepalive_ack)
{
  type: "keepalive_ack",
  data: {
    client_time: ...,      // Echo back for RTT calculation
    server_time: ...,      // Server's current time (Unix ms)
    server_max_seq: 45,    // Highest seq server has for this session
    is_prompting: true,    // Whether agent is currently responding
    is_running: true,      // Whether background session is active
    queue_length: 3,       // Number of messages in queue
    status: "active"       // Session status (active, completed, error)
  }
}
```

### Piggybacked State Sync

The `keepalive_ack` includes additional session state for multi-tab sync and mobile wake recovery:

| Field | Purpose |
|-------|---------|
| `is_prompting` | Sync streaming state with server |
| `is_running` | Detect if background session disconnected |
| `queue_length` | Sync queue badge across tabs |
| `status` | Detect session completion while in background |

### Keepalive Flow

```
1. WebSocket connects → Start keepalive interval (5s native, 10s browser)
2. At each interval: Send keepalive {client_time, last_seen_seq} → Set pendingKeepalive = true
3. Server responds → keepalive_ack {client_time, server_time, server_max_seq, ...}
4. Frontend receives:
   - Reset missedCount, set pendingKeepalive = false
   - Sync isStreaming, queue_length, status, is_running if changed
   - If server_max_seq > last_seen_seq → Request load_events {after_seq}

On missed keepalive:
1. Interval fires, pendingKeepalive is still true → missedCount++
2. If missedCount >= 2 → Force close WebSocket → Triggers reconnect

Zombie detection timing:
- Native macOS app: detected after 10s (2 × 5s intervals)
- Browser: detected after 20s (2 × 10s intervals)
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

// On keepalive_ack received - connection is healthy + check for sync
case "keepalive_ack": {
    const keepalive = keepaliveRef.current[sessionId];
    if (keepalive) {
        keepalive.lastAckTime = Date.now();
        keepalive.missedCount = 0;
        keepalive.pendingKeepalive = false;
    }

    // Check if we're behind the server
    const serverMaxSeq = msg.data?.server_max_seq || 0;
    if (serverMaxSeq > 0) {
        const clientMaxSeq = getMaxSeq(sessionsRef.current[sessionId]?.messages || []);
        if (serverMaxSeq > clientMaxSeq) {
            // Request missing events
            ws.send(JSON.stringify({
                type: "load_events",
                data: { after_seq: clientMaxSeq }
            }));
        }
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
    return timeSinceLastAck < getKeepaliveInterval() * 2 &&
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

## Server Authority and Stale Client Detection

> **📖 See**: [27-web-frontend-sync.md](./27-web-frontend-sync.md) for detailed stale client detection, recovery flows, and keepalive-based sync patterns.

**Key principle**: The server is the single source of truth. When client and server disagree, server always wins.

## Sequence Number Contract

See [docs/devel/websocket-messaging.md](../docs/devel/websocket-messaging.md#sequence-number-contract) for the complete formal specification.

### Frontend Responsibilities

1. **Calculate lastSeenSeq dynamically from messages**:
   ```javascript
   // On WebSocket connect, calculate from messages in state
   const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
   const lastSeq = getMaxSeq(sessionMessages);
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

3. **Reset seq tracker on stale client recovery** (M1 fix):
   ```javascript
   // In events_loaded handler - CRITICAL!
   if (isStaleClient) {
     clearSeenSeqs(sessionId);  // Reset before processing fresh events
   }
   ```
   > **📖 See**: [27-web-frontend-sync.md](./27-web-frontend-sync.md#critical-reset-seq-tracker-on-stale-client-recovery) for why this is critical.

4. **Allow same-seq for coalescing**:
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

