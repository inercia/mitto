# UI Responsiveness A/B Report (mitto-sus.2)

Comparative report for the Chromium-vs-WebKit-vs-WKWebView A/B responsiveness
profile — isolates whether Mitto's perceived slowness vs VS Code is
**application-dominant**, **engine-dominant**, or **mixed**. See
[ui-responsiveness-benchmarks.md § A/B: WebKit and WKWebView legs](./ui-responsiveness-benchmarks.md#ab-webkit-and-wkwebview-legs-mitto-sus2)
for how each leg is run.

## Status

**Two-leg data recorded (Chromium + WebKit).** Per the 2026-09-23 scope
resolution on `mitto-sus.2`, the WKWebView leg C is optional/deferred (no
Playwright automation surface for the packaged native app; the manual
playbook can still be run later against
`tests/ui/perf/results/latest-wkwebview/` and `make bench-ui-ab` will fold
it in). The Chromium leg reuses the reviewed `mitto-sus.1` baseline
(`tests/ui/perf/baseline.json`, recorded 2026-09-19). The WebKit leg was
recorded 2026-09-25 via `make bench-ui-webkit-baseline`
(`tests/ui/perf/baseline-webkit.json`). The full ratio table is
auto-generated at `tests/ui/perf/results/latest/ab-report.md` and
summarised inline below.

## Recorded environment

- **Hardware / OS**: darwin / arm64 (Apple Silicon)
- **Playwright**: 1.58.0
- **Chromium baseline**: `tests/ui/perf/baseline.json` — recorded
  2026-09-19T03:46:03Z on the same host (reused from `mitto-sus.1`).
- **WebKit baseline**: `tests/ui/perf/baseline-webkit.json` — recorded
  2026-09-25T18:36:50Z via `PERF_BROWSER=webkit` +
  `bunx playwright install webkit` (WebKit build v2248, playwright-webkit
  26.0).
- **WKWebView**: not recorded on this pass; leg C descoped by the
  2026-09-23 resolution.

Two WebKit specs asserted preconditions that fail on WebKit-only builds
(`prompt.sent.network` marks never fire; the `composer-during-stream`
`perf-plain-long` fixture records no samples). Those two spec failures are
treated as data, not harness bugs — the aggregator (`scripts/perf-ab.mjs`)
records the missing metrics as `—` rather than aborting, and the surviving
18 WebKit specs still produced 62 samples across 33 scenarios.

## Per-scenario comparison

Full table lives at
[`tests/ui/perf/results/latest/ab-report.md`](../../tests/ui/perf/results/latest/ab-report.md)
(regenerate with `make bench-ui-ab`). Notable `WebKit/Chromium` ratios:

| Scenario | Metric | Chromium | WebKit | WebKit/Chromium |
|---|---|---|---|---|
| composer.keystroke | p50 | 1.50 ms | 7.00 ms | **4.67x** |
| composer.keystroke | p95 | 6.90 ms | 12.00 ms | 1.74x |
| composer.keystroke-during-stream | p50 | 3.90 ms | 7.00 ms | 1.79x |
| composer.keystroke-during-stream | p95 | 7.40 ms | 13.00 ms | 1.76x |
| history-load.max.prepend-step-0 | latencyMs | 60 ms | 172 ms | 2.87x |
| history-load.max.prepend-step-1 | latencyMs | 37 ms | 161 ms | 4.35x |
| history-load.max.prepend-step-3 | latencyMs | 40 ms | 170 ms | 4.25x |
| history-load.medium.prepend-step-0 | latencyMs | 72 ms | 193 ms | 2.68x |
| history-load.medium.prepend-step-2 | latencyMs | 44 ms | 152 ms | 3.45x |
| multi-stream.foreground-fps | fps | 120.59 | 60.59 | 0.50x |
| history-load.*.domNodes | domNodes | (n) | (n) | ~1.00x |
| render.keystroke ChatInput | count | 22 | 22 | 1.00x |
| render.queue-add-delete / set-config-option | count | 1–2 | 1–2 | 1.00x |
| render.postprocess.{beadsLinks,mermaid,tables} | p95 | 0.1–2.7 ms | 0–3 ms | ≤1.11x |
| session.switch.{local,network} | p50 | 1.6–2.6 ms | 3 ms | 1.15–1.88x |

Chromium-only metrics (`usedJSHeapBytes`, CDP `PaintLayoutStats`) are `—` on
the WebKit leg by design — see
[ui-responsiveness-benchmarks.md](./ui-responsiveness-benchmarks.md) for the
exclusion list.

## Dominant-cause classification

- **Application-dominant** (WebKit ≈ Chromium):
  `history-load.*.domNodes` (identical DOM structure across engines),
  `render.queue-add-delete`, `render.set-config-option`, `render.keystroke
  ChatInput`, `render.postprocess.*` (all ≤1.11x), `determinism.applied-marks`,
  `ws.chunk.applied-*` (all 1.00x). These scenarios are governed by
  application code paths — component render counts, DOM shape, buffered-chunk
  coalescing — with only marginal engine-attributable overhead.
- **WebKit/engine-dominant** (WebKit meaningfully slower):
  `composer.keystroke` (**4.67x p50, 1.74x p95**),
  `composer.keystroke-during-stream` (~1.8x),
  `history-load.*.prepend-step-* latencyMs` (**1.2–4.4x** across four steps
  of two fixtures), `session.switch.network p50` (1.88x). These are
  engine-attributable — WebKit's PerformanceObserver/marks path, layout
  under successive prepends, and network-confirmed session-switch timing
  are each slower than Chromium on the same hardware and app build.
- **Wrapper-dominant (WKWebView-specific)**: cannot be classified — leg C
  descoped by 2026-09-23 resolution.
- **Not a WebKit deficiency**: `multi-stream.foreground-fps` 0.50x is a
  driver refresh-rate artifact, not an application effect — headless
  Chromium runs uncapped (~120 fps here) while headless WebKit is capped
  at ~60 fps. Both engines report 0 missed frames, so this ratio is not a
  real UX gap.

## Engine-specific CSS/compositor evidence

Only the `composer.keystroke` / `composer.keystroke-during-stream` p50 gaps
plausibly implicate an engine-specific feature (input event dispatch,
mark-timing precision, or micro-layout under focused-textarea state). No
`backdrop-filter`, infinite animations, or costly compositor promotions
correlate with the observed hot spots (`history-load` prepends are
DOM-mutation heavy, not compositor-heavy). No blanket CSS audit was
performed — the classification data does not indicate one is warranted.

Two WebKit-only precondition failures (`prompt.sent.network` marks absent;
`composer-during-stream` `perf-plain-long` fixture records no samples) are
themselves cross-engine evidence — the `mitto.prompt.sent.network` seam
(`web/static/utils/perfMarks.js`) is not firing under WebKit's
PerformanceObserver, and the `perf-plain-long` streaming fixture behaves
differently under WebKit's message-scheduling. Both are recorded as `—` in
the ratio table; neither blocks the classification above.

## WKWebView-replacement decision

**Deferred pending optional native-leg data.** The two-leg comparison shows
the largest gaps are engine-attributable (WebKit) rather than
wrapper-attributable (WKWebView) — and the wrapper leg was descoped by the
2026-09-23 `mitto-sus.2` resolution. Without WKWebView-specific data, a
replacement decision would not be evidence-based. Two follow-on paths are
open:

1. **Run the WKWebView playbook later** (populates
   `tests/ui/perf/results/latest-wkwebview/`; `make bench-ui-ab` will fold
   it into this report unchanged). Only then does the `wkwebview/webkit`
   ratio column become populated and a `Rejected` / `Referred` decision
   defensible.
2. **Prioritise in-flight `mitto-sus.*` application-level work** on the
   engine-dominant scenarios above (composer keystroke path, history-load
   prepend latency). Even if WKWebView is later replaced, those gaps would
   remain because they are engine-attributable, not wrapper-attributable.

Until (1) is executed, no `Rejected` / `Deferred pending sus.*` /
`Referred to a separate cost-analysis ticket` verdict can be recorded.
