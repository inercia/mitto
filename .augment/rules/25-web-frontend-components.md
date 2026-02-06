---
description: Frontend UI components (ChatInput, QueueDropdown, Message, Icons), component patterns, and button group styling
globs:
  - "web/static/components/*.js"
  - "web/static/components/**/*"
---

# Frontend UI Components

## Component Architecture

All components are in `web/static/components/` and use Preact/HTM with window globals:

```javascript
const { useState, useEffect, useRef, useCallback, html } = window.preact;
```

## Component Inventory

| Component | File | Purpose |
|-----------|------|---------|
| `ChatInput` | `ChatInput.js` | Message composition, image uploads, prompts dropdown, queue controls |
| `QueueDropdown` | `QueueDropdown.js` | Queued messages panel with delete/move actions |
| `Message` | `Message.js` | Renders user/agent/tool/error messages |
| `SettingsDialog` | `SettingsDialog.js` | Configuration, workspaces, auth settings |
| `Icons` | `Icons.js` | SVG icon components (TrashIcon, ChevronUpIcon, etc.) |

## ChatInput Component

### Props

| Prop | Type | Description |
|------|------|-------------|
| `onSend` | `(text, images) => Promise` | Send message (returns Promise for ACK) |
| `onAddToQueue` | `() => void` | Add current text to queue |
| `onToggleQueue` | `() => void` | Toggle queue panel visibility |
| `showQueueDropdown` | `boolean` | Whether queue panel is visible |
| `queueLength` | `number` | Current queue size |
| `queueConfig` | `{enabled, max_size}` | Queue configuration |
| `isStreaming` | `boolean` | Agent currently responding |
| `isReadOnly` | `boolean` | Session in read-only mode |
| `prompts` | `Array` | Predefined prompts for dropdown |

### Button Group Pattern

ChatInput uses a two-row button layout with split button groups:

```
┌─────────────────────────────────────┐
│ [Send/Stop/Full] │ [^] Prompts     │  ← Top row
├─────────────────────────────────────┤
│ [+ Add to Queue] │ [≡] Queue Panel │  ← Bottom row
└─────────────────────────────────────┘
```

**Split button styling**:
```javascript
// Left button: rounded left only
class="... rounded-l-xl"

// Right button: rounded right only, left border
class="... rounded-r-xl border-l border-slate-600"
```

### Queue Button States

| State | Left Button | Right Button |
|-------|-------------|--------------|
| Text empty | Disabled (gray) | Enabled |
| Text present | Enabled | Enabled |
| Improving | Disabled | Enabled |
| Queue panel open | - | Blue background |

### Keyboard Shortcuts

| Keys | Action |
|------|--------|
| `Enter` | Send message |
| `Shift+Enter` | New line |
| `Cmd/Ctrl+Enter` | Add to queue |

## QueueDropdown Component

### Animation Pattern

Roll-up animation from bottom edge:

```javascript
// CSS classes with transition
const dropdownClasses = `... transition-all duration-300 ease-out ${
  isOpen ? "max-h-64 opacity-100" : "max-h-0 opacity-0 pointer-events-none"
}`;

// Transform origin for roll-up effect
style="transform-origin: bottom;"
```

### Auto-Close Behavior

Closes automatically after 5 seconds of inactivity:

```javascript
useEffect(() => {
  if (isOpen) {
    inactivityTimerRef.current = setTimeout(() => {
      onClose();
    }, 5000);
  }
  return () => clearTimeout(inactivityTimerRef.current);
}, [isOpen]);

// Pause timer on hover
const handleMouseEnter = useCallback(() => {
  clearTimeout(inactivityTimerRef.current);
}, []);
```

### Click Outside Pattern

```javascript
useEffect(() => {
  if (!isOpen) return;

  const handleClickOutside = (event) => {
    if (dropdownRef.current && !dropdownRef.current.contains(event.target)) {
      // Check for queue toggle button to avoid immediate close
      const queueButton = event.target.closest("[data-queue-toggle]");
      if (!queueButton) {
        onClose();
      }
    }
  };

  // Delay listener to avoid catching opening click
  const timeoutId = setTimeout(() => {
    document.addEventListener("click", handleClickOutside);
  }, 10);

  return () => {
    clearTimeout(timeoutId);
    document.removeEventListener("click", handleClickOutside);
  };
}, [isOpen, onClose]);
```

## Icons Component

Centralized SVG icons as Preact components:

```javascript
// Import specific icons
import { TrashIcon, ChevronUpIcon, ChevronDownIcon } from "./Icons.js";

// Usage in JSX
<${TrashIcon} className="w-4 h-4" />
```

**Icon naming convention**: `[Name]Icon` (e.g., `TrashIcon`, `ChevronUpIcon`)

## Color Utilities in ChatInput

Helper functions for prompt tag styling:

| Function | Purpose |
|----------|---------|
| `getContrastColor(hex)` | Calculate black/white text for background |
| `hexToHSL(hex)` | Convert hex to HSL for sorting |
| `sortPromptsByColor(prompts)` | Sort prompts by hue for visual grouping |

## Component Import Pattern

```javascript
// In app.js
import { ChatInput } from './components/ChatInput.js';
import { QueueDropdown } from './components/QueueDropdown.js';
import { Message } from './components/Message.js';

// Or via index.js
import { ChatInput, QueueDropdown, Message } from './components/index.js';
```

