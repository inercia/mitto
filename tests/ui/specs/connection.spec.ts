import { test, expect } from "../fixtures/test-fixtures";

/**
 * WebSocket connection and status tests for Mitto Web UI.
 *
 * These tests verify that the WebSocket connection is established correctly
 * and that connection status is displayed to the user.
 */

test.describe("WebSocket Connection", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("should establish WebSocket connection on page load", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // The app should show a connected state after loading with active session
    // The textarea being enabled indicates successful connection
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
  });

  test("should show session is active after connection", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // The chat input should be enabled (indicating active session)
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });

    // Should be able to type
    await textarea.fill("Test connection");
    await expect(textarea).toHaveValue("Test connection");
  });

  test("should handle page refresh gracefully", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Refresh the page
    await page.reload();

    // Wait for app to load again
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // Should reconnect successfully - textarea should be enabled
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
  });

  test("should maintain the active session row after refresh", async ({
    page,
    selectors,
    timeouts,
  }) => {
    const sessionId = await page.evaluate(() =>
      localStorage.getItem("mitto_last_session_id"),
    );
    expect(sessionId).toBeTruthy();
    if (!sessionId) {
      throw new Error("navigateAndEnsureSession did not persist a session ID");
    }
    const sessionRow = page.locator(`[data-session-id="${sessionId}"]`);
    await expect(sessionRow).toBeVisible({
      timeout: timeouts.appReady,
    });

    // Refresh the page
    await page.reload();
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // The exact session row should still be present in the sidebar.
    await expect(sessionRow).toBeVisible({
      timeout: timeouts.appReady,
    });
  });
});

test.describe("Connection State UI", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("should enable chat input when connected", async ({
    page,
    selectors,
  }) => {
    // Textarea should be enabled
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled();

    // Should be able to type
    await textarea.fill("Test message");
    await expect(textarea).toHaveValue("Test message");
  });

  test("should show streaming indicator when agent is responding", async ({
    page,
    selectors,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });

    // Send a message to trigger agent response
    await textarea.fill("Hello, please respond");
    await page.locator(selectors.sendButton).click();

    // The textarea placeholder might change to indicate streaming
    // Or a cancel button might appear
    // We just verify the UI doesn't break during streaming
    await page.waitForTimeout(500);

    // The app should still be functional
    await expect(page.locator(selectors.app)).toBeVisible();
  });
});

test.describe("Session Persistence", () => {
  test("should maintain session after sending message", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Navigate and ensure we have an active session
    await helpers.navigateAndEnsureSession(page);

    // Wait for textarea to be ready
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });

    const testMessage = helpers.uniqueMessage("Session test");
    await textarea.fill(testMessage);
    await page.locator(selectors.sendButton).click();

    // Wait for message to appear
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: 5000,
    });

    // The session should still be active
    await expect(textarea).toBeVisible();
    await expect(textarea).toBeEnabled();
  });
});
