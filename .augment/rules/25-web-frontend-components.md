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
  - ShortcutsEditor
  - global shortcuts
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
| `Toolbar`           | Config-driven action bar (see below)          |
| `ShortcutsEditor`   | Global+folder shortcut button config panel    |

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

**PortalTooltip** (`SessionItem.js`): for overflow-clipped rows, renders via `createPortal()` to escape clip bounds, auto-clamps position to viewport (e.g. "top" → "bottom" near the edge), adds a `.tooltip-blur` background.
```javascript
html`<${PortalTooltip} text=${"Long text..."} position=${"top"}><div class="truncate">Row</div></PortalTooltip>`
```

**daisyUI tooltip** (non-clipped content): `<div class="tooltip tooltip-top" data-tip="Hover text"><button>Action</button></div>`

## Reusable Config-Driven Components

**Toolbar** (`Toolbar.js`) — portable action bar rendered as a segmented pill from an `items` array. Item kinds: `button`, `dropdown`, `overflow`, `separator`, `spacer`, `custom`. Props: `variant` (`"floating"` | `"block"`), `surface`, `ariaLabel`, `testId`.

```javascript
html`<${Toolbar} variant="block" surface="bg-mitto-surface-3"
  ariaLabel="Issue actions" testId="beads-issue-toolbar" items=${headerToolbarItems} />`
```

Used in `BeadsView.js` list actions and issue-detail header — prefer over ad-hoc "..." kebab menus.

**ShortcutsEditor** (`ShortcutsEditor.js`) — one panel reused for both **global** (Settings dialog) and **folder** (Workspaces dialog) shortcut config. Consumers (conversations/beadsIssue/tasksList toolbars) merge global + folder shortcuts at render: global first, folder duplicates of a global `prompt` dropped; leftover duplicates render greyed-out via `redundantPromptNames`. Backend mirror: `GET/PUT /api/global/shortcuts` (`internal/web/handlers/global_shortcuts.go`); type `config.ShortcutButton{Icon, Prompt}`. Refresh via `mitto:global_shortcuts_updated`/`mitto:folder_shortcuts_updated` window events. See `08-config.md` for backend details.

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

Durations: info/success=5s, warning=10s. `error` toasts NEVER auto-dismiss — they persist until the user closes them (dismiss button), so critical errors can't be missed. Max 5 simultaneous. Render via `<ToastContainer />`. Use `error` (red) for actual errors only.

## useResizeHandle / useSwipeNavigation

- `useResizeHandle`: drag to resize. ChatInput uses two instances (QueueDropdown + textarea; max-height in `mitto_ui_textarea_max_height` key).
- `useSwipeNavigation`: swipe left/right with threshold, 500ms window. **Two independent swipe-nav entry points exist — any fix must land in BOTH**: touch/mobile via `isInsideHorizontallyScrollable()` in `hooks/useSwipeNavigation.js`, and macOS trackpad via `isOverHorizontallyScrollable()` in `utils/globalHandlers.js` (called from `menu_darwin.m` → `window.mittoNextConversation()`). Both guards use the `overflow-x` + `scrollWidth > clientWidth` heuristic, which is false for non-overflowing strips (and jsdom reports both as 0). For regions that must absorb horizontal swipes even when not overflowing (e.g. `.mitto-carousel` in `ChatInput.js`), set `data-mitto-no-swipe` on the container — both guards short-circuit on it in the ancestor walk.

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

Filters by tab (Conversations, Loop, Archived) via `getFilterTabForSession()`. On click, restore last-focused session via `getLastActiveSessionIdForTab()` — **user-clicks only**, not programmatic. Guard races with refs to avoid redundant localStorage updates.
