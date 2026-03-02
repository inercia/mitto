---
description: Frontend UI components, custom hooks (useResizeHandle, useSwipeNavigation), ChatInput, QueueDropdown, Icons, component patterns
globs:
  - "web/static/components/*.js"
  - "web/static/components/**/*"
  - "web/static/hooks/*.js"
keywords:
  - ChatInput
  - QueueDropdown
  - Icons
  - ContextMenu
  - SessionItem
  - useResizeHandle
  - useSwipeNavigation
  - component
  - hook
---

# Frontend Components and Hooks

All components use Preact/HTM with window globals: `const { useState, useEffect, useRef, html } = window.preact;`

## Component Inventory

| Component        | File                | Purpose                                              |
| ---------------- | ------------------- | ---------------------------------------------------- |
| `ChatInput`      | `ChatInput.js`      | Message composition, images, prompts dropdown, queue |
| `QueueDropdown`  | `QueueDropdown.js`  | Queued messages panel with delete/move actions       |
| `Message`        | `Message.js`        | Renders user/agent/tool/error messages               |
| `SettingsDialog` | `SettingsDialog.js` | Configuration, workspaces, auth                      |
| `Icons`          | `Icons.js`          | SVG icon components                                  |
| `ContextMenu`    | `app.js`            | Right-click menus with viewport-aware positioning    |
| `SessionItem`    | `app.js`            | Session list item with swipe, context menu, status   |

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

### Resizable Height

```javascript
const { height, isDragging, handleProps } = useResizeHandle({
    initialHeight: getQueueDropdownHeight(),
    minHeight: 100, maxHeight: 500,
    onDragEnd: (h) => setQueueDropdownHeight(h),
});
```

### Auto-Close

Closes after 5s inactivity. Paused on hover and during drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `GripIcon`, `QueueIcon`).

## useResizeHandle Hook

```javascript
const { height, isDragging, handleProps } = useResizeHandle({
    initialHeight: 256, minHeight: 100, maxHeight: 500,
    onDragStart: () => { /* pause timers */ },
    onDragEnd: (finalHeight) => { /* persist */ },
});
// Spread handleProps on resize handle element
```

- Mouse + touch support
- Dragging up increases height
- Sets `user-select: none` and `cursor: ns-resize` during drag

## useSwipeNavigation Hook

```javascript
useSwipeNavigation(containerRef, onSwipeLeft, onSwipeRight, {
    threshold: 80, maxVertical: 80, edgeWidth: 40,
    onEdgeSwipeRight: openSidebar,
});
```

- Swipe must complete within 500ms
- Horizontal > threshold, vertical < maxVertical
- Edge swipes detected by start position within edgeWidth of screen edge

## Creating New Hooks

1. File naming: `use[Name].js`
2. Export from `hooks/index.js`
3. Use `window.preact` globals
4. Always return cleanup from useEffect
5. Include touch events for mobile
