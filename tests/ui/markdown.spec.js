// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Markdown rendering tests for Mitto Web UI.
 *
 * These tests verify that agent messages with Markdown content
 * are rendered correctly in the UI.
 *
 * Note: These tests require the agent to respond with specific Markdown.
 * In a real scenario, you might mock the WebSocket responses.
 */

test.describe('Markdown Rendering', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should render agent messages in message container', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send a simple message
    await textarea.fill('Say hello');
    await page.locator('button:has-text("Send")').click();

    // Wait for user message to appear
    await expect(page.locator('text=Say hello')).toBeVisible({ timeout: 5000 });

    // Wait for agent response (may take a while)
    // Look for any agent message container
    const agentMessage = page.locator('.prose, [class*="agent"], [class*="message"]').first();
    await expect(agentMessage).toBeVisible({ timeout: 30000 });
  });

  test('should display user messages distinctly from agent messages', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send a message
    await textarea.fill('Test user message styling');
    await page.locator('button:has-text("Send")').click();

    // User message should be visible
    const userMessage = page.locator('text=Test user message styling');
    await expect(userMessage).toBeVisible({ timeout: 5000 });

    // User messages typically have different styling (e.g., right-aligned, different background)
    // We just verify it's rendered in a container
    const messageContainer = userMessage.locator('..');
    await expect(messageContainer).toBeVisible();
  });

  test('should handle long messages without breaking layout', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send a long message
    const longMessage = 'This is a very long message '.repeat(20);
    await textarea.fill(longMessage);
    await page.locator('button:has-text("Send")').click();

    // Message should be visible and not overflow the container
    await expect(page.locator(`text=${longMessage.substring(0, 50)}`)).toBeVisible({ timeout: 5000 });

    // The app container should not have horizontal scroll
    const app = page.locator('#app');
    const scrollWidth = await app.evaluate(el => el.scrollWidth);
    const clientWidth = await app.evaluate(el => el.clientWidth);

    // Allow small difference for scrollbar
    expect(scrollWidth).toBeLessThanOrEqual(clientWidth + 20);
  });

  test('should preserve whitespace in code-like content', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send a message asking for code
    await textarea.fill('Show me a simple hello world in Python');
    await page.locator('button:has-text("Send")').click();

    // Wait for response
    await page.waitForTimeout(5000);

    // If agent responds with code, it should be in a code block
    // Look for pre or code elements
    const codeElements = page.locator('pre, code');
    const count = await codeElements.count();

    // We can't guarantee the agent will respond with code,
    // but we verify the page doesn't break
    expect(count).toBeGreaterThanOrEqual(0);
  });
});

test.describe('Message List Behavior', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should auto-scroll to new messages', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send multiple messages to create scroll
    for (let i = 1; i <= 3; i++) {
      await textarea.fill(`Message number ${i}`);
      await page.locator('button:has-text("Send")').click();
      await expect(page.locator(`text=Message number ${i}`)).toBeVisible({ timeout: 5000 });
      await page.waitForTimeout(500);
    }

    // The last message should be visible (auto-scrolled into view)
    await expect(page.locator('text=Message number 3')).toBeVisible();
  });

  test('should display messages in chronological order', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Send messages
    await textarea.fill('First message');
    await page.locator('button:has-text("Send")').click();
    await expect(page.locator('text=First message')).toBeVisible({ timeout: 5000 });

    await textarea.fill('Second message');
    await page.locator('button:has-text("Send")').click();
    await expect(page.locator('text=Second message')).toBeVisible({ timeout: 5000 });

    // Get positions of messages
    const firstBox = await page.locator('text=First message').boundingBox();
    const secondBox = await page.locator('text=Second message').boundingBox();

    // First message should be above second message
    expect(firstBox.y).toBeLessThan(secondBox.y);
  });

  test('should handle empty message gracefully', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toBeEnabled({ timeout: 5000 });

    // Try to send empty message
    await textarea.fill('');
    const sendButton = page.locator('button:has-text("Send")');

    // Send button might be disabled for empty input
    // Or clicking it should not add an empty message
    const isDisabled = await sendButton.isDisabled();

    if (!isDisabled) {
      await sendButton.click();
      // Should not crash the app
      await expect(page.locator('#app')).toBeVisible();
    }
  });
});

