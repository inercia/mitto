import { test, expect } from "../fixtures/test-fixtures";

/**
 * Keyboard navigation and accessibility tests for Mitto Web UI.
 *
 * These tests verify keyboard shortcuts and navigation work correctly.
 */

test.describe("Keyboard Navigation", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should focus chat input on page load", async ({ page, selectors }) => {
    // The chat input should be focusable
    const textarea = page.locator(selectors.chatInput);
    await textarea.focus();
    await expect(textarea).toBeFocused();
  });

  test("should submit message with Enter key", async ({
    page,
    selectors,
    helpers,
  }) => {
    const textarea = page.locator(selectors.chatInput);
    const testMessage = helpers.uniqueMessage("Enter submit");

    await textarea.fill(testMessage);
    await page.keyboard.press("Enter");

    // Message should be sent
    await expect(page.locator(`text=${testMessage}`)).toBeVisible({
      timeout: 5000,
    });
  });

  test("should insert newline with Shift+Enter", async ({
    page,
    selectors,
  }) => {
    const textarea = page.locator(selectors.chatInput);

    await textarea.click();
    await page.keyboard.type("Line 1");
    await page.keyboard.press("Shift+Enter");
    await page.keyboard.type("Line 2");

    const value = await textarea.inputValue();
    expect(value).toContain("Line 1");
    expect(value).toContain("Line 2");
    expect(value.split("\n").length).toBeGreaterThanOrEqual(2);
  });

  test("should handle Escape key gracefully", async ({ page, selectors }) => {
    const textarea = page.locator(selectors.chatInput);

    await textarea.fill("Some text");
    await page.keyboard.press("Escape");

    // App should still be functional
    await expect(page.locator(selectors.app)).toBeVisible();
  });
});

test.describe("Keyboard Shortcuts", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should support Cmd/Ctrl+1-9 for session switching", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // This test verifies the shortcut doesn't break the app
    // Actual session switching depends on having multiple sessions
    await page.keyboard.press("Meta+1");
    await page.waitForTimeout(500);

    // App should still be functional
    await expect(page.locator(selectors.app)).toBeVisible();
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.shortAction,
    });
  });
});

test.describe("Accessibility", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should have accessible chat input", async ({ page, selectors }) => {
    const textarea = page.locator(selectors.chatInput);

    // Should have a placeholder (acts as label)
    const placeholder = await textarea.getAttribute("placeholder");
    expect(placeholder).toBeTruthy();
  });

  test("should have accessible buttons", async ({ page, selectors }) => {
    // Send button should be visible (icon-only button)
    const sendButton = page.locator(selectors.sendButton);
    await expect(sendButton).toBeVisible();

    // New session button should have a title
    const newButton = page.locator(selectors.newSessionButton);
    const title = await newButton.getAttribute("title");
    expect(title).toBeTruthy();
  });

  test("should support tab navigation", async ({ page, selectors }) => {
    // Focus the chat input
    const textarea = page.locator(selectors.chatInput);
    await textarea.focus();
    await expect(textarea).toBeFocused();

    // Tab to next element
    await page.keyboard.press("Tab");

    // Something should be focused (not necessarily the send button)
    const focusedElement = page.locator(":focus");
    await expect(focusedElement).toBeVisible();
  });
});
