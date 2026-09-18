# UI Responsiveness Benchmarks (mitto-sus.1)

Foundation for the `mitto-sus` responsiveness epic: turns "Mitto feels slower
than VS Code" into reproducible measurements and agreed budgets. This
document is the benchmark procedure; it is intentionally **measurement-only**
— no optimizations or framework decisions are made here (those belong to the
later `mitto-sus.*` children).

## Status

`mitto-sus.1` establishes the measurement **foundation**; the remaining
deliverables from its original scope are decomposed into three tracked
follow-up child beads. The parent bead stays open (Done-branch child-aware
guard) until all three close.

- **Landed** (`mitto-sus.1`): the opt-in performance-mark instrumentation
  (`web/static/utils/perfMarks.js`), the first instrumented seams (composer
  keystroke, background chunk apply), deterministic mock-ACP streaming
  fixtures, and a first Playwright benchmark harness slice under
  `tests/ui/specs/perf/` (`composer-latency.spec.ts`,
  `chunk-apply-cost.spec.ts`, sharing `tests/ui/utils/perf.ts` for
  enable/drain/percentile helpers) that exercises both instrumented seams
  via the deterministic fixtures and runs as part of the normal
  `make test-ui` / `npm run test:ui` suite. These specs are **smoke tests
  of the instrumentation** (marks are recorded, durations are valid
  non-negative numbers, p50/p95 are logged) rather than hard budget gates
  — see "Proposed budgets" below for why enforcing them as strict
  `expect()` assertions is deferred.
- **Landed** (`mitto-sus.1.1`): the four remaining seam families —
  `mitto.prompt.sent.{local,network}` (satisfies the AC's "separates local
  rendering from network completion"), `mitto.ws.chunk.received` (pairs
  with the existing `applied` mark for the received→applied budget),
  `mitto.session.switch.*`, and `mitto.render.postprocess.*` — plus one
  Playwright perf spec per family under `tests/ui/specs/perf/`.
- **Implemented and tested, pending review** (`mitto-sus.1.2`): the
  `collectLongTasks` / `collectEventTimings` / `collectFrameStats` /
  `collectPaintLayoutStats` / `collectDOMStats` collectors (see
  "Collectors" below), the remaining fixtures (`perf-mixed-long`,
  `perf-multi-stream-{a,b,c}`, seeded history snapshots), and the
  `composer-during-stream` / `stream-mixed` / `history-load` /
  `conversation-switch` / `multi-stream` scenario specs under
  `tests/ui/specs/perf/*.perf.spec.ts`. This is what makes the harness
  "report long tasks, frame/render costs, layout/paint, DOM size, and
  switch latency" per the parent AC. `history-load.perf.spec.ts` is
  self-seeding (its `beforeAll` builds and runs the `seed-perf-history`
  Go helper automatically — see "Deterministic fixtures" below) and only
  skips if that build/run step itself fails (e.g. no Go toolchain).
  `determinism.perf.spec.ts` covers the bead's "determinism holds across
  repeated runs" acceptance criterion directly: it drives the
  `perf-plain-short` fixture twice across independent sessions and
  asserts both runs assemble byte-identical message text (same-order
  stream chunks) and record the same `mitto.ws.chunk.applied` mark count.
- **Landed** (`mitto-sus.1.3`): the opt-in `make bench-ui` / `make
  bench-ui-baseline` runner targets, the `writePerfSample()` /
  `loadBaseline()` / `getBaselineValue()` helpers in `tests/ui/utils/perf.ts`,
  `scripts/perf-summary.mjs` (aggregates per-run samples into a
  diff-vs-baseline report or a new committed baseline), the committed
  `tests/ui/perf/baseline.json`, its rendered table
  [ui-responsiveness-baseline.md](./ui-responsiveness-baseline.md), and the
  budget-promotion decisions below.
- **Landed** (`mitto-sus.3`): frame-paced foreground coalescing atop the
  existing `sessionUpdateScheduler.js` — once an active-session content burst
  is under way, chunks after the first coalesce into at most one
  `setSessions` commit per animation frame (100ms fallback timer on
  hidden/throttled tabs), cutting per-chunk React reconciles without
  delaying the first visible token. `chunk-apply-cost.spec.ts`'s
  `ws.chunk.applied` marks now carry a `detail.count` payload (surfaced via
  `PerfEntry.detail` in `tests/ui/utils/perf.ts`) recording how many queued
  chunks a given apply drained; this scenario's deterministic fixtures
  happen to arrive already coalesced by the backend's own `StreamBuffer`
  (see the scenario's own comment), so `maxCoalesced` is recorded via
  `writePerfSample` for `make bench-ui` rather than asserted as a hard
  floor here — the coalescing behavior itself is pinned deterministically
  in `sessionUpdateScheduler.test.js` instead.

## Enabling instrumentation

All marks are no-ops unless explicitly enabled, so there is zero overhead on
the normal production path:

- Append `?perf=1` to the app URL, **or**
- Set `window.__mittoPerf = true` before `app.js` bootstraps (e.g. via a
  Playwright `page.addInitScript`).

Once enabled, `installPerfBuffer()` (called once at `app.js` module scope)
mounts a `PerformanceObserver` covering `longtask`, `event`, `paint`,
`first-input`, `mark`, and `measure` entries into a capped ring buffer at
`window.__mittoPerfBuffer` (max 4096 entries).

## Instrumented seams

| Mark / measure name                          | Location                                    | Captures                                   |
| --------------------------------------------- | -------------------------------------------- | ------------------------------------------- |
| `mitto.composer.keystroke`                    | `ChatInput.js` `handleInput`                 | Input-event start                           |
| `mitto.composer.committed`                    | `ChatInput.js` `handleInput` (next frame)    | Draft state locally rendered                |
| `mitto.composer.keystroke-to-committed`       | measure, the pair above                      | Keystroke → next-paint latency              |
| `mitto.ws.chunk.applied`                      | `sessionUpdateScheduler.js` `applyUpdates`   | Background-stream chunk apply cost/cadence  |
| `mitto.prompt.sent.local`                     | `useWSDeliveryVerification.js` `sendPrompt`  | User message locally rendered (fresh sends only) |
| `mitto.prompt.sent.network`                   | `useWSDeliveryVerification.js` `sendPrompt`  | Prompt ACK confirmed (durable/reconnect/retry paths) |
| `mitto.prompt.sent.local-to-network`          | measure, the pair above                      | Local render → network-confirmed latency    |
| `mitto.ws.chunk.received`                     | `useWebSocket.js` `agent_message` handler    | Chunk arrival, paired with `mitto.ws.chunk.applied` |
| `mitto.session.switch.click`                  | `useWebSocket.js` `switchSession`            | User-intent time for a conversation switch  |
| `mitto.session.switch.firstPaint`             | `MessageList.js` (`useLayoutEffect` on `activeSessionId`) | New session's messages committed, pre-paint |
| `mitto.session.switch.click-to-firstPaint`    | measure, the pair above                      | Click → next-paint latency                  |
| `mitto.render.postprocess.tables.{start,end}` | `Message.js` agent-message `useEffect`       | Table-wrapping cost                         |
| `mitto.render.postprocess.mermaid.{start,end}`| `Message.js` agent-message `useEffect`       | Mermaid diagram render cost                 |
| `mitto.render.postprocess.beadsLinks.{start,end}` | `Message.js` agent-message `useEffect`   | Beads-ID linkify + preload cost             |
| `mitto.render.postprocess.{tables,mermaid,beadsLinks}` | measures, the pairs above           | Per-processor per-message render cost       |

## Collectors

Reusable helpers under `tests/ui/utils/perf.ts` (mitto-sus.1.2) for the
metric families that don't map onto a single named `mitto.*` mark/measure.
All read from the same `window.__mittoPerfBuffer` ring buffer as
`getPerfEntries` (via the new `getPerfEntriesByType`, which filters by native
`entryType` instead of a `mitto.` name prefix) except `collectFrameStats`
(in-page `requestAnimationFrame` sampler, no buffer needed):

| Collector                          | Returns                                                    | Notes                                                                 |
| ----------------------------------- | ----------------------------------------------------------- | ---------------------------------------------------------------------- |
| `collectLongTasks(page)`           | `{count, maxDuration, totalBlockingTime}`                  | Reduces buffered `longtask` entries; TBT = Σ `max(duration-50, 0)`.   |
| `collectEventTimings(page, name?)` | `{count, p50, p95}`                                        | Reduces buffered `event`/`first-input` entries, optional name filter. |
| `collectFrameStats(page, ms)`      | `{fps, missedFrames, longestGapMs}`                        | In-page rAF sampler vs. a 60Hz baseline; no `enablePerf()` needed.    |
| `collectPaintLayoutStats(page)`    | `{styleMs, layoutMs, paintMs, scriptingMs} \| null`         | Chromium-only (CDP `Performance.getMetrics`); cumulative — diff two calls for a window's cost. `null` elsewhere. |
| `collectDOMStats(page)`            | `{domNodes, usedJSHeapBytes: number \| null}`               | `usedJSHeapBytes` is Chromium-only (`performance.memory`), `null` elsewhere. |

## Deterministic fixtures

New mock-ACP response fixtures under `tests/fixtures/responses/`, matched by
prompt text against the mock ACP server (`tests/mocks/acp-server/`):

- `perf-plain-short.json` — 6-chunk plain-text response, 5 ms cadence.
- `perf-plain-long.json` — 200-chunk plain-text response, fixed 5 ms
  inter-chunk cadence, deterministic chunk text (`chunk NNN of ...`).
- `perf-mixed-long.json` — mixed-content response (code blocks, GFM
  tables, Mermaid diagrams, absolute URLs, Beads refs) cycled 6 times in
  one turn, exercising all three render post-processors. Trigger:
  `perf mixed long`.
- `perf-multi-stream-{a,b,c}.json` — three plain-text streams (40/80/120
  chunks) with **distinct** trigger regexes (`perf multi stream a|b|c`)
  so three concurrent background conversations never collide.

Trigger a fixture by sending a prompt containing its trigger phrase.

### Seeded history snapshots

`scripts/gen-perf-histories.mjs` generates deterministic (seeded, `--seed`
flag) history-size snapshots — committed JSON message-list content, not
sessions — under `tests/ui/perf/fixtures/histories/{small,medium,max}.json`
(10 / 1,000 / 5,000 messages; `max` is a pragmatic stand-in for the
conversation-size ceiling since no `MAX_HISTORY` constant is exported from
`web/static/` today). Regenerate with `node scripts/gen-perf-histories.mjs`.

Loading one of these into an actual session for `history-load.perf.spec.ts`
is self-seeding: the spec's `test.beforeAll` builds and runs
`tests/ui/helpers/seed-perf-history`, which reads each snapshot and writes it
directly into the live session store (bypassing ACP/UI entirely) — the same
technique `tests/ui/helpers/create-hierarchical-sessions.go` uses for
parent/child fixtures. No manual step is required; the spec only skips if
the build/run itself fails (e.g. no Go toolchain available).

## Manual inspection

```js
// In a page with ?perf=1, after driving the scenario:
performance.getEntriesByName("mitto.composer.keystroke-to-committed");
window.__mittoPerfBuffer.filter((e) => e.name.startsWith("mitto."));
```

## Environmental controls

- Release-style build (`make build`), Playwright-pinned Chromium
  (`bunx playwright --version`).
- Viewport 1440×900, `prefers-reduced-motion: reduce`.
- Warmup: 3 discarded iterations before sampling for input-latency scenarios
  (composer keystroke, prompt send, session switch); 1 warmup + 3 samples for
  stream/frame scenarios (chunk apply, multi-stream, mixed-content render) —
  these are dominated by fixed fixture playback time rather than per-sample
  noise, so a larger warmup/sample count buys little.
- Sample count: 30 for input-latency scenarios; 3 for stream/frame scenarios.
- Fixed RNG/cadence seed: fixtures above use a fixed 5 ms inter-chunk delay
  rather than a random seed, so no seed parameter is required yet.
- No DevTools attached during measurement runs.

### Recording a baseline

```bash
make bench-ui-baseline
```

This builds a release-style binary (`make build` + `tailwind` +
`build-mock-acp`), runs only `tests/ui/specs/perf/` with `PERF_RUN=1`
(env-var contract below), aggregates the run's samples via
`scripts/perf-summary.mjs`, and overwrites `tests/ui/perf/baseline.json` plus
the rendered [ui-responsiveness-baseline.md](./ui-responsiveness-baseline.md)
table. **Review the diff before committing** — `git diff
tests/ui/perf/baseline.json docs/devel/ui-responsiveness-baseline.md` — a
large unexplained swing usually means hardware contention during the run
(close other apps) rather than a real regression.

To check the current tree against the committed baseline **without**
overwriting it, use `make bench-ui` instead: it runs the same suite and
writes a diff report to `tests/ui/perf/results/latest/diff.md`.

**`PERF_RUN` / `PERF_RUN_ID` env-var contract**: every perf spec calls
`writePerfSample(scenario, metric, value, meta?)`
(`tests/ui/utils/perf.ts`), which is a no-op unless `PERF_RUN` is set — so
plain `make test-ui` (no `PERF_RUN`) is unaffected, byte-identical to before
`mitto-sus.1.3`. When set, each call appends one JSON line to
`tests/ui/perf/results/<PERF_RUN_ID>/samples.jsonl` (`PERF_RUN_ID` defaults
to `adhoc` if unset; `make bench-ui`/`make bench-ui-baseline` always pass
`PERF_RUN_ID=latest`). The four gated scenarios below (composer idle/during-
stream, local session switch, `ws.chunk.received→applied`) also read
`getBaselineValue()` under `PERF_RUN` to compute their ceiling; they no-op
(skip the assertion) if no baseline is recorded yet.

**Recorded hardware** for the committed baseline: see the "Recorded
environment" block at the top of
[ui-responsiveness-baseline.md](./ui-responsiveness-baseline.md) (OS, arch,
Playwright/Chromium versions — captured automatically by
`scripts/perf-summary.mjs`; CPU/RAM are not currently probed
programmatically, so note them manually in that file's environment block if
recording on notably different hardware).

**`make bench-ui` is intentionally NOT part of `make test-ui` / `test-all`
/ `test-ci`** — hardware-dependent timing is too noisy across CI runners to
gate ordinary regressions reliably; only the four rows below get a hard gate,
and even those compare against a locally-recorded baseline rather than a
CI-wide fixed number.

## Proposed budgets

Each row below carries a promotion decision (`mitto-sus.1.3`): **gate**
(hard `expect()` under `PERF_RUN=1`, see the named spec), or **record-only**
(sample is written via `writePerfSample()` for trend/context, no assertion).

| # | Scenario                                                    | Budget                                             | Decision | Rationale |
|---|--------------------------------------------------------------|-----------------------------------------------------|----------|-----------|
| 1 | Keystroke → next-paint (idle composer)                       | p95 ≤ 50 ms (fixed)                                 | **gate** | Fundamental UX; low variance under headless Chromium. Gated on p95 only — p50 is too flake-prone. `composer-latency.spec.ts`. |
| 2 | Keystroke → next-paint (during foreground stream)             | p95 ≤ baseline × 1.5                                | **gate** (generous) | Same fundamental, but streaming competes for the main thread — baseline-relative rather than fixed. `composer-during-stream.perf.spec.ts`. |
| 3 | Max long task during streaming                                | ≤ 100 ms (investigate anything > 200 ms)            | record-only | Highly hardware-dependent; hard-gating would flake in CI. `composer-during-stream.perf.spec.ts` / `multi-stream.perf.spec.ts`. |
| 4 | Missed-frame rate during streaming (60 Hz baseline)           | < 5 %                                                | record-only | `requestAnimationFrame` sampling is noisy; treated as a trend metric. `multi-stream.perf.spec.ts`. |
| 5 | Conversation switch click → first paint                       | ≤ 120 ms local (fixed), ≤ 300 ms incl. network fetch | **gate** (local part only) | Local paint is deterministic (`session-switch-latency.spec.ts`); the network/background-stream part (`conversation-switch.perf.spec.ts`) involves mock-ACP scheduler cadence and stays record-only. |
| 6 | `mitto.ws.chunk.received` → `mitto.ws.chunk.applied` (p95)     | ≤ baseline × 1.3                                    | **gate** | Central concern of the mitto-sus epic; must not silently regress. `ws-chunk-received-applied.spec.ts`. |
| 7 | DOM node count per 1 000 rendered messages                   | record baseline, no hard budget yet                 | record-only | Prescribed by the bead description; DOM size varies too much with content mix to fix a number yet. `history-load.perf.spec.ts`. |
| 8 | Retained heap growth per 10-cycle conversation-switch loop    | < 5 MB delta (leak proxy)                            | record-only (deferred) | No existing spec drives this specific 10-cycle loop yet — `collectDOMStats()`'s `usedJSHeapBytes` is recorded per history size in `history-load.perf.spec.ts` as a partial proxy, but a dedicated switch-loop harness is left to a future increment (out of scope for this measurement-foundation bead). |

See [ui-responsiveness-baseline.md](./ui-responsiveness-baseline.md) for the
current recorded numbers each gate compares against.

## Out of scope

Implementing optimizations, virtualization, or a framework/engine decision —
see the parent epic `mitto-sus` and its later children.
