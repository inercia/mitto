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
  - SessionItem
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
| `ConversationPropertiesPanel`| `ConversationPropertiesPanel.js`  | Right-side overlay panel for session properties      |
| `UserDataPanel`              | `UserDataPanel.js`                | Right-side overlay panel for user data / metadata    |
| `ToastContainer`             | `ToastContainer.js`               | Renders toast stack, color-coded by severity         |
| `ContextMenu`                | `app.js`                          | Right-click menus with viewport-aware positioning    |
| `SessionItem`                | `app.js`                          | Session list item with swipe, context menu, status   |

## ChatInput

### Button Group Layout

```
[Send/Stop/Full] | [^] Prompts     ← Top row
[+ Add to Queue] | [≡] Queue Panel ← Bottom row
```

- **Outer container**: provides the border and rounded corners — textarea has no own border
- **Bottom toolbar left**: attach-image, attach-file, improve-prompt, save-prompt
- **Bottom toolbar center**: config selectors (all `type === "select"` configOptions)
- **Bottom toolbar right**: queue-add, queue-toggle, **prompts-toggle**, send/stop/lock

### Keyboard Shortcuts

| Keys             | Action       |
| ---------------- | ------------ |
| `Enter`          | Send message |
| `Shift+Enter`    | New line     |
| `Cmd/Ctrl+Enter` | Add to queue |

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

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `GripIcon`, `QueueIcon`, `TagIcon`). Always use `CloseIcon` SVG — never plain text `✕`. Small: `w-4 h-4` (toasts), Large: `w-5 h-5` (dialogs).

## Side Panel Overlay Pattern

`ConversationPropertiesPanel` and `UserDataPanel` share the same fixed-overlay structure. **Only one panel may be open at a time** — the parent (`app.js`) manages `activePanel` state and passes `isOpen` accordingly.

### Animation Pattern (isClosing / shouldRender)

```javascript
const [isClosing, setIsClosing] = useState(false);
const [shouldRender, setShouldRender] = useState(isOpen);

useEffect(() => {
  if (isOpen) { setShouldRender(true); setIsClosing(false); }
  else if (shouldRender) {
    setIsClosing(true);
    const t = setTimeout(() => { setShouldRender(false); setIsClosing(false); }, 150);
    return () => clearTimeout(t);
  }
}, [isOpen, shouldRender]);

if (!shouldRender) return null;
```

### DOM Structure

```javascript
// Fixed right-side overlay: backdrop left, panel right
html`<div class="fixed inset-0 z-50 flex">
  <div class="flex-1 bg-black/50 properties-backdrop ${isClosing ? 'closing' : ''}"
       onClick=${handleClose} />
  <div class="w-80 bg-mitto-sidebar ... properties-panel ${isClosing ? 'closing' : ''}">
    ${renderPanelContent()}
  </div>
</div>`
```

CSS classes `properties-panel` and `properties-backdrop` control slide-in/fade-in animations. Adding `closing` triggers the exit animation (150ms).

### Icon Convention

Use `TagIcon` for user data / metadata panels (defined in `Icons.js`).

## useToast Hook (Unified Notification System)

**All in-app notifications must go through `useToast`** — never add standalone toast state/timers in `app.js`.

```javascript
const { showToast, dismissToast, toasts } = useToast();
showToast({ message: "Saved", style: "success" }); // auto-dismiss 5s
showToast({ message: "Failed", style: "error" });   // auto-dismiss 10s
showToast({ message: "Pinned", sticky: true });      // no auto-dismiss
```

Severity durations: info/success=5s, warning/error=10s. Max 5 simultaneous (oldest evicted). Render via `<ToastContainer toasts=${toasts} onDismiss=${dismissToast} />`.

**Style selection**: `error` (red) = actual errors only. Use `info` for neutral events. **Anti-pattern**: never use `error` for non-error notifications — red implies urgency.

**v2 theme CSS anti-pattern**: `styles-v2.css` globally remaps `.text-white` → near-black and `bg-blue-600` → red. Components with semantic colors (info=blue, not red) need scoped overrides using their wrapper class. Toast fixes use `.v2-theme .toast-enter .bg-*` and `.v2-theme .toast-enter .text-white`. See existing patterns at `styles-v2.css` ~lines 836–910.

## useResizeHandle / useSwipeNavigation / New Hooks

- `useResizeHandle`: mouse+touch drag for height. Spread `handleProps` on handle element. ChatInput uses **two instances**: one for QueueDropdown panel height, one for textarea max-height (min: 80px, default: 200px, max: 500px, persisted in `mitto_ui_textarea_max_height` localStorage key).
- `useSwipeNavigation`: swipe left/right with threshold, edge detection. 500ms window.
- **New hooks**: `use[Name].js`, export from `hooks/index.js`, use `window.preact` globals, cleanup in useEffect.

## Session List: Parent-Child UI Rules

- Children accordion: only one parent's children expanded at a time (accordion mode always on for `parent:*` groups)
- Group key format: `parent:${session.session_id}` — separate from folder-level keys
- `sessionFamilyMap` (useMemo) maps session IDs to their family key; used in `handleSelectWithCollapse`
- **Child restrictions**: cannot be archived, cannot be made periodic — hide/disable those actions in UI
