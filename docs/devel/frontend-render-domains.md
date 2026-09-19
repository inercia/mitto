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

Deferred to a follow-up bead (SessionList's read-side swap turned out to be
materially larger than originally scoped):

- `SessionList` still takes the `activeSessions`/`storedSessions` props and
  is **not** wrapped in `memo()`. Two blockers surfaced during
  implementation:
  1. `computeAllSessions()` (`lib.js`) merges in far more raw per-session
     fields than the mitto-sus.7 summary-slice extractor captures
     (`loop_configured`, `next_scheduled_at`, `loop_frequency`,
     `loop_trigger`/`loop_triggers`, and more) — safely expanding the
     summary slice means touching that merge function's field list one by
     one, which is its own bounded piece of work.
  2. At least two callback props `App` passes to `SessionList` are inline
     arrow functions recreated every render (`onClose`,
     `onBeadsCreate`), and several more (`toggleTheme`,
     `handleShowSettings`, `handleBeadsOpen`, etc.) were not confirmed
     stable. Wrapping `SessionList` in `memo()` without first auditing and
     stabilizing all ~20 of its callback props would be a no-op that adds
     complexity without isolating anything — worse than leaving it as-is.
  `SessionList` still benefits today from the existing structural-
  fingerprint memoization of `activeSessions` (`useWebSocket.js`), which
  already keeps that array's reference stable across background message-only
  chunks (it excludes `messageCount` and per-message timestamps from the
  fingerprint) — the remaining work is `memo()` + the callback audit.
- Callback-identity stabilization audit for the ~20 callbacks `App` passes
  to `SessionList`/`MessageList`/`ChatInput`.
- Queue state, background-notification state, and workspaces/config-options
  state remain owned by `useWebSocket`/`App` — out of scope for this bead
  (see sibling follow-up beads filed under `mitto-sus` at verify time).
