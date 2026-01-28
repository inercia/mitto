import { test, expect } from '../fixtures/test-fixtures';

/**
 * Error handling and UI resilience tests for Mitto Web UI.
 *
 * These tests verify that the application handles errors gracefully
 * and remains functional after error conditions.
 */

test.describe('Error Handling', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test('should handle empty message submission gracefully', async ({
    page,
    selectors,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Clear the input
    await textarea.fill('');

    // Send button should be disabled
    await expect(sendButton).toBeDisabled();

    // App should still be functional
    await expect(page.locator(selectors.app)).toBeVisible();
  });

  test('should remain functional after rapid message sending', async ({
    page,
    selectors,
    helpers,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Send multiple messages rapidly
    for (let i = 0; i < 3; i++) {
      await textarea.fill(`Rapid message ${i}`);
      if (await sendButton.isEnabled()) {
        await sendButton.click();
      }
      await page.waitForTimeout(100);
    }

    // App should still be functional
    await expect(page.locator(selectors.app)).toBeVisible();
    await expect(textarea).toBeVisible();
  });

  test('should handle page navigation gracefully', async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Navigate away and back
    await page.goto('about:blank');
    await page.goto('/');

    // Wait for app to load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // App should be functional
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
  });
});

test.describe('UI Resilience', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test('should handle viewport resize', async ({ page, selectors, timeouts }) => {
    // Start with desktop size
    await page.setViewportSize({ width: 1280, height: 800 });
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Resize to mobile
    await page.setViewportSize({ width: 375, height: 667 });
    await page.waitForTimeout(500);

    // App should still be functional
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Resize back to desktop
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForTimeout(500);

    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });

  test('should handle multiple page refreshes', async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Refresh multiple times
    for (let i = 0; i < 3; i++) {
      await page.reload();
      await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
        timeout: timeouts.appReady,
      });
    }

    // App should still be functional
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.shortAction,
    });
  });
});

test.describe('Network Error Handling', () => {
  test('should show error state when API fails', async ({ page, selectors }) => {
    // This test would require mocking network failures
    // For now, we just verify the app loads correctly
    await expect(page.locator(selectors.app)).toBeVisible();
  });
});

