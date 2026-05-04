---
description: Frontend UI components, custom hooks (useResizeHandle, useSwipeNavigation), ChatInput, QueueDropdown, Icons, side panels, component patterns
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
| `SettingsDialog`             | `SettingsDialog.js`               | Configuration, workspaces, auth                      |
| `Icons`                      | `Icons.js`                        | SVG icon components                                  |
| `ConversationPropertiesPanel`| `ConversationPropertiesPanel.js`  | Right-side overlay panel for session properties      |
| `UserDataPanel`              | `UserDataPanel.js`                | Right-side overlay panel for user data / metadata    |
| `ContextMenu`                | `app.js`                          | Right-click menus with viewport-aware positioning    |
| `SessionItem`                | `app.js`                          | Session list item with swipe, context menu, status   |

## ChatInput

### Button Group Layout

```
[Send/Stop/Full] | [^] Prompts     ← Top row
[+ Add to Queue] | [≡] Queue Panel ← Bottom row
```

### Keyboard Shortcuts

| Keys             | Action       |
| ---------------- | ------------ |
| `Enter`          | Send message |
| `Shift+Enter`    | New line     |
| `Cmd/Ctrl+Enter` | Add to queue |

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `GripIcon`, `QueueIcon`, `TagIcon`).

- Always use `CloseIcon` SVG — never plain text `✕`. Small: `w-4 h-4` (toasts), Large: `w-5 h-5` (dialogs)
- `TagIcon` = user data / metadata panels

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

## useResizeHandle Hook

```javascript
const { height, isDragging, handleProps } = useResizeHandle({
    initialHeight: 256, minHeight: 100, maxHeight: 500,
    onDragStart: () => { /* pause timers */ },
    onDragEnd: (finalHeight) => { /* persist */ },
});
// Spread handleProps on resize handle element
```

Mouse + touch support. Dragging up increases height. Sets `user-select: none` and `cursor: ns-resize` during drag.

## useSwipeNavigation Hook

```javascript
useSwipeNavigation(containerRef, onSwipeLeft, onSwipeRight, {
    threshold: 80, maxVertical: 80, edgeWidth: 40, onEdgeSwipeRight: openSidebar,
});
```

Swipe completes within 500ms; horizontal > threshold, vertical < maxVertical; edge swipes by start position within `edgeWidth`.

## Creating New Hooks

File naming: `use[Name].js`. Export from `hooks/index.js`. Use `window.preact` globals. Return cleanup from useEffect. Include touch events for mobile.
## Session List: Parent-Child UI Rules

- Children accordion: only one parent's children expanded at a time (accordion mode always on for `parent:*` groups)
- Group key format: `parent:${session.session_id}` — separate from folder-level keys
- `sessionFamilyMap` (useMemo) maps session IDs to their family key; used in `handleSelectWithCollapse`
- **Child restrictions**: cannot be archived, cannot be made periodic — hide/disable those actions in UI
