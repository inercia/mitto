// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Error handling tests for Mitto Web UI.
 *
 * These tests verify that the UI handles errors gracefully
 * and provides appropriate feedback to users.
 */

test.describe('Error Handling', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should not crash on rapid message sending', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send multiple messages rapidly
    for (let i = 0; i < 5; i++) {
      await textarea.fill(`Rapid message ${i}`);
      await page.locator('button:has-text("Send")').click();
      // Don't wait for response, just send quickly
    }

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
    await expect(textarea).toBeVisible();
  });

  test('should handle very long input gracefully', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Create a very long message (10KB)
    const longMessage = 'x'.repeat(10000);
    await textarea.fill(longMessage);

    // Should not crash
    await expect(textarea).toHaveValue(longMessage);

    // Should be able to send
    await page.locator('button:has-text("Send")').click();

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
  });

  test('should handle special characters in messages', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send message with special characters
    const specialMessage = '<script>alert("xss")</script> & "quotes" \'apostrophes\'';
    await textarea.fill(specialMessage);
    await page.locator('button:has-text("Send")').click();

    // Message should be displayed (escaped, not executed)
    await page.waitForTimeout(1000);

    // App should not have executed any script
    await expect(page.locator('#app')).toBeVisible();

    // The text should be visible (properly escaped)
    // Note: The exact rendering depends on how the app escapes HTML
  });

  test('should handle unicode and emoji in messages', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send message with unicode and emoji
    const unicodeMessage = '你好世界 🎉 مرحبا العالم 🚀 Привет мир';
    await textarea.fill(unicodeMessage);
    await page.locator('button:has-text("Send")').click();

    // Message should be visible
    await expect(page.locator(`text=${unicodeMessage}`)).toBeVisible({ timeout: 5000 });
  });

  test('should recover from network interruption simulation', async ({ page, context }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send a message first
    await textarea.fill('Before network issue');
    await page.locator('button:has-text("Send")').click();
    await expect(page.locator('text=Before network issue')).toBeVisible({ timeout: 5000 });

    // Simulate going offline briefly
    await context.setOffline(true);
    await page.waitForTimeout(500);
    await context.setOffline(false);

    // Wait for potential reconnection
    await page.waitForTimeout(2000);

    // App should still be visible
    await expect(page.locator('#app')).toBeVisible();
  });
});

test.describe('UI Resilience', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should handle window resize gracefully', async ({ page }) => {
    // Resize to mobile size
    await page.setViewportSize({ width: 375, height: 667 });
    await page.waitForTimeout(500);

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
    await expect(page.locator('textarea')).toBeVisible();

    // Resize back to desktop
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.waitForTimeout(500);

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
  });

  test('should handle rapid session switching', async ({ page }) => {
    // Create a new session (it's an icon button with title)
    const newButton = page.locator('button[title="New Session"]');
    await expect(newButton).toBeVisible({ timeout: 5000 });

    // Click new session multiple times rapidly
    for (let i = 0; i < 3; i++) {
      await newButton.click();
      await page.waitForTimeout(200);
    }

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
    await expect(page.locator('textarea')).toBeVisible();
  });

  test('should maintain state after multiple operations', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Perform multiple operations
    await textarea.fill('Test message 1');
    await page.locator('button:has-text("Send")').click();
    await expect(page.locator('text=Test message 1')).toBeVisible({ timeout: 5000 });

    // Create new session (it's an icon button with title)
    await page.locator('button[title="New Session"]').click();
    await page.waitForTimeout(1000);

    // Send another message
    await textarea.fill('Test message 2');
    await page.locator('button:has-text("Send")').click();

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
    await expect(textarea).toBeVisible();
  });
});

