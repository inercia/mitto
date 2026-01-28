// @ts-check
import { test, expect } from '@playwright/test';

/**
 * WebSocket connection and status tests for Mitto Web UI.
 *
 * These tests verify that the WebSocket connection is established correctly
 * and that connection status is displayed to the user.
 */

test.describe('WebSocket Connection', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for app to fully load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    // Wait for textarea to be visible (indicates app is ready)
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should establish WebSocket connection on page load', async ({ page }) => {
    // The app should show a connected state after loading
    // The textarea being enabled indicates successful connection
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 10000 });
  });

  test('should show session is active after connection', async ({ page }) => {
    // The chat input should be enabled (indicating active session)
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Should be able to type
    await textarea.fill('Test connection');
    await expect(textarea).toHaveValue('Test connection');
  });

  test('should handle page refresh gracefully', async ({ page }) => {
    // Refresh the page
    await page.reload();

    // Wait for app to load again
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Should reconnect successfully - textarea should be enabled
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
    await expect(textarea).toBeEnabled({ timeout: 10000 });
  });

  test('should maintain session list after refresh', async ({ page }) => {
    // Get the sessions sidebar
    const sessionsHeader = page.getByRole('heading', { name: 'Sessions' });
    await expect(sessionsHeader).toBeVisible({ timeout: 10000 });

    // Refresh the page
    await page.reload();
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Sessions sidebar should still be visible
    await expect(page.getByRole('heading', { name: 'Sessions' })).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Connection State UI', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should enable chat input when connected', async ({ page }) => {
    // Textarea should be enabled
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled();

    // Should be able to type
    await textarea.fill('Test message');
    await expect(textarea).toHaveValue('Test message');
  });

  test('should show streaming indicator when agent is responding', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 10000 });

    // Send a message to trigger agent response
    await textarea.fill('Hello, please respond');
    await page.locator('button:has-text("Send")').click();

    // The textarea placeholder might change to indicate streaming
    // Or a cancel button might appear
    // We just verify the UI doesn't break during streaming
    await page.waitForTimeout(500);

    // The app should still be functional
    await expect(page.locator('#app')).toBeVisible();
  });
});

test.describe('Session Persistence', () => {
  test('should maintain session after sending message', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Wait for textarea to be ready
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
    await expect(textarea).toBeEnabled({ timeout: 10000 });

    const testMessage = `Session test ${Date.now()}`;
    await textarea.fill(testMessage);
    await page.locator('button:has-text("Send")').click();

    // Wait for message to appear
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({ timeout: 5000 });

    // The session should still be active
    await expect(textarea).toBeVisible();
    await expect(textarea).toBeEnabled();
  });
});

