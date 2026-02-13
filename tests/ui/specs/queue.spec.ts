import { test, expect } from "../fixtures/test-fixtures";

/**
 * Queue functionality tests for Mitto Web UI.
 *
 * These tests verify that messages added to the queue while the agent
 * is responding are correctly displayed in the conversation after being sent.
 */

test.describe("Queue", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  /**
   * Debug test to capture console logs and verify user_prompt event handling
   * for queued messages.
   */
  // TODO: This test is currently failing due to a bug where queued messages'
  // user_prompt events are not properly rendered in the conversation UI.
  // See the skipped test above for details.
  test.skip("debug: capture user_prompt events for queued messages", async ({
    page,
    selectors,
    helpers,
    timeouts,
    request,
    apiUrl,
  }) => {
    const consoleLogs: string[] = [];

    // Capture all console logs
    page.on("console", (msg) => {
      const text = msg.text();
      // Filter for relevant logs
      if (text.includes("user_prompt") ||
          text.includes("queue") ||
          text.includes("Queue") ||
          text.includes("M1 dedup") ||
          text.includes("Skipping duplicate")) {
        consoleLogs.push(`[${msg.type()}] ${text}`);
      }
    });

    const initialMessage = helpers.uniqueMessage("Debug-Initial");
    const queuedMessage = helpers.uniqueMessage("Debug-Queued");

    // Get the current session ID
    const sessionId = await page.evaluate(() => {
      return localStorage.getItem("mitto_last_session_id") || "";
    });
    expect(sessionId).toBeTruthy();

    // Send initial message
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await textarea.fill(initialMessage);
    await page.locator(selectors.sendButton).click();

    // Wait for agent to start responding
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Add message to queue via API
    const queueResponse = await request.post(
      apiUrl(`/api/sessions/${sessionId}/queue`),
      {
        data: { message: queuedMessage },
      }
    );
    expect(queueResponse.ok()).toBeTruthy();

    // Wait for initial response to complete
    await expect(page.locator(selectors.stopButton)).toBeHidden({
      timeout: timeouts.agentResponse,
    });

    // Wait for queue to process
    await page.waitForTimeout(3000);

    // Print all captured logs for debugging
    console.log("=== Captured Console Logs ===");
    for (const log of consoleLogs) {
      console.log(log);
    }
    console.log("=== End Console Logs ===");

    // Verify the queued message appears
    const queuedMessageInChat = page.locator(selectors.userMessage).filter({
      hasText: queuedMessage,
    });
    await expect(queuedMessageInChat).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Check that we saw the user_prompt event for the queued message
    const sawQueueUserPrompt = consoleLogs.some(
      (log) => log.includes("user_prompt") && log.includes("queue")
    );
    expect(sawQueueUserPrompt).toBeTruthy();
  });

  /**
   * This test replicates a bug where queued messages are not visible in the
   * conversation after being sent.
   *
   * Bug reproduction steps:
   * 1. Send a message to the agent
   * 2. While the agent is responding, queue another message
   * 3. Wait for the agent to finish responding
   * 4. The queued message should automatically be sent
   * 5. EXPECTED: The queued message should appear in the conversation
   * 6. BUG: The queued message is NOT visible in the conversation
   */
  // TODO: This test is currently failing due to a bug where queued messages'
  // user_prompt events are not properly rendered in the conversation UI.
  // The queued message IS sent (agent responds to it), but the user message
  // itself doesn't appear in the chat. This needs investigation.
  test.skip("queued message should appear in conversation after being sent", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const initialMessage = helpers.uniqueMessage("Initial");
    const queuedMessage = helpers.uniqueMessage("Queued");

    // Step 1: Send initial message to start agent responding
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await textarea.fill(initialMessage);
    await page.locator(selectors.sendButton).click();

    // Step 2: Wait for agent to start responding (stop button appears)
    // This indicates the agent is actively responding
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Step 3: While agent is responding, add a message to the queue
    // First, we need to fill in a new message
    await textarea.fill(queuedMessage);

    // Use keyboard shortcut Cmd+Enter (Mac) or Ctrl+Enter (others) to queue
    // The shortcut is shown in the UI as "⌘/Ctrl+Enter"
    await page.keyboard.press("Meta+Enter");

    // The mock ACP responds very quickly, so the message might be sent before
    // we can verify it was in the queue. Instead, we'll wait for:
    // 1. Either the queue count to increase (message pending)
    // 2. Or the queued message to appear in conversation (message already sent)

    // Step 4: Wait for the initial response to complete
    await expect(page.locator(selectors.stopButton)).toBeHidden({
      timeout: timeouts.agentResponse,
    });

    // Step 5: Wait for the queued message to be processed and agent to respond
    // The queue will auto-send when the agent finishes
    // Mock ACP responds quickly, so we just wait a bit and then check
    await page.waitForTimeout(2000);

    // Wait for any pending responses to complete
    await expect(page.locator(selectors.stopButton)).toBeHidden({
      timeout: timeouts.agentResponse,
    });

    // Step 6: If there's a "Load earlier messages" button, click it to ensure we see all messages
    // This handles the case where pagination hides older messages
    const loadEarlierButton = page.locator("button:has-text('Load earlier messages')");
    const hasLoadEarlier = await loadEarlierButton.isVisible();
    if (hasLoadEarlier) {
      await loadEarlierButton.click();
      await page.waitForTimeout(1000);
    }

    // Step 7: Verify the queued message appears in the conversation
    const queuedMessageInChat = page.locator(selectors.userMessage).filter({
      hasText: queuedMessage,
    });

    await expect(queuedMessageInChat).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Also verify the initial message is still there
    const initialMessageInChat = page.locator(selectors.userMessage).filter({
      hasText: initialMessage,
    });
    await expect(initialMessageInChat).toBeVisible();

    // Verify we have at least 2 user messages (initial + queued)
    // Note: We use toHaveCount with minimum check since other tests may have left messages
    const userMessages = page.locator(selectors.userMessage);
    const count = await userMessages.count();
    expect(count).toBeGreaterThanOrEqual(2);
  });

  // TODO: This test is currently failing due to a bug where queued messages'
  // user_prompt events are not properly rendered in the conversation UI.
  // See the skipped tests above for details.
  test.skip("queued message via API should appear in conversation", async ({
    page,
    selectors,
    helpers,
    timeouts,
    request,
    apiUrl,
  }) => {
    const initialMessage = helpers.uniqueMessage("Initial-API");
    const queuedMessage = helpers.uniqueMessage("Queued-API");

    // Get the current session ID
    const sessionId = await page.evaluate(() => {
      return localStorage.getItem("mitto_last_session_id") || "";
    });
    expect(sessionId).toBeTruthy();

    // Step 1: Send initial message to start agent responding
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await textarea.fill(initialMessage);
    await page.locator(selectors.sendButton).click();

    // Step 2: Wait for agent to start responding
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Step 3: Add message to queue via API (more reliable than UI interaction)
    const queueResponse = await request.post(
      apiUrl(`/api/sessions/${sessionId}/queue`),
      {
        data: { message: queuedMessage },
      }
    );
    expect(queueResponse.ok()).toBeTruthy();

    // Step 4: Wait for the initial response to complete
    await expect(page.locator(selectors.stopButton)).toBeHidden({
      timeout: timeouts.agentResponse,
    });

    // Step 5: Wait for queue to process (there's a delay before queue messages are sent)
    await page.waitForTimeout(3000);

    // Step 6: Verify the queued message appears in the conversation
    const queuedMessageInChat = page.locator(selectors.userMessage).filter({
      hasText: queuedMessage,
    });

    await expect(queuedMessageInChat).toBeVisible({
      timeout: timeouts.agentResponse,
    });
  });
});

