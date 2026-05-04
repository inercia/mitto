---
description: Frontend component structure, Preact/HTM, CDN configuration, lib.js utilities, markdown rendering, context menu positioning anti-patterns
globs:
  - "web/static/app.js"
  - "web/static/index.html"
  - "web/static/preact-loader.js"
  - "web/static/lib.js"
  - "web/static/lib.test.js"
  - "web/static/components/*.js"
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
---

# Web Frontend Core Patterns

## Technology Stack

- **No build step**: Preact + HTM loaded from local vendor files
- **Styling**: Tailwind CSS via Play CDN
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
| `components/SettingsDialog.js` | Settings modal                                |
| `hooks/useWebSocket.js`        | WebSocket connection management               |
| `hooks/useResizeHandle.js`     | Drag-to-resize with mouse and touch           |
| `hooks/useSwipeNavigation.js`  | Mobile swipe gestures                         |
| `utils/api.js`                 | API URL helpers with prefix handling          |
| `utils/storage.js`             | localStorage utilities                        |
| `utils/native.js`              | macOS app detection and native functions      |

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

## Context Menu: Use useMemo Not useState+useEffect

```javascript
// BAD: runs after paint → visible position jump
const [pos, setPos] = useState({x, y});
useEffect(() => setPos(calculatePosition(x, y)), [x, y]);

// GOOD: synchronous during render, no jump
const position = useMemo(() => {
    if (!menuRef.current) return {x, y};
    const rect = menuRef.current.getBoundingClientRect();
    let newX = x, newY = y;
    if (x + rect.width > window.innerWidth) newX = window.innerWidth - rect.width - 8;
    if (y + rect.height > window.innerHeight) newY = window.innerHeight - rect.height - 8;
    return {x: newX, y: newY};
}, [x, y, menuRef.current]);
```

## Click Outside Detection

```javascript
useEffect(() => {
    if (!isOpen) return;
    const handler = (e) => { if (ref.current && !ref.current.contains(e.target)) onClose(); };
    const tid = setTimeout(() => document.addEventListener("mousedown", handler), 10);
    return () => { clearTimeout(tid); document.removeEventListener("mousedown", handler); };
}, [isOpen, onClose]);
```

## Adding New Session Capabilities to Frontend

1. **`useWebSocket.js`** — In `case "connected":` handler, add to session.info
2. **`app.js`** — Pass as prop: `myCapability=${sessionInfo?.my_capability ?? false}`
3. **Component** — Accept prop with default, use for conditional rendering


