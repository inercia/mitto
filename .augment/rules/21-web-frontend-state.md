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

**Separate concerns**: `useLayoutEffect` for session switch (instant), `useEffect` for streaming (smooth scroll).

## Adding New Session Properties (Checklist)

When adding a new field to session state (e.g., `periodic_enabled`), **three places** in the frontend must all be updated or the value will be silently dropped:

| File | Location | What to add |
|------|----------|-------------|
| `useWebSocket.js` | `activeSessions = useMemo(...)` block (~line 1062) | Explicit field mapping from `data.info?.field` |
| `useWebSocket.js` | Fingerprint string (~line 1090) | `\|${s.field}` so changes trigger re-renders |
| `lib.js` | `computeAllSessions` "no stored session" case (~line 444) | Field with default value |

**Anti-pattern**: WebSocket handler (`periodic_updated`) sets `sessions[id].info.periodic_enabled` correctly, but if `activeSessions` useMemo doesn't forward it, the property is lost when `computeAllSessions` runs.

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

## Per-Tab Active Conversation State

**Pattern**: Each filter tab (Conversations, Periodic, Archived) remembers its own last-focused conversation separately.

**Storage helpers** (`web/static/utils/storage.js`):
```javascript
getLastActiveSessionIdForTab(tab)    // key: "mitto_last_session_id_<tab>"
setLastActiveSessionIdForTab(tab, id)
```

**Recording** (in `App` effect in `app.js`):
- When `activeSessionId` changes, compute the tab via `getFilterTabForSession(session)`
- Record the conversation under that tab using `setLastActiveSessionIdForTab(tab, id)`
- Use a guard ref `(prevTab, prevSession)` to avoid redundant localStorage writes during streaming re-renders

**Restoring** (in `SessionList.handleFilterTabChange` click handler):
- On user tab click, fetch the last-focused conversation for that tab via `getLastActiveSessionIdForTab(tab)`
- Only restore if the session still exists AND still belongs to that tab (categories can change: archived → unarchived)
- Programmatic tab changes (e.g., unarchive which explicitly selects a session) skip restoration — only user clicks trigger restore

**Design rationale**: Restore logic lives *only* in the user-click handler (not the global filter-change event) to avoid races with programmatic tab switches that have their own session selection logic.

## Cross-Workspace Child Sessions: Folder Group Key

When `groupingMode === "folder"` and session has `parent_session_id`, resolve the root parent's `working_dir` through the parent chain — not the child's own dir (would collapse the parent's folder group).

```javascript
if (storedSession?.parent_session_id && groupingMode === "folder") {
  const rootParent = resolveRootParentFromList(storedSessions, storedSession.parent_session_id);
  if (rootParent?.working_dir) groupKey = rootParent.working_dir || "Unknown";
}
```
