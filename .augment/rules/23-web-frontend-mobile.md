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

### App-restart Context Load

On app restart with stale seq but no messages in memory: request messages since that seq. If server has 0 new events but has history (`total_count > 0`), auto-request initial batch so conversation isn't empty.

## Stale Client Recovery & Cooldown

Stale state (client seq > server seq) triggers M1 fix: clear seq tracker, reset `lastKnownSeqRef` + localStorage, replace messages. 30s cooldown prevents re-triggering feedback loops from React state batching. See `24-web-frontend-sync.md` for details.

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

## Resilience Events & Session Staleness

| Event          | Purpose                | Staleness                          |
| -------------- | ---------------------- | ---------------------------------- |
| `visibilitychange` | Tab switch, phone wake | If hidden >1h, verify auth first   |
| `online`/`offline` | Network loss/restore   |                                    |
| `freeze`/`resume`  | iOS Safari freeze      |                                    |

## Extended Timeouts for Mobile

Prompt ACK 30s (vs 15s desktop), Keepalive 30s, Reconnect 2s (vs 1s) — account for higher latency and iOS WebSocket suspension.

## macOS App Activation Resync Debounce (mitto-c2p8.3)

macOS fires "App became active" in rapid bursts (multiple sources: `applicationDidBecomeActive`, `NSWorkspaceScreensDidWakeNotification`, `NSWorkspaceDidWakeNotification`). Without debouncing, each burst triggers full staggered reconnect + load_events, causing thundering herd.

**Implementation:**
- Frontend: `APP_ACTIVATE_RESYNC_DEBOUNCE_MS = 15000` (15 seconds)
- Backend: `appActivateDebounce = 2 * time.Second` (Go)
- Pattern: Leading-edge debounce — first activation goes through, subsequent ones within the window are skipped

```javascript
// In app.js: window.mittoAppDidBecomeActive (called by native Swift)
const { debounced, elapsed } = shouldDebounceReconnect(
  appActivateDebounceRef.current,
  "__app_activate__",
  { windowMs: APP_ACTIVATE_RESYNC_DEBOUNCE_MS },
);
if (debounced) {
  console.debug(`[macOS] App became active — skipping redundant resync (${elapsed}ms since last)`);
  return;
}
reconnectAllSessionsStaggered(); // Only on first activation
```

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
