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

## QueueDropdown

Resizable via `useResizeHandle` (initialHeight: `getQueueDropdownHeight()`, min: 100, max: 500). Auto-closes after 5s inactivity; paused on hover and drag.

## Icons

Naming: `[Name]Icon` (e.g., `TrashIcon`, `QueueIcon`). Always `CloseIcon` SVG, never `✕`. Sizes: `w-4 h-4` (toasts), `w-5 h-5` (dialogs).

## daisyUI Badges (Pills / Tags)

All badge/pill components use daisyUI's `badge` class family, managed via a centralized helper function.

**Single-point helper pattern** (BeadsView.js):
```javascript
function badge(label, className = "") {
  return html`<span class="badge badge-sm ${className}">${label}</span>`;
}
// Call sites: badge("P1", "bg-red-600"), badge("active", "bg-accent")
```

**Color preservation**: When migrating custom pills → daisyUI, preserve existing color schemes:
- Solid background colors (e.g., `bg-red-600`) kept for contrast (e.g., red hover state)
- Colored dots (status indicators) preserved separately from badge styling
- Accent/secondary badges use semantic daisyUI colors, not custom tailwind

**Scope**: Pills appear in BeadsView (priority/status), SettingsDialog (server/tags), WorkspacesDialog (source badges), side panels (status/ACP-server/runner-type).

## Side Panel Overlay Pattern

`SessionPanel` is a unified tabbed panel (replaces old `ConversationPropertiesPanel`/`UserDataPanel`) with three tabs: **Changes**, **Properties**, **User Data**. Parent (`app.js`) manages open/close state. Changes tab fetches `GET /api/sessions/{id}/changes`, displays file list with status badges (A=green, M=amber, D=red). Animation: `isClosing`/`shouldRender` pair (150ms).

## useToast Hook (Unified Notification System)

**All in-app notifications must go through `useToast`** — never add standalone toast state/timers in `app.js`.

```javascript
const { showToast, dismissToast, toasts } = useToast();
showToast({ message: "Saved", style: "success" }); // auto-dismiss 5s
showToast({ message: "Pinned", sticky: true });     // no auto-dismiss
```

Severity durations: info/success=5s, warning/error=10s. Max 5 simultaneous. Render via `<ToastContainer toasts=${toasts} onDismiss=${dismissToast} />`. Use `error` (red) for actual errors only.

## useResizeHandle / useSwipeNavigation

- `useResizeHandle`: drag to resize. ChatInput uses two instances (QueueDropdown + textarea; max-height in `mitto_ui_textarea_max_height` key)
- `useSwipeNavigation`: swipe left/right with threshold, 500ms window

## daisyUI Tabs (Radio-based + State-Driven Content)

The **radio tabs-border** pattern (WorkspacesDialog, folder/workspace tabs) uses daisyUI radio inputs with a separate state-driven content region:

```javascript
// Tab bar: radio inputs with daisyUI styling
html`
  <div class="tabs tabs-border">
    ${tabDefs.map(tab => html`
      <input
        type="radio"
        name="ws-folder-tabs"
        role="tab"
        aria-label=${tab.label}
        data-testid=${`ws-tab-${tab.id}`}
        checked=${activeTab === tab.id}
        onChange=${() => setActiveTab(tab.id)}
        class=${"tab " + (activeTab === tab.id ? "tab-active text-mitto-accent" : "")}
      />
    `)}
  </div>

  <!-- Separate content region (state-driven, NOT pure CSS) -->
  <div data-testid="ws-tab-content" class="mt-4">
    ${activeTab === "folders" && html`<${FolderPanel} />`}
    ${activeTab === "workspaces" && html`<${WorkspacePanel} />`}
  </div>
`
```

**Key points**:
- `type="radio"` with `tabs-border` class (daisyUI variant)
- `aria-label` provides visible tab text (radio inputs can't have text children)
- `onChange` (not `onInput`) for checkable inputs
- Preserve `role="tab"`, `data-testid`, distinct radio-group names, and active accent styling
- **Content kept state-driven** (conditional rendering) rather than pure-CSS interleaved `tab-content` divs — preserves lazy-loading effects (panels load only when active)
- Content region must satisfy test assertions like `ws-tab-content > *`

## Session List Tab Filtering

SessionList filters conversations by tab (Conversations, Periodic, Archived) via `getFilterTabForSession(session)`. On tab click, restore the last-focused conversation using `getLastActiveSessionIdForTab(tab)` — but only on user clicks, not programmatic changes. Guard against races with `(prevTab, prevSession)` refs to avoid redundant localStorage updates during streaming.
