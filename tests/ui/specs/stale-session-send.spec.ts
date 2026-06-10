import { test, expect } from "../fixtures/test-fixtures";

/**
 * Stale Session Send Failure + Retry tests for Mitto Web UI.
 *
 * These tests verify the message delivery pipeline when a user sends a message
 * over a zombie WebSocket connection (appears open client-side but server has
 * closed it). The ACK timeout mechanism and retry logic are exercised.
 *
 * Message delivery pipeline:
 *   sendMessage() → attemptSend(3s ACK timeout)
 *     → ON ACK_TIMEOUT: verifyDeliveryAfterReconnect(4s timeout)
 *       → force close → reconnect → load_events to check delivery
 *     → if NOT delivered: retry on fresh connection
 *     → if retry also times out: show error toast
 *
 * Uses Chrome DevTools Protocol (CDP) to simulate zombie WebSocket conditions.
 *
 * NOTE: These tests only run in Chromium (CDP is not available in Firefox/WebKit).
 */

test.describe("Stale Session Send Recovery", () => {
  test.skip(
    ({ browserName }) => browserName !== "chromium",
    "CDP (newCDPSession) is only available in Chromium"
  );
  test.setTimeout(60000);

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  /**
   * Test 1: Error toast appears on dead connection; app recovers after retry.
   *
   * Flow:
   * 1. Create fresh session, send baseline message (establishes healthy connection)
   * 2. Go offline via CDP to create zombie WebSocket
   * 3. Type message and click Send
   * 4. Wait for ACK timeout → verify/retry → error toast (≤15s)
   * 5. Restore network, wait for recovery
   * 6. Click Send again (message preserved) → verify success
   */
  test("should show error when sending on dead connection and recover on retry", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    test.skip(!!process.env.MITTO_EXTERNAL_SERVER,
      'Strict mode violation in Docker due to duplicate message elements');
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Establish a healthy session with a baseline message
    const initialMessage = helpers.uniqueMessage("Baseline message");
    await helpers.sendMessageAndWait(page, initialMessage);

    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Go offline — WebSocket becomes a zombie from the client's perspective
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // Wait 2s: enough for keepalive to consider connection unhealthy,
      // but NOT 20-25s (which triggers the keepalive-forced reconnect)
      await page.waitForTimeout(2000);

      // Type and send a message over the dead connection
      const stalledMessage = helpers.uniqueMessage("Message on stale connection");
      await page.locator(selectors.chatInput).fill(stalledMessage);
      await page.locator(selectors.sendButton).click();

      // ACK timeout (3s) + reconnect attempt (~4s) + error shown ≈ 7-10s
      // The send error appears as a daisyUI alert-warning bar below the chat input
      const errorToast = page.locator(".alert.alert-warning").first();
      await expect(errorToast).toBeVisible({ timeout: 15000 });

      // The error message should reference delivery failure
      const errorText = await errorToast.textContent();
      const hasDeliveryError =
        errorText?.includes("timed out") ||
        errorText?.includes("could not be confirmed") ||
        errorText?.includes("Connection lost");
      expect(
        hasDeliveryError,
        `Expected delivery error text, got: "${errorText}"`
      ).toBe(true);

      // Message text must be PRESERVED in the input for easy retry
      const inputValue = await page.locator(selectors.chatInput).inputValue();
      expect(inputValue).toContain(stalledMessage.split(" ").slice(-2).join(" "));

      // The "preserved" hint must be visible
      await expect(page.getByText("preserved")).toBeVisible();

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for recovery: chat input becomes enabled (WebSocket reconnected)
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Retry send with preserved message text
      await page.locator(selectors.sendButton).click();

      // The retried user message should appear in chat. Use .first(): a
      // send-failure + retry can leave two matching bubbles (optimistic render
      // plus the server echo after recovery).
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: "stale connection" }).first()
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Agent should respond (confirms full round-trip restored)
      await helpers.waitForAgentResponse(page);
    } finally {
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });

  /**
   * Test 2: Message text is preserved in the input on send failure.
   *
   * Flow:
   * 1. Create fresh session
   * 2. Go offline
   * 3. Type a specific message and click Send
   * 4. Wait for error toast
   * 5. Verify textarea still contains the original message text
   * 6. Verify the "preserved" hint text is visible
   * 7. Restore network, retry, verify message appears in chat
   */
  test("should preserve message text on send failure for easy retry", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    test.skip(!!process.env.MITTO_EXTERNAL_SERVER,
      'Strict mode violation in Docker due to duplicate message elements');
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    const cdpSession = await page.context().newCDPSession(page);
    const importantMessage = "Important message that must not be lost";

    try {
      // Go offline before typing
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      await page.locator(selectors.chatInput).fill(importantMessage);
      await page.locator(selectors.sendButton).click();

      // Wait for error toast
      const errorToast = page.locator(".alert.alert-warning").first();
      await expect(errorToast).toBeVisible({ timeout: 15000 });

      // Key assertion: input text must be preserved verbatim
      const inputValue = await page.locator(selectors.chatInput).inputValue();
      expect(inputValue).toContain("must not be lost");

      // The retry hint must be visible
      await expect(page.getByText("preserved")).toBeVisible();

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for recovery
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Retry: click Send with preserved text still in input
      await page.locator(selectors.sendButton).click();

      // The message should appear as a user message in the chat. Use .first():
      // a send-failure + retry can leave two matching bubbles (optimistic
      // render plus the server echo after recovery).
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: "must not be lost" }).first()
      ).toBeVisible({ timeout: timeouts.agentResponse });
    } finally {
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });

  /**
   * Test 3: Send failure when connection drops mid-delivery (ACK never arrives).
   *
   * Clicks Send then immediately drops the network. The ws.send() call may or
   * may not have flushed before the partition, so both outcomes are valid:
   *   A) ACK arrives before partition → delivery succeeds, no error toast
   *   B) ACK blocked by partition → ACK timeout fires → error toast → retry
   *
   * Either way the test asserts that the session is functional after recovery.
   *
   * Flow:
   * 1. Create fresh session, send baseline message
   * 2. Fill a new message in the input
   * 3. Click Send → go offline within 50ms (race: ACK may or may not arrive)
   * 4. Race: wait for either error toast OR user message appearance (whichever
   *    comes first within 15s)
   * 5. Restore network, wait for recovery
   * 6. If error toast appeared: retry with preserved text; assert message visible
   * 7. If message already delivered: assert agent responds (session still works)
   */
  test("should handle send failure when connection drops mid-delivery", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    const initialMessage = helpers.uniqueMessage("Baseline message");
    await helpers.sendMessageAndWait(page, initialMessage);

    const cdpSession = await page.context().newCDPSession(page);
    const midDeliveryMessage = helpers.uniqueMessage("Mid-delivery message");
    // Use a stable unique suffix for locator matching
    const uniqueSuffix = midDeliveryMessage.split(" ").slice(-2).join(" ");

    try {
      await page.locator(selectors.chatInput).fill(midDeliveryMessage);

      // Click Send then go offline almost simultaneously
      await page.locator(selectors.sendButton).click();
      await page.waitForTimeout(50);

      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // Race: which comes first — delivery error toast OR the user message bubble?
      // Both are valid outcomes depending on whether the ACK arrived in 50ms.
      const errorToastLocator = page.locator(".alert.alert-warning").first();
      const userMsgLocator = page
        .locator(selectors.userMessage)
        .filter({ hasText: uniqueSuffix })
        .first();

      let errorToastAppeared = false;
      try {
        await Promise.race([
          errorToastLocator
            .waitFor({ state: "visible", timeout: 15000 })
            .then(() => { errorToastAppeared = true; }),
          userMsgLocator.waitFor({ state: "visible", timeout: 15000 }),
        ]);
      } catch {
        // Neither appeared within 15s — this is a genuine test failure
        throw new Error(
          "Neither error toast nor user message appeared within 15s after going offline mid-delivery"
        );
      }

      // Restore network in all cases
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for app to recover (input enabled = WebSocket reconnected)
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      if (errorToastAppeared) {
        // Delivery failed — message must be preserved, retry via Send button
        const inputValue = await page.locator(selectors.chatInput).inputValue();
        expect(inputValue).toContain(uniqueSuffix.split(" ")[0]);

        await page.locator(selectors.sendButton).click();
        await expect(userMsgLocator).toBeVisible({ timeout: timeouts.agentResponse });
      } else {
        // Delivery succeeded before partition — message already visible in chat
        await expect(userMsgLocator).toBeVisible({ timeout: timeouts.agentResponse });
      }

      // Either way, the session must be fully functional: agent responds
      await helpers.waitForAgentResponse(page);
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

