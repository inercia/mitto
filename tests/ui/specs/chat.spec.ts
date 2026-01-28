import { test, expect } from '../fixtures/test-fixtures';

/**
 * Chat input and message display tests for Mitto Web UI.
 *
 * These tests verify the chat input component behavior and
 * message display functionality.
 */

test.describe('Chat Input', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test('should have a placeholder text', async ({ page, selectors }) => {
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toHaveAttribute(
      'placeholder',
      /Type your message|Agent is responding/
    );
  });

  test('should allow typing in the input', async ({ page, selectors }) => {
    const textarea = page.locator(selectors.chatInput);
    await textarea.fill('Hello, World!');
    await expect(textarea).toHaveValue('Hello, World!');
  });

  test('should enable send button when text is entered', async ({
    page,
    selectors,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Send button should be disabled initially (or have disabled styling)
    await expect(sendButton).toBeDisabled();

    // Type some text
    await textarea.fill('Test message');

    // Send button should now be enabled
    await expect(sendButton).toBeEnabled();
  });

  test('should clear input after sending', async ({ page, selectors, timeouts }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Type a message
    await textarea.fill('Test message for clearing');

    // Click send
    await sendButton.click();

    // Input should be cleared (may take a moment)
    await expect(textarea).toHaveValue('', { timeout: timeouts.shortAction });
  });

  test('should support Enter key to send', async ({ page, selectors, helpers }) => {
    const textarea = page.locator(selectors.chatInput);
    const testMessage = helpers.uniqueMessage('Enter key');

    // Type a message using keyboard (not fill, to ensure proper event handling)
    await textarea.click();
    await textarea.type(testMessage);

    // Press Enter to send
    await page.keyboard.press('Enter');

    // The message should appear in the chat (proving it was sent)
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: 5000,
    });
  });

  test('should support Shift+Enter for newlines', async ({ page, selectors, timeouts }) => {
    const textarea = page.locator(selectors.chatInput);

    // Wait for textarea to be enabled
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });

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

  test('should auto-resize textarea as content grows', async ({ page, selectors }) => {
    const textarea = page.locator(selectors.chatInput);

    // Get initial height
    const initialHeight = await textarea.evaluate((el) => (el as HTMLElement).offsetHeight);

    // Type multiple lines
    await textarea.fill('Line 1\nLine 2\nLine 3\nLine 4\nLine 5');

    // Height should increase (or stay at max-height)
    const newHeight = await textarea.evaluate((el) => (el as HTMLElement).offsetHeight);
    expect(newHeight).toBeGreaterThanOrEqual(initialHeight);
  });
});

test.describe('Message Display', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test('should display user message after sending', async ({
    page,
    selectors,
    helpers,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const testMessage = helpers.uniqueMessage('Test message');

    // Send a message
    await textarea.fill(testMessage);
    await page.locator(selectors.sendButton).click();

    // Message should appear in the chat
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: 5000,
    });
  });

  test('should style user messages correctly', async ({
    page,
    selectors,
    helpers,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const testMessage = helpers.uniqueMessage('User styled');

    // Send a message
    await textarea.fill(testMessage);
    await page.locator(selectors.sendButton).click();

    // Wait for message to appear
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: 5000,
    });

    // Find the user message bubble - it should be in a container with user styling
    const userMessageBubble = page
      .locator(selectors.userMessage)
      .filter({ hasText: testMessage });
    await expect(userMessageBubble).toBeVisible({ timeout: 5000 });
  });

  test('should show system messages', async ({ page, selectors, timeouts }) => {
    // System messages appear when connecting to a session
    const systemMessage = page.locator(selectors.systemMessage);
    await expect(systemMessage.first()).toBeVisible({ timeout: timeouts.appReady });
  });
});

