/**
 * UI responsiveness benchmark (mitto-sus.1): composer keystroke -> next-paint
 * latency.
 *
 * Validates the `mitto.composer.keystroke-to-committed` perf measure recorded
 * by ChatInput.js's `handleInput` seam (see web/static/utils/perfMarks.js).
 *
 * This is a smoke test of the instrumentation itself, not a hard performance
 * gate: docs/devel/ui-responsiveness-benchmarks.md proposes budgets (e.g. p50
 * <= 16ms) but does not yet enforce them as CI assertions, since headless
 * Chromium timing varies too much across CI hardware for a strict production
 * budget to be reliable here. The assertions instead prove marks are recorded
 * with valid, non-negative numeric durations and report p50/p95 to the test
 * log, so a future dedicated benchmark harness can build on this without
 * re-deriving how to enable and drain the perf buffer.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  writePerfSample,
} from "../../utils/perf";

// mitto-sus.1.3 row 1 ("Keystroke -> next-paint, idle composer"): promoted
// to a hard **gate** on p95 only (p50 is too flake-prone under headless
// Chromium). Fixed absolute ceiling from
// docs/devel/ui-responsiveness-benchmarks.md "Proposed budgets" — low
// variance for this scenario means a baseline-relative ceiling isn't needed.
const IDLE_P95_BUDGET_MS = 50;

test.describe("Perf: composer keystroke latency", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records a keystroke-to-committed measure per input event", async ({
    page,
    helpers,
    selectors,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled();
    await textarea.pressSequentially("perf composer latency check", {
      delay: 15,
    });

    // Let the paired "committed" mark (recorded on the next animation frame)
    // flush before draining the buffer.
    await page.waitForTimeout(200);

    const measures = await getPerfEntries(
      page,
      "mitto.composer.keystroke-to-committed",
    );

    // One measure per keystroke; "perf composer latency check" is 29 chars.
    expect(measures.length).toBeGreaterThan(0);
    for (const m of measures) {
      expect(Number.isFinite(m.duration)).toBe(true);
      expect(m.duration).toBeGreaterThanOrEqual(0);
    }

    const durations = measures.map((m) => m.duration);
    const p50 = percentile(durations, 50);
    const p95 = percentile(durations, 95);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] composer.keystroke-to-committed: n=${durations.length} ` +
        `p50=${p50.toFixed(2)}ms p95=${p95.toFixed(2)}ms`,
    );
    writePerfSample("composer.keystroke", "p50", p50, { n: durations.length });
    writePerfSample("composer.keystroke", "p95", p95);

    // Only enforced under `make bench-ui` (PERF_RUN=1); make test-ui's
    // smoke-test contract (no PERF_RUN) is unaffected.
    if (process.env.PERF_RUN) {
      expect(p95).toBeLessThan(IDLE_P95_BUDGET_MS);
    }
  });

  test("records no marks when perf instrumentation is not enabled", async ({
    page,
    helpers,
    selectors,
  }) => {
    // Deliberately skip enablePerf() to prove the seam is a true no-op by
    // default (zero-cost production path, mitto-sus.1 plan requirement).
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled();
    await textarea.pressSequentially("no perf here", { delay: 15 });
    await page.waitForTimeout(200);

    const hasBuffer = await page.evaluate(
      () => (window as unknown as { __mittoPerfBuffer?: unknown[] })
        .__mittoPerfBuffer !== undefined,
    );
    expect(hasBuffer).toBe(false);

    const measures = await page.evaluate(() =>
      performance
        .getEntriesByType("measure")
        .filter((e) => e.name.startsWith("mitto.")),
    );
    expect(measures.length).toBe(0);
  });
});
