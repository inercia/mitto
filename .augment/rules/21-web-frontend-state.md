---
description: Frontend state management, refs vs state, stale closure prevention, scroll positioning
globs:
  - "web/static/app.js"
  - "web/static/hooks/*.js"
  - "web/static/components/*.js"
keywords:
  - useState
  - useCallback
  - useRef
  - stale closure
  - ref vs state
  - scroll
---

# Frontend State Management

## Stale Closure Prevention with Refs

**Use refs for values accessed in callbacks to avoid stale closures:**

```javascript
// Problem: activeSessionId in useCallback captures stale value
const handleMessage = useCallback(
  (msg) => {
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

## Scroll Positioning Pattern

Use `useLayoutEffect` (before paint) for scroll positioning to avoid visible "jump" artifacts:

```javascript
// Position at bottom synchronously BEFORE paint when switching sessions
useLayoutEffect(() => {
  const container = messagesContainerRef.current;
  if (!container) return;

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

```javascript
// useLayoutEffect: Session switch - instant scroll, before paint
useLayoutEffect(() => {
  if (sessionJustChanged) scrollToBottomInstant();
}, [activeSessionId, messages.length]);

// useEffect: Streaming updates - smooth scroll, after paint is fine
useEffect(() => {
  if (isStreaming && isUserAtBottom) scrollToBottom(true);
}, [messages.length, isStreaming]);
```

## Settings Dialog Patterns

### State After Save

When saving settings that affect external state, update local state immediately after save:

```javascript
const handleSave = async () => {
  await fetch("/api/config", { method: "POST", body: JSON.stringify(settings) });
  const statusRes = await fetch("/api/external-status");
  const { enabled, port } = await statusRes.json();
  setCurrentExternalPort(port);
};
```

### Config Readonly Mode

Some deployments use file-based config that shouldn't be modified via UI:

```javascript
const [configReadonly, setConfigReadonly] = useState(false);
if (configReadonly) return; // Don't open settings dialog
```
