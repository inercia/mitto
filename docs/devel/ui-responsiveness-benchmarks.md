# UI Responsiveness Benchmarks (mitto-sus.1)

Foundation for the `mitto-sus` responsiveness epic: turns "Mitto feels slower
than VS Code" into reproducible measurements and agreed budgets. This
document is the benchmark procedure; it is intentionally **measurement-only**
— no optimizations or framework decisions are made here (those belong to the
later `mitto-sus.*` children).

## Status

- **Landed**: the opt-in performance-mark instrumentation (`web/static/utils/perfMarks.js`)
  and the first instrumented seams (composer keystroke, background chunk
  apply), plus deterministic mock-ACP streaming fixtures.
- **Pending** (tracked as the next stage of `mitto-sus.1`): the Playwright
  benchmark harness under `tests/ui/perf/` that drives these seams/fixtures
  end-to-end, computes the metrics below, and records `baseline.json`. Until
  that lands, the marks below can be inspected manually (see "Manual
  inspection" below) but there is no automated `make bench-ui` target yet.

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

Additional seams called for by the bead scope (`mitto.ws.chunk.received`,
`mitto.session.switch.*`, `mitto.render.postprocess.*`) are deferred to a
follow-up increment to keep this landing surgical; see the `Implementation:`
comment on `mitto-sus.1` for the rationale.

## Deterministic fixtures

New mock-ACP response fixtures under `tests/fixtures/responses/`, matched by
prompt text against the mock ACP server (`tests/mocks/acp-server/`):

- `perf-plain-short.json` — 6-chunk plain-text response, 5 ms cadence.
- `perf-plain-long.json` — 200-chunk plain-text response, fixed 5 ms
  inter-chunk cadence, deterministic chunk text (`chunk NNN of ...`).

Trigger a fixture by sending a prompt containing `perf plain short` or
`perf plain long` respectively.

## Manual inspection (until the harness lands)

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
assertions or kept as "record + report" once the Playwright harness exists
and produces real baseline numbers (Test/Review phases of `mitto-sus.1`).

## Out of scope

Implementing optimizations, virtualization, or a framework/engine decision —
see the parent epic `mitto-sus` and its later children.
