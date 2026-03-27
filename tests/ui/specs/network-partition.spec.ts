import { test, expect } from "../fixtures/test-fixtures";

/**
 * Network partition recovery tests for Mitto Web UI.
 *
 * These tests verify that the keepalive mechanism correctly detects zombie
 * WebSocket connections when a network partition occurs, and that the app
 * recovers by reconnecting and syncing missed events.
 *
 * Uses Chrome DevTools Protocol (CDP) to simulate real network offline events.
 * Keepalive: 10s interval, max 2 missed → ~20-25s until forced reconnect.
 *
 * NOTE: These tests only run in Chromium (CDP is not available in Firefox/WebKit).
 */

test.describe("Network Partition Recovery", () => {
  test.skip(
    ({ browserName }) => browserName !== "chromium",
    "CDP (newCDPSession) is only available in Chromium"
  );
  test.setTimeout(90000);

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  /**
   * Test 1: Verify keepalive detects zombie connection and app recovers.
   *
   * Flow:
   * 1. Create fresh session, send baseline message
   * 2. Monitor console logs for keepalive messages
   * 3. Go offline via CDP
   * 4. Wait 25s (2 missed keepalives at 10s each → forced reconnect)
   * 5. Restore network
   * 6. Verify app recovers (input enabled, can send/receive again)
   * 7. Assert keepalive zombie detection was logged
   */
  test("should detect zombie connection and recover after network partition", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    const sessionId = await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Send baseline message to establish session with events and seq numbers
    const initialMessage = helpers.uniqueMessage("Baseline message");
    await helpers.sendMessageAndWait(page, initialMessage);

    // Start monitoring console logs BEFORE the network partition
    const consoleLogs: string[] = [];
    page.on("console", (msg) => {
      const text = msg.text();
      if (
        text.includes("Keepalive") ||
        text.includes("keepalive") ||
        text.includes("missed") ||
        text.includes("forcing reconnect") ||
        text.includes("force reconnect") ||
        text.includes("zombie")
      ) {
        consoleLogs.push(text);
      }
    });

    // Get CDP session for network emulation
    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Simulate network partition: go completely offline
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // Wait for 2 missed keepalives to accumulate and trigger forced reconnect
      // Keepalive interval: 10s, max missed: 2 → ~20-25s total
      await page.waitForTimeout(25000);

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // App should recover: chat input becomes enabled again
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Send another message to confirm the connection is fully functional
      const recoveryMessage = helpers.uniqueMessage("Recovery message");
      await page.locator(selectors.chatInput).fill(recoveryMessage);
      await page.locator(selectors.sendButton).click();

      // Recovery message should appear in chat
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: recoveryMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Agent should respond (confirms full round-trip is working)
      await helpers.waitForAgentResponse(page);

      // Key assertion: keepalive zombie detection must have been logged
      const hasKeepaliveDetection = consoleLogs.some(
        (log) =>
          log.includes("Keepalive missed") ||
          log.includes("Too many missed keepalives") ||
          log.includes("forcing reconnect"),
      );

      expect(
        hasKeepaliveDetection,
        `Expected keepalive zombie detection logs. Got:\n${consoleLogs.join("\n")}`,
      ).toBe(true);

      // Suppress unused variable warning — sessionId used implicitly by helpers
      void sessionId;
    } finally {
      // Always restore network conditions to avoid affecting subsequent tests
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });


  /**
   * Test 2: Verify missed events are synced after network partition during streaming.
   *
   * Flow:
   * 1. Create fresh session, send initial message, record baseline seq watermark
   * 2. Send second message to start agent streaming
   * 3. Within 100ms of sending, go offline via CDP
   * 4. Wait 25s offline (agent finishes on server; keepalive detects zombie)
   * 5. Restore network
   * 6. Wait for reconnect (chatInput enabled)
   * 7. Assert: load_events was sent (lastLoadEventsTimestamp increased)
   * 8. Assert: streaming message and agent response appear in UI
   * 9. Assert: no fatal error state in UI
   */
  test("should recover all events after network partition during streaming", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Send initial message and wait for complete response — establishes baseline
    const initialMessage = helpers.uniqueMessage("Initial message");
    await helpers.sendMessageAndWait(page, initialMessage);

    // Start monitoring console logs BEFORE the partition
    const consoleLogs: string[] = [];
    page.on("console", (msg) => {
      const text = msg.text();
      if (
        text.includes("Syncing session") ||
        text.includes("load_events") ||
        text.includes("keepalive") ||
        text.includes("Keepalive") ||
        text.includes("reconnect") ||
        text.includes("Reconnect") ||
        text.includes("Force reconnect") ||
        text.includes("connected") ||
        text.includes("WebSocket")
      ) {
        consoleLogs.push(text);
      }
    });

    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Send second message to start agent streaming
      const streamingMessage = helpers.uniqueMessage("Streaming message");
      await page.locator(selectors.chatInput).fill(streamingMessage);
      await page.locator(selectors.sendButton).click();

      // Go offline within 100ms of sending — the server will complete the response
      // while the client is partitioned (no way to deliver it until reconnect)
      await page.waitForTimeout(100);
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // Stay offline for 25s — keepalive misses accumulate, zombie detected
      await page.waitForTimeout(25000);

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for the app to reconnect — chatInput becoming enabled proves
      // the WebSocket reconnected and sent connected + load_events
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Key assertion 1: Both user messages are visible
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: initialMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: streamingMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Key assertion 2: Agent responses are visible — the streaming response
      // that was generated while offline must have been recovered after reconnect
      const agentMessages = page.locator(selectors.agentMessage);
      await expect(agentMessages).toHaveCount(2, {
        timeout: timeouts.agentResponse,
      });

      // Key assertion 3: No fatal error state in UI
      const fatalErrorCount = await page
        .locator("[data-error='fatal'], .fatal-error")
        .count();
      expect(fatalErrorCount).toBe(0);

      // Key assertion 4: App is functional — can send a follow-up message
      const followUpMessage = helpers.uniqueMessage("Follow-up after partition");
      await helpers.sendMessageAndWait(page, followUpMessage);
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: followUpMessage }),
      ).toBeVisible();
    } finally {
      // Always restore network conditions
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });
});

