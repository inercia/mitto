---
description: Frontend patterns, Preact/HTM, state management, and UI components
globs:
  - "web/**/*"
  - "web/static/**/*"
  - "**/*.js"
  - "**/*.html"
  - "**/*.css"
---

# Web Frontend Patterns

## Technology Stack

- **No build step**: Preact + HTM loaded from CDN
- **Styling**: Tailwind CSS via Play CDN
- **Markdown**: marked.js + DOMPurify for user message rendering
- **Embedding**: `go:embed` directive in `web/embed.go`
- **Single binary**: All assets embedded in Go binary

## Frontend Component Structure

```
App
├── SessionList (sidebar, hidden on mobile)
├── Header (connection status, streaming indicator)
├── MessageList
│   └── Message (user/agent/thought/tool/error/system)
├── ChatInput (textarea + send/cancel)
└── Dialogs
    ├── SettingsDialog (configuration, workspaces, auth)
    ├── WorkspaceDialog (workspace selection for new session)
    ├── KeyboardShortcutsDialog (help for shortcuts)
    ├── RenameDialog (rename session)
    ├── DeleteDialog (confirm session deletion)
    └── CleanInactiveDialog (bulk delete inactive sessions)
```

## File Structure

| File | Purpose |
|------|---------|
| `app.js` | Main Preact application, state management |
| `lib.js` | Pure utility functions (testable without DOM) |
| `lib.test.js` | Jest tests for lib.js |
| `preact-loader.js` | CDN imports, library initialization |
| `styles.css` | Custom CSS for Markdown rendering |
| `index.html` | HTML shell, Tailwind config |
| `components/Message.js` | Message rendering component |

## lib.js Functions

The library provides pure functions for state manipulation:

| Function | Purpose |
|----------|---------|
| `computeAllSessions()` | Merge active + stored sessions, sort by time |
| `convertEventsToMessages()` | Transform stored events to display messages |
| `createSessionState()` | Create new session state object |
| `addMessageToSessionState()` | Add message with automatic trimming |
| `updateLastMessageInSession()` | Immutably update last message |
| `removeSessionFromState()` | Remove session and determine next active |
| `limitMessages()` | Enforce MAX_MESSAGES limit |
| `getMinSeq(events)` | Get minimum sequence number from events array |
| `getMaxSeq(events)` | Get maximum sequence number from events array |
| `hasMarkdownContent(text)` | Detect if text contains Markdown formatting |
| `renderUserMarkdown(text)` | Render user message as Markdown HTML |
| `generatePromptId()` | Generate unique prompt ID for delivery tracking |
| `savePendingPrompt()` | Save prompt to localStorage before sending |
| `removePendingPrompt()` | Remove prompt after ACK received |
| `getPendingPrompts()` | Get all pending prompts from localStorage |
| `getPendingPromptsForSession()` | Get pending prompts for a specific session |
| `cleanupExpiredPrompts()` | Remove prompts older than 5 minutes |
| `mergeMessagesWithSync()` | Deduplicate and append messages when syncing after reconnect |
| `getMessageHash()` | Create content hash for message deduplication |

## Hooks Directory Structure

The `hooks/` directory contains reusable Preact hooks:

| File | Purpose |
|------|---------|
| `useWebSocket.js` | WebSocket connections, session management, message handling |
| `useSwipeNavigation.js` | Mobile swipe gestures for navigation |
| `index.js` | Re-exports for clean imports |

## Memory Management

```javascript
// MAX_MESSAGES prevents memory issues in long sessions
export const MAX_MESSAGES = 100;

// Messages auto-trimmed when added
const newMessages = limitMessages([...session.messages, message]);
```

## Promise-Based Message Sending with ACK

Messages are sent with delivery confirmation and retry capability:

### Architecture

```
User clicks Send → onSend() returns Promise → Wait for ACK → Resolve/Reject

ChatInput                       useWebSocket                    Backend
    |                               |                              |
    |-- onSend(text, images) ------>|                              |
    |   (returns Promise)           |-- sendPrompt() ------------->|
    |                               |   (saves to pending queue)   |
    |   [Show spinner]              |                              |
    |                               |<-- prompt_received (ACK) ----|
    |<-- Promise resolves --------- |                              |
    |   [Clear spinner, clear text] |                              |
    |                               |                              |
    |   OR on timeout/error:        |                              |
    |<-- Promise rejects -----------|                              |
    |   [Show error, keep text]     |                              |
```

### Send Button States

The ChatInput component shows different states during message sending:

| State | Button Appearance | Text Area |
|-------|-------------------|-----------|
| Normal | "Send" button enabled | Editable |
| Sending | Spinner + "Sending..." | Disabled |
| Error | "Send" enabled | Editable (text preserved) |
| Streaming | "Stop" button | Disabled |

```javascript
// In ChatInput component
const [isSending, setIsSending] = useState(false);
const [sendError, setSendError] = useState(null);

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
// In lib.js
export function generatePromptId() {
    return `prompt_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
}

export function savePendingPrompt(sessionId, promptId, message, imageIds = []) {
    const pending = getPendingPrompts();
    pending[promptId] = { sessionId, message, imageIds, timestamp: Date.now() };
    localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
}

export function removePendingPrompt(promptId) {
    const pending = getPendingPrompts();
    delete pending[promptId];
    localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
}
```

### ACK Handling in useWebSocket

The `sendPrompt` function returns a Promise that resolves when ACK is received:

```javascript
const sendPrompt = useCallback((message, images = [], options = {}) => {
    const timeout = options.timeout || SEND_ACK_TIMEOUT;

    return new Promise((resolve, reject) => {
        // Validate WebSocket connection
        const ws = sessionWsRefs.current[activeSessionId];
        if (!ws || ws.readyState !== WebSocket.OPEN) {
            reject(new Error('WebSocket not connected'));
            return;
        }

        // Generate unique prompt ID
        const promptId = generatePromptId();

        // Save to pending queue BEFORE sending
        savePendingPrompt(activeSessionId, promptId, message, imageIds);

        // Set up timeout
        const timeoutId = setTimeout(() => {
            delete pendingSendsRef.current[promptId];
            reject(new Error('Message send timed out'));
        }, timeout);

        // Track pending send
        pendingSendsRef.current[promptId] = { resolve, reject, timeoutId };

        // Send with prompt_id for acknowledgment
        ws.send(JSON.stringify({
            type: 'prompt',
            data: { message, image_ids: imageIds, prompt_id: promptId }
        }));
    });
}, [activeSessionId]);

// Handle ACK from server
case 'prompt_received':
    if (msg.data.prompt_id) {
        removePendingPrompt(msg.data.prompt_id);
        const pending = pendingSendsRef.current[msg.data.prompt_id];
        if (pending) {
            clearTimeout(pending.timeoutId);
            pending.resolve({ success: true });
            delete pendingSendsRef.current[msg.data.prompt_id];
        }
    }
    break;
```

### Error Display and Retry

When sending fails, the error is shown with the message preserved:

```javascript
${sendError && html`
    <div class="bg-orange-900/50 border border-orange-700 text-orange-200 px-4 py-2 rounded-lg">
        <span>${sendError}</span>
        <span class="text-xs ml-1">(Your message is preserved - click Send to retry)</span>
    </div>
`}
```

## State Management Patterns

**Use refs for values accessed in callbacks to avoid stale closures:**

```javascript
// Problem: activeSessionId in useCallback captures stale value
const handleMessage = useCallback((msg) => {
    // activeSessionId here is stale - it was captured when callback was created
    if (!activeSessionId) return;  // BUG: always null on first messages!
}, [activeSessionId]);

// Solution: Use a ref that's always current
const activeSessionIdRef = useRef(activeSessionId);
useEffect(() => {
    activeSessionIdRef.current = activeSessionId;
}, [activeSessionId]);

const handleMessage = useCallback((msg) => {
    const currentSessionId = activeSessionIdRef.current;  // Always current!
    if (!currentSessionId) return;
}, []);  // No dependency on activeSessionId
```

**Race condition pattern in WebSocket handlers:**
- WebSocket messages can arrive before React state updates complete
- Session switching: `session_switched` sets `activeSessionId`, but `agent_message` may arrive first
- Always use refs for state that callbacks need to read during async operations

**Function definition order in hooks:**
- `useCallback` functions must be defined before they're used in dependency arrays
- If function A uses function B, define B before A
- Circular dependencies require refs to break the cycle

## CDN Selection for Frontend Libraries

**Recommended CDN for ES modules**: Skypack (`cdn.skypack.dev`)
- Handles internal module resolution correctly
- Works with Preact hooks imports

**Avoid for ES modules**:
- `unpkg.com` and `jsdelivr.net` - May fail with "Failed to resolve module specifier" errors
  when libraries have internal imports without full paths
- `esm.sh` - Generally works but may have availability issues

```html
<!-- Recommended -->
<script type="module">
    import { h, render } from 'https://cdn.skypack.dev/preact@10.19.3';
    import { useState, useEffect, useLayoutEffect, useRef, useCallback } from 'https://cdn.skypack.dev/preact@10.19.3/hooks';
    import htm from 'https://cdn.skypack.dev/htm@3.1.1';
</script>
```

**Note**: This project bundles libraries via `window` globals in `preact-loader.js`:

```javascript
// preact-loader.js - loads and exposes libraries
import { marked } from 'https://cdn.skypack.dev/marked@12.0.0';
import DOMPurify from 'https://cdn.skypack.dev/dompurify@3.0.8';

window.preact = { h, render, useState, useEffect, useLayoutEffect, useRef, useCallback, useMemo, html };
window.marked = marked;
window.DOMPurify = DOMPurify;

// app.js - imports from window.preact bundle
const { h, render, useState, useEffect, useLayoutEffect, useRef, useCallback, useMemo, html } = window.preact;
```

## User Message Markdown Rendering

User messages support Markdown rendering with performance safeguards:

### Architecture

```
User Message Text
       ↓
hasMarkdownContent() → false → Plain text display (<pre>)
       ↓ true
Length > MAX_MARKDOWN_LENGTH → Plain text display
       ↓ within limit
window.marked.parse() → DOMPurify.sanitize() → HTML display
```

### Performance Safeguards

1. **Heuristic detection**: `hasMarkdownContent()` checks for Markdown patterns before rendering
2. **Length limit**: Messages > 10,000 chars skip Markdown processing
3. **Memoization**: `useMemo()` prevents re-rendering on every component update
4. **Graceful fallback**: Any error returns `null` → plain text display

### Markdown Detection Patterns

The `hasMarkdownContent()` function detects:
- Headers (`#`, `##`, etc.)
- Bold (`**text**`, `__text__`)
- Italic (`*text*`, `_text_`)
- Code (`` `code` ``, ``` ```blocks``` ```)
- Links (`[text](url)`)
- Lists (`- item`, `1. item`)
- Blockquotes (`> text`)
- Tables, horizontal rules, strikethrough

### Usage in Message Component

```javascript
import { renderUserMarkdown } from '../lib.js';

// In Message component
const renderedHtml = useMemo(() => renderUserMarkdown(message.text), [message.text]);
const useMarkdown = renderedHtml !== null;

return useMarkdown
    ? html`<div class="markdown-content markdown-content-user" dangerouslySetInnerHTML=${{ __html: renderedHtml }} />`
    : html`<pre class="whitespace-pre-wrap font-sans text-sm m-0">${message.text}</pre>`;
```

### Styling User Message Markdown

User messages use `.markdown-content-user` class for styling that works with the user message background:

```css
/* User message markdown - darker backgrounds for contrast */
.markdown-content-user pre {
    background: rgba(0, 0, 0, 0.15);
}
.markdown-content-user :not(pre) > code {
    background: rgba(0, 0, 0, 0.15);
}
.markdown-content-user a {
    color: #1d4ed8;
}
```

## Dual Validation (Frontend + Backend)

For destructive operations, implement validation in both layers:

1. **Frontend (immediate feedback)**:
   - Load related data when dialog opens (e.g., stored sessions)
   - Check constraints before allowing action
   - Show clear error message to user

2. **Backend (security)**:
   - Always validate even if frontend checks
   - Return structured error responses (JSON with error code, message, details)
   - Use appropriate HTTP status codes (409 Conflict for referential integrity)

```javascript
// Frontend: SettingsDialog loads sessions to check workspace usage
const loadStoredSessions = async () => {
    const res = await fetch('/api/sessions');
    if (res.ok) {
        setStoredSessions(await res.json());
    }
};

const removeWorkspace = (workingDir) => {
    const count = storedSessions.filter(s => s.working_dir === workingDir).length;
    if (count > 0) {
        setError(`Cannot remove: ${count} conversation(s) using it`);
        return;
    }
    // Proceed with removal
};
```

## Settings Dialog Patterns

### State After Save

When saving settings that affect external state (like external port), update local state immediately after save:

```javascript
const handleSave = async () => {
    await fetch('/api/config', { method: 'POST', body: JSON.stringify(settings) });

    // Fetch updated external status to get actual port
    const statusRes = await fetch('/api/external-status');
    const { enabled, port } = await statusRes.json();

    // Update local state so UI reflects new values
    setCurrentExternalPort(port);

    // Show feedback with actual values
    if (enabled && port > 0) {
        showToast(`External access on port ${port}`);
    }
};
```

### Config Readonly Mode

Some deployments use file-based config that shouldn't be modified via UI:

```javascript
// Check if config is read-only
const [configReadonly, setConfigReadonly] = useState(false);

// Disable settings access when readonly
if (configReadonly) {
    return;  // Don't open settings dialog
}
```

## useEffect vs useLayoutEffect

**Critical distinction** for DOM positioning and scroll handling:

| Hook | Timing | Use When |
|------|--------|----------|
| `useEffect` | After paint (async) | Data fetching, subscriptions, side effects |
| `useLayoutEffect` | Before paint (sync) | DOM positioning, scroll restoration, measurements |

### Scroll Positioning Pattern

**Problem**: Using `useEffect` for scroll positioning causes visible "jump" artifacts:
1. Content renders at wrong scroll position
2. Browser paints (user sees wrong position)
3. `useEffect` runs and fixes scroll
4. Browser paints again (user sees jump)

**Solution**: Use `useLayoutEffect` for all scroll positioning on session switches:

```javascript
// Position at bottom synchronously BEFORE paint when switching sessions
useLayoutEffect(() => {
    const container = messagesContainerRef.current;
    if (!container) return;

    // Detect session switch
    if (prevActiveSessionIdRef.current !== activeSessionId) {
        prevActiveSessionIdRef.current = activeSessionId;

        // Instant scroll - bypass CSS scroll-behavior: smooth
        const originalBehavior = container.style.scrollBehavior;
        container.style.scrollBehavior = 'auto';
        container.scrollTop = container.scrollHeight;
        container.style.scrollBehavior = originalBehavior;
    }
}, [activeSessionId, messages.length]);
```

### Bypassing CSS Smooth Scrolling

When you need instant scroll positioning but CSS has `scroll-behavior: smooth`:

```javascript
const scrollToBottomInstant = () => {
    if (!container) return;
    // Temporarily disable smooth scrolling
    const originalBehavior = container.style.scrollBehavior;
    container.style.scrollBehavior = 'auto';
    container.scrollTop = container.scrollHeight;
    // Restore after scroll completes
    container.style.scrollBehavior = originalBehavior;
};
```

### Separating Concerns: Session Switch vs Streaming

Use separate hooks for different scroll scenarios:

```javascript
// useLayoutEffect: Session switch - instant scroll, no animation, before paint
useLayoutEffect(() => {
    if (sessionJustChanged) {
        scrollToBottomInstant();
    }
}, [activeSessionId, messages.length]);

// useEffect: Streaming updates - smooth scroll, after paint is fine
useEffect(() => {
    if (isStreaming && isUserAtBottom) {
        scrollToBottom(true);  // smooth: true
    }
}, [messages.length, isStreaming]);
```

### Async Message Loading Pattern

When session switching involves async data loading:

```javascript
// Problem: useLayoutEffect fires, but messages haven't loaded yet

// Solution: Use a ref to track "just switched" state
const sessionJustSwitchedRef = useRef(false);

useLayoutEffect(() => {
    if (activeSessionId !== prevActiveSessionIdRef.current) {
        prevActiveSessionIdRef.current = activeSessionId;

        if (messages.length > 0) {
            // Messages already loaded, scroll now
            scrollToBottomInstant();
        } else {
            // Messages loading async, scroll when they arrive
            sessionJustSwitchedRef.current = true;
        }
    }

    // Handle delayed message arrival
    if (sessionJustSwitchedRef.current && messages.length > 0) {
        sessionJustSwitchedRef.current = false;
        scrollToBottomInstant();
    }
}, [activeSessionId, messages.length]);
```

## Mobile Wake Resync

Mobile browsers (iOS Safari, Android Chrome) suspend WebSocket connections when the device sleeps. The frontend must resync when the app becomes visible again.

### Problem

1. User opens Mitto on phone, views a conversation
2. Phone goes to sleep (screen off)
3. WebSocket connection is terminated by the browser
4. Agent continues processing in the background (server-side)
5. User wakes phone - UI shows stale messages

### Solution: Sequence Number Tracking

Track the last seen event sequence number in localStorage:

```javascript
import { getLastSeenSeq, setLastSeenSeq } from '../utils/storage.js';

// Update lastSeenSeq when loading a session
const lastSeq = events.length > 0 ? getMaxSeq(events) : 0;
if (lastSeq > 0) {
    setLastSeenSeq(sessionId, lastSeq);
}

// Update lastSeenSeq when prompt completes
case 'prompt_complete': {
    if (msg.data.event_count) {
        setLastSeenSeq(sessionId, msg.data.event_count);
    }
    break;
}
```

### Sync on WebSocket Reconnect

When per-session WebSocket connects, request missed events:

```javascript
ws.onopen = () => {
    // Sync events we may have missed while disconnected
    const lastSeq = getLastSeenSeq(sessionId);
    if (lastSeq > 0) {
        ws.send(JSON.stringify({
            type: 'sync_session',
            data: { session_id: sessionId, after_seq: lastSeq }
        }));
    }
};
```

### Force Reconnect on Visibility Change

**The Zombie Connection Problem:** Mobile browsers (especially iOS Safari) may keep WebSocket connections in a "zombie" state after the phone sleeps. The connection appears open (`readyState === OPEN`) but is actually dead.

**Solution:** Force a fresh reconnection whenever the app becomes visible:

```javascript
// Force reconnect - closes zombie connection and creates fresh one
const forceReconnectActiveSession = useCallback(() => {
    const currentSessionId = activeSessionIdRef.current;
    if (!currentSessionId) return;

    // Close existing WebSocket (may be zombie)
    const existingWs = sessionWsRefs.current[currentSessionId];
    if (existingWs) {
        delete sessionWsRefs.current[currentSessionId];
        existingWs.close();
    }

    // Create fresh connection - ws.onopen will sync events
    connectToSession(currentSessionId);
}, [connectToSession]);

useEffect(() => {
    const handleVisibilityChange = () => {
        if (document.visibilityState === 'visible') {
            // Refresh session list
            fetchStoredSessions();

            // Force reconnect to get a clean connection
            // The ws.onopen handler will sync events and retry pending prompts
            setTimeout(() => {
                forceReconnectActiveSession();
            }, 300);
        }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => document.removeEventListener('visibilitychange', handleVisibilityChange);
}, [fetchStoredSessions, forceReconnectActiveSession]);
```

### Key Storage Functions

| Function | Purpose |
|----------|---------|
| `getLastSeenSeq(sessionId)` | Get last seen sequence number from localStorage |
| `setLastSeenSeq(sessionId, seq)` | Store sequence number in localStorage |
| `getLastActiveSessionId()` | Get last active session for page reload recovery |
| `setLastActiveSessionId(id)` | Store active session ID |

### WebSocket Message Types for Sync

| Direction | Type | Data | Purpose |
|-----------|------|------|---------|
| Frontend → Backend | `sync_session` | `{session_id, after_seq}` | Request events after sequence |
| Backend → Frontend | `session_sync` | `{events, last_seq}` | Response with missed events |

### Buffered Content for New Observers

When a client connects mid-stream, the backend sends any buffered content that hasn't been persisted yet. This ensures thoughts and agent messages are visible even before the prompt completes.

**How it works:**
1. Client connects to session WebSocket
2. Backend's `AddObserver` checks if session is currently prompting
3. If prompting, backend sends buffered thought and message content
4. Client receives this as regular `agent_thought` and `agent_message` events

**Why this matters:**
- Thoughts are buffered and only persisted when prompt completes
- Without this, clients connecting mid-stream would miss thoughts
- This caused the issue where thoughts were visible on macOS but not iOS

## Message Ordering

### Why NOT to Sort by Sequence Number

**IMPORTANT**: Do NOT re-sort messages by `seq` or timestamp. This causes incorrect ordering.

The problem is that different event types are persisted at different times:
1. **Tool calls** are persisted **immediately** when they occur (get early `seq` numbers)
2. **Agent messages** are **buffered** during streaming and persisted when the prompt completes (get later `seq` numbers)

If we sorted by `seq`, all tool calls would appear before the agent message, even though they were interleaved during the actual conversation:

```
Actual streaming order:    "Let me help..." → tool_call → "I found..." → tool_call → "Done!"
Persisted seq order:       tool_call(seq=2) → tool_call(seq=3) → agent_message(seq=4)
```

### Correct Ordering Strategy

The frontend preserves message order using these principles:

1. **Streaming messages** are displayed in the order they arrive via WebSocket
2. **Loaded sessions** use the order from `events.jsonl` (which is append-only)
3. **Sync messages** are appended at the end (they represent events that happened AFTER the last seen event)
4. **Deduplication** prevents the same message from appearing twice

## Message Deduplication

### The Duplicate Message Problem

Duplicate messages can occur when:
1. **Sync after streaming**: Messages received via WebSocket streaming don't have sequence numbers. When reconnecting, sync may return the same messages (now with seq numbers).
2. **Multiple observers**: If multiple WebSocket connections exist for the same session, each receives the same message.
3. **Reconnection race conditions**: Old WebSocket's `onclose` handler may interfere with new connection.

### Deduplication in mergeMessagesWithSync

The `mergeMessagesWithSync` function handles deduplication and appending (NOT sorting):

```javascript
export function mergeMessagesWithSync(existingMessages, newMessages) {
  if (!existingMessages || existingMessages.length === 0) {
    return newMessages || [];
  }
  if (!newMessages || newMessages.length === 0) {
    return existingMessages;
  }

  // Create hash set of existing messages for deduplication
  const existingHashes = new Set();
  for (const m of existingMessages) {
    existingHashes.add(getMessageHash(m));
  }

  // Filter out duplicates from new messages
  const filteredNewMessages = newMessages.filter((m) => {
    const hash = getMessageHash(m);
    return !existingHashes.has(hash);
  });

  if (filteredNewMessages.length === 0) {
    return existingMessages;
  }

  // Append new messages at the end - they happened after existing messages
  return [...existingMessages, ...filteredNewMessages];
}
```

### Using mergeMessagesWithSync in session_sync Handler

```javascript
case 'session_sync': {
    const events = msg.data.events || [];
    const newMessages = convertEventsToMessages(events);

    setSessions(prev => {
        const session = prev[sessionId] || { messages: [], info: {} };
        const existingMessages = session.messages;

        // mergeMessagesWithSync handles deduplication and appending
        const mergedMessages = mergeMessagesWithSync(existingMessages, newMessages);

        return {
            ...prev,
            [sessionId]: {
                ...session,
                messages: limitMessages(mergedMessages),
                // ...
            }
        };
    });
    break;
}
```

### Deduplication in user_prompt Handler

When receiving prompts from other clients, check if already exists:

```javascript
case 'user_prompt': {
    const { is_mine, message } = msg.data;

    if (!is_mine) {
        setSessions(prev => {
            const session = prev[sessionId];
            if (!session) return prev;

            // Check if message already exists
            const messageContent = message?.substring(0, 200) || '';
            const alreadyExists = session.messages.some(m =>
                m.role === ROLE_USER &&
                (m.text || '').substring(0, 200) === messageContent
            );

            if (alreadyExists) {
                console.log('Skipping duplicate user_prompt');
                return prev;
            }

            // Add the message...
        });
    }
    break;
}
```

## WebSocket Connection Management

### Preventing Reference Leaks on Close

When a WebSocket closes, only delete its reference if it's still the current one:

```javascript
ws.onclose = () => {
    // Only delete ref if it still points to THIS WebSocket
    // A newer WebSocket may have been created during reconnection
    if (sessionWsRefs.current[sessionId] === ws) {
        delete sessionWsRefs.current[sessionId];
    } else {
        console.log('WebSocket closed but ref points to different WebSocket');
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

    // Clear pending reconnect timer
    if (sessionReconnectRefs.current[currentSessionId]) {
        clearTimeout(sessionReconnectRefs.current[currentSessionId]);
        delete sessionReconnectRefs.current[currentSessionId];
    }

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

### Debug Logging for WebSocket Issues

Add unique IDs to WebSocket connections for debugging:

```javascript
const connectToSession = useCallback((sessionId) => {
    const ws = new WebSocket(`${protocol}//${host}/api/sessions/${sessionId}/ws`);
    const wsId = Math.random().toString(36).substring(2, 8);
    ws._debugId = wsId;

    ws.onopen = () => {
        console.log(`WebSocket connected: ${sessionId} (ws: ${wsId})`);
    };

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        console.log(`[WS ${wsId}] Received:`, msg.type);
    };

    ws.onclose = () => {
        console.log(`WebSocket closed: ${sessionId} (ws: ${wsId})`);
    };
}, []);
```

## API Prefix Handling

### How It Works

The API prefix is injected by the server into the HTML page and used by all API/WebSocket calls:

```html
<!-- In index.html - replaced by server -->
<script>window.mittoApiPrefix = "{{API_PREFIX}}";</script>
```

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

### Debugging API Prefix Issues

**Symptom**: "Connecting to server" forever, requests go to wrong path

**Add temporary debug logging:**

```javascript
// In utils/api.js - add at module load
console.log('[api.js] window.mittoApiPrefix:', window.mittoApiPrefix);

// In wsUrl function
export function wsUrl(path) {
    const prefix = getApiPrefix();
    const url = `${protocol}//${window.location.host}${prefix}${path}`;
    console.log('[wsUrl] prefix:', prefix, 'path:', path, 'full URL:', url);
    return url;
}
```

**Check server logs for path mismatch:**
```
path=/api/events        ← WRONG (no prefix)
path=/mitto/api/events  ← CORRECT (has prefix)
```

**Common causes:**
1. Cached JavaScript with old/empty prefix
2. HTML not being processed by CSP nonce middleware
3. Script loading order issue (api.js loaded before prefix is set)

### Script Loading Order

The API prefix MUST be set before any scripts that use it:

```html
<!-- CORRECT ORDER -->
<script>window.mittoApiPrefix = "{{API_PREFIX}}";</script>  <!-- 1. Set prefix -->
<script src="./theme-loader.js"></script>                   <!-- 2. May use prefix -->
<script type="module" src="./preact-loader.js"></script>    <!-- 3. Loads app -->
```

## Mobile Browser Debugging

### iOS Safari Remote Debugging

To see console logs from iPhone Safari:

1. **On iPhone**: Settings → Safari → Advanced → Web Inspector (enable)
2. **Connect iPhone to Mac via USB**
3. **On Mac**: Safari → Develop menu → Your iPhone → The page

### Common Mobile Issues

| Issue | Symptom | Solution |
|-------|---------|----------|
| Cached assets | Works on simulator, fails on device | Clear Safari cache or use Private Browsing |
| Zombie WebSocket | "Connecting" after phone wake | Force reconnect on visibility change |
| Wrong API path | Requests without `/mitto` prefix | Clear cache, check `window.mittoApiPrefix` |

### Testing External Access

When testing via Tailscale Funnel or other external access:

1. **Always clear cache first** - Mobile Safari aggressively caches
2. **Use Private Browsing** - Ensures no cached assets
3. **Check both network paths** - Internal Tailscale vs Funnel may behave differently
4. **Monitor server logs** - Look for requests with/without API prefix

### Force Cache Clear on iOS Safari

1. Settings → Safari → Clear History and Website Data
2. Or: Long-press refresh button → "Request Desktop Site" then back
3. Or: Use Private Browsing mode for testing

## Caching Considerations

### Why Caching Matters

During development, cached assets cause hard-to-debug issues:

- **Cached HTML**: May have wrong `{{API_PREFIX}}` (not replaced)
- **Cached JS**: May use old API paths or have stale logic
- **Cached CSS**: May not reflect style changes

### Server-Side Cache Headers

The backend sets no-cache headers for all static assets:

```
Cache-Control: no-cache, no-store, must-revalidate
Pragma: no-cache
Expires: 0
```

### Client-Side Considerations

Even with no-cache headers, browsers may still cache:
- Service workers (if any)
- Back-forward cache (bfcache)
- Memory cache during session

**Always test with hard refresh** (Cmd+Shift+R on Mac, Ctrl+Shift+R on Windows)

