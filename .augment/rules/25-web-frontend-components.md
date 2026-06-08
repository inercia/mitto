---
description: Frontend UI components, custom hooks (useResizeHandle, useSwipeNavigation, useToast), ChatInput, QueueDropdown, ToastContainer, Icons, side panels, component patterns
globs:
  - "web/static/components/*.js"
  - "web/static/components/**/*"
  - "web/static/hooks/*.js"
  - "web/static/app.js"
keywords:
  - ChatInput
  - QueueDropdown
  - Icons
  - SessionList
  - SessionItem
  - accordion
  - children
  - expand
  - collapse
  - ContextMenu
  - useResizeHandle
  - useSwipeNavigation
  - ConversationPropertiesPanel
  - UserDataPanel
  - side panel
  - overlay
  - useToast
  - ToastContainer
  - toast
  - component
  - hook
---

# Frontend Components and Hooks

All components use Preact/HTM with window globals: `const { useState, useEffect, useRef, html } = window.preact;`

## Key Components

| Component           | Purpose                                       |
| ------------------- | --------------------------------------------- |
| `ChatInput`         | Composition, images, prompts, queue           |
| `QueueDropdown`     | Queued messages panel                         |
| `Message`           | User/agent/tool/error messages                |
| `SettingsDialog`    | Settings modal                                |
| `SessionPanel`      | Unified overlay (Changes + Properties tabs)   |
| `ContextMenu`       | Right-click menu with viewport-aware position |
| `SessionItem`       | List item with swipe, menu, status            |

## ChatInput

Single bordered container: textarea + bottom toolbar (left/center/right). **No external button column** — all actions in always-visible bottom bar.

- **Center bar**: config selectors + context usage % (use `filter()` not `find()`)
- **Context %**: Primary from ACP `context_usage`, fallback: `input_tokens ÷ getContextWindowSize()`
- **Shortcuts**: `Enter`=send · `Shift+Enter`=newline · `Cmd/Ctrl+Enter`=queue

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `QueueIcon`). Always `CloseIcon` SVG, never `✕`. Sizes: `w-4 h-4` (toasts), `w-5 h-5` (dialogs).

## Side Panel Overlay Pattern

`SessionPanel` is a unified tabbed panel that replaced the old separate `ConversationPropertiesPanel` and `UserDataPanel`. It has three tabs: **Changes**, **Properties**, and **User Data** (in that order). The parent (`app.js`) manages open/close state; default tab is `"changes"`.

### Changes Tab
Fetches `GET /api/sessions/{id}/changes` when active. Displays branch name, file count, refresh button, and file list with color-coded status badges (A=green, M=amber, D=red, R=blue, ?=gray) and `+N/-N` stats. Clicking a file opens the viewer in diff mode (`view=diff` param).

Animation: `isClosing`/`shouldRender` pair for slide-in/slide-out (150ms). Use `TagIcon` for metadata panels.

## useToast Hook (Unified Notification System)

**All in-app notifications must go through `useToast`** — never add standalone toast state/timers in `app.js`.

```javascript
const { showToast, dismissToast, toasts } = useToast();
showToast({ message: "Saved", style: "success" }); // auto-dismiss 5s
showToast({ message: "Pinned", sticky: true });     // no auto-dismiss
```

Severity durations: info/success=5s, warning/error=10s. Max 5 simultaneous. Render via `<ToastContainer toasts=${toasts} onDismiss=${dismissToast} />`. Use `error` (red) for actual errors only.

## useResizeHandle / useSwipeNavigation

- `useResizeHandle`: drag to resize. ChatInput uses two instances (QueueDropdown + textarea; max-height in `mitto_ui_textarea_max_height` key)
- `useSwipeNavigation`: swipe left/right with threshold, 500ms window

## Hooks in Conditionals (Anti-Pattern)

Preact/React hooks must be called unconditionally. When a hook is needed inside a conditional, extract a dedicated sub-component:

```javascript
// BAD: useState inside if-block — violates Rules of Hooks
function Message({ isThought }) {
  if (isThought) {
    const [collapsed, setCollapsed] = useState(true);  // ❌
    return html`...`;
  }
}

// GOOD: extract ThoughtBubble so hooks always run at top level
function ThoughtBubble({ message }) {
  const [collapsed, setCollapsed] = useState(true);  // ✓ always called
  return html`...`;
}
function Message({ isThought, ...props }) {
  if (isThought) return html`<${ThoughtBubble} ...${props} />`;
}
```

Define extracted components in the **same file** — no new files needed.

## Session List Tab Filtering

The SessionList component filters conversations by a tab (Conversations, Periodic, Archived) derived via `getFilterTabForSession(session)`:

```javascript
function getFilterTabForSession(session) {
  if (session.archived) return "archived";
  if (session.periodic_enabled) return "periodic";
  return "conversations";
}
```

**Multi-click behavior**: Clicking a tab button fires `handleFilterTabChange` which:
1. Sets the active filter tab (updates UI list)
2. If the session for that tab still exists and belongs to it, restores the last-focused conversation via `getLastActiveSessionIdForTab(tab)`
3. Only user clicks trigger restoration (programmatic changes skip it)

**Guard against races**: Keep `(prevTab, prevSession)` refs in the recording effect to avoid redundant localStorage updates during streaming.
