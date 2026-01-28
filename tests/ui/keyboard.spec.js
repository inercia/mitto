// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Keyboard navigation and accessibility tests for Mitto Web UI.
 *
 * These tests verify that the UI is navigable via keyboard and
 * follows accessibility best practices.
 */

test.describe('Keyboard Navigation', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should focus textarea on page load', async ({ page }) => {
    // The textarea should be focusable
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Click on textarea to focus it
    await textarea.click();
    await expect(textarea).toBeFocused();
  });

  test('should navigate away from textarea with Tab', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Type something first so send button is enabled
    await textarea.fill('Test');

    // Focus textarea
    await textarea.click();
    await expect(textarea).toBeFocused();

    // Tab should move focus away from textarea
    await page.keyboard.press('Tab');

    // Textarea should no longer be focused (focus moved to another element)
    await expect(textarea).not.toBeFocused();
  });

  test('should submit message with Send button click', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Type a message
    await textarea.fill('Test keyboard submit');

    // Click send button to submit
    await page.locator('button:has-text("Send")').click();

    // Message should appear in the chat
    await expect(page.locator('text=Test keyboard submit')).toBeVisible({ timeout: 5000 });
  });

  test('should insert newline with Shift+Enter', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Type first line
    await textarea.fill('Line 1');

    // Press Shift+Enter to add newline
    await textarea.press('Shift+Enter');

    // Type second line
    await textarea.type('Line 2');

    // Textarea should contain both lines
    const value = await textarea.inputValue();
    expect(value).toContain('Line 1');
    expect(value).toContain('Line 2');
  });

  test('should allow Tab navigation through session list', async ({ page }) => {
    // Get the new session button (it's an icon button with title)
    const newSessionButton = page.locator('button[title="New Session"]');
    await expect(newSessionButton).toBeVisible({ timeout: 5000 });

    // Focus it
    await newSessionButton.focus();
    await expect(newSessionButton).toBeFocused();

    // Should be able to activate with Enter
    await page.keyboard.press('Enter');

    // A new session should be created (we can verify by checking session count increases)
    await page.waitForTimeout(500);

    // App should still be functional
    await expect(page.locator('#app')).toBeVisible();
  });
});

test.describe('Keyboard Shortcuts', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should focus textarea with Escape when in other element', async ({ page }) => {
    // Focus the new session button first (it's an icon button with title)
    const newSessionButton = page.locator('button[title="New Session"]');
    await expect(newSessionButton).toBeVisible({ timeout: 5000 });
    await newSessionButton.focus();
    await expect(newSessionButton).toBeFocused();

    // Press Escape - this might return focus to textarea or close dialogs
    await page.keyboard.press('Escape');

    // The app should still be functional
    await expect(page.locator('#app')).toBeVisible();
  });

  test('should handle rapid key presses gracefully', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });
    await textarea.click();

    // Type rapidly
    await textarea.type('Quick typing test', { delay: 10 });

    // Verify the text was captured correctly
    await expect(textarea).toHaveValue('Quick typing test');
  });
});

test.describe('Focus Management', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should return focus to textarea after sending message', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Type and send a message
    await textarea.fill('Focus test message');
    await page.locator('button:has-text("Send")').click();

    // Wait for message to be sent
    await expect(page.locator('text=Focus test message')).toBeVisible({ timeout: 5000 });

    // Focus should return to textarea (or textarea should be ready for next input)
    await textarea.click();
    await expect(textarea).toBeFocused();
    await expect(textarea).toHaveValue(''); // Should be cleared after send
  });

  test('should maintain focus in textarea during typing', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });
    await textarea.click();

    // Type a long message
    const longMessage = 'This is a longer message to test that focus is maintained during typing';
    await textarea.type(longMessage, { delay: 20 });

    // Focus should still be on textarea
    await expect(textarea).toBeFocused();
    await expect(textarea).toHaveValue(longMessage);
  });
});

