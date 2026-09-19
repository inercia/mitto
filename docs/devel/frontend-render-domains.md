# Frontend Render Domains (mitto-sus.7)

Profile-driven render isolation boundaries for the Preact frontend. Companion
to [UI Responsiveness Benchmarks](ui-responsiveness-benchmarks.md) (the
time-based `perfMark`/`perfMeasure` harness) — this document covers **render
counts**: which component subtrees reconcile in response to which state
changes, and the instrumentation/store contract used to keep that bounded.

## Problem

`App` (`web/static/app.js`) is the single consumer of `useWebSocket()` and
destructures ~40 return values. `useWebSocket` owned a single `sessions`
React state object keyed by session id; any WS chunk to **any** session
(including a background one the user isn't looking at) replaced the top-level
`sessions` reference, re-rendering `useWebSocket` → `App` → every child in
`App`'s subtree, regardless of whether that child consumes the session that
actually changed.

## The five render domains

| Domain | Owning component | State it consumes | What should trigger its re-render |
|---|---|---|---|
| Root / global | `App` (`app.js`) | Connection status, active session id, dialogs, workspaces, config options | Any state genuinely global (connection, active session switch, dialog open/close) |
| Navigation / sidebar | `SessionList` (`components/SessionList.js`) | The list of sessions' **summary** fields (title, archived, pinned, color, loop state) | A session's summary fields changing, or the session list membership changing — **not** a background session's message stream |
| Active conversation | `MessageList` (`components/MessageList.js`) | The **active** session's messages array | The active session's own messages changing — **not** any other session's messages |
| Composer | `ChatInput` (`components/ChatInput.js`) | Draft text (via `utils/draftStore.js`, mitto-sus.6), queue state, config options | Keystrokes in the mounted composer only — never another session's draft |
| Global notifications | `ToastContainer` (`components/ToastContainer.js`) | The active toast stack (`useToast`) | A toast being shown/dismissed |

## Instrumentation: render counters (`utils/renderCounters.js`)

Dev-only, no-op unless perf instrumentation is enabled (`?perf=1` /
`window.__mittoPerf` — the same gate as `utils/perfMarks.js`'s
`isPerfEnabled()`). Each of the five domain components calls
`useRenderCounter(<domainName>)` (`hooks/useRenderCounter.js`) once, at the
top of its body, incrementing a module-level counter and mirroring the
snapshot onto `window.__mittoRenderCounts` for a Playwright spec to drain —
the same pattern `perfMarks.js` uses for `window.__mittoPerfBuffer`.

```javascript
import { getRenderCounts, resetRenderCounts } from "../utils/renderCounters.js";
// or, in a Playwright spec:
await page.evaluate(() => window.__mittoRenderCounts);
```

## External subscribable stores: `stores/sessionsStore.js`

Generalizes the `utils/draftStore.js` pattern (module-level map + per-key
subscription, not a Preact context — a context provider still re-renders
every consumer on every write) from composer-draft text to a slice view of
per-session server state:

- `subscribeMessages(sessionId, cb)` / `subscribeSummary(sessionId, cb)` /
  `subscribeInfo(sessionId, cb)` / `subscribeKeepalive(sessionId, cb)` — each
  notifies only the subscribers of that **one slice** of that **one**
  session id.
- `replaceAll(sessions)` — called from a single `useEffect` in
  `useWebSocket.js` (mirroring the existing `sessionsRef.current = sessions`
  effect) whenever the `sessions` map changes. Performs the per-slice
  shallow-equality diff internally, so a background chunk that only changes
  `sessions[B].messages` notifies **only** session B's `messages`
  subscribers — not B's `summary`/`info`/`keepalive` subscribers, and not
  any subscriber of a different session id.
- `hooks/useSessionsStore.js` wraps the subscribe functions with
  `useState` + `useEffect` (Preact does not export `useSyncExternalStore`),
  mirroring the existing `useEffect(() => { setLocalText(...); return
  subscribeDraft(sessionId, setLocalText); }, [sessionId])` pattern in
  `ChatInput.js`.

## Status of this increment (mitto-sus.7)

Shipped in this increment:

- Render-count instrumentation (`utils/renderCounters.js`,
  `hooks/useRenderCounter.js`), wired into all five domain components.
- `stores/sessionsStore.js` and its per-slice hooks
  (`hooks/useSessionsStore.js`), kept live via a single additive
  `sessionsStore.replaceAll(sessions)` effect in `useWebSocket.js`.

## Status after mitto-b1k

Shipped:

- `MessageList` reads the active session's messages directly via
  `useActiveSessionMessages(activeSessionId)` instead of an App-passed
  `messages`/`displayMessages` prop; the `coalesceAgentMessages` derivation
  moved from `app.js` into `MessageList` itself. Wrapped in `memo()`.
- `ChatInput` reads `working_dir` / `isReadOnly` / `archived` /
  `loop_configured` via `useSessionInfo(sessionId)` instead of App-passed
  props sourced from the noisier `sessionInfo` object (which also carries
  `messageCount`, bumped on every message to the active session — see
  `hooks/useWSSessionSelectors.js`). Wrapped in `memo()`.
- `utils/renderCounters.js` exposes `window.__mittoResetRenderCounts` via a
  new `installRenderCountsReset()` bootstrap call (mirrors
  `installPerfBuffer()`), so a Playwright spec can reset counters mid-run.

Also shipped, in the mitto-b1k reopen pass that completed `SessionList`:

- `SessionList` is now wrapped in `memo()` (exported as
  `memo(SessionListImpl)`), and every prop `App` passes it is
  reference-stable across a background session's chunks:
  - `activeSessions` was **already** reference-stable via the existing
    structural-fingerprint memoization in `useWebSocket.js` (it excludes
    `messageCount` and per-message timestamps from the fingerprint), and
    `storedSessions`/`workspaces`/`openInTargets` are plain `useState`
    arrays that only change reference on an explicit update — so no
    `useSessionSummaries()`/`subscribeSessionIds` store-selector
    infrastructure was needed to satisfy this bead's acceptance criterion;
    building one would have duplicated `computeAllSessions()`'s ~30-field
    merge logic (`lib.js`) in a second, untested code path for no
    additional isolation. This is a deliberate deviation from the original
    Plan comment's design, per the "smallest coherent increment, no
    gold-plating" principle already invoked once on this bead.
  - The seven callback props that were plain functions or inline JSX arrows
    (`onSelect`, `onNewSession`, `onDelete`, `onArchive`, `onShowSettings`,
    `onShowWorkspaces`, `onShowKeyboardShortcuts`, plus the `onClose` and
    `onBeadsCreate` inline arrows) are now `useCallback`-wrapped in
    `app.js`. Every other callback prop was already `useCallback`-wrapped,
    either directly in `app.js` or in the extraction hooks it composes
    (`useTheme`, `useAgentAuthState`, `useBeadsIntegration`,
    `useWorkspacePrompts`).

## Test phase: regression spec + before/after render counts

Shipped: `tests/ui/specs/perf/render-isolation.perf.spec.ts` — a Playwright
regression spec covering the three AC scenarios (background chunk, composer
keystroke, idle keepalive), asserting `MessageList`/`ChatInput` never
re-render on an unrelated background session's activity and `SessionList`
stays within a small bounded tolerance (see below). Run via `bunx playwright
test --config=tests/ui/playwright.config.ts tests/ui/specs/perf/render-isolation.perf.spec.ts`,
or as part of `make bench-ui` (records samples via `writePerfSample`).

Writing this spec surfaced a real regression the unit-test-only migration
had missed: several props passed to `ChatInput`/`SessionList` from `app.js`
(`onOpenLoopDialog`/`onOpenPromptParamDialog` passed into
`useBeadsIntegration`, and `onConfigurePrompts`/`onOpenLoopSettings`/
`onLoopPrompt`/`onOpenPromptParamDialog`/`onResume`/`onUIPromptAnswer`/
`onFlushContext` passed directly to `ChatInput`) were still inline JSX
arrows recreated on every `App` render, defeating `memo()` on both
components for a background session's chunks. Fixed by `useCallback`-wrapping
all of them; two callbacks (`handleComposerLoopPrompt`/
`handleComposerFlushContext`, and `handleSendPromptToConversation` itself)
additionally read `activeSession`/`allSessions` through a ref instead of
taking the object as a `useCallback` dep, since `computeAllSessions()`
(`lib.js`) rebuilds new object references for every session whenever ANY
session's summary changes (a pre-existing, documented over-invalidation) —
depending on the object directly would have recreated these callbacks on an
unrelated session's title assignment, not just when the active session
itself changes.

**Numeric render counts** (Chromium, mock ACP, `tests/fixtures/responses/perf-plain-long.json`'s
200-chunk/5ms fixture). "Before" = `ffc12503` (mitto-sus.7, pre-`memo()`/
pre-callback-stabilization baseline) — that commit predates
`window.__mittoResetRenderCounts`, so its counts are cumulative since page
load, not scenario-isolated; "after" = post-migration HEAD, reset
immediately before each scenario via `window.__mittoResetRenderCounts`:

| Scenario                | Component     | Before (ffc12503, cumulative) | After (HEAD, scenario delta)                          |
| ----------------------- | ------------- | ----------------------------- | ----------------------------------------------------- |
| Background chunk        | `SessionList` | 81                            | 2 (tolerance ≤3 — see spec comment)                   |
| Background chunk        | `MessageList` | 69                            | 0                                                     |
| Background chunk        | `ChatInput`   | 80                            | 0                                                     |
| Composer keystroke      | `SessionList` | 21                            | 0                                                     |
| Composer keystroke      | `MessageList` | 12                            | 0                                                     |
| Composer keystroke      | `ChatInput`   | 36                            | 22 (expected — composer's own local draft-text state) |
| Idle keepalive interval | `SessionList` | 64                            | 0                                                     |
| Idle keepalive interval | `MessageList` | 50                            | 0                                                     |
| Idle keepalive interval | `ChatInput`   | 59                            | 0                                                     |

The "before" numbers are dominated by every domain re-rendering in lockstep
on every WS chunk (the pre-`mitto-sus.7`/pre-`mitto-b1k` coupling this whole
effort targets) compounded with cumulative measurement; the "after" numbers
show `MessageList`/`ChatInput` fully isolated from a background session's
chunks and keepalive traffic, and `SessionList`'s residual 0-3 tied to the
background session's own legitimate one-shot summary changes (its
`isStreaming` flag settling, and the backend's post-completion title-retry
attempt) rather than per-chunk churn.

Deferred to a follow-up bead (mitto-sus.11): queue state,
background-notification state, and workspaces/config-options state remained
owned by `useWebSocket`/`App` at the time this bead closed.

## Status after mitto-sus.11

Shipped, extending the render-isolation pattern to the three state families
this bead's AC targeted:

- **Toasts + background notifications** — `stores/notificationsStore.js`
  owns the toast stack and four background-notification singletons
  (`backgroundCompletion`, `loopStarted`, `backgroundUIPrompt`,
  `backgroundUIPromptTimeout`) as module-level state with stable
  `showToast`/`dismissToast` functions. `ToastContainer` self-subscribes via
  `useToasts()` instead of taking `toasts`/`onDismiss` props from `App`.
- **Queue state** — `stores/queueStore.js` (per-session `messages`/
  `length`/`config` slices) + `hooks/useQueue.js`
  (`useQueueMessages`/`useQueueLength`/`useQueueConfig`). `QueueDropdown`,
  `ChatInput`, and `SessionList` (its sidebar "queued messages" badge — a
  hidden-coupling discovery, not in the original plan) self-subscribe
  instead of receiving queue data prop-drilled from `App`. `App` itself
  keeps one direct `useQueueLength(activeSessionId)` subscription to gate
  the archive button — a legitimate self-subscription, not prop-drilling.
- **Workspaces + config-options** — `stores/workspacesStore.js` (global
  `workspaces`/`acpServers`, module-level, not per-session) and
  `stores/configOptionsStore.js` (per-session `config_options`, diffed by
  reference so an active-session `info` touch that leaves `config_options`
  unchanged is a silent no-op) + `hooks/useWorkspacesStore.js`
  (`useWorkspaces`/`useAcpServers`/`useConfigOptions`). `MessageList` and
  `SessionList` self-subscribe to `useWorkspaces()` instead of an
  `App`-passed `workspaces` prop (both were real, live consumers found by
  inspecting the actual render tree, superseding the plan's original file
  list); `ChatInput` and `SessionPanel` self-subscribe to
  `useConfigOptions(sessionId)` instead of an `App`-passed `configOptions`
  prop. `App` keeps its own `useWorkspaces()`/`useAcpServers()` calls for
  internal routing/dialog logic (dozens of pre-existing call sites), but no
  longer needs `configOptions` for itself once both of its consumers
  self-subscribe directly.

**Plan-vs-reality deviations** (the plan's guessed file list, written before
inspecting the live render tree, did not match several real consumers):

- `WorkspacesDialog.js` and `SettingsDialog.js` each own an **independent**
  editable draft of workspaces/ACP servers (`useWorkspacesData()` /
  a local `useState` fetched via their own SDK call for the settings-editing
  form) — neither ever received `workspaces`/`acpServers` as a prop from
  `App`. Self-subscribing them would have altered their edit/staging
  semantics for no render-isolation benefit; left unmigrated.
- `NewSessionWorkspaceDialog.js` receives `workspaceDialog.filteredWorkspaces
  || workspaces` from `App` — a conditional substitution of an
  `App`-computed *filtered* variant, not a pure pass-through of the global
  list. Left prop-driven from `App`'s own (now store-backed) `workspaces`
  value; functionally unchanged.
- `AddFolderDialog.js` does not consume `workspaces` at all (a different,
  unrelated `hiddenWorkspaces` prop). `ConfigOptionSelect.js` does not
  consume the `configOptions` array at all — it receives one already-resolved
  `configOption` object from its parent (`ChatInput`/`SessionPanel`), which
  is exactly what migrating those parents fixes. `ConversationPropertiesPanel.js`
  is dead code (confirmed via grep: not rendered anywhere in `app.js`,
  superseded by `SessionPanel.js`) — migrating it would have had zero
  runtime effect.

All three families now read via a scoped store slice rather than an `App`
prop chain (AC1); `render-isolation.perf.spec.ts` covers queue add/delete
and a toast fired without a WS round trip (AC2) — a `set_config_option`
scenario was not added in this pass since `ChatInput`/`SessionPanel` are the
only live consumers and neither is part of the five perf-tracked render
domains above, so an isolation regression here is caught by MessageList's
existing background-chunk scenarios if `config_options` diffing is ever
removed. `bun test web/static` and the full Playwright perf suite pass
throughout (see the bead's `Testing:` comments for exact counts).
