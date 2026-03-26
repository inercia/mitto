import { test, expect } from "../fixtures/test-fixtures";

/**
 * Network Degradation Recovery Tests
 *
 * These tests use Chrome DevTools Protocol (CDP) to simulate realistic network
 * conditions: severe throttling, brief outages, and flapping connections.
 *
 * They verify the app remains resilient to network instability without
 * duplicating messages or losing data.
 *
 * NOTE: CDP network emulation is only available in Chromium. These tests will
 * be skipped in Firefox and WebKit projects.
 */

test.describe("Network Degradation Recovery", () => {
  test.setTimeout(60000);

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  // ---------------------------------------------------------------------------
  // Test 1: Severe network throttling
  // ---------------------------------------------------------------------------
  // Applies a 5 KB/s cap and 2000 ms latency via CDP, sends a message, and
  // verifies the message eventually appears without errors or duplicates.
  test("should handle severe network throttling without data loss", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Monitor console for unexpected errors
    const consoleLogs: string[] = [];
    page.on("console", (msg) => {
      consoleLogs.push(`[${msg.type()}] ${msg.text()}`);
    });

    const testMessage = helpers.uniqueMessage("Throttle test");
    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Apply severe throttling: 5 KB/s download/upload, 2000 ms latency
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: 5 * 1024,
        uploadThroughput: 5 * 1024,
        latency: 2000,
      });

      // Send the message (input will be sluggish but should work)
      await helpers.sendMessage(page, testMessage);

      // User message should appear in the chat (even if delayed)
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: testMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Restore before waiting for the agent response (speeds up the rest)
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Agent response should eventually arrive
      await helpers.waitForStreamingComplete(page);

      // Verify the app is still functional after restoring normal conditions
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.shortAction,
      });

      // No duplicate user messages
      const userMessageCount = await page
        .locator(selectors.userMessage)
        .filter({ hasText: testMessage })
        .count();
      expect(userMessageCount).toBe(1);

      // No error-level console messages related to the message
      const errors = consoleLogs.filter(
        (log) => log.startsWith("[error]") && !log.includes("favicon"),
      );
      expect(errors.length).toBe(0);
    } finally {
      // Always restore normal network conditions
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });


  // ---------------------------------------------------------------------------
  // Test 2: Brief network outage (3 seconds offline then recovery)
  // ---------------------------------------------------------------------------
  // Sends a first message to establish a baseline, goes offline for 3 s, comes
  // back, sends a second message, and verifies both messages appear exactly once.
  test("should recover gracefully from brief network outage", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Baseline: send an initial message so we have something to count against
    const firstMessage = helpers.uniqueMessage("Before outage");
    await helpers.sendMessageAndWait(page, firstMessage);

    // Count messages after the baseline exchange
    const userCountBefore = await page.locator(selectors.userMessage).count();
    const agentCountBefore = await page.locator(selectors.agentMessage).count();
    expect(userCountBefore).toBeGreaterThanOrEqual(1);
    expect(agentCountBefore).toBeGreaterThanOrEqual(1);

    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Go offline for 3 seconds (too short for the 10 s keepalive to trigger)
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      await page.waitForTimeout(3000);

      // Restore network
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for the app to recover (chat input must be enabled again)
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.agentResponse,
      });

      // Give WebSocket time to fully reconnect
      await helpers.waitForWebSocketReady(page);

      // Send a second message after recovery
      const secondMessage = helpers.uniqueMessage("After outage");
      await helpers.sendMessageAndWait(page, secondMessage);

      // Both user messages must be visible
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: firstMessage }),
      ).toBeVisible({ timeout: timeouts.shortAction });
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: secondMessage }),
      ).toBeVisible({ timeout: timeouts.shortAction });

      // At least 2 agent responses (one per prompt)
      const agentCountAfter = await page.locator(selectors.agentMessage).count();
      expect(agentCountAfter).toBeGreaterThanOrEqual(agentCountBefore + 1);

      // No duplicate user messages: exactly userCountBefore + 1 user messages
      const userCountAfter = await page.locator(selectors.userMessage).count();
      expect(userCountAfter).toBe(userCountBefore + 1);

      // Chat input is still functional
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.shortAction,
      });
    } finally {
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });



  // ---------------------------------------------------------------------------
  // Test 3: Rapidly flapping network (on/off 3 times) — no duplicate messages
  // ---------------------------------------------------------------------------
  // Sends a first message to get a known baseline, then rapidly toggles the
  // network on and off to simulate a flapping connection.  After stabilisation,
  // the user-message count and agent-message count must not have increased
  // (deduplication is working) and the app must remain functional.
  test("should not show duplicate messages after network flap", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Send a message to establish a baseline
    const baselineMessage = helpers.uniqueMessage("Flap baseline");
    await helpers.sendMessageAndWait(page, baselineMessage);

    // Capture counts before flapping starts
    const userCountBefore = await page.locator(selectors.userMessage).count();
    const agentCountBefore = await page.locator(selectors.agentMessage).count();
    expect(userCountBefore).toBeGreaterThanOrEqual(1);
    expect(agentCountBefore).toBeGreaterThanOrEqual(1);

    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Rapidly toggle network 3 times (500 ms offline, 1000 ms online)
      for (let i = 0; i < 3; i++) {
        await cdpSession.send("Network.emulateNetworkConditions", {
          offline: true,
          downloadThroughput: 0,
          uploadThroughput: 0,
          latency: 0,
        });
        await page.waitForTimeout(500);
        await cdpSession.send("Network.emulateNetworkConditions", {
          offline: false,
          downloadThroughput: -1,
          uploadThroughput: -1,
          latency: 0,
        });
        await page.waitForTimeout(1000);
      }

      // Wait for the app to fully stabilise after the flapping
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.agentResponse,
      });
      await helpers.waitForWebSocketReady(page);

      // Give any in-flight reconnect/sync a moment to settle
      await page.waitForTimeout(2000);

      // User message count must not have changed (no duplicates from reconnect sync)
      const userCountAfter = await page.locator(selectors.userMessage).count();
      expect(userCountAfter).toBe(userCountBefore);

      // Agent message count must not have changed (no ghost agent responses)
      const agentCountAfter = await page.locator(selectors.agentMessage).count();
      expect(agentCountAfter).toBe(agentCountBefore);

      // App must still be functional: send one more message successfully
      const recoveryMessage = helpers.uniqueMessage("Post-flap check");
      await helpers.sendMessageAndWait(page, recoveryMessage);

      await expect(
        page.locator(selectors.userMessage).filter({ hasText: recoveryMessage }),
      ).toBeVisible({ timeout: timeouts.shortAction });
    } finally {
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });
});
