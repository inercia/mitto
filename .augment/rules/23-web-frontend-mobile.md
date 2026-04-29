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
  - lastKnownSeqRef
  - stale recovery cooldown
  - staleRecoveryCooldownRef
---

# Mobile Wake Resync and Browser Handling

Mobile browsers suspend WebSocket connections when the device sleeps. The frontend must resync when visible again.

## The Problem

Phone sleeps -> WebSocket terminated -> Agent continues server-side -> Phone wakes -> UI shows stale messages. Connections may also enter "zombie state" (appearing open but dead).

## Seq Watermark: Three-Tier Priority

The highest seq known for a session is the maximum of three sources (priority order):

1. **`lastKnownSeqRef`** — updated on every received event, survives WS reconnects within the same page session
2. **`localStorage`** (`getLastSeenSeq` / `setLastSeenSeq`) — written by `updateLastKnownSeq`, survives app restarts and WKWebView page reloads
3. **React state** (`messages` / `lastLoadedSeq`) — fallback

```javascript
// In ws.onopen — three-tier watermark resolution:
const refSeq = lastKnownSeqRef.current[sessionId] || 0;
// Restore from localStorage on app restart (refSeq is 0 only then)
const persistedSeq = refSeq === 0 ? getLastSeenSeq(sessionId) : 0;
if (persistedSeq > 0) {
  lastKnownSeqRef.current[sessionId] = persistedSeq; // populate ref
}
const stateSeq = Math.max(
  getMaxSeq(session?.messages || []),
  session?.lastLoadedSeq || 0,
);
const lastSeq = Math.max(refSeq, persistedSeq, stateSeq);
```

### App-restart context-load fallback

When `lastSeq > 0` but no messages are in memory (app restart / WKWebView reload),
the `ws.onopen` handler sends `{ after_seq: lastSeq }` and sets
`needsContextLoadRef.current[sessionId] = true`.

If the server returns **0 new events** (nothing happened while app was closed) and
the session has history (`total_count > 0`), the `events_loaded` handler
automatically issues a secondary `{ limit: 50 }` request so the conversation is
not shown as empty:

```javascript
// In events_loaded handler — context-load fallback:
if (
  needsContextLoadRef.current[sessionId] &&
  !isPrepend &&
  newMessages.length === 0 &&
  (currentSession?.messages?.length || 0) === 0 &&
  totalCount > 0
) {
  delete needsContextLoadRef.current[sessionId];
  ws.send(JSON.stringify({ type: "load_events", data: { limit: INITIAL_EVENTS_LIMIT } }));
}
```

`needsContextLoadRef` is cleared by `clearPendingSync` on WebSocket close so stale flags
never carry over to the next connection.

> See [Sequence Numbers — Frontend Responsibilities](../../docs/devel/websockets/sequence-numbers.md#frontend-responsibilities) for the full pattern.

## Stale Client Recovery & Cooldown

When the client has stale state (e.g., localStorage watermark of seq 735 but server only has 730 after a restart), the `events_loaded` handler detects `clientLastSeq > serverMaxSeq` and runs the **M1 fix**: clears the seq tracker, resets `lastKnownSeqRef` and localStorage, and replaces all messages with fresh data from the server.

**Cooldown**: After stale recovery, a 30-second per-session cooldown (`staleRecoveryCooldownRef`) prevents the keepalive handler from re-triggering stale detection. Without this, React's async state batching can leave `getMaxSeq(sessionMessages)` returning the old stale value when the next keepalive fires (5 seconds later), creating a feedback loop of repeated stale recoveries. The cooldown is cleared on WebSocket close so fresh connections always get an unguarded stale check.

> **See also**: [Sequence Numbers — M1 Fix](../../docs/devel/websockets/sequence-numbers.md#m1-client-side-deduplication) for the full reset logic, and [WebSocket Patterns — Stale Recovery Cooldown](../../.augment/rules/22-web-frontend-websocket.md#stale-recovery-cooldown) for the implementation pattern.

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
