/**
 * UI responsiveness benchmark (mitto-sus.1.2): conversation switch latency
 * UNDER LOAD — session A is background-streaming while the user switches away
 * and back.
 *
 * Extends session-switch-latency.spec.ts (the no-load baseline, landed in
 * mitto-sus.1.1) with the scenario the parent AC actually calls out: "rapid
 * switching between conversations, including one streaming background
 * conversation." Validates the same `mitto.session.switch.click`,
 * `.firstPaint`, and `.click-to-firstPaint` seams still fire correctly while
 * session A has an in-flight background stream.
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for the proposed (not yet
 * enforced) click -> first-paint budget.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  writePerfSample,
} from "../../utils/perf";

test.describe("Perf: conversation switch latency under background load", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records session.switch measures when switching away from and back to a streaming session", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Session A: start a long stream but do NOT wait for it to finish, then
    // immediately switch away — A keeps streaming in the background per
    // useWSConnection.js's isStreaming keep-connected rule.
    const sessionA = await helpers.createFreshSession(page);
    await helpers.sendMessage(page, "perf plain long");
    await helpers.waitForUserMessage(page, "perf plain long");

    await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, "perf session switch check B");

    // Switch back to session A while it is (likely) still streaming.
    await page.locator(`[data-session-id="${sessionA}"]`).click();
    await expect
      .poll(async () =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .toBe(sessionA);
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
      `[perf] session.switch.click-to-firstPaint (under load): n=${durations.length} ` +
        `p50=${p50.toFixed(2)}ms p95=${p95.toFixed(2)}ms`,
    );
    // mitto-sus.1.3 row 5, network part: **record-only** — this scenario
    // includes a live background stream (mock-ACP scheduler cadence), so
    // unlike the local-only session-switch-latency.spec.ts it is not gated.
    writePerfSample("session.switch.network", "p50", p50, {
      n: durations.length,
    });
    writePerfSample("session.switch.network", "p95", p95);

    await helpers.waitForStreamingSettled(page);
  });
});
