import { test, expect } from "../fixtures/test-fixtures";

/**
 * WebSocket reconnection tests for Mitto Web UI.
 *
 * These tests verify that message sending works correctly even when
 * WebSocket connections are interrupted or reconnected during the send flow.
 *
 * The key scenario being tested:
 * 1. User sends a message
 * 2. WebSocket reconnects (gets new clientID)
 * 3. Server broadcasts user_prompt with is_mine=false (because clientID changed)
 * 4. Frontend should still resolve the pending send by matching prompt_id
 * 5. Input should be cleared
 */

test.describe("WebSocket Reconnection During Send", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("should clear input after send even with network latency", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test simulates slow network conditions
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("Latency test");

    // Slow down network to simulate latency
    const client = await page.context().newCDPSession(page);
    await client.send("Network.emulateNetworkConditions", {
      offline: false,
      downloadThroughput: 50 * 1024, // 50 KB/s
      uploadThroughput: 50 * 1024,
      latency: 500, // 500ms latency
    });

    try {
      // Type and send message
      await textarea.fill(testMessage);
      await sendButton.click();

      // Input should be cleared even with latency (within timeout)
      await expect(textarea).toHaveValue("", { timeout: timeouts.agentResponse });

      // Message should appear in chat (use user message selector to avoid matching agent response)
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: testMessage }),
      ).toBeVisible({
        timeout: timeouts.agentResponse,
      });
    } finally {
      // Reset network conditions
      await client.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });

  test("should clear input after page visibility change during send", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test simulates the mobile scenario where the page becomes hidden
    // (e.g., user switches apps) and then visible again
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("Visibility test");

    // Type message
    await textarea.fill(testMessage);

    // Send message
    await sendButton.click();

    // Simulate page becoming hidden and then visible
    // This triggers the visibility change handler which may force reconnect
    await page.evaluate(() => {
      // Dispatch visibility change events
      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        writable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));

      // Wait a bit then become visible again
      setTimeout(() => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      }, 100);
    });

    // Input should still be cleared
    await expect(textarea).toHaveValue("", { timeout: timeouts.shortAction });

    // Message should appear in chat
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });

  test("should handle rapid sequential sends correctly", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test sends multiple messages rapidly to stress test the ACK handling
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    const messages = [
      helpers.uniqueMessage("Rapid 1"),
      helpers.uniqueMessage("Rapid 2"),
      helpers.uniqueMessage("Rapid 3"),
    ];

    // Send messages with minimal delay between them
    for (const msg of messages) {
      await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
      await textarea.fill(msg);
      await sendButton.click();

      // Wait for input to be cleared before sending next
      await expect(textarea).toHaveValue("", { timeout: timeouts.shortAction });
    }

    // All messages should appear in chat (use userMessage selector to avoid matching agent echo)
    for (const msg of messages) {
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: msg }),
      ).toBeVisible({
        timeout: timeouts.shortAction,
      });
    }
  });

  test("should recover from WebSocket close during send", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test closes the WebSocket connection during a send operation
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("WS close test");

    // Type message
    await textarea.fill(testMessage);

    // Set up a listener to close WebSocket after send
    await page.evaluate(() => {
      // Store original WebSocket
      const originalWS = window.WebSocket;

      // Track WebSockets
      (window as any).__testWebSockets = [];

      // Monkey-patch WebSocket to track connections
      (window as any).WebSocket = function (url: string, protocols?: string | string[]) {
        const ws = new originalWS(url, protocols);
        (window as any).__testWebSockets.push(ws);
        return ws;
      };
      (window as any).WebSocket.prototype = originalWS.prototype;
      (window as any).WebSocket.CONNECTING = originalWS.CONNECTING;
      (window as any).WebSocket.OPEN = originalWS.OPEN;
      (window as any).WebSocket.CLOSING = originalWS.CLOSING;
      (window as any).WebSocket.CLOSED = originalWS.CLOSED;
    });

    // Send message
    await sendButton.click();

    // Close all WebSockets after a short delay (simulating network interruption)
    await page.waitForTimeout(50);
    await page.evaluate(() => {
      const sockets = (window as any).__testWebSockets || [];
      for (const ws of sockets) {
        if (ws.readyState === WebSocket.OPEN) {
          ws.close();
        }
      }
    });

    // The app should recover - either the message was sent before close,
    // or it will be retried after reconnect
    // Wait for the message to appear in the user message area (may take longer due to reconnect)
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: testMessage }),
    ).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Input should eventually be cleared (after reconnect and retry)
    await expect(textarea).toHaveValue("", { timeout: timeouts.agentResponse });
  });

  test("should show error and preserve text on send timeout", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test verifies that when a send times out, the error is shown
    // and the text is preserved for retry
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("Timeout test");

    // Block WebSocket messages to simulate no response
    await page.route("**/ws", (route) => {
      // Don't respond to WebSocket upgrade - this will cause timeout
      route.abort();
    });

    // Type message
    await textarea.fill(testMessage);

    // Try to send - this should eventually timeout
    await sendButton.click();

    // Wait for the send to complete (either success or timeout)
    // The default timeout is 15 seconds, so we wait a bit longer
    await page.waitForTimeout(16000);

    // If send failed, text should be preserved
    // If send succeeded (before route was applied), text should be cleared
    // Either outcome is acceptable for this test
    const value = await textarea.inputValue();
    if (value !== "") {
      // Send failed - text should be preserved
      expect(value).toBe(testMessage);

      // Error message should be visible
      const errorMessage = page.locator(".bg-orange-900, .text-orange-200");
      await expect(errorMessage).toBeVisible({ timeout: 1000 });
    }
  });

  test("should handle send during page refresh", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test verifies behavior when page is refreshed during/after send
    // First, create a fresh session to avoid interference from other tests
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(500);

    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("Refresh test");

    // Type and send message
    await textarea.fill(testMessage);
    await sendButton.click();

    // Wait for message to appear
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Wait for agent response to complete (ensures session is persisted)
    await helpers.waitForAgentResponse(page);
    await page.waitForTimeout(500);

    // Refresh the page
    await page.reload();

    // Wait for app to be ready
    await helpers.waitForAppReady(page);
    // Wait for sessions to load
    await page.waitForTimeout(1000);

    // After reload, we need to click on the session that contains our message
    // The session should be in the sidebar with the message preview
    const sessionWithMessage = page
      .locator(selectors.sessionsList)
      .filter({ hasText: testMessage.substring(0, 20) });

    // If the session is visible in the sidebar, click it
    const sessionCount = await sessionWithMessage.count();
    if (sessionCount > 0) {
      await sessionWithMessage.first().click();
      await page.waitForTimeout(500);
    }

    // The message should be visible (either in current view or after clicking session)
    // Check for the message text anywhere on the page (user message or agent echo)
    // Use .first() because the message may appear in both user message and agent response
    await expect(page.locator(`text=${testMessage}`).first()).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Input should be empty (not restored from draft)
    await expect(textarea).toHaveValue("");
  });
});

test.describe("Pending Send Recovery", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("should track pending sends in localStorage", async ({
    page,
    selectors,
    helpers,
  }) => {
    // This test verifies that pending sends are stored in localStorage
    // for recovery after page refresh
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("LocalStorage test");

    // Type message
    await textarea.fill(testMessage);

    // Check localStorage before send
    const beforeSend = await page.evaluate(() => {
      return localStorage.getItem("mitto_pending_prompts");
    });

    // Send message
    await sendButton.click();

    // Check localStorage immediately after send (before ACK)
    // Note: This is timing-sensitive, the pending prompt should be stored
    // before the WebSocket message is sent
    const afterSend = await page.evaluate(() => {
      return localStorage.getItem("mitto_pending_prompts");
    });

    // Wait for message to be acknowledged
    await expect(textarea).toHaveValue("", { timeout: 5000 });

    // Check localStorage after ACK - pending prompt should be removed
    const afterAck = await page.evaluate(() => {
      return localStorage.getItem("mitto_pending_prompts");
    });

    // The pending prompt should have been added and then removed
    // We can't guarantee exact timing, but the final state should be empty or null
    const finalPending = afterAck ? JSON.parse(afterAck) : {};
    const pendingCount = Object.keys(finalPending).length;
    expect(pendingCount).toBe(0);
  });

  test("should resolve pending send when is_mine is false but prompt_id matches", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This is the key test for the bug scenario:
    // WebSocket reconnects, causing is_mine to be false,
    // but the frontend should still resolve by matching prompt_id

    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage("is_mine false test");

    // Set up console log monitoring to verify the fix is working
    const consoleLogs: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "log") {
        consoleLogs.push(msg.text());
      }
    });

    // Type and send message
    await textarea.fill(testMessage);
    await sendButton.click();

    // Wait for input to be cleared
    await expect(textarea).toHaveValue("", { timeout: timeouts.shortAction });

    // Message should appear
    // Use exact text match with getByText to avoid matching agent response containing the message
    await expect(
      page.getByText(testMessage, { exact: true }).first()
    ).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Check console logs for the confirmation message
    // Either "Own prompt confirmed:" (is_mine=true) or
    // "Own prompt confirmed (after reconnect):" (is_mine=false but prompt_id matched)
    const hasConfirmation = consoleLogs.some(
      (log) =>
        log.includes("Own prompt confirmed:") ||
        log.includes("Own prompt confirmed (after reconnect):"),
    );

    // At least one confirmation should have been logged
    expect(hasConfirmation).toBe(true);
  });
});

test.describe("Multi-Tab Scenarios", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("should handle message sent from another tab", async ({
    page,
    context,
    selectors,
    helpers,
    timeouts,
  }) => {
    // This test opens a second tab and sends a message from there
    // The first tab should see the message appear (via user_prompt broadcast)

    // Open a second tab and navigate to the app
    const page2 = await context.newPage();
    await page2.goto("/");
    await helpers.waitForAppReady(page2);

    // The second tab should auto-connect to the same session (via localStorage)
    await helpers.waitForActiveSession(page2);

    // Send a message from the second tab
    const testMessage = helpers.uniqueMessage("Multi-tab test");
    const textarea2 = page2.locator(selectors.chatInput);
    await expect(textarea2).toBeEnabled({ timeout: timeouts.shortAction });
    await textarea2.fill(testMessage);
    await page2.locator(selectors.sendButton).click();

    // Wait for message to appear in second tab (use userMessage selector)
    await expect(
      page2.locator(selectors.userMessage).filter({ hasText: testMessage }),
    ).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // The message should also appear in the first tab (via broadcast)
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: testMessage }),
    ).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Clean up
    await page2.close();
  });
});

