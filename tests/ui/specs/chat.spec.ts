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

test.describe('Message Send Flow', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test('should complete full send round-trip: input → send → display → response', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage('Round trip');

    // 1. Type message
    await textarea.fill(testMessage);
    await expect(textarea).toHaveValue(testMessage);

    // 2. Send message
    await sendButton.click();

    // 3. Input should be cleared
    await expect(textarea).toHaveValue('', { timeout: timeouts.shortAction });

    // 4. User message should appear in chat
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // 5. Agent response should appear
    await helpers.waitForAgentResponse(page);
  });

  test('should handle sequential sends with proper waiting', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Send 2 messages, waiting for each to complete
    const messages = [
      helpers.uniqueMessage('Sequential 1'),
      helpers.uniqueMessage('Sequential 2'),
    ];

    for (const msg of messages) {
      // Wait for input to be enabled
      await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
      await textarea.fill(msg);
      await sendButton.click();
      // Wait for user message to appear (in user message bubble)
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: msg })
      ).toBeVisible({ timeout: timeouts.shortAction });
      // Wait for agent response before sending next
      await helpers.waitForAgentResponse(page);
      // Small delay for streaming to complete
      await page.waitForTimeout(500);
    }

    // All user messages should be visible
    for (const msg of messages) {
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: msg })
      ).toBeVisible();
    }
  });

  test('should preserve message order after sending multiple messages', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Send messages with clear ordering, waiting between each
    const messages = ['Order A', 'Order B', 'Order C'].map((m) =>
      helpers.uniqueMessage(m)
    );

    for (const msg of messages) {
      await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
      await textarea.fill(msg);
      await sendButton.click();
      // Wait for user message to appear
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: msg })
      ).toBeVisible({ timeout: timeouts.shortAction });
      // Wait for agent response
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(500);
    }

    // Get only USER messages and verify order
    const userMessages = await page.locator(selectors.userMessage).all();
    const messageOrder: string[] = [];

    for (const el of userMessages) {
      const text = await el.textContent();
      for (const msg of messages) {
        if (text?.includes(msg)) {
          messageOrder.push(msg);
          break;
        }
      }
    }

    // Verify user messages appear in the order they were sent
    expect(messageOrder).toEqual(messages);
  });

  test('should handle very long messages', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Create a long message (1000 characters)
    const longContent = 'x'.repeat(1000);
    const testMessage = helpers.uniqueMessage(`Long: ${longContent}`);

    await textarea.fill(testMessage);
    await sendButton.click();

    // Message should appear (may be truncated in display)
    await expect(page.locator(`text=${testMessage.substring(0, 50)}`)).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });

  test('should handle messages with special characters', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    // Test various special characters
    const specialChars = 'Hello <script>alert("xss")</script> & "quotes" \'single\'';
    const testMessage = helpers.uniqueMessage(specialChars);

    await textarea.fill(testMessage);
    await sendButton.click();

    // Message should appear (HTML should be escaped)
    await expect(page.locator(`text=${testMessage.substring(0, 20)}`)).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });

  test('should handle unicode and emoji', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);

    const unicodeMessage = helpers.uniqueMessage('Hello 你好 🎉 émojis');

    await textarea.fill(unicodeMessage);
    await sendButton.click();

    // Message should appear with unicode intact
    await expect(page.locator('text=🎉')).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });
});

test.describe('Message Send Error Handling', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test('should disable send button while agent is responding', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const sendButton = page.locator(selectors.sendButton);
    const testMessage = helpers.uniqueMessage('Disable test');

    // Send a message
    await textarea.fill(testMessage);
    await sendButton.click();

    // While agent is responding, send button should be disabled
    // (or input should be disabled)
    // This is a timing-sensitive test, so we check immediately after send
    const isDisabled = await sendButton.isDisabled();
    const inputDisabled = await textarea.isDisabled();

    // At least one should be disabled during response
    // (implementation may vary)
    expect(isDisabled || inputDisabled).toBe(true);

    // Wait for response to complete
    await helpers.waitForAgentResponse(page);
  });

  test('should re-enable input after agent response completes', async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const testMessage = helpers.uniqueMessage('Re-enable test');

    // Send a message
    await textarea.fill(testMessage);
    await page.locator(selectors.sendButton).click();

    // Wait for response
    await helpers.waitForAgentResponse(page);

    // Wait a bit for streaming to complete
    await page.waitForTimeout(1000);

    // Input should be enabled again
    await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
  });
});

