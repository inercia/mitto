---
description: Frontend UI components (ChatInput, QueueDropdown, Message, Icons, ContextMenu, SessionItem), component patterns, and button group styling
globs:
  - "web/static/components/*.js"
  - "web/static/components/**/*"
  - "web/static/app.js"
keywords:
  - context menu
  - session item
  - chat input
  - queue dropdown
---

# Frontend UI Components

## Component Architecture

All components are in `web/static/components/` and use Preact/HTM with window globals:

```javascript
const { useState, useEffect, useRef, useCallback, html } = window.preact;
```

## Component Inventory

| Component        | File                | Purpose                                                              |
| ---------------- | ------------------- | -------------------------------------------------------------------- |
| `ChatInput`      | `ChatInput.js`      | Message composition, image uploads, prompts dropdown, queue controls |
| `QueueDropdown`  | `QueueDropdown.js`  | Queued messages panel with delete/move actions                       |
| `Message`        | `Message.js`        | Renders user/agent/tool/error messages                               |
| `SettingsDialog` | `SettingsDialog.js` | Configuration, workspaces, auth settings                             |
| `Icons`          | `Icons.js`          | SVG icon components (TrashIcon, ChevronUpIcon, etc.)                 |
| `ContextMenu`    | `app.js`            | Generic context menu (right-click menus) with viewport-aware positioning |
| `SessionItem`    | `app.js`            | Session list item with swipe actions, context menu, status indicators |

## ChatInput Component

### Props

| Prop                | Type                        | Description                            |
| ------------------- | --------------------------- | -------------------------------------- |
| `onSend`            | `(text, images) => Promise` | Send message (returns Promise for ACK) |
| `onAddToQueue`      | `() => void`                | Add current text to queue              |
| `onToggleQueue`     | `() => void`                | Toggle queue panel visibility          |
| `showQueueDropdown` | `boolean`                   | Whether queue panel is visible         |
| `queueLength`       | `number`                    | Current queue size                     |
| `queueConfig`       | `{enabled, max_size}`       | Queue configuration                    |
| `isStreaming`       | `boolean`                   | Agent currently responding             |
| `isReadOnly`        | `boolean`                   | Session in read-only mode              |
| `prompts`           | `Array`                     | Predefined prompts for dropdown        |

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

| State            | Left Button     | Right Button    |
| ---------------- | --------------- | --------------- |
| Text empty       | Disabled (gray) | Enabled         |
| Text present     | Enabled         | Enabled         |
| Improving        | Disabled        | Enabled         |
| Queue panel open | -               | Blue background |

### Keyboard Shortcuts

| Keys             | Action       |
| ---------------- | ------------ |
| `Enter`          | Send message |
| `Shift+Enter`    | New line     |
| `Cmd/Ctrl+Enter` | Add to queue |

## QueueDropdown Component

### Resizable Height

The queue dropdown supports drag-to-resize with height persistence:

```javascript
import { useResizeHandle } from "../hooks/useResizeHandle.js";
import {
  getQueueDropdownHeight,
  setQueueDropdownHeight,
  getQueueHeightConstraints,
} from "../utils/storage.js";

const heightConstraints = getQueueHeightConstraints();
const { height, isDragging, handleProps } = useResizeHandle({
  initialHeight: getQueueDropdownHeight(),
  minHeight: heightConstraints.min, // 100px
  maxHeight: heightConstraints.max, // 500px
  onDragStart: () => clearTimeout(inactivityTimerRef.current), // Pause auto-close
  onDragEnd: (finalHeight) => setQueueDropdownHeight(finalHeight), // Persist
});
```

**Resize handle UI** at top edge:

```javascript
<div class="queue-resize-handle cursor-ns-resize" ...${handleProps}>
  <${GripIcon} className="w-6 h-1.5 text-gray-500" />
</div>
```

**Key behaviors:**

- Drag up to expand, down to collapse
- Height persisted to localStorage
- Transitions disabled during drag for smooth resizing
- Inactivity timer paused during drag

### Animation Pattern

Roll-up animation from bottom edge (transitions disabled during drag):

```javascript
const dropdownClasses = `... ${isDragging ? "" : "transition-all duration-300 ease-out"} ${
  isOpen ? "opacity-100" : "opacity-0 pointer-events-none"
}`;

// Use explicit height instead of max-h for resize support
const dropdownStyle = isOpen
  ? `height: ${height}px; box-shadow: 0 -8px 16px rgba(0, 0, 0, 0.3);`
  : "height: 0px;";
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
import { TrashIcon, ChevronUpIcon, ChevronDownIcon, GripIcon } from "./Icons.js";

// Usage in JSX
<${TrashIcon} className="w-4 h-4" />
<${GripIcon} className="w-6 h-1.5" />  // Horizontal resize handle
```

**Icon naming convention**: `[Name]Icon` (e.g., `TrashIcon`, `ChevronUpIcon`, `GripIcon`)

**Common icons:**
| Icon | Purpose |
|------|---------|
| `TrashIcon` | Delete actions |
| `ChevronUpIcon` / `ChevronDownIcon` | Move up/down, expand/collapse |
| `GripIcon` | Horizontal drag handle for resize |
| `DragHandleIcon` | Vertical drag handle (6 dots) |
| `QueueIcon` | Queue panel toggle |

## Color Utilities in ChatInput

Helper functions for prompt tag styling:

| Function                      | Purpose                                   |
| ----------------------------- | ----------------------------------------- |
| `getContrastColor(hex)`       | Calculate black/white text for background |
| `hexToHSL(hex)`               | Convert hex to HSL for sorting            |
| `sortPromptsByColor(prompts)` | Sort prompts by hue for visual grouping   |

## ContextMenu Component

Renders a context menu (right-click menu) with viewport-aware positioning.

**Location**: Defined in `app.js` (not a separate component file)

### Props

| Prop      | Type              | Description                         |
| --------- | ----------------- | ----------------------------------- |
| `x`       | `number`          | Mouse click X coordinate            |
| `y`       | `number`          | Mouse click Y coordinate            |
| `items`   | `Array<MenuItem>` | Menu items to display               |
| `onClose` | `() => void`      | Called when menu should close       |

### MenuItem Object

```javascript
{
  label: "Archive",          // Display text
  icon: html`<${ArchiveIcon} />`,  // Optional icon component
  onClick: () => { ... },    // Action handler
  disabled: false,           // Gray out and prevent click
  danger: false              // Red styling for destructive actions
}
```

### Positioning Pattern

See `28-anti-patterns-ui.md` for the critical pattern about synchronous vs async positioning. The menu adjusts position to stay within viewport bounds.

## SessionItem Component

Renders a session in the sidebar list with swipe actions, context menu, and status indicators.

**Location**: Defined in `app.js`

### Features

- **Swipe actions**: Left swipe reveals delete button (mobile)
- **Context menu**: Right-click for Archive, Properties, Periodic, Delete
- **Status indicators**: Active (green), Streaming (blue pulse), Archived (muted)
- **Workspace badge**: Shows workspace color/code when grouped

### Context Menu Integration

```javascript
function SessionItem({ session, ... }) {
  const [contextMenu, setContextMenu] = useState(null);

  const handleContextMenu = (e) => {
    e.preventDefault();
    e.stopPropagation();  // Don't bubble to parent
    setContextMenu({ x: e.clientX, y: e.clientY });
  };

  return html`
    <${Fragment}>
      ${contextMenu && html`
        <${ContextMenu}
          x=${contextMenu.x}
          y=${contextMenu.y}
          items=${contextMenuItems}
          onClose=${() => setContextMenu(null)}
        />
      `}
      <div onContextMenu=${handleContextMenu}>
        ...
      </div>
    <//>
  `;
}
```

## Component Import Pattern

```javascript
// In app.js
import { ChatInput } from "./components/ChatInput.js";
import { QueueDropdown } from "./components/QueueDropdown.js";
import { Message } from "./components/Message.js";

// Or via index.js
import { ChatInput, QueueDropdown, Message } from "./components/index.js";
```
