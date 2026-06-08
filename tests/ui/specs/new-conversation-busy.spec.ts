import { test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * Tests for New Conversation busy-state UX (mitto-8yv.1).
 *
 * Acceptance criteria:
 *  1. Clicking "New Conversation" while agent is busy shows an explicit
 *     busy/spinner state (button currently never disabled).
 *  2. On 503 session_creation_timeout a clear toast appears and the UI
 *     auto-retries — no silent instant-return clicks.
 *  3. This test: click → busy state → toast → auto-retry → session created.
 */

test.describe("New Conversation: busy state + auto-retry", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    const conversationsTab = page.getByRole("tab", { name: "Conversations" });
    if (await conversationsTab.isVisible()) {
      const isSelected = await conversationsTab.getAttribute("aria-selected");
      if (isSelected !== "true") {
        await conversationsTab.click();
      }
    }
  });

  /**
   * Full flow: click New Conversation → 503 → busy spinner + toast → clock tick
   * 30 s → auto-retry succeeds → new session appears, button re-enabled.
   */
  test("shows busy state and auto-retries after 503 session_creation_timeout", async ({
    page,
    selectors,
  }) => {
    const sessionItems = page.locator(selectors.sessionsList);
    const initialCount = await sessionItems.count();

    // Install fake clock so we can fast-forward the 30 s retry timer.
    await page.clock.install({ time: Date.now() });

    let creationCallCount = 0;

    // Intercept POST /api/sessions:
    //   • 1st call → 503 session_creation_timeout (agent busy)
    //   • 2nd call → let it reach the real test server (success)
    await page.route(`**${apiUrl("/api/sessions")}`, async (route, request) => {
      if (request.method() !== "POST") {
        await route.continue();
        return;
      }
      creationCallCount++;
      if (creationCallCount === 1) {
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: "session_creation_timeout",
            message: "Agent is busy \u2014 please try again in a moment",
          }),
        });
      } else {
        await route.continue();
      }
    });

    // Use the stable data-testid selector so assertions work even after the
    // button's title changes from "New Conversation" to "Creating conversation…".
    const newButton = page.locator('[data-testid="new-conversation-btn"]');
    await expect(newButton).toBeVisible({ timeout: 5000 });

    // Click New Conversation.
    await newButton.click();

    // --- Acceptance criterion 1: button must show busy/spinner state ---
    // The button becomes disabled and its title changes while a retry is pending.
    await expect(newButton).toBeDisabled({ timeout: 5000 });
    await expect(newButton).toHaveAttribute(
      "title",
      /creating conversation/i,
      { timeout: 5000 },
    );

    // --- Acceptance criterion 2: a visible toast with the busy message ---
    const busyToast = page
      .locator(".toast-enter")
      .filter({ hasText: /agent is busy/i });
    await expect(busyToast).toBeVisible({ timeout: 5000 });

    // Advance the fake clock by 30 s — triggers the auto-retry setTimeout.
    await page.clock.fastForward(30000);

    // --- Acceptance criterion 3: retry fires and session is created ---
    await expect(async () => {
      const count = await sessionItems.count();
      expect(count).toBeGreaterThan(initialCount);
    }).toPass({ timeout: 15000 });

    // Button must return to the normal (enabled) state after success.
    await expect(newButton).toBeEnabled({ timeout: 5000 });
    // After success the title reverts to "New Conversation".
    await expect(newButton).toHaveAttribute("title", /new conversation/i, {
      timeout: 5000,
    });

    // Verify exactly two POST attempts were made (first failed, second succeeded).
    expect(creationCallCount).toBe(2);
  });

  /**
   * Regression: button must NOT silently ignore clicks (old backoff behaviour).
   * On 503 the UI must be visibly in a retrying state — not back to idle.
   */
  test("button does not silently return to idle after 503 (no silent backoff)", async ({
    page,
    selectors,
  }) => {
    await page.clock.install({ time: Date.now() });

    // Always 503 — we just want to verify the busy state persists.
    await page.route(`**${apiUrl("/api/sessions")}`, async (route, request) => {
      if (request.method() === "POST") {
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: "session_creation_timeout",
            message: "Agent is busy \u2014 please try again in a moment",
          }),
        });
      } else {
        await route.continue();
      }
    });

    const newButton = page.locator('[data-testid="new-conversation-btn"]');
    await expect(newButton).toBeVisible({ timeout: 5000 });
    await newButton.click();

    // After the 503, button must still be disabled (retry pending) rather
    // than immediately re-enabled (the old silent-backoff behaviour).
    await expect(newButton).toBeDisabled({ timeout: 5000 });
  });
});
