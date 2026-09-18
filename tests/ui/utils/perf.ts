// Helpers for the UI responsiveness benchmark specs (mitto-sus.1).
//
// Wraps the opt-in perf-mark instrumentation in web/static/utils/perfMarks.js:
// enable it before navigation, then drain the `window.__mittoPerfBuffer` ring
// buffer it populates once enabled. See docs/devel/ui-responsiveness-benchmarks.md
// for the full seam catalogue and the (not yet enforced) proposed budgets.

import { Page } from "@playwright/test";

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
