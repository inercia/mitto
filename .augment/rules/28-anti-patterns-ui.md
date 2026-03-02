---
description: UI rendering anti-patterns, positioning, event handling, context menus, and synchronous vs async calculations
globs:
  - "web/static/app.js"
  - "web/static/components/*.js"
keywords:
  - context menu
  - positioning
  - useState useEffect
  - useMemo
  - render timing
  - click outside
  - dropdown
  - menu position
  - viewport bounds
---

# UI Rendering Anti-Patterns

## Context Menu and Dropdown Positioning

### ❌ Don't: Use useState + useEffect for Position Calculations

```javascript
// BAD: Position adjustment happens AFTER initial render
function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);
  const [adjustedPos, setAdjustedPos] = useState({ x, y });

  // Problem: This runs AFTER the first paint
  useEffect(() => {
    if (menuRef.current) {
      const rect = menuRef.current.getBoundingClientRect();
      // Adjust if menu would overflow viewport...
      setAdjustedPos({ x: newX, y: newY });
    }
  }, [x, y]);

  // First render uses STALE x, y values!
  // When x, y change (new right-click), the component:
  // 1. Renders with OLD adjustedPos (from previous useState)
  // 2. Then useEffect runs and updates
  // 3. Re-renders with new position
  // Result: Menu appears at wrong position momentarily or not at all
  return html`<div style="left: ${adjustedPos.x}px; top: ${adjustedPos.y}px;">`;
}
```

**Symptoms:**
- Context menu doesn't appear on first right-click
- Menu appears briefly at wrong position then jumps
- Works inconsistently across browsers (Edge, Safari affected more than Chrome)

### ✅ Do: Use useMemo for Synchronous Calculations

```javascript
// GOOD: Position calculated synchronously during render
function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);

  // Calculate position synchronously - no render cycle delay
  // For initial render, use raw x, y (no ref yet)
  // For subsequent renders, adjust based on menu dimensions
  const position = useMemo(() => {
    // On first render, menuRef.current is null - use raw position
    if (!menuRef.current) {
      return { x, y };
    }

    // Menu exists - calculate adjusted position
    const rect = menuRef.current.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;

    let newX = x;
    let newY = y;

    if (x + rect.width > viewportWidth) {
      newX = viewportWidth - rect.width - 8;
    }
    if (y + rect.height > viewportHeight) {
      newY = viewportHeight - rect.height - 8;
    }

    return { x: newX, y: newY };
  }, [x, y, menuRef.current]); // Include ref.current to recalc when available

  return html`<div ref=${menuRef} style="left: ${position.x}px; top: ${position.y}px;">`;
}
```

### Alternative: useLayoutEffect for DOM-Dependent Positioning

When you need the DOM to exist before calculating:

```javascript
// GOOD: useLayoutEffect for position adjustment
function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);
  const [adjustedPos, setAdjustedPos] = useState({ x, y });

  // Key: Reset position immediately when coordinates change
  useLayoutEffect(() => {
    setAdjustedPos({ x, y }); // Sync reset
  }, [x, y]);

  // Then adjust based on actual menu size
  useLayoutEffect(() => {
    if (menuRef.current) {
      const rect = menuRef.current.getBoundingClientRect();
      // Adjust as needed...
    }
  }, [x, y]);
}
```

## Click Outside Detection

### Pattern: Delay Click Outside Listener

```javascript
// Delay adding listener to avoid catching the click that opened the menu
useEffect(() => {
  if (!isOpen) return;

  const handleClickOutside = (e) => {
    if (ref.current && !ref.current.contains(e.target)) {
      onClose();
    }
  };

  // Delay to avoid catching opening click
  const timeoutId = setTimeout(() => {
    document.addEventListener("mousedown", handleClickOutside);
  }, 10);

  return () => {
    clearTimeout(timeoutId);
    document.removeEventListener("mousedown", handleClickOutside);
  };
}, [isOpen, onClose]);
```

## Native App Context Menu Handling

### ❌ Don't: Block All Context Menus Globally

```javascript
// BAD: Prevents ALL context menus, including custom ones
document.addEventListener("contextmenu", (e) => {
  e.preventDefault(); // Blocks everything!
});
```

### ✅ Do: Allow Custom Context Menu Handlers

```javascript
// GOOD: Only block default menu in areas without custom handlers
document.addEventListener("contextmenu", (e) => {
  // Allow default for text inputs
  if (e.target.tagName === "INPUT" || e.target.tagName === "TEXTAREA") {
    return;
  }

  // Check if a custom handler will show a menu
  const hasCustomMenu = e.target.closest("[data-has-context-menu]");
  if (!hasCustomMenu) {
    e.preventDefault();
  }
});
```

## Lessons Learned

### 1. Synchronous vs Async Rendering

| Use Case | Hook | Timing |
|----------|------|--------|
| Derived values | `useMemo` | Synchronous (during render) |
| Side effects | `useEffect` | After paint |
| DOM measurements | `useLayoutEffect` | Before paint |

### 2. useState Resets Only on Mount

`useState({ x, y })` captures initial props. When props change:
- The state is NOT automatically updated
- You need explicit synchronization
- Better: derive the value with `useMemo` if it depends on props

### 3. Browser Differences

- Chrome: More forgiving of async positioning
- Safari/Edge: Stricter timing, async issues more visible
- Always test in multiple browsers

## Related Files

- `21-web-frontend-state.md` - useEffect vs useLayoutEffect patterns
- `25-web-frontend-components.md` - Component patterns


