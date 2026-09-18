// Helpers for the UI responsiveness benchmark specs (mitto-sus.1).
//
// Wraps the opt-in perf-mark instrumentation in web/static/utils/perfMarks.js:
// enable it before navigation, then drain the `window.__mittoPerfBuffer` ring
// buffer it populates once enabled. See docs/devel/ui-responsiveness-benchmarks.md
// for the full seam catalogue and the (not yet enforced) proposed budgets.

import { Page } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export interface PerfEntry {
  name: string;
  entryType: string;
  startTime: number;
  duration: number;
}

/**
 * Enable the opt-in UI responsiveness perf instrumentation for this page.
 * Must be called BEFORE the page navigates (it installs an init script so
 * `window.__mittoPerf` is set before app.js bootstraps and calls
 * `installPerfBuffer()`).
 */
export async function enablePerf(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as unknown as { __mittoPerf: boolean }).__mittoPerf = true;
  });
}

/**
 * Drain `window.__mittoPerfBuffer` entries whose name starts with the given
 * `mitto.*` prefix (e.g. "mitto.composer." or "mitto.ws.chunk.applied").
 */
export async function getPerfEntries(
  page: Page,
  namePrefix: string,
): Promise<PerfEntry[]> {
  return page.evaluate((prefix) => {
    const buffer =
      (window as unknown as { __mittoPerfBuffer?: PerfEntry[] })
        .__mittoPerfBuffer || [];
    return buffer.filter((entry) => entry.name.startsWith(prefix));
  }, namePrefix);
}

/** Nearest-rank percentile (p in [0, 100]) over a list of numeric samples. */
export function percentile(values: number[], p: number): number {
  if (values.length === 0) return NaN;
  const sorted = [...values].sort((a, b) => a - b);
  const rank = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.min(Math.max(rank, 0), sorted.length - 1)];
}

/**
 * Drain `window.__mittoPerfBuffer` entries of the given native PerformanceEntry
 * `entryType` (e.g. "longtask", "event", "first-input"). Unlike getPerfEntries
 * (name-prefix filter for our own `mitto.*` marks/measures), this is for
 * browser-native entries whose `name` is not under our control.
 */
export async function getPerfEntriesByType(
  page: Page,
  entryType: string,
): Promise<PerfEntry[]> {
  return page.evaluate((type) => {
    const buffer =
      (window as unknown as { __mittoPerfBuffer?: PerfEntry[] })
        .__mittoPerfBuffer || [];
    return buffer.filter((entry) => entry.entryType === type);
  }, entryType);
}

// --- Collectors (mitto-sus.1.2) -------------------------------------------
//
// Reusable helpers for the remaining metric families from the mitto-sus.1
// parent acceptance criteria: long tasks, input/event timings, frame/render
// costs, layout/paint, and DOM size. See
// docs/devel/ui-responsiveness-benchmarks.md "Collectors" for the seam each
// one reduces and its Chromium-only caveats.

export interface LongTaskStats {
  count: number;
  maxDuration: number;
  /** Sum of max(duration - 50ms, 0) across all long tasks (standard TBT formula). */
  totalBlockingTime: number;
}

/** Reduces buffered `longtask` PerformanceObserver entries. Requires enablePerf(). */
export async function collectLongTasks(page: Page): Promise<LongTaskStats> {
  const entries = await getPerfEntriesByType(page, "longtask");
  return {
    count: entries.length,
    maxDuration: entries.reduce((max, e) => Math.max(max, e.duration), 0),
    totalBlockingTime: entries.reduce(
      (sum, e) => sum + Math.max(e.duration - 50, 0),
      0,
    ),
  };
}

export interface EventTimingStats {
  count: number;
  p50: number;
  p95: number;
}

/**
 * Reduces buffered `event` / `first-input` entries into p50/p95 durations.
 * Pass `nameFilter` (exact entry name, e.g. "keydown") to narrow to one
 * interaction type; omit to aggregate across all observed event timings.
 */
export async function collectEventTimings(
  page: Page,
  nameFilter?: string,
): Promise<EventTimingStats> {
  const [eventEntries, firstInputEntries] = await Promise.all([
    getPerfEntriesByType(page, "event"),
    getPerfEntriesByType(page, "first-input"),
  ]);
  let entries = [...eventEntries, ...firstInputEntries];
  if (nameFilter) entries = entries.filter((e) => e.name === nameFilter);
  const durations = entries.map((e) => e.duration);
  return {
    count: durations.length,
    p50: percentile(durations, 50),
    p95: percentile(durations, 95),
  };
}

export interface FrameStats {
  fps: number;
  missedFrames: number;
  longestGapMs: number;
}

/**
 * Samples `requestAnimationFrame` callbacks in-page for `durationMs` against
 * a 60Hz baseline. A "missed" frame is a gap > 1.5x the 60Hz budget (~25ms).
 * Runs entirely in the page (no PerformanceObserver / enablePerf() needed).
 */
export async function collectFrameStats(
  page: Page,
  durationMs: number,
): Promise<FrameStats> {
  return page.evaluate((duration) => {
    return new Promise<{
      fps: number;
      missedFrames: number;
      longestGapMs: number;
    }>((resolve) => {
      const budgetMs = 1000 / 60;
      const start = performance.now();
      let last = start;
      let frames = 0;
      let missedFrames = 0;
      let longestGapMs = 0;
      function tick(now: number) {
        if (frames > 0) {
          const gap = now - last;
          if (gap > budgetMs * 1.5) missedFrames += 1;
          if (gap > longestGapMs) longestGapMs = gap;
        }
        last = now;
        frames += 1;
        if (now - start < duration) {
          requestAnimationFrame(tick);
        } else {
          const elapsedSec = (now - start) / 1000;
          resolve({
            fps: elapsedSec > 0 ? frames / elapsedSec : 0,
            missedFrames,
            longestGapMs,
          });
        }
      }
      requestAnimationFrame(tick);
    });
  }, durationMs);
}

export interface PaintLayoutStats {
  styleMs: number;
  layoutMs: number;
  paintMs: number;
  scriptingMs: number;
}

/**
 * Chromium-only: reads cumulative style/layout/paint/scripting time via CDP
 * `Performance.getMetrics` (values are cumulative since navigation start, in
 * seconds; converted to ms here). Returns null on non-Chromium browsers or if
 * CDP is unavailable — callers must treat null as "not measured", never throw.
 * To measure a window's cost, call before and after and subtract.
 */
export async function collectPaintLayoutStats(
  page: Page,
): Promise<PaintLayoutStats | null> {
  const browserName = page.context().browser()?.browserType().name();
  if (browserName !== "chromium") return null;
  try {
    const client = await page.context().newCDPSession(page);
    await client.send("Performance.enable");
    const { metrics } = await client.send("Performance.getMetrics");
    const byName = new Map(metrics.map((m) => [m.name, m.value]));
    await client.detach().catch(() => {});
    return {
      styleMs: (byName.get("RecalcStyleDuration") ?? 0) * 1000,
      layoutMs: (byName.get("LayoutDuration") ?? 0) * 1000,
      paintMs: (byName.get("PaintDuration") ?? 0) * 1000,
      scriptingMs: (byName.get("ScriptDuration") ?? 0) * 1000,
    };
  } catch {
    return null;
  }
}

export interface DOMStats {
  domNodes: number;
  usedJSHeapBytes: number | null;
}

/** DOM node count + (Chromium-only) retained JS heap size; heap is null elsewhere. */
export async function collectDOMStats(page: Page): Promise<DOMStats> {
  return page.evaluate(() => {
    const domNodes = document.getElementsByTagName("*").length;
    const heap = (
      performance as unknown as { memory?: { usedJSHeapSize?: number } }
    ).memory;
    return {
      domNodes,
      usedJSHeapBytes:
        heap && typeof heap.usedJSHeapSize === "number"
          ? heap.usedJSHeapSize
          : null,
    };
  });
}

// --- Baseline recording + gating (mitto-sus.1.3) ---------------------------
//
// `make bench-ui` runs the perf specs with PERF_RUN=1, causing writePerfSample
// to append every recorded sample to a per-run results file. `make
// bench-ui-baseline` additionally aggregates that run into the committed
// `tests/ui/perf/baseline.json` (via scripts/perf-summary.mjs). Specs that
// carry a hard budget gate (see docs/devel/ui-responsiveness-benchmarks.md
// "Proposed budgets") read the committed baseline back via getBaselineValue
// to compute a relative ceiling; the gate itself only runs under PERF_RUN=1,
// so `make test-ui`'s smoke-test contract (no PERF_RUN) is unaffected.

const PERF_RESULTS_ROOT = path.resolve(__dirname, "../perf/results");
const PERF_BASELINE_PATH = path.resolve(__dirname, "../perf/baseline.json");

export interface BaselineSample {
  n?: number;
  [metric: string]: number | undefined;
}

export interface Baseline {
  recorded_at: string;
  environment: Record<string, string>;
  samples: Record<string, BaselineSample>;
}

/**
 * Record one perf sample for the current `make bench-ui` run. No-op unless
 * `PERF_RUN` is set, so calling this from every perf spec has zero effect on
 * the normal `make test-ui` smoke-test path. Appends one JSON line to
 * `tests/ui/perf/results/<PERF_RUN_ID>/samples.jsonl` (combined across specs
 * — Playwright's perf config runs serially with a single worker, so
 * sequential appendFileSync calls never interleave).
 */
export function writePerfSample(
  scenario: string,
  metric: string,
  value: number,
  meta?: Record<string, unknown>,
): void {
  if (!process.env.PERF_RUN) return;
  const runId = process.env.PERF_RUN_ID || "adhoc";
  const runDir = path.join(PERF_RESULTS_ROOT, runId);
  fs.mkdirSync(runDir, { recursive: true });
  const line = JSON.stringify({
    scenario,
    metric,
    value,
    ...(meta ? { meta } : {}),
    ts: new Date().toISOString(),
  });
  fs.appendFileSync(path.join(runDir, "samples.jsonl"), line + "\n");
}

/** Reads the committed `tests/ui/perf/baseline.json`, or null if absent/unreadable. */
export function loadBaseline(): Baseline | null {
  try {
    const raw = fs.readFileSync(PERF_BASELINE_PATH, "utf-8");
    return JSON.parse(raw) as Baseline;
  } catch {
    return null;
  }
}

/** Convenience accessor: a single (scenario, metric) value from the baseline, or null. */
export function getBaselineValue(
  scenario: string,
  metric: string,
): number | null {
  const value = loadBaseline()?.samples?.[scenario]?.[metric];
  return typeof value === "number" ? value : null;
}
