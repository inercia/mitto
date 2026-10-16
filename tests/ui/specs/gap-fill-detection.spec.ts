import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

type CapturedFrame = { url: string; message: any };

function observeWebSocketFrames(page: any) {
  const socketUrls: string[] = [];
  const closedSocketUrls: string[] = [];
  const frames: CapturedFrame[] = [];

  page.on("websocket", (socket: any) => {
    const url = socket.url();
    socketUrls.push(url);
    socket.on("close", () => closedSocketUrls.push(url));
    socket.on("framesent", (event: any) => {
      const raw =
        typeof event.payload === "string"
          ? event.payload
          : event.payload.toString();
      try {
        frames.push({ url, message: JSON.parse(raw) });
      } catch {
        // Non-JSON WebSocket frames are irrelevant to the load_events assertions.
      }
    });
  });

  return { socketUrls, closedSocketUrls, frames };
}

function sessionLoadEvents(frames: CapturedFrame[], sessionId: string) {
  return frames.filter(
    ({ url, message }) =>
      url.includes(sessionId) && message?.type === "load_events",
  );
}

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
    const observed = observeWebSocketFrames(page);

    // 1. Create session and establish some sequence numbers
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(
      page,
      helpers.uniqueMessage("gap-fill-baseline"),
    );

    const framesBeforeGap = observed.frames.length;
    const sessionSocketCount = () =>
      observed.socketUrls.filter((url) => url.includes(sessionId)).length;
    const closedSessionSocketCount = () =>
      observed.closedSocketUrls.filter((url) => url.includes(sessionId)).length;
    const socketsBeforeGap = sessionSocketCount();
    const closedBeforeGap = closedSessionSocketCount();
    expect(socketsBeforeGap).toBeGreaterThan(0);

    // 4. Manipulate the client watermark to simulate the client having missed
    //    all events (clientMaxSeq=0 → any max_seq > 0 triggers gap fill).
    //    _setLastKnownSeq writes the sequence-store watermark read by SessionStream.
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) debug._setLastKnownSeq(sid, 0);
      },
      { sid: sessionId },
    );

    // 5. Trigger a new agent response — it will carry max_seq > 0 while the
    //    client now believes it's at seq 0 → gap detected → checkAndFillGap fires
    await helpers.sendMessage(page, helpers.uniqueMessage("gap-fill-trigger"));

    // 6. Observe the actual outbound gap-fill frame (500ms debounce, up to 5s).
    const gapFillFrames = () =>
      sessionLoadEvents(
        observed.frames.slice(framesBeforeGap),
        sessionId,
      ).filter(({ message }) => message.data?.after_seq === 0);
    await expect
      .poll(() => gapFillFrames().length, {
        timeout: 5_000,
        intervals: [200, 500, 500],
      })
      .toBeGreaterThan(0);

    // 7. Verify the wire request contains the gap-fill window from seq 0.
    const gapFillFrame = gapFillFrames()[0];
    expect(gapFillFrame.message.data).toEqual({ after_seq: 0, limit: 100 });

    // The gap fill is in-band on the existing session socket, not a reconnect.
    expect(sessionSocketCount()).toBe(socketsBeforeGap);
    expect(closedSessionSocketCount()).toBe(closedBeforeGap);
  });

  test("should not fire duplicate gap fill within debounce window", async ({
    page,
    helpers,
  }) => {
    const observed = observeWebSocketFrames(page);
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(
      page,
      helpers.uniqueMessage("debounce-baseline"),
    );
    const framesBeforeGap = observed.frames.length;

    // Reset lastKnownSeq to 0 to force gap detection.
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) debug._setLastKnownSeq(sid, 0);
      },
      { sid: sessionId },
    );

    // Send two messages rapidly — both carry max_seq indicating a gap
    await helpers.sendMessage(page, helpers.uniqueMessage("debounce-1"));
    await helpers.sendMessage(page, helpers.uniqueMessage("debounce-2"));

    // Wait for debounce + processing (debounce=500ms, give extra buffer)
    await page.waitForTimeout(1500);

    // The debounce should collapse multiple gap detections into at most a few requests
    // (could be 2 if the first debounce fired before the second message arrived,
    // but certainly should not be 10+ — no flooding)
    const gapFillCount = sessionLoadEvents(
      observed.frames.slice(framesBeforeGap),
      sessionId,
    ).filter(({ message }) => message.data?.after_seq === 0).length;
    expect(gapFillCount).toBeGreaterThan(0);
    expect(gapFillCount).toBeLessThanOrEqual(3);
  });

  test("should NOT trigger gap fill when client is ahead of server (stale state)", async ({
    page,
    helpers,
  }) => {
    const observed = observeWebSocketFrames(page);
    // 1. Create session and establish baseline sequence numbers
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(
      page,
      helpers.uniqueMessage("stale-client-baseline"),
    );
    const framesBeforeStaleTrigger = observed.frames.length;

    // 4. Simulate stale client: set lastKnownSeq to 99999 (WAY ahead of server)
    await page.evaluate(
      ({ sid }) => {
        const debug = (window as any).__debug;
        if (debug?._setLastKnownSeq) {
          debug._setLastKnownSeq(sid, 99999);
        }
      },
      { sid: sessionId },
    );

    // 5. Send another message — server responds with agent_message carrying max_seq ≈ 5-10.
    //    checkAndFillGap sees: clientMaxSeq=99999, maxSeq≈10 →
    //    isStaleClientState(99999, 10) === true → returns early → NO gap fill fires.
    await helpers.sendMessage(
      page,
      helpers.uniqueMessage("stale-client-trigger"),
    );

    // 6. Wait longer than GAP_FILL_DEBOUNCE_MS (500ms) to allow any spurious fire to happen
    await page.waitForTimeout(800);

    // 7. A stale-ahead client must not request incremental gap-fill events.
    const incrementalLoadEvents = sessionLoadEvents(
      observed.frames.slice(framesBeforeStaleTrigger),
      sessionId,
    ).filter(({ message }) => message.data?.after_seq !== undefined);
    expect(incrementalLoadEvents).toHaveLength(0);
  });
});
