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

Deferred to a follow-up bead:

- Numeric before/after render-count measurements (background chunk /
  keystroke / keepalive scenarios) captured against a pre-migration
  baseline and post-migration HEAD.
- A Playwright regression spec under `tests/ui/specs/perf/` asserting the
  post-migration render-count bounds, so future regressions are caught by
  CI.
- Queue state, background-notification state, and workspaces/config-options
  state remain owned by `useWebSocket`/`App` — out of scope for this bead
  (see sibling follow-up beads filed under `mitto-sus` at verify time).
