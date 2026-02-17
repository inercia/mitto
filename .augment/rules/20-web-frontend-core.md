---
description: Frontend component structure, file organization, Preact/HTM patterns, and CDN configuration
globs:
  - "web/static/app.js"
  - "web/static/index.html"
  - "web/static/styles*.css"
  - "web/static/preact-loader.js"
  - "web/static/theme-loader.js"
  - "web/static/tailwind-config*.js"
keywords:
  - Preact
  - HTM
  - Tailwind
  - CDN
  - frontend structure
  - component hierarchy
---

# Web Frontend Core Patterns

## Technology Stack

- **No build step**: Preact + HTM loaded from local vendor files
- **Styling**: Tailwind CSS via Play CDN
- **Markdown**: marked.js + DOMPurify for user message rendering
- **Diagrams**: Mermaid.js loaded dynamically from CDN when needed
- **Embedding**: `go:embed` directive in `web/embed.go`
- **Single binary**: All assets embedded in Go binary

## Frontend Component Structure

```
App
├── SessionList (sidebar, hidden on mobile)
├── Header (connection status, streaming indicator)
├── MessageList
│   └── Message (user/agent/thought/tool/error/system)
├── QueueDropdown (above ChatInput, roll-up animation)
├── ChatInput
│   ├── Textarea + image preview
│   ├── Send/Stop button group with prompts dropdown
│   └── Queue button group (Add to Queue | Toggle Panel)
└── Dialogs
    ├── SettingsDialog (configuration, workspaces, auth)
    ├── WorkspaceDialog (workspace selection for new session)
    ├── KeyboardShortcutsDialog (help for shortcuts)
    ├── RenameDialog (rename session)
    ├── DeleteDialog (confirm session deletion)
    └── CleanInactiveDialog (bulk delete inactive sessions)
```

## File Structure

| File                           | Purpose                                       |
| ------------------------------ | --------------------------------------------- |
| `app.js`                       | Main Preact application, state management     |
| `lib.js`                       | Pure utility functions (testable without DOM) |
| `lib.test.js`                  | Jest tests for lib.js                         |
| `preact-loader.js`             | CDN imports, library initialization           |
| `styles.css` / `styles-v2.css` | Custom CSS for Markdown rendering             |
| `index.html`                   | HTML shell, Tailwind config                   |
| `components/ChatInput.js`      | Message composition with queue controls       |
| `components/QueueDropdown.js`  | Queue panel with roll-up animation            |
| `components/Message.js`        | Message rendering component                   |
| `components/Icons.js`          | SVG icon components                           |
| `components/SettingsDialog.js` | Settings modal                                |
| `hooks/useWebSocket.js`        | WebSocket connection management               |
| `hooks/useSwipeNavigation.js`  | Mobile swipe gestures                         |
| `utils/api.js`                 | API URL helpers with prefix handling          |
| `utils/storage.js`             | localStorage utilities                        |
| `utils/native.js`              | macOS app detection and native functions      |
| `utils/csrf.js`                | CSRF token handling for secure requests       |

## CDN Selection for Frontend Libraries

**Recommended CDN for ES modules**: Skypack (`cdn.skypack.dev`)

- Handles internal module resolution correctly
- Works with Preact hooks imports

**Avoid for ES modules**:

- `unpkg.com` and `jsdelivr.net` - May fail with "Failed to resolve module specifier"
- `esm.sh` - Generally works but may have availability issues

```html
<!-- Recommended -->
<script type="module">
  import { h, render } from "https://cdn.skypack.dev/preact@10.19.3";
  import {
    useState,
    useEffect,
    useLayoutEffect,
    useRef,
    useCallback,
  } from "https://cdn.skypack.dev/preact@10.19.3/hooks";
  import htm from "https://cdn.skypack.dev/htm@3.1.1";
</script>
```

**Note**: This project bundles libraries via `window` globals in `preact-loader.js`:

```javascript
// preact-loader.js - loads and exposes libraries
import { marked } from "https://cdn.skypack.dev/marked@12.0.0";
import DOMPurify from "https://cdn.skypack.dev/dompurify@3.0.8";

window.preact = {
  h,
  render,
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  useMemo,
  html,
};
window.marked = marked;
window.DOMPurify = DOMPurify;

// app.js - imports from window.preact bundle
const {
  h,
  render,
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  useMemo,
  html,
} = window.preact;
```

## Mermaid Diagram Integration

Mermaid.js is loaded dynamically from CDN only when mermaid diagrams are present:

```javascript
// preact-loader.js - Mermaid integration
window.mermaidReady = false;
window.mermaidLoading = false;
window.mermaidRenderQueue = [];
window.mermaidSvgCache = new Map();  // Cache for streaming updates

// Load from CDN when needed
async function loadMermaid() {
  const script = document.createElement("script");
  script.src = "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js";
  // ...
}

// Render diagrams in a container
window.renderMermaidDiagrams = renderMermaidInContainer;
```

**Usage in Message component:**

```javascript
// Message.js - trigger rendering after HTML insertion
useEffect(() => {
  if (agentMessageRef.current && window.renderMermaidDiagrams) {
    window.renderMermaidDiagrams(agentMessageRef.current);
  }
}, [message.html]);
```

**How it works:**

1. Backend converts ` ```mermaid` to `<pre class="mermaid">`
2. `renderMermaidDiagrams()` finds `<pre class="mermaid">` elements
3. Loads Mermaid.js from CDN if not already loaded
4. Renders diagrams to SVG, replacing `<pre>` with `<div class="mermaid-diagram">`
5. Caches rendered SVGs for streaming updates (content-based hash)

**Known issue**: Firefox/Safari tracking protection may block CDN resources.

## Memory Management

```javascript
// MAX_MESSAGES prevents memory issues in long sessions
// Set high enough to allow meaningful history loading (500+ messages)
export const MAX_MESSAGES = 500;

// Messages auto-trimmed when added
const newMessages = limitMessages([...session.messages, message]);
```

## Dual Validation (Frontend + Backend)

For destructive operations, implement validation in both layers:

1. **Frontend (immediate feedback)**: Check constraints before allowing action
2. **Backend (security)**: Always validate even if frontend checks

```javascript
// Frontend: Check workspace usage before removal
const removeWorkspace = (workingDir) => {
  const count = storedSessions.filter(
    (s) => s.working_dir === workingDir,
  ).length;
  if (count > 0) {
    setError(`Cannot remove: ${count} conversation(s) using it`);
    return;
  }
  // Proceed with removal
};
```
