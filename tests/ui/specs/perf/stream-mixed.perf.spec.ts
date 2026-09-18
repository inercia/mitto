/**
 * UI responsiveness benchmark (mitto-sus.1.2): mixed-content streaming cost.
 *
 * Drives the deterministic `perf-mixed-long` mock-ACP fixture — code blocks,
 * GFM tables, Mermaid diagrams, absolute URLs, and Beads references cycled
 * six times in one streamed agent turn — and validates the
 * `mitto.render.postprocess.{tables,mermaid,beadsLinks}` measures (landed in
 * mitto-sus.1.1) plus `collectPaintLayoutStats()` (Chromium-only; null
 * elsewhere) for style/layout/paint/scripting cost during the run.
 *
 * Smoke test of the instrumentation + fixture, not a hard performance gate —
 * see docs/devel/ui-responsiveness-benchmarks.md.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  collectPaintLayoutStats,
  writePerfSample,
} from "../../utils/perf";

const PROCESSORS = ["tables", "mermaid", "beadsLinks"] as const;

test.describe("Perf: mixed-content stream cost", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records render.postprocess measures and paint/layout stats for perf-mixed-long", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    const before = await collectPaintLayoutStats(page);

    await helpers.sendMessageAndWait(page, "perf mixed long");
    await helpers.waitForStreamingSettled(page);

    for (const processor of PROCESSORS) {
      const entries = await getPerfEntries(
        page,
        `mitto.render.postprocess.${processor}`,
      );
      const measures = entries.filter(
        (e) =>
          e.entryType === "measure" &&
          e.name === `mitto.render.postprocess.${processor}`,
      );
      expect(measures.length).toBeGreaterThan(0);
      for (const m of measures) {
        expect(Number.isFinite(m.duration)).toBe(true);
        expect(m.duration).toBeGreaterThanOrEqual(0);
      }
      const p95 = percentile(
        measures.map((m) => m.duration),
        95,
      );
      // eslint-disable-next-line no-console
      console.log(
        `[perf] render.postprocess.${processor} (mixed): n=${measures.length} p95=${p95.toFixed(2)}ms`,
      );
      // Record-only (mitto-sus.1.3): same seam as render-postprocess-cost.spec.ts
      // but under mixed-content load; distinct scenario key to keep them apart.
      writePerfSample(`render.postprocess.${processor}-mixed`, "p95", p95, {
        n: measures.length,
      });
    }

    const after = await collectPaintLayoutStats(page);
    if (before && after) {
      // eslint-disable-next-line no-console
      console.log(
        `[perf] paint/layout delta (mixed): style=${(after.styleMs - before.styleMs).toFixed(2)}ms ` +
          `layout=${(after.layoutMs - before.layoutMs).toFixed(2)}ms ` +
          `paint=${(after.paintMs - before.paintMs).toFixed(2)}ms ` +
          `scripting=${(after.scriptingMs - before.scriptingMs).toFixed(2)}ms`,
      );
      writePerfSample("paint-layout.mixed-delta", "styleMs", after.styleMs - before.styleMs);
      writePerfSample("paint-layout.mixed-delta", "layoutMs", after.layoutMs - before.layoutMs);
      writePerfSample("paint-layout.mixed-delta", "paintMs", after.paintMs - before.paintMs);
      writePerfSample(
        "paint-layout.mixed-delta",
        "scriptingMs",
        after.scriptingMs - before.scriptingMs,
      );
    } else {
      // eslint-disable-next-line no-console
      console.log("[perf] paint/layout stats unavailable (non-Chromium browser)");
    }
  });
});
