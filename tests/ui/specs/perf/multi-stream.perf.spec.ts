/**
 * UI responsiveness benchmark (mitto-sus.1.2): foreground frame stability
 * while several background conversations stream simultaneously.
 *
 * Drives the deterministic `perf-multi-stream-{a,b,c}` fixtures (distinct
 * trigger regexes so three concurrent background streams never collide) plus
 * `perf-plain-long` for the foreground session, then samples
 * collectFrameStats()/collectLongTasks() on the foreground session while all
 * four streams are in flight.
 *
 * Smoke test of the instrumentation + fixtures, not a hard performance gate —
 * see docs/devel/ui-responsiveness-benchmarks.md.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import { enablePerf, collectFrameStats, collectLongTasks } from "../../utils/perf";

test.describe("Perf: foreground frame stability under multi-stream load", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("samples foreground fps/long-tasks while 3 background conversations stream", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Foreground session: start the long stream but don't wait — it should
    // still be in flight while we spin up the background sessions below.
    const foregroundId = await helpers.createFreshSession(page);
    await helpers.sendMessage(page, "perf plain long");
    await helpers.waitForUserMessage(page, "perf plain long");

    // Three background sessions, each with a distinct trigger so their
    // streams don't collide. Creating a session switches the active session
    // away from the foreground one temporarily; each keeps streaming in the
    // background once created per useWSConnection.js's isStreaming rule.
    for (const label of ["a", "b", "c"]) {
      await helpers.createFreshSession(page);
      await helpers.sendMessage(page, `perf multi stream ${label}`);
      await helpers.waitForUserMessage(page, `perf multi stream ${label}`);
    }

    // Switch back to the foreground session while the others are (likely)
    // still streaming in the background.
    await page.locator(`[data-session-id="${foregroundId}"]`).click();
    await expect
      .poll(async () =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .toBe(foregroundId);

    const [frameStats, longTasks] = await Promise.all([
      collectFrameStats(page, 1500),
      collectLongTasks(page),
    ]);

    expect(Number.isFinite(frameStats.fps)).toBe(true);
    expect(frameStats.fps).toBeGreaterThanOrEqual(0);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] foreground frame stats under multi-stream load: fps=${frameStats.fps.toFixed(1)} ` +
        `missedFrames=${frameStats.missedFrames} longestGap=${frameStats.longestGapMs.toFixed(2)}ms ` +
        `longTasks=${longTasks.count} maxLongTask=${longTasks.maxDuration.toFixed(2)}ms`,
    );

    await helpers.waitForStreamingSettled(page);
  });
});
