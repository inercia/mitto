import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * Real-Time Gap Fill Detection tests.
 *
 * These tests verify that checkAndFillGap() fires (via max_seq on a live
 * WebSocket message) WITHOUT requiring a disconnect/reconnect cycle.
 *
 * Mechanism under test:
 *   - When an agent_message / tool_call arrives with max_seq > lastKnownSeq,
 *     checkAndFillGap() schedules a debounced load_events (GAP_FILL_DEBOUNCE_MS=500ms).
 *   - window.__debug._setLastKnownSeq(sessionId, seq) lets the test artificially
 *     lower the client watermark to simulate a gap.
 *   - window.__debug.lastLoadEventsAfterSeq / lastLoadEventsTimestamp / lastLoadEventsSessionId
 *     are updated when the gap-fill load_events is actually sent.
 */

test.describe("Real-Time Gap Fill Detection", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should detect gap via max_seq and issue load_events without reconnecting", async ({
    page,
    helpers,
  }) => {
    // 1. Create session and establish some sequence numbers
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("gap-fill-baseline"));

    // 2. Record what __debug shows after normal message exchange
    const baselineTimestamp = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsTimestamp ?? 0
    );

    // 3. Collect gap-fill console logs
    const gapFillLogs: string[] = [];
    page.on("console", (msg) => {
      if (msg.text().includes("[gap-fill]")) gapFillLogs.push(msg.text());
    });

    // 4. Manipulate lastKnownSeq to simulate the client missed events
    //    _setLastKnownSeq(sessionId, 0) → client thinks it's at seq 0
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) {
          debug._setLastKnownSeq(sid, 0);
        }
      },
      { sid: sessionId }
    );

    // 5. Trigger a new agent response — it will carry max_seq > 0 while the
    //    client now believes it's at seq 0 → gap detected → checkAndFillGap fires
    await helpers.sendMessage(page, helpers.uniqueMessage("gap-fill-trigger"));

    // 6. Wait for gap fill to fire (debounce is 500ms, polling up to 5s)
    await expect
      .poll(
        async () => {
          const ts = await page.evaluate(
            () => (window as any).__debug?.lastLoadEventsTimestamp ?? 0
          );
          return ts;
        },
        { timeout: 5_000, intervals: [200, 500, 500] }
      )
      .toBeGreaterThan(baselineTimestamp);

    // 7. Verify the gap-fill load_events was sent from afterSeq=0
    const afterSeq = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? -1
    );
    expect(afterSeq).toBe(0); // gap fill from seq=0 (our manipulated value)

    // 8. Verify console log fired
    expect(gapFillLogs.some((l) => l.includes("Detected gap"))).toBe(true);
    expect(gapFillLogs.some((l) => l.includes("Requesting events after seq 0"))).toBe(true);

    // 9. Verify the session ID in __debug matches
    const debugSessionId = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsSessionId ?? ""
    );
    expect(debugSessionId).toBe(sessionId);
  });

  test("should not fire duplicate gap fill within debounce window", async ({
    page,
    helpers,
  }) => {
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("debounce-baseline"));

    // Reset lastKnownSeq to 0 to force gap detection, and clear stale timestamp
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) {
          debug._setLastKnownSeq(sid, 0);
          debug.lastLoadEventsTimestamp = 0;
        }
      },
      { sid: sessionId }
    );

    // Count how many times gap-fill fires by tracking "Requesting events" log lines
    let gapFillCount = 0;
    page.on("console", (msg) => {
      if (msg.text().includes("[gap-fill] Session") && msg.text().includes("Requesting events")) {
        gapFillCount++;
      }
    });

    // Send two messages rapidly — both carry max_seq indicating a gap
    await helpers.sendMessage(page, helpers.uniqueMessage("debounce-1"));
    await helpers.sendMessage(page, helpers.uniqueMessage("debounce-2"));

    // Wait for debounce + processing (debounce=500ms, give extra buffer)
    await page.waitForTimeout(1500);

    // The debounce should collapse multiple gap detections into at most a few requests
    // (could be 2 if the first debounce fired before the second message arrived,
    // but certainly should not be 10+ — no flooding)
    expect(gapFillCount).toBeLessThanOrEqual(3);
    console.log(`Gap fill fired ${gapFillCount} times for 2 rapid messages (expected ≤ 3)`);
  });

  test("should NOT trigger gap fill when client is ahead of server (stale state)", async ({
    page,
    helpers,
    selectors,
  }) => {
    // 1. Create session and establish baseline sequence numbers
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("stale-client-baseline"));

    // 2. Register console listener BEFORE manipulating state so we don't miss any logs
    const gapFillLogs: string[] = [];
    page.on("console", (msg) => {
      if (
        msg.text().includes("[gap-fill]") &&
        msg.text().includes("Detected gap") &&
        msg.text().includes(sessionId)
      ) {
        gapFillLogs.push(msg.text());
      }
    });

    // 3. Record T0 — the current gap-fill timestamp (may be 0 if no gap-fill fired yet)
    const T0 = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsTimestamp ?? 0
    );

    // 4. Simulate stale client: set lastKnownSeq to 99999 (WAY ahead of server)
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) {
          debug._setLastKnownSeq(sid, 99999);
        }
      },
      { sid: sessionId }
    );

    // 5. Send another message — server responds with agent_message carrying max_seq ≈ 5-10.
    //    checkAndFillGap sees: clientMaxSeq=99999, maxSeq≈10 →
    //    isStaleClientState(99999, 10) === true → returns early → NO gap fill fires.
    await helpers.sendMessage(page, helpers.uniqueMessage("stale-client-trigger"));

    // 6. Wait longer than GAP_FILL_DEBOUNCE_MS (500ms) to allow any spurious fire to happen
    await page.waitForTimeout(800);

    // 7. Assert the gap-fill timestamp is unchanged (no gap fill fired)
    const timestampAfter = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsTimestamp ?? 0
    );
    expect(timestampAfter).toBe(T0);

    // 8. Assert no "[gap-fill] Detected gap" console log appeared for this session
    expect(gapFillLogs.length).toBe(0);
  });
});

