---
description: Mobile browser handling, visibility change, wake resync, session staleness, localStorage anti-patterns, zombie detection, extended timeouts, iOS Safari debugging
globs:
  - "web/static/app.js"
  - "web/static/hooks/useSwipeNavigation.js"
  - "web/static/utils/storage.js"
  - "web/static/hooks/useWebSocket.js"
  - "cmd/mitto-app/**/*"
keywords:
  - mobile browser
  - visibility change
  - visibilitychange
  - wake resync
  - phone sleep
  - zombie connection detection
  - iOS Safari
  - Android Chrome
  - WKWebView localStorage
  - mobile anti-pattern
  - localStorage stale
  - lastSeenSeq
---

# Mobile Wake Resync and Browser Handling

Mobile browsers suspend WebSocket connections when the device sleeps. The frontend must resync when visible again.

## The Problem

Phone sleeps -> WebSocket terminated -> Agent continues server-side -> Phone wakes -> UI shows stale messages. Connections may also enter "zombie state" (appearing open but dead).

## Critical: Calculate Seq from State, Not localStorage

```javascript
// BAD: localStorage can be stale in WKWebView
const lastSeq = localStorage.getItem(`mitto_last_seen_seq_${sessionId}`);

// GOOD: Calculate dynamically from messages in state
const lastSeq = getMaxSeq(sessionsRef.current[sessionId]?.messages || []);
```

## Force Reconnect on Visibility Change

```javascript
useEffect(() => {
    const handler = () => {
        if (document.visibilityState === "visible") {
            fetchStoredSessions();
            setTimeout(() => forceReconnectActiveSession(), 300);
        }
    };
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
}, [fetchStoredSessions, forceReconnectActiveSession]);
```

## Additional Resilience Events

| Event                         | Purpose                | Response Time |
| ----------------------------- | ---------------------- | ------------- |
| `visibilitychange`            | Tab switch, phone wake | ~300ms        |
| `online`/`offline`            | Network loss/restore   | ~500ms        |
| `navigator.connection.change` | WiFi <-> Cellular      | ~500ms        |
| `freeze`/`resume`             | iOS Safari page freeze | ~300ms        |

## Session Staleness Detection

When phone locked overnight, auth session may have expired:

```javascript
const STALE_THRESHOLD_MS = 60 * 60 * 1000; // 1 hour
if (hiddenDuration > STALE_THRESHOLD_MS) {
    const { authenticated } = await checkAuthWithRetry();
    if (!authenticated) return; // Redirect to login
}
```

## Extended Timeouts for Mobile

| Timeout      | Desktop | Mobile | Reason                    |
| ------------ | ------- | ------ | ------------------------- |
| Prompt ACK   | 15s     | 30s    | Higher latency            |
| Keepalive    | 15s     | 30s    | iOS may suspend WebSocket |
| Reconnect    | 1s      | 2s     | Network stabilization     |

## Agent Response as Implicit ACK

```javascript
case "agent_message":
case "agent_thought":
    resolvePendingSend(sessionId);  // ACK was lost but prompt was received
    break;
```

## Key Storage Functions

| Function                         | Purpose                                           |
| -------------------------------- | ------------------------------------------------- |
| `getLastActiveSessionId()`       | Last active session for page reload recovery      |
| `getQueueDropdownHeight()`       | Saved queue dropdown height (default: 256px)      |
| `setQueueDropdownHeight(height)` | Persist height (clamped to 100-500px)             |
| `getQueueHeightConstraints()`    | Get min/max/default constraints                   |

## Mobile Browser Debugging

### iOS Safari Remote Debugging

1. iPhone: Settings -> Safari -> Advanced -> Web Inspector
2. Connect iPhone to Mac via USB
3. Mac: Safari -> Develop -> Your iPhone -> The page

### Common Issues

| Issue            | Symptom                       | Solution                                   |
| ---------------- | ----------------------------- | ------------------------------------------ |
| Cached assets    | Works in simulator, not phone | Clear Safari cache or Private Browsing      |
| Zombie WebSocket | "Connecting" after wake       | Force reconnect on visibility change        |
| Wrong API path   | Missing `/mitto` prefix       | Clear cache, check `window.mittoApiPrefix`  |
