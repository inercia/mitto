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
- **Pending — baseline + budget-promotion** (`mitto-sus.1.3`, blocked by
  `.1.1` and `.1.2`): record `tests/ui/perf/baseline.json` against a
  release-style build, add the `make bench-ui` opt-in runner target
  (kept out of `make test-ui` / CI so ordinary regressions do not blame
  this suite), render the baseline into a Markdown table, and walk each
  row of the budget table below to promote it to a hard `expect()` gate,
  a "record + report" row, or an adjusted threshold with rationale.

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

## Environmental controls (for the eventual automated harness)

- Release-style build (`make build`), Playwright-pinned Chromium.
- Viewport 1440×900, `prefers-reduced-motion: reduce`.
- Warmup: 3 discarded iterations before sampling.
- Sample count: 30 for input-latency scenarios.
- Fixed RNG/cadence seed: fixtures above use a fixed 5 ms inter-chunk delay
  rather than a random seed, so no seed parameter is required yet.
- No DevTools attached during measurement runs.

## Proposed budgets (targets for review, not yet enforced as hard gates)

| Scenario                                                    | Budget                                             |
| ------------------------------------------------------------ | --------------------------------------------------- |
| Keystroke → next-paint (idle composer)                       | p50 ≤ 16 ms, p95 ≤ 50 ms                            |
| Keystroke → next-paint (during foreground stream)             | p50 ≤ 32 ms, p95 ≤ 100 ms                           |
| Max long task during streaming                                | ≤ 100 ms (investigate anything > 200 ms)            |
| Missed-frame rate during streaming (60 Hz baseline)           | < 5 %                                                |
| Conversation switch click → first paint                       | ≤ 120 ms local, ≤ 300 ms incl. network fetch         |
| `mitto.ws.chunk.received` → `mitto.ws.chunk.applied` (p95)     | ≤ 8 ms at chunk 200 of `perf-plain-long`             |
| DOM node count per 1 000 rendered messages                   | record baseline, no hard budget yet                 |
| Retained heap growth per 10-cycle conversation-switch loop    | < 5 MB delta (leak proxy)                            |

These budgets will be reviewed and either promoted to hard `expect()`
assertions or kept as "record + report" once `mitto-sus.1.3` records
`baseline.json` against a release-style build.

## Out of scope

Implementing optimizations, virtualization, or a framework/engine decision —
see the parent epic `mitto-sus` and its later children.
