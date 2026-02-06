---
description: Custom Preact hooks for drag/resize, swipe navigation, and reusable interaction patterns
globs:
  - "web/static/hooks/*.js"
  - "web/static/hooks/index.js"
---

# Frontend Custom Hooks

## Hooks Directory Structure

| File | Purpose |
|------|---------|
| `hooks/useWebSocket.js` | WebSocket connections, session management |
| `hooks/useSwipeNavigation.js` | Mobile swipe gestures for navigation |
| `hooks/useResizeHandle.js` | Drag-to-resize with mouse and touch support |
| `hooks/index.js` | Re-exports for clean imports |

## useResizeHandle Hook

Provides drag-to-resize functionality with mouse and touch support:

```javascript
import { useResizeHandle } from "../hooks/useResizeHandle.js";

const { height, isDragging, handleProps } = useResizeHandle({
  initialHeight: 256,
  minHeight: 100,
  maxHeight: 500,
  onDragStart: () => { /* pause timers */ },
  onDragEnd: (finalHeight) => { /* persist height */ },
});

// Spread handleProps on the resize handle element
<div class="resize-handle cursor-ns-resize" ...${handleProps}>
  <${GripIcon} />
</div>
```

### Return Values

| Property | Type | Description |
|----------|------|-------------|
| `height` | `number` | Current height in pixels |
| `isDragging` | `boolean` | Whether drag is in progress |
| `handleProps` | `object` | Props to spread on handle element |
| `setHeight` | `function` | Manually set height |

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `initialHeight` | `number` | 256 | Starting height |
| `minHeight` | `number` | 100 | Minimum allowed height |
| `maxHeight` | `number` | 500 | Maximum allowed height |
| `onHeightChange` | `function` | null | Called during drag with new height |
| `onDragStart` | `function` | null | Called when drag begins |
| `onDragEnd` | `function` | null | Called when drag ends with final height |

### Implementation Details

- **Mouse support**: `mousedown` → `mousemove` → `mouseup`
- **Touch support**: `touchstart` → `touchmove` → `touchend`
- **Direction**: Dragging up (negative deltaY) increases height
- **Body styles**: Sets `user-select: none` and `cursor: ns-resize` during drag
- **Cleanup**: Removes all listeners on unmount or drag end

## useSwipeNavigation Hook

Handles touch-based swipe gestures for mobile navigation:

```javascript
import { useSwipeNavigation } from "../hooks/useSwipeNavigation.js";

useSwipeNavigation(
  containerRef,
  onSwipeLeft,   // e.g., next session
  onSwipeRight,  // e.g., previous session
  {
    threshold: 80,      // Min distance to trigger
    maxVertical: 80,    // Max vertical movement allowed
    edgeWidth: 40,      // Edge zone width for special actions
    onEdgeSwipeRight: openSidebar,  // Swipe from left edge
  }
);
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `threshold` | `number` | 50 | Minimum horizontal distance to trigger swipe |
| `maxVertical` | `number` | 100 | Maximum vertical movement (prevents scroll conflicts) |
| `edgeWidth` | `number` | 30 | Width of edge zone for special swipes |
| `onEdgeSwipeRight` | `function` | null | Called for right swipe starting from left edge |
| `onEdgeSwipeLeft` | `function` | null | Called for left swipe starting from right edge |

### Gesture Detection

- Swipe must complete within 500ms
- Horizontal movement must exceed `threshold`
- Vertical movement must be less than `maxVertical`
- Edge swipes detected by starting position within `edgeWidth` of screen edge

## Hook Import Pattern

```javascript
// In components
import { useResizeHandle } from "../hooks/useResizeHandle.js";
import { useSwipeNavigation } from "../hooks/useSwipeNavigation.js";

// Or via index.js
import { useResizeHandle, useSwipeNavigation } from "../hooks/index.js";

// In app.js (uses hooks/index.js)
import { useWebSocket, useSwipeNavigation, useResizeHandle } from "./hooks/index.js";
```

## Creating New Hooks

Follow these patterns when creating new hooks:

1. **File naming**: `use[Name].js` (e.g., `useResizeHandle.js`)
2. **Export from index.js**: Add to `hooks/index.js` for clean imports
3. **Window globals**: Use `const { useEffect, useRef, useCallback } = window.preact;`
4. **Cleanup**: Always return cleanup function from useEffect
5. **Touch support**: Include touch events for mobile compatibility
6. **Options object**: Use destructuring with defaults for configuration

```javascript
const { useState, useEffect, useRef, useCallback } = window.preact;

export function useMyHook(options = {}) {
  const { option1 = defaultValue, option2 = null } = options;
  
  // State and refs
  const [state, setState] = useState(initialValue);
  const ref = useRef(null);
  
  // Effects with cleanup
  useEffect(() => {
    // Setup
    return () => { /* cleanup */ };
  }, [dependencies]);
  
  return { state, ref, /* other values */ };
}
```

