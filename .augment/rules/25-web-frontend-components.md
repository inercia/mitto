---
description: Frontend UI components, custom hooks (useResizeHandle, useSwipeNavigation), ChatInput, QueueDropdown, Icons, component patterns
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


## Session List: Parent-Child UI Rules

### Accordion Mode for Children (Always On)

Children groups **always** use accordion mode — only one parent's children can be expanded at a time. This is enforced in two ways, regardless of the global `single_expanded_group` config setting:

**1. Expand toggle:** When expanding children of a conversation, children in all other conversations are automatically collapsed. Implemented in `handleToggleGroup`:
```javascript
const isParentGroup = groupKey.startsWith("parent:");
if (willExpand && (getSingleExpandedGroupMode() || isParentGroup)) {
  // Collapse all other groups in the same category
}
```

**2. Session selection:** When clicking on any session that doesn't belong to the currently expanded "family" (parent + its children), all expanded parent-child groups are collapsed. Implemented via `handleSelectWithCollapse` which wraps `onSelect`:
- `sessionFamilyMap` (useMemo) maps every session ID to its family's `parent:${id}` key
- On select, finds expanded `parent:*` groups and collapses those that don't match the selected session's family

Parent-child group keys use the format `parent:${session.session_id}`. These are kept separate from folder-level group keys so that toggling a session's children doesn't collapse the workspace folder.

### Child Session Restrictions in UI

These backend rules should be reflected in UI behavior:
- **Children cannot be archived** — only deleted when parent is archived (cascade delete)
- **Children cannot be made periodic** — only top-level sessions can have periodic config
- **Children cannot be directly archived** — the archive action should be hidden or disabled for child sessions
