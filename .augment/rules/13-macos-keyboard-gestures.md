---
description: macOS keyboard shortcuts and trackpad gestures for the native app
globs:
  - "cmd/mitto-app/menu_darwin.m"
  - "cmd/mitto-app/main.go"
  - "web/static/app.js"
keywords:
  - keyboard shortcut
  - trackpad gesture
  - swipe
  - hotkey
  - key binding
  - menu item
  - NSMenuItem
  - keyEquivalent
---

# macOS Keyboard Shortcuts and Trackpad Gestures

## Keyboard Shortcuts System

### Architecture

Keyboard shortcuts are handled at two levels:

1. **Native macOS menu** (`cmd/mitto-app/menu_darwin.m`): Shortcuts handled by the native app menu
2. **Web frontend JavaScript** (`web/static/app.js`): Shortcuts handled by `keydown` event listeners

### Shortcut Categories

| Category | Handler | Works in Browser | Works in macOS App |
|----------|---------|------------------|-------------------|
| Native menu shortcuts | `menu_darwin.m` | ❌ No | ✅ Yes |
| Web shortcuts | `handleGlobalKeyDown` | ✅ Yes | ✅ Yes |
| Global hotkey | Carbon Events API | ❌ No | ✅ Yes (system-wide) |
| Trackpad gestures | `menu_darwin.m` (scroll event monitor) | ❌ No | ✅ Yes |

## Trackpad Gestures

### Two-Finger Horizontal Swipe Navigation

The macOS app supports two-finger horizontal swipe gestures to navigate between conversations:

| Gesture | Action | Implementation |
|---------|--------|----------------|
| Swipe left (two fingers) | Go to next conversation | Calls `window.mittoNextConversation()` |
| Swipe right (two fingers) | Go to previous conversation | Calls `window.mittoPrevConversation()` |

**Implementation details** (`cmd/mitto-app/menu_darwin.m`):
- Uses `NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskScrollWheel` to track scroll events
- Accumulates scroll delta during the gesture (from `NSEventPhaseBegan` to `NSEventPhaseEnded`)
- Requires horizontal movement > 100px and dominant over vertical (2:1 ratio)
- Passes events through so normal WebView scrolling still works
- Calls Go callback `goSwipeNavigationCallback()` which evaluates JavaScript

## KEYBOARD_SHORTCUTS Array

Define shortcuts centrally for the help dialog:

```javascript
const KEYBOARD_SHORTCUTS = [
    // macOnly: true = only works in native macOS app
    { keys: '⌘⇧M', description: 'Show/hide window', macOnly: true, section: 'Global' },
    { keys: '⌘N', description: 'New conversation', macOnly: true, section: 'Conversations' },
    // No macOnly = works in both browser and macOS app
    { keys: '⌘1-9', description: 'Switch to conversation 1-9', section: 'Conversations' },
    { keys: '⌘,', description: 'Settings', section: 'Navigation' },
];
```

### Filtering Shortcuts by Environment

```javascript
// Check if running in the native macOS app
const isMacApp = typeof window.mittoPickFolder === 'function';

// Filter shortcuts - hide macOnly when in browser
KEYBOARD_SHORTCUTS.forEach(shortcut => {
    if (shortcut.macOnly && !isMacApp) {
        return;  // Skip native-only shortcuts in browser
    }
    // Add to sections...
});
```

## Adding New Shortcuts

### Adding Web Shortcuts (Browser + macOS App)

To add a shortcut that works in both browser and macOS app:

1. Add to `KEYBOARD_SHORTCUTS` array (no `macOnly` flag)
2. Add handler in `handleGlobalKeyDown` useEffect:

```javascript
useEffect(() => {
    const handleGlobalKeyDown = (e) => {
        if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
            if (e.key === ',') {
                e.preventDefault();
                if (!configReadonly) {
                    setSettingsDialog({ isOpen: true, forceOpen: false });
                }
            }
        }
    };
    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
}, [configReadonly]);
```

### Adding Native macOS Shortcuts

To add a shortcut that only works in the macOS app:

1. Add to `KEYBOARD_SHORTCUTS` with `macOnly: true`
2. Add menu item in `menu_darwin.m`:

```objc
NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:@"Action Name"
                                              action:@selector(actionMethod:)
                                       keyEquivalent:@"k"];
[item setKeyEquivalentModifierMask:NSEventModifierFlagCommand];
[item setTarget:handler];
[menu addItem:item];
```

3. Add handler method in `MittoMenuHandler`
4. Add Go callback in `main.go`
5. Expose JavaScript function via `window.mittoActionName`

## Common Shortcuts Reference

| Shortcut | Action | Type |
|----------|--------|------|
| `⌘⇧M` | Show/hide window (global) | Native only |
| `⌘N` | New conversation | Native only |
| `⌘W` | Close conversation | Native only |
| `⌘1-9` | Switch to conversation | Web |
| `⌘,` | Settings | Web |
| `⌘[` / `⌘]` | Previous/next conversation | Web |
| `Escape` | Cancel/close dialogs | Web |

