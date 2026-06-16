---
description: Frontend component structure, Preact/HTM, CDN configuration, lib.js utilities, markdown rendering, context menu positioning anti-patterns, daisyUI drawer compositing
globs:
  - "web/static/app.js"
  - "web/static/index.html"
  - "web/static/preact-loader.js"
  - "web/static/lib.js"
  - "web/static/lib.test.js"
  - "web/static/components/*.js"
  - "web/static/styles.css"
keywords:
  - Preact
  - HTM
  - frontend component
  - context menu
  - positioning
  - useMemo
  - lib.js
  - markdown rendering
  - renderUserMarkdown
  - daisyUI drawer
  - GPU compositing
  - will-change
---

# Web Frontend Core Patterns

## Technology Stack

- **No build step**: Preact + HTM loaded from local vendor files
- **Styling**: Pre-built Tailwind CSS (`make tailwind` to regenerate). **Must rebuild** when adding new Tailwind classes not already in `tailwind.css`
- **Markdown**: marked.js + DOMPurify for user message rendering
- **Diagrams**: Mermaid.js loaded dynamically from CDN when needed
- **Single binary**: All assets embedded via `go:embed` in `web/embed.go`

## Frontend Component Structure

```
App
├── SessionList (sidebar)
├── Header (connection status)
├── MessageList → Message (user/agent/thought/tool/error)
├── QueueDropdown (above ChatInput)
├── ChatInput (textarea, send/stop, prompts, queue)
└── Dialogs (Settings, Workspace, Rename, Delete, etc.)
```

## File Structure

| File                           | Purpose                                       |
| ------------------------------ | --------------------------------------------- |
| `app.js`                       | Main app, state management, ContextMenu, SessionItem |
| `lib.js`                       | Pure utility functions (testable without DOM) |
| `preact-loader.js`             | CDN imports, Mermaid integration              |
| `components/ChatInput.js`      | Message composition with queue controls       |
| `components/QueueDropdown.js`  | Queue panel with roll-up animation            |
| `components/Message.js`        | Message rendering component                   |
| `components/Icons.js`          | SVG icon components                           |
| `components/SessionPanel.js`   | Unified side panel (Properties + User Data tabs) |
| `components/SettingsDialog.js` | Settings modal                                |
| `hooks/useWebSocket.js`        | WebSocket connection management               |
| `hooks/useResizeHandle.js`     | Drag-to-resize with mouse and touch           |
| `hooks/useSwipeNavigation.js`  | Mobile swipe gestures                         |
| `utils/api.js`                 | API URL helpers with prefix handling          |
| `utils/storage.js`             | localStorage utilities                        |
| `utils/native.js`              | macOS app detection and native functions      |
| `utils/models.js`              | Model context window sizes (`MODEL_CONTEXT_WINDOWS`, `getContextWindowSize`) |

## lib.js Core Functions

| Function                       | Purpose                                                |
| ------------------------------ | ------------------------------------------------------ |
| `computeAllSessions()`         | Merge active + stored sessions, sort by time           |
| `convertEventsToMessages()`    | Transform stored events to display messages            |
| `addMessageToSessionState()`   | Add message with automatic trimming                    |
| `getMaxSeq(events)`            | Get maximum sequence number (used for sync)            |
| `mergeMessagesWithSync()`      | Deduplicate and merge messages on reconnect            |
| `generatePromptId()`           | Generate unique prompt ID for delivery tracking        |
| `savePendingPrompt()`          | Save prompt to localStorage before sending             |
| `hasMarkdownContent(text)`     | Heuristic Markdown detection before rendering          |
| `renderUserMarkdown(text)`     | Render with marked.js + DOMPurify (graceful fallback)  |
| `looksLikeFilePath(s)`         | Detect if a string looks like a file/dir path          |

**Design principle**: Keep `lib.js` functions pure (no side effects, no DOM) for testability.

## Internal File Viewer URL Pattern

When rendering a value that might be a file path, use `looksLikeFilePath()` and build a viewer URL:

```javascript
import { looksLikeFilePath } from "../lib.js";
import { getAPIPrefix } from "../utils/index.js";

if (value && looksLikeFilePath(value)) {
  const apiPrefix = getAPIPrefix();
  const workspaceUUID = window.mittoCurrentWorkspaceUUID || "";
  const wsPath = window.mittoCurrentWorkspace || "";
  const relativePath = value.replace(/^\.\//, "");
  let viewerUrl = null;
  if (workspaceUUID) {
    viewerUrl = `${apiPrefix}/viewer.html?ws=${encodeURIComponent(workspaceUUID)}&path=${encodeURIComponent(relativePath)}`;
    if (wsPath) viewerUrl += `&ws_path=${encodeURIComponent(wsPath)}`;
  }
  // render <a class="file-link" href=${viewerUrl}>
}
```

**Critical**: `app.js` has a global `document.addEventListener("click", ...)` (~line 161) that matches `/viewer.html?` URLs. Component-level onClick handlers on file links MUST call both `e.preventDefault()` AND `e.stopPropagation()` — omitting `stopPropagation()` causes a double viewer window to open.

```javascript
// CORRECT: prevents bubbling to global handler
onClick=${(e) => {
  e.preventDefault();
  e.stopPropagation();  // required — without this, global handler also fires
  if (!viewerUrl) return;
  openViewer(viewerUrl);
}}
```

## Context Menu: Clamp Position with useLayoutEffect

To keep a menu on-screen near a window edge, measure it and reposition. Two
naive approaches both fail:

```javascript
// BAD: useEffect runs AFTER paint → visible position jump.
const [pos, setPos] = useState({x, y});
useEffect(() => setPos(calculatePosition(x, y)), [x, y]);

// BAD: useMemo keyed on a ref never recomputes — refs don't trigger re-renders,
// so the menu stays at its raw (overflowing) position. Even when an unrelated
// re-render happens, the memo reads the DOM BEFORE the new content commits, so a
// menu that grows (e.g. async-loaded items) is measured too short and clips.
const position = useMemo(() => { /* ...read menuRef.current... */ }, [x, y, menuRef.current]);
```

```javascript
// GOOD: useLayoutEffect runs synchronously BEFORE paint → no jump, and measures
// the committed DOM so growth is handled. Key on item COUNT so it re-runs when
// content changes; guard setState to avoid a render loop. Clamp top/left ≥ margin
// and add `max-h-[95vh] overflow-y-auto` so taller-than-viewport menus scroll.
const [position, setPosition] = useState({x, y});
useLayoutEffect(() => {
    const el = menuRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const m = 8;
    let newX = x, newY = y;
    if (newX + rect.width > window.innerWidth) newX = window.innerWidth - rect.width - m;
    if (newY + rect.height > window.innerHeight) newY = window.innerHeight - rect.height - m;
    newX = Math.max(m, newX); newY = Math.max(m, newY);
    setPosition((prev) => (prev.x === newX && prev.y === newY ? prev : {x: newX, y: newY}));
}, [x, y, items.length]);
```

Arbitrary `vh` max-heights are JIT-generated: only values already in
`web/static/tailwind.css` (e.g. `60vh`, `70vh`, `95vh`) work without a rebuild.

## Click Outside Detection

```javascript
useEffect(() => {
    if (!isOpen) return;
    const handler = (e) => { if (ref.current && !ref.current.contains(e.target)) onClose(); };
    const tid = setTimeout(() => document.addEventListener("mousedown", handler), 10);
    return () => { clearTimeout(tid); document.removeEventListener("mousedown", handler); };
}, [isOpen, onClose]);
```

## daisyUI Drawer GPU Compositing Bug

**Symptom**: When using daisyUI's `.drawer-side` (e.g., SessionPanel on the right) with a full-window overlay underneath, moving the mouse over the overlay shows a blank/ghost area.

**Root cause**: daisyUI's base `.drawer-side` panel child carries `will-change: transform` + a `translate` transition. This permanently promotes the panel to its own GPU layer. With a fixed-position overlay, **two competing slide animations run on different GPU layers** → the stale layer fails to invalidate on pointer-move.

**Anti-pattern** (ineffective): Adding `translateZ(0)` or scoped positioning. These have been reverted as they don't prevent the core issue.

**Verified fix** (in `styles.css`): Add an unlayered CSS rule that neutralizes the redundant compositing on the panel child:

```css
.drawer-end > .drawer-toggle ~ .drawer-side > :not(.drawer-overlay) {
  will-change: auto;
  translate: none;
  transition: none;
}
```

This rule:
- Targets only the `.-side` panel child (the one with compositing), not the overlay
- Removes `will-change: transform`, breaking the forced GPU promotion
- Neutralizes the competing `translate` transition
- Does NOT affect keyframe animations (e.g., `.properties-panel` slide), which use `animation` not `transition`
- Is safe to apply to any drawer except `.drawer-overlay`

## Adding New Session Capabilities to Frontend

1. **`useWebSocket.js`** — In `case "connected":` handler, add to session.info
2. **`app.js`** — Pass as prop: `myCapability=${sessionInfo?.my_capability ?? false}`
3. **Component** — Accept prop with default, use for conditional rendering
