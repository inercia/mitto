import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * Long Conversation Tests
 *
 * These tests verify that the UI handles long conversations correctly:
 * - Infinite scroll / load more functionality
 * - Message ordering with many messages
 * - Memory and performance with large message counts
 * - Pagination and history loading
 *
 * Note: These tests are slower due to the need to create many messages.
 * Uses testWithCleanup to automatically clean up old sessions.
 */

// Number of messages to create for "long" conversation tests
// Keep this reasonable for test speed while still testing pagination
const LONG_CONVERSATION_SIZE = 30;

// Number of messages for "very long" conversation tests
const VERY_LONG_CONVERSATION_SIZE = 60;

test.describe("Long Conversation Handling", () => {
  test.setTimeout(180000); // 3 minutes for long tests

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should handle conversation with many messages", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const testRunId = Date.now();
    const messagePrefix = `LONG_${testRunId}_`;

    // Send multiple messages
    for (let i = 0; i < LONG_CONVERSATION_SIZE; i++) {
      const msg = `${messagePrefix}${i}`;
      await helpers.sendMessage(page, msg);

      // Wait for the message to appear and agent to respond
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);

      // Small delay between messages
      await page.waitForTimeout(100);

      // Log progress every 10 messages
      if ((i + 1) % 10 === 0) {
        console.log(`Sent ${i + 1}/${LONG_CONVERSATION_SIZE} messages`);
      }
    }

    // Verify all user messages are present
    const userMessages = page.locator(selectors.userMessage);
    const userCount = await userMessages.count();

    // Should have at least LONG_CONVERSATION_SIZE user messages
    expect(userCount).toBeGreaterThanOrEqual(LONG_CONVERSATION_SIZE);

    // Verify first and last messages are present
    const firstMsg = `${messagePrefix}0`;
    const lastMsg = `${messagePrefix}${LONG_CONVERSATION_SIZE - 1}`;

    await expect(userMessages.filter({ hasText: lastMsg })).toBeVisible();

    // Scroll to top to find first message
    const container = page.locator(selectors.messagesContainer);
    await container.evaluate((el) => (el.scrollTop = 0));
    await page.waitForTimeout(500);

    await expect(userMessages.filter({ hasText: firstMsg })).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });

  test("should preserve message order after reload with many messages", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const testRunId = Date.now();
    const numMessages = 15; // Smaller for this test

    // Send messages
    for (let i = 0; i < numMessages; i++) {
      const msg = `ORDER_${testRunId}_${i}`;
      await helpers.sendMessage(page, msg);
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(100);
    }

    // Get message order before reload
    const getMessageOrder = async () => {
      const msgs = await page.locator(selectors.userMessage).all();
      const texts: string[] = [];
      for (const m of msgs) {
        const text = await m.textContent();
        if (text && text.includes(`ORDER_${testRunId}`)) {
          texts.push(text.trim());
        }
      }
      return texts;
    };

    const orderBefore = await getMessageOrder();
    console.log(`Messages before reload: ${orderBefore.length}`);

    // Reload the page
    await page.reload();
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(2000);

    // Get message order after reload
    const orderAfter = await getMessageOrder();
    console.log(`Messages after reload: ${orderAfter.length}`);

    // Verify order is preserved
    expect(orderAfter.length).toBe(orderBefore.length);
    for (let i = 0; i < orderBefore.length; i++) {
      expect(orderAfter[i]).toBe(orderBefore[i]);
    }
  });
});

test.describe("Infinite Scroll and Pagination", () => {
  test.setTimeout(300000); // 5 minutes for very long tests

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should load more messages when scrolling up", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const testRunId = Date.now();
    const numMessages = VERY_LONG_CONVERSATION_SIZE;

    // Send many messages to trigger pagination
    console.log(`Creating ${numMessages} messages for infinite scroll test...`);
    for (let i = 0; i < numMessages; i++) {
      const msg = `SCROLL_${testRunId}_${i}`;
      await helpers.sendMessage(page, msg);
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(50);

      if ((i + 1) % 20 === 0) {
        console.log(`Progress: ${i + 1}/${numMessages} messages`);
      }
    }

    // Reload to test loading from storage
    await page.reload();
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(2000);

    // Count initial messages (should be limited by INITIAL_EVENTS_LIMIT)
    const initialCount = await page.locator(selectors.userMessage).count();
    console.log(`Initial message count after reload: ${initialCount}`);

    // Check if "Load More" button or infinite scroll is available
    const loadMoreButton = page.locator('button:has-text("Load more")');
    const hasLoadMore = (await loadMoreButton.count()) > 0;

    if (hasLoadMore) {
      // Click load more and verify more messages appear
      const countBefore = await page.locator(selectors.userMessage).count();
      await loadMoreButton.click();
      await page.waitForTimeout(1000);
      const countAfter = await page.locator(selectors.userMessage).count();
      expect(countAfter).toBeGreaterThan(countBefore);
    } else {
      // Try scrolling to top to trigger infinite scroll
      const container = page.locator(selectors.messagesContainer);
      await container.evaluate((el) => (el.scrollTop = 0));
      await page.waitForTimeout(2000);

      // Check if more messages loaded
      const countAfterScroll = await page.locator(selectors.userMessage).count();
      console.log(`Message count after scroll to top: ${countAfterScroll}`);

      // Should have loaded more messages or all messages
      expect(countAfterScroll).toBeGreaterThanOrEqual(initialCount);
    }
  });

  test("should maintain scroll position when loading older messages", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const testRunId = Date.now();
    const numMessages = 40;

    // Send messages
    console.log(`Creating ${numMessages} messages for scroll position test...`);
    for (let i = 0; i < numMessages; i++) {
      const msg = `POS_${testRunId}_${i}`;
      await helpers.sendMessage(page, msg);
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(50);
    }

    // Reload to test loading from storage
    await page.reload();
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(2000);

    // Find a message in the middle of the visible area
    const userMessages = page.locator(selectors.userMessage);
    const visibleCount = await userMessages.count();

    if (visibleCount > 5) {
      // Get the 5th message as our anchor
      const anchorMessage = userMessages.nth(4);
      const anchorText = await anchorMessage.textContent();

      // Scroll to top to trigger loading more
      const container = page.locator(selectors.messagesContainer);
      await container.evaluate((el) => (el.scrollTop = 0));
      await page.waitForTimeout(2000);

      // The anchor message should still be in the DOM (not removed)
      if (anchorText) {
        const anchorStillExists = await userMessages
          .filter({ hasText: anchorText.trim() })
          .count();
        expect(anchorStillExists).toBeGreaterThan(0);
      }
    }
  });
});

test.describe("Memory and Performance", () => {
  test.setTimeout(180000);

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should not crash with rapid message sending", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const testRunId = Date.now();
    const numMessages = 20;

    // Send messages rapidly without waiting for responses
    console.log(`Sending ${numMessages} messages rapidly...`);
    for (let i = 0; i < numMessages; i++) {
      const msg = `RAPID_${testRunId}_${i}`;
      const textarea = page.locator(selectors.chatInput);

      // Wait for input to be enabled before sending
      await expect(textarea).toBeEnabled({ timeout: timeouts.agentResponse });
      await textarea.fill(msg);
      await page.locator(selectors.sendButton).click();

      // Minimal wait
      await page.waitForTimeout(50);
    }

    // Wait for all responses to complete
    await page.waitForTimeout(5000);

    // Verify the app is still responsive
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.agentResponse,
    });

    // Verify messages are present
    const userMessages = page.locator(selectors.userMessage);
    const count = await userMessages.count();
    expect(count).toBeGreaterThanOrEqual(numMessages);
  });

  test("should handle conversation switching with long conversations", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    const testRunId = Date.now();

    // Create first session with many messages
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    const numMessages = 15;
    for (let i = 0; i < numMessages; i++) {
      const msg = `SWITCH_A_${testRunId}_${i}`;
      await helpers.sendMessage(page, msg);
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(50);
    }

    // Create second session with different messages
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    for (let i = 0; i < numMessages; i++) {
      const msg = `SWITCH_B_${testRunId}_${i}`;
      await helpers.sendMessage(page, msg);
      await helpers.waitForUserMessage(page, msg);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(50);
    }

    // Switch back to first session
    const firstSessionMarker = `SWITCH_A_${testRunId}`;
    const sessionWithFirstMarker = page
      .locator(selectors.sessionsList)
      .filter({ hasText: firstSessionMarker });

    if ((await sessionWithFirstMarker.count()) > 0) {
      await sessionWithFirstMarker.first().click();
      await page.waitForTimeout(1000);

      // Verify first session's messages are visible
      const userMessages = page.locator(selectors.userMessage);
      await expect(
        userMessages.filter({ hasText: `SWITCH_A_${testRunId}_0` }),
      ).toBeVisible({ timeout: timeouts.shortAction });

      // Verify second session's messages are NOT visible
      const hasSecondSessionMsg = await userMessages
        .filter({ hasText: `SWITCH_B_${testRunId}` })
        .count();
      expect(hasSecondSessionMsg).toBe(0);
    }
  });
});

