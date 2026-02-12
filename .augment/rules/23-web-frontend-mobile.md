---
description: Mobile browser handling, visibility change, wake resync, session staleness, localStorage, zombie connection detection, and iOS Safari debugging
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/hooks/useSwipeNavigation.js"
  - "web/static/utils/storage.js"
keywords:
  - mobile
  - visibility change
  - wake
  - sleep
  - zombie connection
  - keepalive
  - iOS Safari
  - Android Chrome
---

# Mobile Wake Resync and Browser Handling

Mobile browsers (iOS Safari, Android Chrome) suspend WebSocket connections when the device sleeps. The frontend must resync when the app becomes visible again.

## The Problem

1. User opens Mitto on phone, views a conversation
2. Phone goes to sleep (screen off)
3. WebSocket connection is terminated by the browser
4. Agent continues processing in the background (server-side)
5. User wakes phone - UI shows stale messages

**Additionally**: Connections can enter "zombie state" - appearing open but actually dead.

## Two-Part Solution

### 1. Keepalive for Zombie Connection Detection

See [22-web-frontend-websocket.md](./22-web-frontend-websocket.md) for full keepalive mechanism.

Mobile browsers can leave WebSockets in "zombie" state - appearing open but dead. The keepalive mechanism detects this:

```javascript
const KEEPALIVE_INTERVAL_MS = 25000;  // Every 25 seconds
const KEEPALIVE_MAX_MISSED = 2;       // Force reconnect after 2 missed

// If keepalive_ack not received, increment missedCount
// If missedCount >= 2, force close WebSocket → triggers reconnect
```

### 2. Dynamic Sequence Number Calculation

**Important**: The `lastSeenSeq` is calculated dynamically from messages in state, NOT stored in localStorage. This avoids stale localStorage issues, especially in WKWebView.

```javascript
import { getMaxSeq } from '../lib.js';

// Calculate lastSeenSeq from messages in state (not localStorage)
const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
const lastSeq = getMaxSeq(sessionMessages);
```

## Sync on WebSocket Reconnect

When per-session WebSocket connects, request missed events using `load_events`:

```javascript
ws.onopen = () => {
    // Calculate lastSeenSeq dynamically from messages in state
    const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
    const lastSeq = getMaxSeq(sessionMessages);
    if (lastSeq > 0) {
        console.log(`Syncing session ${sessionId} from seq ${lastSeq} (calculated from ${sessionMessages.length} messages)`);
        ws.send(JSON.stringify({
            type: 'load_events',
            data: { after_seq: lastSeq }
        }));
    } else {
        // Initial load
        ws.send(JSON.stringify({
            type: 'load_events',
            data: { limit: INITIAL_EVENTS_LIMIT }
        }));
    }
};
```

This approach eliminates the stale localStorage problem because the seq is always calculated from the actual messages being displayed.

## Force Reconnect on Visibility Change

**The Zombie Connection Problem:** Mobile browsers may keep WebSocket connections in a "zombie" state. The connection appears open but is actually dead.

**Solution:** Force fresh reconnection when app becomes visible:

```javascript
useEffect(() => {
    const handleVisibilityChange = () => {
        if (document.visibilityState === 'visible') {
            fetchStoredSessions();
            setTimeout(() => forceReconnectActiveSession(), 300);
        }
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => document.removeEventListener('visibilitychange', handleVisibilityChange);
}, [fetchStoredSessions, forceReconnectActiveSession]);
```

## Additional Resilience Mechanisms

Beyond visibility change, the frontend uses multiple event sources for resilience:

| Event | Purpose | Response Time |
|-------|---------|---------------|
| `visibilitychange` | Tab switch, phone wake | ~300ms |
| `online`/`offline` | Network loss/restore | ~500ms |
| `navigator.connection.change` | WiFi ↔ Cellular | ~500ms |
| `freeze`/`resume` | iOS Safari page freeze | ~300ms |

```javascript
// Online/offline events
window.addEventListener("online", () => {
    setTimeout(() => forceReconnectActiveSession(), 500);
});

// Network Information API (Chrome, Edge)
navigator.connection?.addEventListener("change", () => {
    setTimeout(() => forceReconnectActiveSession(), 500);
});

// Page Lifecycle API (iOS Safari)
document.addEventListener("freeze", () => {
    // Close WebSocket cleanly before freeze
    ws?.close();
});
document.addEventListener("resume", () => {
    setTimeout(() => forceReconnectActiveSession(), 300);
});
```

## Session Staleness Detection

When a phone has been locked overnight, the server-side auth session may have expired (24-hour duration):

```javascript
const STALE_THRESHOLD_MS = 60 * 60 * 1000; // 1 hour
const lastHiddenTimeRef = useRef(null);

const handleVisibilityChange = async () => {
    if (document.visibilityState === 'hidden') {
        lastHiddenTimeRef.current = Date.now();
        return;
    }

    if (document.visibilityState === 'visible') {
        const hiddenDuration = lastHiddenTimeRef.current
            ? Date.now() - lastHiddenTimeRef.current : 0;

        if (hiddenDuration > STALE_THRESHOLD_MS) {
            const { authenticated, networkError } = await checkAuthWithRetry();
            if (!authenticated && !networkError) return; // Redirect to login
        }
        // Proceed with normal reconnection...
    }
};
```

## Key Storage Functions

Located in `web/static/utils/storage.js`:

### Session Functions

| Function | Purpose |
|----------|---------|
| `getLastActiveSessionId()` | Get last active session for page reload recovery |
| `setLastActiveSessionId(id)` | Store active session ID |

**Note**: `getLastSeenSeq` and `setLastSeenSeq` are deprecated. The seq is now calculated dynamically from messages in state using `getMaxSeq()` from `lib.js`.

### UI Preference Functions

| Function | Purpose |
|----------|---------|
| `getQueueDropdownHeight()` | Get saved queue dropdown height (default: 256px) |
| `setQueueDropdownHeight(height)` | Save queue dropdown height (clamped to 100-500px) |
| `getQueueHeightConstraints()` | Get min/max/default height constraints |

**Usage example:**
```javascript
import { getQueueDropdownHeight, setQueueDropdownHeight, getQueueHeightConstraints } from "../utils/storage.js";

const constraints = getQueueHeightConstraints();
// { min: 100, max: 500, default: 256 }

const savedHeight = getQueueDropdownHeight();  // Returns saved or default
setQueueDropdownHeight(300);  // Persists to localStorage
```

## WebSocket Message Types for Sync

| Direction | Type | Data | Purpose |
|-----------|------|------|---------|
| Frontend → Backend | `load_events` | `{after_seq}` | Request events after sequence |
| Backend → Frontend | `events_loaded` | `{events, last_seq, ...}` | Response with missed events |

**Note:** The deprecated `sync_session`/`session_sync` messages are still supported for backward compatibility, but new code should use `load_events`/`events_loaded`.

## Client-Side Deduplication

When handling `events_loaded` for sync (not initial load or pagination), use `mergeMessagesWithSync`:

```javascript
case "events_loaded": {
  if (session.messages.length > 0 && !isPrepend) {
    // Sync after reconnect - merge with deduplication
    messages = mergeMessagesWithSync(session.messages, newMessages);
  }
}
```

This handles cases where `lastSeenSeq` is stale (visibility change during streaming).

## Buffered Content for New Observers

When a client connects mid-stream, the backend sends any buffered content that hasn't been persisted yet. This ensures thoughts and agent messages are visible even before the prompt completes.

## Mobile Browser Debugging

### iOS Safari Remote Debugging

1. **On iPhone**: Settings → Safari → Advanced → Web Inspector (enable)
2. **Connect iPhone to Mac via USB**
3. **On Mac**: Safari → Develop menu → Your iPhone → The page

### Common Mobile Issues

| Issue | Symptom | Solution |
|-------|---------|----------|
| Cached assets | Works on simulator, fails on device | Clear Safari cache or use Private Browsing |
| Zombie WebSocket | "Connecting" after phone wake | Force reconnect on visibility change |
| Wrong API path | Requests without `/mitto` prefix | Clear cache, check `window.mittoApiPrefix` |

### Force Cache Clear on iOS Safari

1. Settings → Safari → Clear History and Website Data
2. Or: Long-press refresh button → "Request Desktop Site" then back
3. Or: Use Private Browsing mode for testing

