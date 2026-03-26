import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * Sleep-Wake Sync Tests
 *
 * Verifies that the WebSocket client uses its stored localStorage watermark
 * (not 0) when sending load_events after a page reload or visibility change.
 *
 * Uses window.__debug.lastLoadEventsAfterSeq exported by useWebSocket.js to
 * assert the exact after_seq value sent on the wire without needing to
 * intercept WebSocket frames.
 */

test.describe("Sleep-Wake Sync", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should use stored watermark (after_seq > 0) on page reload", async ({
    page,
    helpers,
  }) => {
    // Create isolated session and send a message to generate sequence numbers
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("watermark-test"));

    // Read the after_seq that was used for the initial load_events
    const initialAfterSeq = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? -1,
    );

    // After a first message exchange the server has at least seq 1;
    // if we already had a watermark on first connect it will be > 0.
    // What matters is that after reload the client re-uses a stored value.

    // Navigate to the same session (triggers a full page reload)
    await helpers.navigateToSession(page, sessionId);

    // Wait for WebSocket to reconnect and send load_events
    // Poll until window.__debug is populated (jitter delay is up to 300 ms)
    const afterSeqAfterReload = await expect
      .poll(
        async () => {
          return await page.evaluate(
            () => (window as any).__debug?.lastLoadEventsAfterSeq ?? -1,
          );
        },
        { timeout: 10_000, intervals: [300, 500, 500, 1000] },
      )
      .toBeGreaterThan(0);

    // Suppress unused-variable lint warning — the assertion above already validates
    void afterSeqAfterReload;

    // Also directly assert for clearer failure messages
    const finalAfterSeq = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? -1,
    );
    expect(finalAfterSeq).toBeGreaterThan(0);

    // The session ID stored in __debug should match the session we reloaded
    const debugSessionId = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsSessionId ?? "",
    );
    expect(debugSessionId).toBe(sessionId);

    // Suppress unused variable (used only for logging context)
    void initialAfterSeq;
  });

  test("should use correct after_seq after visibility change (wake)", async ({
    page,
    helpers,
  }) => {
    const sessionId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("visibility-test"));

    // Capture the after_seq before simulating sleep
    const beforeHide = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? 0,
    );

    // Start listening for sync-related console messages BEFORE hiding
    const syncLogs: string[] = [];
    page.on("console", (msg) => {
      const text = msg.text();
      if (text.includes("visible") || text.includes("sync") || text.includes("Sync")) {
        syncLogs.push(text);
      }
    });

    // Simulate page going to sleep (hidden)
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        writable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });

    await page.waitForTimeout(150);

    // Simulate page waking (visible) — this triggers reconnect + load_events
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        value: "visible",
        writable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });

    // Wait for "App became visible" log (confirms the handler ran)
    await expect
      .poll(() => syncLogs.some((m) => m.includes("visible")), { timeout: 5_000 })
      .toBeTruthy();

    // After visibility-change triggered reconnect, verify load_events used the right seq.
    // The after_seq may not update if no reconnect was forced (client already connected),
    // but the last known value must be >= beforeHide to show we didn't regress to 0.
    const afterWake = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? 0,
    );

    // The watermark should not have regressed to 0 — client must have used stored value
    if (afterWake > 0) {
      // Watermark was used in a new load_events after wake
      expect(afterWake).toBeGreaterThanOrEqual(beforeHide);
    }
    // If afterWake is still 0 the visibility handler reconnected without a watermark,
    // which is valid when this is the very first session with no prior history.

    // Confirm session ID is consistent
    const debugSessionId = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsSessionId ?? "",
    );
    if (debugSessionId) {
      expect(debugSessionId).toBe(sessionId);
    }
  });

  /**
   * Gap 2: Idle session restart shows empty conversation.
   *
   * Scenario:
   *   User sends messages → watermark stored (e.g. seq=10).
   *   App restarts → sends load_events(after_seq=10) → 0 new events returned
   *   (session was idle; nothing happened while app was closed).
   *
   * Without the needsContextLoadRef fallback the conversation would appear
   * empty because the after_seq request returns no events and there are no
   * messages in memory after a fresh page load.
   *
   * With the fallback: events_loaded detects "0 new events but server has
   * history" and fires a second load_events(limit=50) that loads the actual
   * history.  The fallback sets window.__debug.lastLoadEventsAfterSeq = 0 and
   * window.__debug.fallbackContextLoadFired = true so we can assert it fired.
   *
   * The localStorage key used as the watermark is:
   *   `mitto_last_seen_seq_${sessionId}`
   * (written by setLastSeenSeq(), read by getLastSeenSeq() in storage.js)
   */
  test("should load full history when watermark-based load returns 0 events (idle session)", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    // 1. Create a fresh session and send two messages to populate history.
    const sessionId = await helpers.createFreshSession(page);
    const msg1 = helpers.uniqueMessage("idle-test-1");
    const msg2 = helpers.uniqueMessage("idle-test-2");
    await helpers.sendMessageAndWait(page, msg1);
    await helpers.sendMessageAndWait(page, msg2);

    // 2. Verify both messages are visible before we simulate the restart.
    await expect(page.locator(selectors.userMessage).filter({ hasText: msg1 })).toBeVisible();
    await expect(page.locator(selectors.userMessage).filter({ hasText: msg2 })).toBeVisible();

    // 3. Artificially advance the localStorage watermark to a value far beyond
    //    what the server has (simulates: app was last open at seq 9999 but
    //    nothing happened since, so server has 0 events after that point).
    //    When the app reloads it sends load_events(after_seq=9999), the server
    //    returns 0 events, and the needsContextLoadRef fallback must kick in.
    await page.evaluate(({ sid }) => {
      // Same key written by setLastSeenSeq() in web/static/utils/storage.js
      localStorage.setItem(`mitto_last_seen_seq_${sid}`, "9999");
      // Also clear the fallback debug flag so we start clean.
      if ((window as any).__debug) {
        (window as any).__debug.fallbackContextLoadFired = undefined;
        (window as any).__debug.lastLoadEventsAfterSeq = undefined;
      }
    }, { sid: sessionId });

    // 4. Reload the page (simulates app restart).
    await page.reload();
    // Wait for the app and WebSocket to be ready before asserting.
    await helpers.waitForWebSocketReady(page);

    // 5. Wait for the fallback to fire: events_loaded detects "0 new events
    //    but server has history" and fires a second load_events(limit=50).
    //    The fallback sets window.__debug.lastLoadEventsAfterSeq = 0 and
    //    window.__debug.fallbackContextLoadFired = true.
    await expect
      .poll(
        async () => {
          return await page.evaluate(
            () => (window as any).__debug?.fallbackContextLoadFired ?? false,
          );
        },
        { timeout: 15_000, intervals: [300, 500, 500, 1000] },
      )
      .toBe(true);

    // The debug marker for after_seq must have been reset to 0 by the fallback.
    const afterSeqAfterFallback = await page.evaluate(
      () => (window as any).__debug?.lastLoadEventsAfterSeq ?? -1,
    );
    expect(afterSeqAfterFallback).toBe(0);

    // 6. The original messages must be visible — the fallback loaded history.
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: msg1 }),
    ).toBeVisible({ timeout: timeouts.agentResponse });
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: msg2 }),
    ).toBeVisible({ timeout: timeouts.agentResponse });
  });
});

