---
description: WebSocket connection, keepalive, delivery verification, reconnection, and reference management
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/utils/api.js"
  - "web/static/utils/csrf.js"
  - "internal/web/session_ws*.go"
  - "internal/web/ws_*.go"
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
  - RECONNECT_BASE_DELAY_MS
  - KEEPALIVE_INTERVAL_NATIVE_MS
---

# WebSocket, Connection Management, and Keepalive

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

On ACK timeout: force close zombie WS → wait for fresh connection → check `last_user_prompt_id` matches pending promptId → resolve if match, reject if not.

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

### Reconnect Debounce

Multiple reconnect triggers (visibility change, keepalive miss, app activate) can fire within milliseconds of each other. Debounce collapses these into a single reconnect:

```javascript
const RECONNECT_DEBOUNCE_MS = 3000; // General debounce window (3s)
const APP_ACTIVATE_RESYNC_DEBOUNCE_MS = 15000; // macOS app-activate (15s)

// Leading-edge debounce: first call goes through, subsequent within window are suppressed
const { debounced, elapsed } = shouldDebounceReconnect(tracker, sessionId, {
  windowMs: RECONNECT_DEBOUNCE_MS,
});
if (debounced) return; // Skip this reconnect
```

## Keepalive Mechanism

Dual purpose: zombie connection detection + sequence sync (see `24-web-frontend-sync.md` for sync details).

### Configuration

```javascript
const KEEPALIVE_INTERVAL_NATIVE_MS = 5000;   // macOS app
const KEEPALIVE_INTERVAL_BROWSER_MS = 10000; // Browser
const KEEPALIVE_MAX_MISSED = 2;               // Reconnect after 2 missed
```

### Flow

```
Connect → Start keepalive interval
Each interval: Send {type: "keepalive", data: {client_time, last_seen_seq}}
Server: keepalive_ack {server_max_seq, is_prompting, queue_length, status}
  → Reset missedCount
  → Sync check (see 24-web-frontend-sync.md)
Missed: missedCount++ → If >= 2 → Force close → Reconnect
```

## `connected` Handler: Use Nullish Coalescing

The `connected` message is sent on every (re)connect but may omit fields unavailable at send time. Using `||` overwrites existing values, causing UI flicker. Always use `??`:

```javascript
// BAD: wipes existing config_options
config_options: msg.data.config_options || []

// GOOD: preserve on reconnect
config_options: msg.data.config_options ?? session.info?.config_options ?? []
```

## API Prefix Handling

```javascript
export function apiUrl(path) { return getApiPrefix() + path; }
export function wsUrl(path) {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    return `${protocol}//${location.host}${getApiPrefix()}${path}`;
}
```
