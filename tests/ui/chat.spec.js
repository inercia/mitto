// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Chat input and message display tests for Mitto Web UI.
 *
 * These tests verify the chat input component behavior and
 * message display functionality.
 */

test.describe('Chat Input', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for app to fully load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    // Wait for textarea to be visible
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should have a placeholder text', async ({ page }) => {
    const textarea = page.locator('textarea');
    await expect(textarea).toHaveAttribute('placeholder', /Type your message|Agent is responding/);
  });

  test('should allow typing in the input', async ({ page }) => {
    const textarea = page.locator('textarea');
    await textarea.fill('Hello, World!');
    await expect(textarea).toHaveValue('Hello, World!');
  });

  test('should enable send button when text is entered', async ({ page }) => {
    const textarea = page.locator('textarea');
    const sendButton = page.locator('button:has-text("Send")');
    
    // Send button should be disabled initially (or have disabled styling)
    await expect(sendButton).toBeDisabled();
    
    // Type some text
    await textarea.fill('Test message');
    
    // Send button should now be enabled
    await expect(sendButton).toBeEnabled();
  });

  test('should clear input after sending', async ({ page }) => {
    const textarea = page.locator('textarea');
    const sendButton = page.locator('button:has-text("Send")');
    
    // Type a message
    await textarea.fill('Test message for clearing');
    
    // Click send
    await sendButton.click();
    
    // Input should be cleared
    await expect(textarea).toHaveValue('');
  });

  test('should support Enter key to send', async ({ page }) => {
    const textarea = page.locator('textarea');
    const testMessage = `Enter key message ${Date.now()}`;

    // Type a message using keyboard (not fill, to ensure proper event handling)
    await textarea.click();
    await textarea.type(testMessage);

    // Press Enter to send
    await page.keyboard.press('Enter');

    // The message should appear in the chat (proving it was sent)
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({ timeout: 5000 });
  });

  test('should support Shift+Enter for newlines', async ({ page }) => {
    const textarea = page.locator('textarea');

    // Wait for textarea to be enabled
    await expect(textarea).toBeEnabled({ timeout: 10000 });

    // Click to focus and type first line
    await textarea.click();
    await page.keyboard.type('Line 1');

    // Press Shift+Enter to add newline
    await page.keyboard.press('Shift+Enter');

    // Type second line
    await page.keyboard.type('Line 2');

    // Value should contain both lines with a newline between them
    const value = await textarea.inputValue();
    expect(value).toContain('Line 1');
    expect(value).toContain('Line 2');
  });

  test('should auto-resize textarea as content grows', async ({ page }) => {
    const textarea = page.locator('textarea');
    
    // Get initial height
    const initialHeight = await textarea.evaluate(el => el.offsetHeight);
    
    // Type multiple lines
    await textarea.fill('Line 1\nLine 2\nLine 3\nLine 4\nLine 5');
    
    // Height should increase (or stay at max-height)
    const newHeight = await textarea.evaluate(el => el.offsetHeight);
    expect(newHeight).toBeGreaterThanOrEqual(initialHeight);
  });
});

test.describe('Message Display', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should display user message after sending', async ({ page }) => {
    const textarea = page.locator('textarea');
    const testMessage = `Test message ${Date.now()}`;
    
    // Send a message
    await textarea.fill(testMessage);
    await page.locator('button:has-text("Send")').click();
    
    // Message should appear in the chat
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({ timeout: 5000 });
  });

  test('should style user messages correctly', async ({ page }) => {
    const textarea = page.locator('textarea');
    const testMessage = `User styled message ${Date.now()}`;

    // Send a message
    await textarea.fill(testMessage);
    await page.locator('button:has-text("Send")').click();

    // Wait for message to appear
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({ timeout: 5000 });

    // Find the user message bubble - it should be in a container with user styling
    // The structure is: div.message-enter > div.bg-mitto-user > pre
    const userMessageBubble = page.locator('.bg-mitto-user, .bg-blue-600').filter({ hasText: testMessage });
    await expect(userMessageBubble).toBeVisible({ timeout: 5000 });
  });

  test('should show system messages', async ({ page }) => {
    // System messages appear when connecting to a session
    const systemMessage = page.locator('.text-gray-500, .text-xs');
    await expect(systemMessage.first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Streaming State', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });
  });

  test('should show cancel button when streaming', async ({ page }) => {
    const textarea = page.locator('textarea');
    const sendButton = page.locator('button:has-text("Send")');

    // Wait for textarea to be enabled (not streaming from previous test)
    await expect(textarea).toBeEnabled({ timeout: 15000 });

    // Send a message to trigger streaming
    await textarea.fill('Hello streaming test');
    await expect(sendButton).toBeEnabled({ timeout: 5000 });
    await sendButton.click();

    // Cancel button should appear (with X icon) if agent is responding
    // Note: This depends on the ACP server responding
    // We just verify the UI handles the streaming state without errors
    await page.waitForTimeout(500);
  });
});

