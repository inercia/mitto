---
description: Frontend state management, refs vs state, useCallback patterns, stale closure prevention, useEffect vs useLayoutEffect, useMemo for derived state
globs:
  - "web/static/app.js"
keywords:
  - useState
  - useEffect
  - useLayoutEffect
  - useCallback
  - useRef
  - useMemo
  - stale closure
  - ref vs state
  - scroll
  - derived state
---

# Frontend State Management

## State Management Patterns

**Use refs for values accessed in callbacks to avoid stale closures:**

```javascript
// Problem: activeSessionId in useCallback captures stale value
const handleMessage = useCallback(
  (msg) => {
    // activeSessionId here is stale - it was captured when callback was created
    if (!activeSessionId) return; // BUG: always null on first messages!
  },
  [activeSessionId],
);

// Solution: Use a ref that's always current
const activeSessionIdRef = useRef(activeSessionId);
useEffect(() => {
  activeSessionIdRef.current = activeSessionId;
}, [activeSessionId]);

const handleMessage = useCallback((msg) => {
  const currentSessionId = activeSessionIdRef.current; // Always current!
  if (!currentSessionId) return;
}, []); // No dependency on activeSessionId
```

**Race condition pattern in WebSocket handlers:**

- WebSocket messages can arrive before React state updates complete
- Session switching: `session_switched` sets `activeSessionId`, but `agent_message` may arrive first
- Always use refs for state that callbacks need to read during async operations

**Function definition order in hooks:**

- `useCallback` functions must be defined before they're used in dependency arrays
- If function A uses function B, define B before A
- Circular dependencies require refs to break the cycle

## useEffect vs useLayoutEffect

**Critical distinction** for DOM positioning and scroll handling:

| Hook              | Timing              | Use When                                          |
| ----------------- | ------------------- | ------------------------------------------------- |
| `useEffect`       | After paint (async) | Data fetching, subscriptions, side effects        |
| `useLayoutEffect` | Before paint (sync) | DOM positioning, scroll restoration, measurements |

### Scroll Positioning Pattern

**Problem**: Using `useEffect` for scroll positioning causes visible "jump" artifacts.

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
    container.style.scrollBehavior = "auto";
    container.scrollTop = container.scrollHeight;
    container.style.scrollBehavior = originalBehavior;
  }
}, [activeSessionId, messages.length]);
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
    scrollToBottom(true); // smooth: true
  }
}, [messages.length, isStreaming]);
```

## useMemo for Derived State

**Use `useMemo` for values derived from props that need synchronous calculation:**

```javascript
// BAD: useState + useEffect for derived position
function ContextMenu({ x, y }) {
  const [adjustedPos, setAdjustedPos] = useState({ x, y });

  useEffect(() => {
    // This runs AFTER render - first paint uses stale values!
    setAdjustedPos(calculateAdjustedPosition(x, y));
  }, [x, y]);

  return html`<div style="left: ${adjustedPos.x}px">`;
}

// GOOD: useMemo for synchronous derived values
function ContextMenu({ x, y }) {
  const menuRef = useRef(null);

  const position = useMemo(() => {
    // Calculated synchronously during render
    if (!menuRef.current) return { x, y };
    return calculateAdjustedPosition(x, y, menuRef.current);
  }, [x, y, menuRef.current]);

  return html`<div ref=${menuRef} style="left: ${position.x}px">`;
}
```

**Key insight**: `useState` captures the initial value and doesn't auto-update when props change. For derived values, use:
- `useMemo` for synchronous calculations
- `useLayoutEffect` + `setState` if you need DOM measurements before paint
- `useEffect` + `setState` only for async operations where delay is acceptable

See `28-anti-patterns-ui.md` for detailed context menu positioning patterns.

## Settings Dialog Patterns

### State After Save

When saving settings that affect external state, update local state immediately after save:

```javascript
const handleSave = async () => {
  await fetch("/api/config", {
    method: "POST",
    body: JSON.stringify(settings),
  });

  // Fetch updated external status to get actual port
  const statusRes = await fetch("/api/external-status");
  const { enabled, port } = await statusRes.json();

  // Update local state so UI reflects new values
  setCurrentExternalPort(port);
};
```

### Config Readonly Mode

Some deployments use file-based config that shouldn't be modified via UI:

```javascript
// Check if config is read-only
const [configReadonly, setConfigReadonly] = useState(false);

// Disable settings access when readonly
if (configReadonly) {
  return; // Don't open settings dialog
}
```
