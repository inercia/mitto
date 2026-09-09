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

## Streaming Bottom-Growth: NO `scrollTop` Compensation (mitto-u5r)

`.messages-container-reverse` is a **normal top-anchored** `display:block` scroller (`scrollTop=0` = visual top). `flex-col-reverse` is on the **inner** wrapper only (DOM ordering) — it does NOT make the scroller bottom-anchored. Bottom growth extends the document below the viewport, so a scrolled-up reader's `scrollTop` is naturally stable in both WebKit and Chromium.

- **Anti-pattern**: `container.scrollTop += scrollHeightDelta` on bottom growth CAUSES per-chunk upward drift of `delta` px — it does not fix drift. The only legitimate `scrollTop += heightDiff` in `useScrollManagement.js` is prepend/"load more" restoration (top growth). Top and bottom growth are **not symmetric**.
- Regression guard: `useScrollManagement.test.js` asserts `scrollTop` stays put on streaming bottom growth. See memory `streaming-scroll-drift-compensation-mitto-u5r` for empirical proof.
- **Layout-probe fixtures**: For scroll/anchoring questions prefer a standalone HTML fixture replicating the exact CSS run on Playwright WebKit + Chromium — headless-WebKit-against-the-live-app is too flaky (WebKit crashes, composer race) for a clean A/B.

## Two-Signal Scroll Pattern (mitto-47l)

`useScrollManagement` exports **two independent** scroll-tied signals — do NOT
substitute one for the other:

| Signal | Threshold | Purpose |
|--------|-----------|---------|
| `isUserAtBottom` | tight (~50px) | prompt-responsive: auto-scroll on new content, show "scroll-to-bottom" button |
| `isScrolledUp` | asymmetric hysteresis (collapse >160px, expand <60px) + debounced collapse (250ms) | UI-affinity: mobile composer collapse — stays put across streaming layout jitter |

`isScrolledUp` is **not** the inverse of `isUserAtBottom`. The dead-band absorbs
the moving bottom during streaming; only the collapse (hide) transition is
debounced (expand is immediate); `scrollToBottom` cancels any pending collapse
so a streaming re-pin never commits a transient hide. Any new scroll-tied
UI-affinity transition (header condensation, toolbar auto-hide, etc.) should
reuse `isScrolledUp` — do NOT invent a fresh threshold-only or debounce-only
variant, and do NOT key UI-affinity off `isUserAtBottom`. See
`web/static/hooks/useScrollManagement.js` and the debounced-collapse describe
block in `web/static/components/ChatInput.test.js` for the reference impl and
regression pins.

## Adding New Session Properties (Checklist)

When adding a new field to session state (e.g., `loop_enabled`), **three places** in the frontend must all be updated or the value will be silently dropped:

| File | Location | What to add |
|------|----------|-------------|
| `useWebSocket.js` | `activeSessions = useMemo(...)` block (~line 1062) | Explicit field mapping from `data.info?.field` |
| `useWebSocket.js` | Fingerprint string (~line 1090) | `\|${s.field}` so changes trigger re-renders |
| `lib.js` | `computeAllSessions` "no stored session" case (~line 444) | Field with default value |

**Anti-pattern**: WebSocket handler (`loop_updated`) sets `sessions[id].info.loop_enabled` correctly, but if `activeSessions` useMemo doesn't forward it, the property is lost when `computeAllSessions` runs.

## Settings Dialog Patterns

When saving settings that affect external state, update local state immediately after (e.g., re-fetch port/status). Some deployments use file-based config with `configReadonly` flag — skip settings dialog if true.

## Per-Tab Active Conversation State

Each filter tab (Conversations, Loop, Archived) remembers its own last-focused conversation. Storage helpers: `getLastActiveSessionIdForTab(tab)` / `setLastActiveSessionIdForTab(tab, id)` in `storage.js`.

**Recording**: In `App` effect, compute tab via `getFilterTabForSession()`, record with guard ref to avoid redundant writes during streaming.

**Restoring**: Only in click handler (`SessionList.handleFilterTabChange`), restore if session still exists and belongs to tab. Programmatic tab changes (e.g., unarchive) skip restoration to avoid races.

## Per-Folder Loading State

**Pattern**: When creating a new conversation, scope the loading spinner to a specific `workingDir` rather than showing a global spinner. This prevents UX confusion when multiple folders have pending operations.

**Implementation**: Store loading state as a map keyed by `workingDir`:
```javascript
const [newConversationLoading, setNewConversationLoading] = useState({}); // { workingDir: true }
const isLoadingForFolder = newConversationLoading[workingDir];
```

**Usage**: Only show spinner in the folder's section when `newConversationLoading[workingDir] === true`. Clear the flag per-folder after the session response arrives.

## Cross-Workspace Child Sessions: Folder Group Key

When `groupingMode === "folder"` and session has `parent_session_id`, resolve the root parent's `working_dir` through the parent chain — not the child's own dir (would collapse the parent's folder group).

```javascript
if (storedSession?.parent_session_id && groupingMode === "folder") {
  const rootParent = resolveRootParentFromList(storedSessions, storedSession.parent_session_id);
  if (rootParent?.working_dir) groupKey = rootParent.working_dir || "Unknown";
}
```
