---
description: Mobile browser handling, visibility change, wake resync, session staleness, localStorage, and iOS Safari debugging
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/hooks/useSwipeNavigation.js"
  - "web/static/utils/storage.js"
---

# Mobile Wake Resync and Browser Handling

Mobile browsers (iOS Safari, Android Chrome) suspend WebSocket connections when the device sleeps. The frontend must resync when the app becomes visible again.

## The Problem

1. User opens Mitto on phone, views a conversation
2. Phone goes to sleep (screen off)
3. WebSocket connection is terminated by the browser
4. Agent continues processing in the background (server-side)
5. User wakes phone - UI shows stale messages

## Solution: Sequence Number Tracking

Track the last seen event sequence number in localStorage:

```javascript
import { getLastSeenSeq, setLastSeenSeq } from '../utils/storage.js';

// Update lastSeenSeq when loading a session
const lastSeq = events.length > 0 ? getMaxSeq(events) : 0;
if (lastSeq > 0) setLastSeenSeq(sessionId, lastSeq);

// Update lastSeenSeq when prompt completes
case 'prompt_complete': {
    if (msg.data.event_count) setLastSeenSeq(sessionId, msg.data.event_count);
    break;
}
```

## Sync on WebSocket Reconnect

When per-session WebSocket connects, request missed events:

```javascript
ws.onopen = () => {
    const lastSeq = getLastSeenSeq(sessionId);
    if (lastSeq > 0) {
        ws.send(JSON.stringify({
            type: 'sync_session',
            data: { session_id: sessionId, after_seq: lastSeq }
        }));
    }
};
```

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

### Session Sync Functions

| Function | Purpose |
|----------|---------|
| `getLastSeenSeq(sessionId)` | Get last seen sequence number from localStorage |
| `setLastSeenSeq(sessionId, seq)` | Store sequence number in localStorage |
| `getLastActiveSessionId()` | Get last active session for page reload recovery |
| `setLastActiveSessionId(id)` | Store active session ID |

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
| Frontend → Backend | `sync_session` | `{session_id, after_seq}` | Request events after sequence |
| Backend → Frontend | `session_sync` | `{events, last_seq}` | Response with missed events |

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

