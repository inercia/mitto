/**
 * UI responsiveness benchmark (mitto-sus.1.1): WebSocket chunk
 * received -> applied cost.
 *
 * Validates the `mitto.ws.chunk.received` mark recorded at the top of
 * useWebSocket.js's `agent_message` handler, paired with the existing
 * `mitto.ws.chunk.applied` mark from sessionUpdateScheduler.js's
 * `applyUpdates`. The active-session path in the scheduler applies every
 * update immediately (no timer-based coalescing — that only happens for
 * background/inactive sessions), so for the active session under test each
 * `.received` mark has exactly one corresponding `.applied` mark from the
 * same event.
 *
 * Smoke test of the instrumentation, not a hard performance gate — see
 * docs/devel/ui-responsiveness-benchmarks.md for proposed (not yet enforced)
 * budgets, including the received->applied p95 this pair is designed to
 * measure.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import { enablePerf, getPerfEntries, percentile } from "../../utils/perf";

test.describe("Perf: ws chunk received -> applied cost", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records ws.chunk.received marks paired 1:1 with ws.chunk.applied for the active session", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    await helpers.sendMessageAndWait(page, "perf plain long");
    await helpers.waitForStreamingSettled(page);

    const received = await getPerfEntries(page, "mitto.ws.chunk.received");
    const applied = await getPerfEntries(page, "mitto.ws.chunk.applied");

    expect(received.length).toBeGreaterThan(0);
    expect(applied.length).toBeGreaterThan(0);
    // Active-session updates are applied synchronously in the same handler
    // invocation as the received mark, so counts should track closely; allow
    // +/-1 slack for a trailing chunk still in flight when the DOM settles.
    expect(Math.abs(received.length - applied.length)).toBeLessThanOrEqual(1);

    for (const m of received) {
      expect(Number.isFinite(m.startTime)).toBe(true);
    }

    // Pair each received mark with the applied mark from the same tick
    // (index-aligned after sorting by arrival order) to derive the empirical
    // received -> applied latency.
    const sortedReceived = [...received].sort(
      (a, b) => a.startTime - b.startTime,
    );
    const sortedApplied = [...applied].sort(
      (a, b) => a.startTime - b.startTime,
    );
    const pairCount = Math.min(sortedReceived.length, sortedApplied.length);
    const pairedDurations: number[] = [];
    for (let i = 0; i < pairCount; i++) {
      pairedDurations.push(sortedApplied[i].startTime - sortedReceived[i].startTime);
    }

    expect(pairedDurations.length).toBeGreaterThan(0);
    for (const d of pairedDurations) {
      expect(Number.isFinite(d)).toBe(true);
      expect(d).toBeGreaterThanOrEqual(0);
    }

    const p95 = percentile(pairedDurations, 95);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] ws.chunk.received-to-applied: n=${pairedDurations.length} ` +
        `p95=${p95.toFixed(2)}ms`,
    );
  });
});
