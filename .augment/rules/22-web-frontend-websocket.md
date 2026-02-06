---
description: WebSocket connection management, message handling, reconnection, promise-based sending, and API URL helpers
globs:
  - "web/static/hooks/useWebSocket.js"
  - "web/static/hooks/index.js"
  - "web/static/utils/api.js"
  - "web/static/utils/csrf.js"
---

# WebSocket and Message Handling

## Hooks Directory Structure

| File | Purpose |
|------|---------|
| `hooks/useWebSocket.js` | WebSocket connections, session management, message handling |
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

### Sequence Number Based Ordering

All streaming events include a sequence number (`seq`) assigned when the event is received from the ACP:
- Assigned immediately when event is received (not at persistence time)
- Included in all WebSocket messages (`agent_message`, `tool_call`, etc.)
- Sorting by `seq` gives correct chronological order

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

