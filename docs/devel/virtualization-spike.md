# Variable-Height Conversation Virtualization — Spike (mitto-sus.8)

Spike, not a shipped feature: measurements, prototypes, and a go/no-go
recommendation for virtualizing `MessageList.js`. No production code path is
changed by this doc; the one behavior fix below (session-restore race) is a
pre-existing bug this spike's measurement work surfaced, not virtualization.

## TL;DR

- **Recommendation: no-op for now, re-measure after real user reports.**
  Initial paint is capped at ~1,000 DOM nodes regardless of history size
  (`INITIAL_EVENTS_LIMIT`); growth only happens once a user repeatedly clicks
  "Load earlier messages", and even at a full `MAX_MESSAGES=1000` load we
  project ~10k DOM nodes — well inside what Chromium/WKWebView render at
  60 fps without virtualization (see Threshold table).
- `content-visibility: auto` (Prototype A) does **not** reduce DOM node
  count — only real windowing (Prototype B) or a library (Prototype C) would.
- Custom windowing is architecturally invasive (reverse-order layout,
  streaming height changes, prepend compensation, search/copy/accessibility
  trade-offs — see Prototype B) for a threshold that isn't yet reached.
- If revisited, prefer `react-virtuoso` over a hand-rolled window — its
  variable-height + prepend support fits this exact problem — but it
  requires `preact/compat` + a new bundler entry (see Prototype C).

## Root-cause fix that unblocked measurement (not virtualization-related)

The Plan phase flagged `history-load.small/medium/max` reporting identical
`domNodes=578` in the pre-existing baseline. Investigating why traced to a
real bug in `web/static/hooks/useWebSocket.js`: an effect
`useEffect(() => setLastActiveSessionId(activeSessionId), [activeSessionId])`
ran on **every mount** with the initial `activeSessionId=null`, synchronously
clearing `localStorage.mitto_last_session_id` before the async cold-start
restore (`connectToEvents`'s WS "open" handler, which reads
`getLastActiveSessionId()` over the network) got a chance to read it —
losing the race on every single page reload/restart. In the Playwright
harness this meant `page.reload()` always landed on the Dashboard (fixed
DOM shape) instead of the target conversation, hence the identical numbers.
Reproduced with a fully organic session (no synthetic localStorage
injection) before applying the fix. Fixed by skipping the very first
(mount-time) persist so the async restore always wins the race — see the
`hasPersistedActiveSessionOnceRef` guard in `useWebSocket.js`.

## Baseline (after the fix) — `tests/ui/perf/baseline.json`

| Scenario | domNodes | heap (MB) |
|---|---|---|
| history-load.small (10 msgs total) | 639 | 17.1 |
| history-load.medium (1,000 msgs total, initial load) | 1,021 | 17.1 |
| history-load.max (5,000 msgs total, initial load) | 1,021 | 17.1 |

medium == max on initial load: **initial DOM size is capped by
`INITIAL_EVENTS_LIMIT` (50 events / session, `web/static/lib.js`), not by
total history size.** Growth only happens once the user pages back.

### Prepend ("Load earlier messages") growth — new measurement

| Step | domNodes | Δ vs prev | click→render latency |
|---|---|---|---|
| initial | 1,021 | — | — |
| step 0 | 1,975 | +954 | 60–72 ms |
| step 1 | 2,450 | +475 | 37–130 ms |
| step 2 | 2,925 | +475 | 44–48 ms |
| step 3 | 3,400 | +475 | 40–127 ms |

~475 nodes per load-more page (25 message pairs) ⇒ ~19 nodes/message.
Extrapolated to the `MAX_MESSAGES=1000` retention ceiling (`web/static/lib.js`):
**~1,021 + 19×950 ≈ 19k DOM nodes worst case** — cross-checked directly: a
run that accidentally loaded ~500 pairs (1,000 total messages, the full cap)
via the seq-watermark path measured **9,899 DOM nodes**, consistent with
this projection. Click-to-render latency stays under ~130 ms throughout —
no hard budget breach observed even at this scale (see "Out of scope"
below re: not yet measuring scroll-FPS at the fully-grown state).

### Content-visibility prototype vs baseline (Prototype A, gated)

Same seeded "medium" session, same growth steps, `.mitto-perf-cv` OFF vs ON:

| | domNodes | layout Δ (CDP cumulative) |
|---|---|---|
| OFF | 2,303 | 0.00 ms |
| ON | 2,303 | -0.37 ms |

**DOM node count is identical.** `content-visibility: auto` skips layout/
paint work for offscreen rows but does **not** remove them from the DOM —
confirms it cannot fix a DOM-node-count problem, only a paint-cost one. The
layout-delta numbers are noise: CDP `Performance.getMetrics` is too coarse
to resolve the cost of a single 475-node prepend (both readings round to
~0). A real signal would need either a much larger accumulated window
(10k+ nodes) or frame-level instrumentation (Long Animation Frames /
manual `requestAnimationFrame` deltas) instead of cumulative CDP counters.

## Prototype B — custom windowing (design only, not implemented)

Per the recorded plan, this is a design-decision write-up, not code — the
threshold above doesn't justify building it yet:

- **Stable identity:** reuse `messageKey(msg)` (already used by
  `MessageList.js`'s `useMemo`) as the window's row key.
- **Reverse ordering:** `MessageList.js` renders via
  `[...displayMessages].reverse().flatMap(...)` inside a `flex-col-reverse`
  container. A window would need either (a) absolute-position rows with a
  spacer matching total estimated height, which fights `column-reverse`'s
  browser-native bottom-anchoring, or (b) keep `column-reverse` and only
  vary which rows are *mounted* (not absolutely positioned) — simpler, but
  loses fixed-pixel scroll-position math for jump-to-message.
- **Prepend compensation:** the window must report `scrollHeight` deltas
  back into `useScrollManagement`'s existing `justLoadedMoreRef` prepend
  path so it keeps working unmodified.
- **Streaming growth:** the newest (bottom) row's height changes every
  chunk (`sessionUpdateScheduler.js`). A `ResizeObserver` on the currently
  streaming row would need to update the height cache live and keep the
  pinned-to-bottom scroll position correct without a mount/unmount thrash.
- **Search / copy / browser find:** unmounted rows lose native `Ctrl-F`
  and text selection. Trade-off: mount-all during an active find (detect
  via the `Find` DOM API or keyboard shortcut) or accept the regression —
  no clean answer; would need a product decision if pursued.
- **Accessibility:** unmounted rows are invisible to assistive tech.
  Needs either an off-screen full-content mirror (extra DOM, defeats the
  purpose) or accepting a real AT regression — same "no clean answer" as
  search.
- **Session switching:** the height cache + window state must be keyed and
  reset per `activeSessionId`, mirroring `useScrollManagement`'s existing
  session-switch reset.
- **Deep-link / retry:** scrolling to a specific message id (retry
  targets, deep links) must force that row's index into the mounted range
  before scrolling, not scroll to a coordinate that doesn't exist yet.

## Prototype C — library feasibility (doc-only, no install)

Both candidates are **React-only** (peer-dep on `react`/`react-dom`); this
project has no bundler and no React — it's raw ESM + Preact-from-globals +
HTM (`docs/devel/frontend-bundler-spike.md`). Adoption would require:
1. `preact/compat` aliased to `react`/`react-dom` (the standard technique;
   not currently vendored).
2. A per-component esbuild bundle (precedent: `vendor/codemirror/`, already
   built the same way — the bundler spike above concluded esbuild is "paid
   for" and viable for exactly this kind of single-component bundle).
3. Two new npm dependencies — **requires explicit approval** per the bead.

| | `react-window` | `react-virtuoso` |
|---|---|---|
| Gzip size | ~2–6 KB | ~17 KB |
| Variable-height support | Manual (`VariableSizeList`, you supply estimates) | Built-in, designed for this |
| Prepend / "load older" support | Manual | Built-in (`firstItemIndex`, `startReached`) |
| Fit for this problem | Needs the most custom glue | Best structural fit |
| License | MIT | MIT |

**If ever pursued, `react-virtuoso` is the better fit** — variable-height
and prepend are exactly its designed use case, avoiding most of Prototype
B's hand-rolled height-cache and prepend-compensation work. `react-window`
would still leave us building most of that logic ourselves for a smaller
bundle. Neither removes the search/copy/accessibility trade-offs above.

## Threshold table

| History size × content | Budget | Status |
|---|---|---|
| Initial paint, any size | ~1,021 DOM nodes | Well within norms (record-only, `ui-responsiveness-benchmarks.md` row 7) |
| Fully paged to `MAX_MESSAGES=1000` | ~10–19k DOM nodes (measured/projected) | Not yet observed to cause jank; scroll-FPS at this scale is unmeasured (see below) |
| Prepend click→render | 37–130 ms | No hard budget; comparable to existing record-only latencies |

## Risk register

| Risk | Notes |
|---|---|
| Correctness (reverse order, streaming) | Custom windowing (B) touches the trickiest seams in the app; highest regression risk of the three prototypes |
| Accessibility | Both B and a library regress AT access to unmounted rows; no clean mitigation identified |
| WKWebView parity | Not yet measured in this spike — the mock-ACP Playwright harness runs Chromium only; flagged as an explicit gap |
| Dependency cost | C requires `preact/compat` shim + 2 new deps + a new bundler entry — highest process cost of the three |
| Premature optimization | Current numbers don't show a problem — building B or adopting C now is speculative |

## Staged rollout / fallback (if revisited later)

1. Land the scroll-FPS-at-full-load measurement gap (below) as a follow-up
   bead — establish an actual jank threshold before building anything.
2. If breached: try `content-visibility` in production first (near-zero
   integration cost, only regresses accessibility/find at the CSS level,
   both already true today for very long pages elsewhere in the app).
3. Only if that's insufficient, evaluate `react-virtuoso` behind the
   `preact/compat` shim as a scoped, single-component migration — not a
   hand-rolled window (Prototype B's per-seam integration cost is higher
   than adopting a library built for exactly this).

## Out of scope / known measurement gaps

- Scroll-FPS at the fully-grown (~1000-message) state was not captured —
  only at the initial ~50-message load. This is the actual gap between
  "DOM node count grows" and "users perceive jank"; a follow-up should wire
  `collectFrameStats` into the prepend-growth loop in
  `history-load.perf.spec.ts`.
- Chromium vs WKWebView comparison not performed (harness is Chromium-only).
- No production code changed; `content-visibility` prototype and its
  `mitto-perf-cv`/`mitto-msg-row` seam are dormant unless explicitly enabled.
