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

## Component Inventory

| Component                    | File                              | Purpose                                              |
| ---------------------------- | --------------------------------- | ---------------------------------------------------- |
| `ChatInput`                  | `ChatInput.js`                    | Message composition, images, prompts dropdown, queue |
| `QueueDropdown`              | `QueueDropdown.js`                | Queued messages panel with delete/move actions       |
| `Message`                    | `Message.js`                      | Renders user/agent/tool/error messages               |
| `ConfirmDialog`              | `ConfirmDialog.js`                | Reusable modal confirmation dialog; supports `children` prop for extra content below message |
| `SettingsDialog`             | `SettingsDialog.js`               | Configuration, workspaces, auth                      |
| `Icons`                      | `Icons.js`                        | SVG icon components                                  |
| `SessionPanel`               | `SessionPanel.js`                 | Unified right-side overlay with tabs (Changes + Properties + User Data) |
| `ToastContainer`             | `ToastContainer.js`               | Renders toast stack, color-coded by severity         |
| `PeriodicPromptSelector`     | `PeriodicPromptSelector.js`       | Dropdown to select a workspace prompt by name for periodic execution |
| `PeriodicFrequencyPanel`     | `PeriodicFrequencyPanel.js`       | Frequency controls; `disabled=true` shows "run now" icon but controls (input/select/time) stay editable |
| `ContextMenu`                | `ContextMenu.js`                  | Shared right-click menu with viewport-aware positioning + hover-flyout submenus (used by SessionItem/SessionList and BeadsView) |
| `SessionItem`                | `app.js`                          | Session list item with swipe, context menu, status   |

## ChatInput

### Layout: "Contained Composition" (GitHub-style)

Single bordered container: textarea (no own border) + bottom toolbar with left/center/right sections.

- **Outer container**: provides border and rounded corners — textarea has no own border
- **Bottom toolbar left**: improve-prompt (magic wand), attach-image, attach-file, save-prompt (native only), clear/delete
- **Bottom toolbar center**: config selectors (model, mode — only when `configOptions` has `type === "select"`) + context usage percentage
- **Bottom toolbar right**: queue-toggle (visible when queue non-empty or dropdown open), prompts-toggle, enqueue (visible while streaming), send/stop/lock
- **No floating action-toolbar** — all actions are in the always-visible bottom bar

> **Anti-pattern**: Do NOT use the old "textarea + external button column" layout. The floating toolbar that appeared on focus has been removed.

### Config Selectors (configOptions)

ChatInput accepts `configOptions` (array) and `onSetConfigOption` (callback) props from `app.js`. The center bar shows **all** `type === "select"` options (e.g. "Mode" and "Model"):

```javascript
const selectConfigOptions = useMemo(() => {
  return configOptions?.filter(o => o.type === "select" && o.options?.length > 0) || [];
}, [configOptions]);
// Renders one <select> per option; hidden when array is empty
// Each: <select disabled=${isStreaming} onInput=${e => onSetConfigOption?.(opt.id, e.target.value)}>
```

- Disabled while streaming; hidden when no select-type config options exist
- CSS classes: `chat-input-model-selector` (container) / `chat-input-model-select` (each `<select>`)
- **Anti-pattern**: Do NOT use `find()` to show only the first option — use `filter()` to show all

### Context Usage Percentage (center bar)

**Primary**: `context_usage` from ACP `SessionUsageUpdate` (UNSTABLE — most agents don't send it). **Fallback**: `tokenUsage.input_tokens ÷ getContextWindowSize(modelId)` from `utils/models.js`. Implement both.

### Keyboard Shortcuts

**Shortcuts**: `Enter`=send · `Shift+Enter`=new line · `Cmd/Ctrl+Enter`=queue

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `GripIcon`, `QueueIcon`, `TagIcon`). Always use `CloseIcon` SVG — never plain text `✕`. Small: `w-4 h-4` (toasts), Large: `w-5 h-5` (dialogs).

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

## useResizeHandle / useSwipeNavigation / New Hooks

- `useResizeHandle`: mouse+touch drag for height. Spread `handleProps` on handle element. ChatInput uses **two instances**: one for QueueDropdown panel height, one for textarea max-height (min: 80px, default: 200px, max: 500px, persisted in `mitto_ui_textarea_max_height` localStorage key).
- `useSwipeNavigation`: swipe left/right with threshold, edge detection. 500ms window.

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
