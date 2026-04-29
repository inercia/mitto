---
description: WebSocket connection, keepalive, sequence sync, deduplication, stale client detection, reconnection, delivery verification, and WebSocket anti-patterns
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/hooks/index.js"
  - "web/static/utils/api.js"
  - "web/static/utils/csrf.js"
  - "web/static/lib.js"
  - "internal/web/session_ws*.go"
  - "internal/web/ws_*.go"
  - "internal/web/events_ws*.go"
keywords:
  - websocket connection
  - websocket reconnect
  - keepalive
  - keepalive_ack
  - prompt_received
  - ACK timeout
  - delivery verification
  - last_user_prompt_id
  - connectToSession
  - forceReconnectActiveSession
  - stale client detection
  - sequence sync
  - message deduplication
  - server authority
  - lastSeenSeq
  - lastKnownSeqRef
  - mergeMessagesWithSync
  - getMaxSeq
  - seenSeqsRef
  - clearSeenSeqs
  - isSeqDuplicate
  - M1 fix
  - isStaleClientState
  - staleRecoveryCooldownRef
  - STALE_RECOVERY_COOLDOWN_MS
  - lastSentSeq
  - race condition
  - zombie connection
  - session_gone
  - circuit breaker
  - isTerminalSessionError
  - handleSessionGone
---

# WebSocket, Sync, and Deduplication

> **Full Protocol**: See [docs/devel/websockets/](../../docs/devel/websockets/) for spec, message formats, and communication flows.

## Promise-Based Message Sending with ACK

```javascript
const handleSubmit = async (e) => {
  e.preventDefault();
  setIsSending(true);
  try {
    await onSend(text, images); // Returns Promise that resolves on ACK
    setText("");                // Only clear on success
  } catch (err) {
    setSendError(err.message); // Text preserved for retry
  } finally {
    setIsSending(false);
  }
};
```

### Delivery Verification on Timeout

On ACK timeout: force close zombie WS -> wait for fresh connection -> check `last_user_prompt_id` matches pending promptId -> resolve if match, reject if not.

## Connection Management

### Preventing Reference Leaks

```javascript
ws.onclose = () => {
  if (sessionWsRefs.current[sessionId] === ws) {
    delete sessionWsRefs.current[sessionId];  // Only if still current
  }
};
```

### Force Reconnect Pattern

Delete ref BEFORE closing to prevent onclose from scheduling another reconnect:

```javascript
const forceReconnectActiveSession = useCallback(() => {
  const existingWs = sessionWsRefs.current[currentSessionId];
  if (existingWs) {
    delete sessionWsRefs.current[currentSessionId];  // Delete FIRST
    existingWs.close();
  }
  connectToSession(currentSessionId);
}, [connectToSession]);
```

### Exponential Backoff

```javascript
const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
const RECONNECT_JITTER_FACTOR = 0.3;

function calculateReconnectDelay(attempt) {
  const delay = Math.min(RECONNECT_BASE_DELAY_MS * Math.pow(2, attempt), RECONNECT_MAX_DELAY_MS);
  return Math.floor(delay + delay * RECONNECT_JITTER_FACTOR * Math.random());
}
```

## Keepalive Mechanism

Dual purpose: zombie connection detection + sequence sync.

### Configuration

```javascript
const KEEPALIVE_INTERVAL_NATIVE_MS = 5000;   // macOS app (local)
const KEEPALIVE_INTERVAL_BROWSER_MS = 10000;  // Browser (remote)
const KEEPALIVE_MAX_MISSED = 2;               // Force reconnect after 2 missed
```

### Flow

```
WebSocket connects -> Start keepalive interval
Each interval: Send {type: "keepalive", data: {client_time, last_seen_seq}}
Server responds: keepalive_ack {server_max_seq, is_prompting, is_running, queue_length, status}
  -> Reset missedCount, sync state
  -> If server_max_seq > client + tolerance -> Request load_events

Missed keepalive: missedCount++ -> If >= 2 -> Force close -> Reconnect
```

### keepalive_ack Handler

```javascript
case "keepalive_ack": {
    keepalive.missedCount = 0;
    keepalive.pendingKeepalive = false;

    const maxSeq = msg.data?.max_seq || 0;
    // Use lastKnownSeqRef (primary) with React state (fallback), same pattern as reconnection
    const refSeq = lastKnownSeqRef.current[sessionId] || 0;
    const stateSeq = getMaxSeq(sessions[sessionId]?.messages || []);
    const clientMaxSeq = Math.max(refSeq, stateSeq);
    if (maxSeq > clientMaxSeq + KEEPALIVE_SYNC_TOLERANCE) {
        ws.send(JSON.stringify({ type: "load_events", data: { after_seq: clientMaxSeq } }));
    }
}
```

## Server Authority (Sequence Sync)

**The server is always right.** When client and server disagree, server wins.

### Sequence Tracking for Reconnection

The highest received seq is tracked via `lastKnownSeqRef` (primary) with React state as fallback:

```javascript
// Primary: dedicated ref, updated on every event, survives reconnections
const refSeq = lastKnownSeqRef.current[sessionId] || 0;
// Fallback: React state (may be empty during reconnection)
const stateSeq = Math.max(
  getMaxSeq(session?.messages || []),
  session?.lastLoadedSeq || 0,
);
const lastSeq = Math.max(refSeq, stateSeq);
```

Update `lastKnownSeqRef` via `updateLastKnownSeq(sessionId, seq)` in every event handler (`agent_message`, `tool_call`, `user_prompt`, `agent_thought`, `tool_update`, `prompt_complete`, `events_loaded`).

### Stale Client Detection

```javascript
const isStaleClient = clientLastSeq > 0 && serverLastSeq > 0 && clientLastSeq > serverLastSeq;
```

When detected: full reload (discard client messages, use server data), reset seq tracker AND `lastKnownSeqRef`.

| Scenario                        | Client Action                     |
| ------------------------------- | --------------------------------- |
| `clientSeq > serverSeq`         | Client stale -> full reload       |
| `clientSeq < serverSeq - tol.`  | Client behind -> request missing  |
| `clientSeq ~ serverSeq`         | In sync -> no action              |

### Stale Recovery Cooldown

After stale recovery completes, a per-session cooldown (`staleRecoveryCooldownRef`) prevents re-triggering for `STALE_RECOVERY_COOLDOWN_MS` (30 seconds). This prevents a feedback loop where React state batching and the auto-load `setTimeout` leave `getMaxSeq(sessionMessages)` returning stale values when the next keepalive fires:

```javascript
// Set on stale recovery:
staleRecoveryCooldownRef.current[sessionId] = Date.now();

// Checked in keepalive_ack before triggering stale detection:
const lastRecovery = staleRecoveryCooldownRef.current[sessionId];
if (lastRecovery && (Date.now() - lastRecovery) < STALE_RECOVERY_COOLDOWN_MS) {
  break; // Skip — within cooldown window
}
```

Cleared on WebSocket close so fresh connections get a clean stale check.

## Three-Tier Deduplication

> Canonical reference: [synchronization.md — Deduplication Strategy](../../docs/devel/websockets/synchronization.md#deduplication-strategy)

1. **Server-side** (`lastSentSeq`): Prevents duplicates during streaming
2. **M1 seq tracker** (`seenSeqsRef`): Skips duplicates during streaming
3. **Client-side merge** (`mergeMessagesWithSync`): Handles reconnect overlap

### Critical: Reset Seq Tracker on Stale Recovery

```javascript
if (isStaleClient) {
    clearSeenSeqs(sessionId);  // MUST reset before processing events
    delete lastKnownSeqRef.current[sessionId];  // Reset reconnection watermark too
}
```

Without reset: tracker's `highestSeq` from stale state causes fresh events to be rejected as "very old" duplicates, and the reconnection watermark would request non-existent events.

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

All streaming messages include `max_seq`. Call `checkAndFillGap` in all message handlers to detect missed events without waiting for keepalive.

## Timeout Anti-Pattern

```javascript
// BAD: Timeout fires even on synchronous error
try { await sendPrompt(message); clearTimeout(timeoutId); } catch (err) { ... }

// GOOD: Clear in finally block
try { await sendPrompt(message); } catch (err) { ... }
finally { clearTimeout(timeoutId); }
```

## Connection State Anti-Pattern

```javascript
// BAD: readyState OPEN but connection is zombie
if (ws.readyState === WebSocket.OPEN) ws.send(message);

// GOOD: Also check keepalive health
if (ws.readyState === WebSocket.OPEN && isConnectionHealthy(sessionId)) ws.send(message);
```

## HTML Coalescing Anti-Pattern

```javascript
// BAD: Blindly append without dedup check
const newHtml = (last.html || "") + msg.data.html;

// GOOD: Check for duplicate content
if (!existingHtml.endsWith(incomingHtml)) {
    const newHtml = existingHtml + incomingHtml;
}
```

## Circuit Breaker: Terminal Session Errors

When a session is deleted, the client must stop reconnecting immediately. Three layers of defense:

### 1. Explicit `session_gone` Handler

```javascript
case "session_gone": {
    console.warn(`[CircuitBreaker] Server sent session_gone for ${sessionId}`);
    handleSessionGone(sessionId, msg.data?.reason || "session_gone from server");
    break;
}
```

### 2. Defense-in-Depth in `error` Handler

For backward compatibility with older servers that send generic `error` instead of `session_gone`:

```javascript
case "error": {
    const errorMessage = msg.data?.message || "";
    if (isTerminalSessionError(errorMessage)) {
        handleSessionGone(sessionId, errorMessage);
        break;
    }
    // ... existing error handling ...
}
```

The `isTerminalSessionError()` utility (in `utils/websocket.js`) checks for "session not found" in the error message.

### 3. REST Existence Check on Reconnect

In `ws.onclose`, check session existence before reconnecting — not just when `!ws._wasOpen` but also after any failed attempt:

```javascript
const attempt = sessionReconnectAttemptsRef.current[sessionId] || 0;
if (!ws._wasOpen || attempt >= 1) {
    const { exists } = await checkSessionExists(sessionId, authFetch, apiUrl);
    if (!exists) {
        handleSessionGone(sessionId, "session not found (verified via REST)");
        return;
    }
}
```

### Anti-Pattern: Generic Error as Terminal

```javascript
// BAD: Treats ALL errors as non-terminal, retries indefinitely
case "error":
    appendError(msg.data.message);
    break;

// GOOD: Check for terminal session errors first
case "error": {
    if (isTerminalSessionError(msg.data?.message)) {
        handleSessionGone(sessionId, msg.data.message);
        break;
    }
    appendError(msg.data.message);
    break;
}
```

## API Prefix Handling

```javascript
export function apiUrl(path) { return getApiPrefix() + path; }
export function wsUrl(path) {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    return `${protocol}//${location.host}${getApiPrefix()}${path}`;
}
```
