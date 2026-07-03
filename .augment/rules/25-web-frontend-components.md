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
  - tabs
  - radio tabs
  - WorkspacesDialog
  - daisyUI tabs
---

# Frontend Components and Hooks

All components use Preact/HTM with window globals: `const { useState, useEffect, useRef, html } = window.preact;`

## Key Components

| Component           | Purpose                                       |
| ------------------- | --------------------------------------------- |
| `ChatInput`         | Composition, images, prompts, queue           |
| `QueueDropdown`     | Queued messages panel                         |
| `Message`           | User/agent/tool/error messages                |
| `SettingsDialog`    | Settings modal                                |
| `SessionPanel`      | Unified overlay (Changes + Properties tabs)   |
| `ContextMenu`       | Right-click menu with viewport-aware position |
| `SessionItem`       | List item with swipe, menu, status            |

## ChatInput

Single bordered container: textarea + bottom toolbar (left/center/right). **No external button column** — all actions in always-visible bottom bar.

- **Center bar**: config selectors + context usage % (use `filter()` not `find()`)
- **Context %**: Primary from ACP `context_usage`, fallback: `input_tokens ÷ getContextWindowSize()`
- **Shortcuts**: `Enter`=send · `Shift+Enter`=newline · `Cmd/Ctrl+Enter`=queue

### Named-Prompt Sends

Menu prompt selections (prompts menu, Cmd+/ slash picker) call `onSend("", [], [], { promptName })` — **never the full prompt body**. All menus go through the shared helper `web/static/hooks/useConversationSeeding.js` (`seedConversationWithPrompt` for existing conversations, `startConversationWithPrompt` for atomic create+seed). Named prompts render in the message list as `NamedPromptPill` (`[data-testid="named-prompt-pill"]`); the queue dropdown shows `msg.prompt_name || msg.title`. The backend resolves name → text at dispatch in the target conversation's context.

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Tooltip Patterns

### PortalTooltip (Viewport-Clamped)

For overflow-clipped rows (e.g., SessionList), render tooltips in a body-level portal to escape clip bounds:

```javascript
html`<${PortalTooltip} text=${"Long text..."} position=${"top"} >
  <div class="truncate">Clipped row</div>
</PortalTooltip>`
```

**Features**:
- Escapes overflow:hidden containers via `createPortal()`
- Auto-clamps position to viewport (e.g., "top" → "bottom" if near top edge)
- Applies background blur behind tooltip (`.tooltip-blur`)
- Used in `SessionItem.js` for session titles/paths

### daisyUI Tooltip

For non-clipped content (in-component tooltips), use daisyUI `tooltip`:

```javascript
html`<div class="tooltip tooltip-top" data-tip=${"Hover text"}>
  <button>Action</button>
</div>`
```

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `QueueIcon`). Always `CloseIcon` SVG, never `✕`. Sizes: `w-4 h-4` (toasts), `w-5 h-5` (dialogs).

## daisyUI Badges (Pills / Tags)

Centralized helper (e.g., `BeadsView.js`):
```javascript
function badge(label, className = "") {
  return html`<span class="badge badge-sm ${className}">${label}</span>`;
}
```

**When migrating**: Preserve solid colors (e.g., `bg-red-600` for contrast); use semantic daisyUI colors elsewhere. Used in BeadsView, SettingsDialog, WorkspacesDialog, side panels.

## Side Panel Overlay Pattern

`SessionPanel`: unified tabbed panel (Changes/Properties/User Data). Parent manages open/close. Changes: `GET /api/sessions/{id}/changes` with status badges (A=green, M=amber, D=red). Animation: `isClosing`/`shouldRender` (150ms).

## useToast Hook

**All notifications go through `useToast`** — never add standalone toast state/timers in `app.js`.

```javascript
const { showToast, dismissToast, toasts } = useToast();
showToast({ message: "Saved", style: "success" }); // auto-dismiss 5s
```

Durations: info/success=5s, warning/error=10s. Max 5 simultaneous. Render via `<ToastContainer />`. Use `error` (red) for actual errors only.

## useResizeHandle / useSwipeNavigation

- `useResizeHandle`: drag to resize. ChatInput uses two instances (QueueDropdown + textarea; max-height in `mitto_ui_textarea_max_height` key)
- `useSwipeNavigation`: swipe left/right with threshold, 500ms window

## daisyUI Tabs (Radio-based + State-Driven)

**radio tabs-border** pattern: radio inputs + separate state-driven content region (NOT CSS-interleaved `tab-content`). This preserves lazy-loading.

```javascript
html`<div class="tabs tabs-border">
  ${tabDefs.map(tab => html`
    <input type="radio" name="group" role="tab" aria-label=${tab.label}
      checked=${activeTab === tab.id} onChange=${() => setActiveTab(tab.id)}
      class=${"tab " + (activeTab === tab.id ? "tab-active text-mitto-accent" : "")} />
  `)}
</div>`
```

**Key**: `aria-label` (radio text), `onChange` (not `onInput`), state-driven content region.

## Session List Tab Filtering

Filters by tab (Conversations, Periodic, Archived) via `getFilterTabForSession()`. On click, restore last-focused session via `getLastActiveSessionIdForTab()` — **user-clicks only**, not programmatic. Guard races with refs to avoid redundant localStorage updates.
