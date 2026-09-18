/**
 * UI responsiveness benchmark (mitto-sus.1.1): per-message render
 * post-processor cost.
 *
 * Validates the `mitto.render.postprocess.{tables,mermaid,beadsLinks}.
 * {start,end}` marks and their three paired measures recorded by
 * Message.js's agent-message useEffect. All three processors run
 * unconditionally on every agent-message HTML update (each wrapped
 * individually, inside the `if (agentMessageRef.current)` guard), so a
 * single rendered agent message — regardless of its content — exercises
 * all three seams.
 *
 * Uses the deterministic `markdown-table` mock-ACP fixture
 * (tests/fixtures/responses/markdown-table.json) so the `tables` processor
 * also has real table markup to wrap, not just an empty querySelectorAll.
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for the epic's suspected
 * root-cause framing (post-processor cost during streaming) this seam
 * family supports investigating.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  writePerfSample,
} from "../../utils/perf";

const PROCESSORS = ["tables", "mermaid", "beadsLinks"] as const;

test.describe("Perf: render post-processor cost", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records render.postprocess.{tables,mermaid,beadsLinks} marks and measures for a rendered agent message", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    await helpers.sendMessageAndWait(page, "TEST:table-split");
    await helpers.waitForStreamingSettled(page);

    for (const processor of PROCESSORS) {
      const entries = await getPerfEntries(
        page,
        `mitto.render.postprocess.${processor}`,
      );
      const startMarks = entries.filter(
        (e) =>
          e.entryType === "mark" &&
          e.name === `mitto.render.postprocess.${processor}.start`,
      );
      const endMarks = entries.filter(
        (e) =>
          e.entryType === "mark" &&
          e.name === `mitto.render.postprocess.${processor}.end`,
      );
      const measures = entries.filter(
        (e) =>
          e.entryType === "measure" &&
          e.name === `mitto.render.postprocess.${processor}`,
      );

      expect(startMarks.length).toBeGreaterThan(0);
      expect(endMarks.length).toBeGreaterThan(0);
      expect(measures.length).toBeGreaterThan(0);
      for (const m of measures) {
        expect(Number.isFinite(m.duration)).toBe(true);
        expect(m.duration).toBeGreaterThanOrEqual(0);
      }

      const durations = measures.map((m) => m.duration);
      const p95 = percentile(durations, 95);
      // eslint-disable-next-line no-console
      console.log(
        `[perf] render.postprocess.${processor}: n=${durations.length} ` +
          `p95=${p95.toFixed(2)}ms`,
      );
      // Record-only (mitto-sus.1.3): not one of the 8 budgeted rows —
      // per-processor render cost signal, no gate yet.
      writePerfSample(`render.postprocess.${processor}`, "p95", p95, {
        n: durations.length,
      });
    }
  });
});
