/**
 * UI responsiveness benchmark (mitto-sus.1.1): conversation switch latency.
 *
 * Validates the `mitto.session.switch.click` mark (useWebSocket.js's
 * `switchSession`, covering every switch entry point since they all funnel
 * through this one callback) and the `mitto.session.switch.firstPaint` mark
 * + `mitto.session.switch.click-to-firstPaint` measure (MessageList.js's
 * `useLayoutEffect` keyed on `activeSessionId`).
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for the proposed (not yet
 * enforced) click -> first-paint budget.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import { enablePerf, getPerfEntries, percentile } from "../../utils/perf";

test.describe("Perf: session switch latency", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records session.switch.click, .firstPaint and their measure on a conversation switch", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    const sessionA = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, "perf session switch check A");

    await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, "perf session switch check B");

    // Switch back to session A via its sidebar entry — this is the same
    // data-session-id click target used by every switch path (sidebar,
    // keyboard, swipe, native menu all funnel through switchSession).
    await page.locator(`[data-session-id="${sessionA}"]`).click();
    await expect
      .poll(async () =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .toBe(sessionA);
    // Let the paired firstPaint mark (useLayoutEffect, fires pre-paint) and
    // its measure settle before draining the buffer.
    await page.waitForTimeout(200);

    const entries = await getPerfEntries(page, "mitto.session.switch.");
    const clickMarks = entries.filter(
      (e) => e.entryType === "mark" && e.name === "mitto.session.switch.click",
    );
    const firstPaintMarks = entries.filter(
      (e) =>
        e.entryType === "mark" &&
        e.name === "mitto.session.switch.firstPaint",
    );
    const measures = entries.filter(
      (e) =>
        e.entryType === "measure" &&
        e.name === "mitto.session.switch.click-to-firstPaint",
    );

    expect(clickMarks.length).toBeGreaterThan(0);
    expect(firstPaintMarks.length).toBeGreaterThan(0);
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
      `[perf] session.switch.click-to-firstPaint: n=${durations.length} ` +
        `p50=${p50.toFixed(2)}ms p95=${p95.toFixed(2)}ms`,
    );
  });
});
