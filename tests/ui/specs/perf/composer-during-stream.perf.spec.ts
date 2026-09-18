/**
 * UI responsiveness benchmark (mitto-sus.1.2): composer keystroke -> next-paint
 * latency WHILE a foreground response is actively streaming.
 *
 * Extends composer-latency.spec.ts's idle-composer measurement with the
 * "during foreground stream" scenario from the mitto-sus.1 parent AC
 * (separate p50/p95 budget: streaming competes for the main thread). Also
 * reports collectLongTasks() so a slow keystroke can be correlated with a
 * concurrent long task from chunk-apply work.
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for the proposed (not yet
 * enforced) "during foreground stream" budget.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  collectLongTasks,
  writePerfSample,
  getBaselineValue,
} from "../../utils/perf";

// mitto-sus.1.3 row 2 ("Keystroke -> next-paint, during foreground stream"):
// promoted to a **gate (generous)** — streaming competes for the main
// thread, so the ceiling is baseline-relative (1.5x the recorded p95)
// rather than a fixed absolute number. Skipped if no baseline is recorded
// yet (e.g. the very first `make bench-ui-baseline` run).
const DURING_STREAM_P95_BASELINE_FACTOR = 1.5;

test.describe("Perf: composer latency during foreground stream", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records keystroke-to-committed measures while perf-plain-long streams", async ({
    page,
    helpers,
    selectors,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    // Start the long deterministic stream WITHOUT waiting for it to finish —
    // the whole point of this scenario is typing while chunks are still
    // being applied.
    await helpers.sendMessage(page, "perf plain long");
    await helpers.waitForUserMessage(page, "perf plain long");

    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled();
    await textarea.pressSequentially("typing during an active stream test", {
      delay: 15,
    });

    // Let the paired "committed" mark flush before draining the buffer.
    await page.waitForTimeout(200);

    const measures = await getPerfEntries(
      page,
      "mitto.composer.keystroke-to-committed",
    );
    expect(measures.length).toBeGreaterThan(0);
    for (const m of measures) {
      expect(Number.isFinite(m.duration)).toBe(true);
      expect(m.duration).toBeGreaterThanOrEqual(0);
    }

    const durations = measures.map((m) => m.duration);
    const p50 = percentile(durations, 50);
    const p95 = percentile(durations, 95);
    const longTasks = await collectLongTasks(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] composer.keystroke-to-committed (during stream): n=${durations.length} ` +
        `p50=${p50.toFixed(2)}ms p95=${p95.toFixed(2)}ms ` +
        `longTasks=${longTasks.count} maxLongTask=${longTasks.maxDuration.toFixed(2)}ms ` +
        `tbt=${longTasks.totalBlockingTime.toFixed(2)}ms`,
    );
    writePerfSample("composer.keystroke-during-stream", "p50", p50, {
      n: durations.length,
    });
    writePerfSample("composer.keystroke-during-stream", "p95", p95);
    // Row 3 ("max long task during streaming") is record-only (too
    // hardware-dependent to gate reliably) — reported here for trend/context.
    writePerfSample(
      "long-task.during-composer-stream",
      "maxDuration",
      longTasks.maxDuration,
      { count: longTasks.count },
    );

    if (process.env.PERF_RUN) {
      const baselineP95 = getBaselineValue(
        "composer.keystroke-during-stream",
        "p95",
      );
      if (baselineP95 !== null) {
        expect(p95).toBeLessThan(baselineP95 * DURING_STREAM_P95_BASELINE_FACTOR);
      }
    }

    await helpers.waitForStreamingSettled(page);
  });
});
