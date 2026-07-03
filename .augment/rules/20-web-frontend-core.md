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

Use `looksLikeFilePath()` to detect paths, build viewer URL with `workspace UUID` + `path` params.

**Critical**: File link onClick must call **BOTH** `e.preventDefault()` AND `e.stopPropagation()` — omitting the latter causes double viewer windows (global click handler at line 161 in `app.js` fires too).

```javascript
onClick=${(e) => { e.preventDefault(); e.stopPropagation(); openViewer(viewerUrl); }}
```

## Context Menu Positioning: useLayoutEffect

Use `useLayoutEffect` (runs BEFORE paint) to clamp position, not `useEffect` or `useMemo` (both measure too late/early). Key on `items.length` to re-run on content changes. Clamp with `Math.max(margin, calculated)` and add `max-h-[95vh] overflow-y-auto` for scrolling.

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

## Adding Session Capabilities

Backend `connected` message → `useWebSocket.js` (add to session.info) → `app.js` (pass as prop) → Component (use).

## API Endpoint Registry

**MANDATORY**: All API calls use `endpoints` from `web/static/utils/endpoints.js`. Never hardcode URL strings.

```javascript
// ✅ Correct
const url = endpoints.sessions.get(sessionId);
const url = endpoints.workspacePrompts.list({ working_dir: dir, session_id: id });
const ws = new WebSocket(endpoints.sessions.ws(sessionId));

// ❌ Wrong
const url = apiUrl(`/api/sessions/${sessionId}`);
const url = apiUrl("/api/workspace-prompts?working_dir=" + encodeURIComponent(dir));
```

The `qs()` helper omits `undefined`/`null`/`""` params and uses `URLSearchParams`. WebSocket builders return `wss://...` via `wsUrl()`. See `.augment/rules/20-web-frontend-core.md` docs or `web/static/utils/endpoints.js` for full builder list.
