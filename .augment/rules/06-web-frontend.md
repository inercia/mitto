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
| `app.js` | Main Preact application, components, state management |
| `lib.js` | Pure utility functions (testable without DOM) |
| `lib.test.js` | Jest tests for lib.js |
| `styles.css` | Custom CSS for Markdown rendering |
| `index.html` | HTML shell, CDN imports, Tailwind config |

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

## Memory Management

```javascript
// MAX_MESSAGES prevents memory issues in long sessions
export const MAX_MESSAGES = 100;

// Messages auto-trimmed when added
const newMessages = limitMessages([...session.messages, message]);
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

**Note**: This project bundles hooks via `window.preact` in `index.html` to avoid separate imports:

```javascript
// app.js - imports from window.preact bundle
const { h, render, useState, useEffect, useLayoutEffect, useRef, useCallback, useMemo, html } = window.preact;
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

