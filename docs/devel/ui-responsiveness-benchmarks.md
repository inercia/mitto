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
- **Pending — extend seams** (`mitto-sus.1.1`): add
  `mitto.prompt.sent.{local,network}` (satisfies the AC's "separates local
  rendering from network completion"), `mitto.ws.chunk.received` (pairs
  with the existing `applied` mark for the received→applied budget),
  `mitto.session.switch.*`, and `mitto.render.postprocess.*` seams; extend
  the perf-spec suite to cover each.
- **Pending — collectors + scenarios** (`mitto-sus.1.2`): add
  `collectLongTasks` / `collectFrameStats` / `collectPaintLayoutStats` /
  `collectDOMStats` / `collectEventTimings` helpers under
  `tests/ui/utils/`; add the remaining fixtures (`perf-mixed-long`,
  `perf-multi-stream-{a,b,c}`, seeded history snapshots); add the
  `composer-during-stream` / `stream-mixed` / `history-load` /
  `conversation-switch` / `multi-stream` scenario specs. This is what
  makes the harness "report long tasks, frame/render costs, layout/paint,
  DOM size, and switch latency" per the parent AC.
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

Additional seams (`mitto.prompt.sent.{local,network}`,
`mitto.ws.chunk.received`, `mitto.session.switch.*`,
`mitto.render.postprocess.*`) are tracked as follow-up `mitto-sus.1.1`.

## Deterministic fixtures

New mock-ACP response fixtures under `tests/fixtures/responses/`, matched by
prompt text against the mock ACP server (`tests/mocks/acp-server/`):

- `perf-plain-short.json` — 6-chunk plain-text response, 5 ms cadence.
- `perf-plain-long.json` — 200-chunk plain-text response, fixed 5 ms
  inter-chunk cadence, deterministic chunk text (`chunk NNN of ...`).

Trigger a fixture by sending a prompt containing `perf plain short` or
`perf plain long` respectively.

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
