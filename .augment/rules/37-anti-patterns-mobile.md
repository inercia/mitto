---
description: Mobile browser and WKWebView anti-patterns, localStorage issues, sync state
globs:
  - "web/static/hooks/useWebSocket.js"
  - "cmd/mitto-app/**/*"
keywords:
  - mobile anti-pattern
  - WKWebView
  - localStorage stale
  - phone wake
  - zombie connection
  - lastSeenSeq
  - sync state
---

# Mobile and WKWebView Anti-Patterns

## localStorage Sync State

### ❌ Don't: Store Sync State in localStorage

```javascript
// BAD: Storing lastSeenSeq in localStorage
localStorage.setItem(`mitto_last_seen_seq_${sessionId}`, lastSeq);
// Later...
const lastSeq = localStorage.getItem(`mitto_last_seen_seq_${sessionId}`);
// This value can be stale in WKWebView!
```

**Problem**: WKWebView's localStorage can desynchronize from actual data store, causing:
- Stale seq values that don't match displayed messages
- Sync requests that return 0 events when messages exist
- Messages appearing to be "lost" until page reload

### ✅ Do: Calculate Sync State from Application State

```javascript
// GOOD: Calculate lastSeenSeq dynamically from messages in state
import { getMaxSeq } from "../lib.js";

ws.onopen = () => {
  // Calculate from actual messages being displayed
  const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
  const lastSeq = getMaxSeq(sessionMessages);

  if (lastSeq > 0) {
    ws.send(JSON.stringify({
      type: "load_events",
      data: { after_seq: lastSeq },
    }));
  } else {
    // Initial load
    ws.send(JSON.stringify({
      type: "load_events",
      data: { limit: INITIAL_EVENTS_LIMIT },
    }));
  }
};
```

**Benefits**:
- Always reflects actual displayed messages
- No stale localStorage issues
- Works correctly in WKWebView
- Simpler code (no localStorage read/write)

## Zombie Connection Detection

### ❌ Don't: Trust WebSocket readyState

```javascript
// BAD: Phone wakes up, readyState is still OPEN
if (ws.readyState === WebSocket.OPEN) {
  // Connection may be dead but not yet detected
  ws.send(message);
}
```

### ✅ Do: Use Keepalive with Timeout

```javascript
// GOOD: Track keepalive responses
const keepaliveInterval = setInterval(() => {
  if (ws.readyState === WebSocket.OPEN) {
    // Track pending keepalive
    keepaliveRef.current[sessionId] = {
      lastSent: Date.now(),
      pending: true
    };
    ws.send(JSON.stringify({ type: "keepalive" }));
  }
}, 30000);

// In message handler
case "keepalive_ack":
  const keepalive = keepaliveRef.current[sessionId];
  if (keepalive) {
    keepalive.pending = false;
    keepalive.missedCount = 0;
  }
  break;
```

## Visibility Change Handling

### ❌ Don't: Ignore Visibility Changes

```javascript
// BAD: No reconnection on wake
// User opens phone, sees stale data
```

### ✅ Do: Check Connection Health on Wake

```javascript
// GOOD: Reconnect and sync on visibility change
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    // Check all active connections
    Object.keys(sessionWsRefs.current).forEach((sessionId) => {
      const ws = sessionWsRefs.current[sessionId];
      const keepalive = keepaliveRef.current[sessionId];
      
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        // Reconnect
        connectToSession(sessionId);
      } else if (keepalive?.pending) {
        // Keepalive was pending before sleep - connection likely dead
        ws.close();
        connectToSession(sessionId);
      } else {
        // Connection seems healthy - request sync
        ws.send(JSON.stringify({
          type: "load_events",
          data: { after_seq: getMaxSeq(sessions[sessionId]?.messages || []) }
        }));
      }
    });
  }
});
```

## Extended Timeouts

### Pattern: Mobile Needs Longer Timeouts

| Timeout | Desktop | Mobile | Reason |
|---------|---------|--------|--------|
| Prompt ACK | 15s | 30s | Higher latency, network variability |
| Keepalive | 15s | 30s | iOS may suspend WebSocket |
| Reconnect delay | 1s | 2s | Give network time to stabilize |

```javascript
const isMobile = /iPhone|iPad|iPod|Android/i.test(navigator.userAgent);
const ACK_TIMEOUT = isMobile ? 30000 : 15000;
```

## Agent Response as Implicit ACK

### Pattern: Treat Agent Response as ACK

```javascript
// If agent starts responding, prompt was received
case "agent_message":
case "agent_thought":
  // Resolve any pending prompt promises
  resolvePendingSend(sessionId);
  // Continue processing message...
  break;
```

This handles cases where:
1. The `prompt_received` ACK was lost due to a network hiccup
2. The prompt was received but ACK timing was disrupted
3. Mobile network transitions caused ACK delivery issues

## Lessons Learned

### 1. Calculate Sync State from Application State

For sync state (like `lastSeenSeq`), calculate dynamically from application state:
- localStorage can become stale, especially in WKWebView
- Application state (messages in React/Preact state) is always current
- Use `getMaxSeq(messages)` instead of `localStorage.getItem('lastSeenSeq')`

### 2. Mobile/WKWebView Requires Extra Validation

Don't trust browser state in mobile contexts:
- Connections can be "zombie" (OPEN but dead)
- localStorage can be stale in WKWebView
- Always validate with server on reconnect
- Use keepalive to detect unhealthy connections

## Related Documentation

- `23-web-frontend-mobile.md` - Mobile handling patterns
- `22-web-frontend-websocket.md` - WebSocket patterns
- [WebSocket Protocol](../../docs/devel/websockets/) - Protocol spec

