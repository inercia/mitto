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

**Design principle**: Keep `lib.js` functions pure (no side effects, no DOM) for testability.

## User Message Markdown Rendering

```
User text → hasMarkdownContent() → false → Plain text (<pre>)
                                 → true → Length > 10K? → Plain text
                                        → marked.parse() → DOMPurify → HTML display
```

## Memory Management

```javascript
export const MAX_MESSAGES = 500;
const newMessages = limitMessages([...session.messages, message]);
```

## Context Menu Positioning Anti-Pattern

### Don't: useState + useEffect for Position

```javascript
// BAD: First render uses stale values, menu jumps
const [adjustedPos, setAdjustedPos] = useState({ x, y });
useEffect(() => {
    // Runs AFTER paint - visible position jump
    setAdjustedPos(calculateAdjustedPosition(x, y));
}, [x, y]);
```

### Do: useMemo for Synchronous Calculation

```javascript
// GOOD: Position calculated during render, no jump
const position = useMemo(() => {
    if (!menuRef.current) return { x, y };
    const rect = menuRef.current.getBoundingClientRect();
    let newX = x, newY = y;
    if (x + rect.width > window.innerWidth) newX = window.innerWidth - rect.width - 8;
    if (y + rect.height > window.innerHeight) newY = window.innerHeight - rect.height - 8;
    return { x: newX, y: newY };
}, [x, y, menuRef.current]);
```

## Click Outside Detection

```javascript
// Delay listener to avoid catching the opening click
useEffect(() => {
    if (!isOpen) return;
    const handler = (e) => {
        if (ref.current && !ref.current.contains(e.target)) onClose();
    };
    const tid = setTimeout(() => document.addEventListener("mousedown", handler), 10);
    return () => { clearTimeout(tid); document.removeEventListener("mousedown", handler); };
}, [isOpen, onClose]);
```

## Dual Validation (Frontend + Backend)

For destructive operations, validate in both layers. Frontend for immediate feedback, backend for security.
