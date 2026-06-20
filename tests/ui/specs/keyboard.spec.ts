import { test, expect } from "../fixtures/test-fixtures";

/**
 * Keyboard navigation and accessibility tests for Mitto Web UI.
 *
 * These tests verify keyboard shortcuts and navigation work correctly.
 */

test.describe("Keyboard Navigation", () => {
  test.beforeEach(async ({ page, helpers }) => {
    // These tests focus/fill/click the chat textarea, which is rendered but
    // DISABLED when no active regular session exists. navigateAndWait alone is
    // satisfied by the sidebar header, so in isolation (no prior test created a
    // session) the textarea stays disabled and the tests fail. Ensure an active
    // regular session so the textarea is enabled and editable.
    await helpers.navigateAndEnsureSession(page);
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

test.describe("Session Navigation", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    // Ensure we start on the Conversations tab
    const conversationsTab = page.getByRole("tab", { name: "Conversations" });
    if (await conversationsTab.isVisible()) {
      const isSelected = await conversationsTab.getAttribute("aria-selected");
      if (isSelected !== "true") {
        await conversationsTab.click();
      }
    }
  });

  /**
   * Swipe navigation assessment (Meta+Control+ArrowUp/Down covers keyboard; swipe covers touch):
   *
   * useSwipeNavigation attaches touchstart/touchmove/touchend listeners to the
   * main content ref. In the native macOS app (WKWebView), swipe gestures are
   * intercepted by native recognizers that call registered window functions
   * (e.g., window.mittoSwipeLeft / window.mittoSwipeRight). In headless Chromium,
   * the browser context runs without hasTouch: true, so touchstart events are not
   * dispatched by pointer/mouse actions, and simulating a full
   * touchstart→touchmove→touchend sequence across 80+ pixels is unreliable.
   * Swipe navigation is therefore covered by manual testing on macOS only; a
   * deterministic Playwright test is intentionally omitted to avoid a flaky suite.
   */

  test("Meta+Control+ArrowUp/Down should switch the active session across ≥2 sessions", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // Create two sessions so there are ≥2 to navigate between.
    // createFreshSession leaves the newly created session as the active one.
    const session1Id = await helpers.createFreshSession(page);
    const session2Id = await helpers.createFreshSession(page);

    // Sessions are ordered newest-first in navigableSessions, so:
    //   index 0 → session2 (created last, visually at top of list)
    //   index 1 → session1 (created first, visually below session2)
    // Navigate to session1 (bottom of 2) so both arrow directions are exercisable.
    await helpers.navigateToSession(page, session1Id);

    // Sanity: confirm session1 is now active
    await expect
      .poll(
        () => page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
        { timeout: timeouts.shortAction },
      )
      .toBe(session1Id);

    // --- ArrowUp: navigate to session above (session2, index 0) ---
    await page.keyboard.press("Meta+Control+ArrowUp");

    // Primary assertion: localStorage ground-truth reflects the switch
    await expect
      .poll(
        () => page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
        { timeout: timeouts.shortAction },
      )
      .toBe(session2Id);

    // Secondary assertion: sidebar active highlight moved to session2
    await expect(
      page.locator(selectors.activeSessionItemById(session2Id)).first(),
    ).toBeVisible({ timeout: timeouts.shortAction });

    // --- ArrowDown: navigate back to session1 (index 1) ---
    await page.keyboard.press("Meta+Control+ArrowDown");

    // Primary assertion: localStorage shows session1 is active again
    await expect
      .poll(
        () => page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
        { timeout: timeouts.shortAction },
      )
      .toBe(session1Id);

    // Secondary assertion: sidebar active highlight returned to session1
    await expect(
      page.locator(selectors.activeSessionItemById(session1Id)).first(),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("Meta+Control+ArrowUp should not crash at top of list (edge case)", async ({
    page,
    helpers,
    timeouts,
  }) => {
    // Create two sessions; navigate to session2 (index 0 = top of the list)
    await helpers.createFreshSession(page);
    await helpers.createFreshSession(page);
    // session2 is already active (index 0 = top). ArrowUp should be a no-op.
    const beforeId = await page.evaluate(() =>
      localStorage.getItem("mitto_last_session_id"),
    );

    await page.keyboard.press("Meta+Control+ArrowUp");
    await page.waitForTimeout(300);

    // App must still be functional and active session must NOT have changed
    await expect(page.locator("#app")).toBeVisible();
    const afterId = await page.evaluate(() =>
      localStorage.getItem("mitto_last_session_id"),
    );
    expect(afterId).toBe(beforeId);
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

    // New session button should expose an accessible name (aria-label).
    // The tooltip migrated from a native `title` to a daisyUI tooltip, so the
    // accessible name now lives on `aria-label` rather than `title`.
    const newButton = page.locator(selectors.newSessionButton);
    const ariaLabel = await newButton.getAttribute("aria-label");
    expect(ariaLabel).toBeTruthy();
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
