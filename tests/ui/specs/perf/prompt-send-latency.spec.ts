/**
 * UI responsiveness benchmark (mitto-sus.1.1): prompt-send local-render vs
 * network-confirmed latency.
 *
 * Validates the `mitto.prompt.sent.local` / `mitto.prompt.sent.network` marks
 * and the `mitto.prompt.sent.local-to-network` measure recorded by
 * useWSDeliveryVerification.js's `sendPrompt` seam (see
 * web/static/utils/perfMarks.js). This is the pair that satisfies the
 * parent bead's (mitto-sus.1) "separates local rendering from network
 * completion" acceptance-criteria bullet.
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for proposed (not yet enforced)
 * budgets.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  writePerfSample,
} from "../../utils/perf";

test.describe("Perf: prompt send latency (local vs network)", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records prompt.sent.local, prompt.sent.network and their measure for a sent prompt", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    await helpers.sendMessageAndWait(page, "perf prompt send latency check");

    // Fetch everything under the "prompt.sent." family in one call, then
    // disambiguate by entryType + exact name: a prefix match alone would
    // also catch "mitto.prompt.sent.local-to-network" when filtering for
    // "mitto.prompt.sent.local".
    const entries = await getPerfEntries(page, "mitto.prompt.sent.");
    const localMarks = entries.filter(
      (e) => e.entryType === "mark" && e.name === "mitto.prompt.sent.local",
    );
    const networkMarks = entries.filter(
      (e) => e.entryType === "mark" && e.name === "mitto.prompt.sent.network",
    );
    const measures = entries.filter(
      (e) =>
        e.entryType === "measure" &&
        e.name === "mitto.prompt.sent.local-to-network",
    );

    expect(localMarks.length).toBeGreaterThan(0);
    expect(networkMarks.length).toBeGreaterThan(0);
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
      `[perf] prompt.sent.local-to-network: n=${durations.length} ` +
        `p50=${p50.toFixed(2)}ms p95=${p95.toFixed(2)}ms`,
    );
    // Record-only (mitto-sus.1.3): not one of the 8 budgeted rows — useful
    // baseline signal for the "separates local from network" AC, no gate yet.
    writePerfSample("prompt.sent.local-to-network", "p50", p50, {
      n: durations.length,
    });
    writePerfSample("prompt.sent.local-to-network", "p95", p95);
  });
});
