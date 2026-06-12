import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * "Make periodic" context menu action tests.
 *
 * Verifies that right-clicking a regular (non-periodic, non-child) conversation
 * and selecting "Make periodic" from the context menu:
 *   1. Sends PUT /api/sessions/{id}/periodic with the draft body.
 *   2. The periodic_updated broadcast triggers the frontend to flip
 *      session.periodic_enabled=true.
 *   3. The PeriodicFrequencyPanel (data-testid="periodic-frequency-panel") becomes
 *      visible in the ChatInput, confirming the inline editor opened.
 *
 * Setup mirrors conversation-context-menu.spec.ts (right-click the session item).
 */

// daisyUI context menus render as fixed-position <ul class="menu fixed z-50 …">
const MENU = ".menu.fixed.z-50.shadow-xl";

test.describe("Make periodic — context menu action", () => {
  let sessionId: string;

  test.beforeEach(async ({ page, request, helpers }) => {
    // Create a fresh regular (non-periodic) top-level session.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Make Periodic Test ${Date.now()}` },
    });
    expect(createResp.ok(), `POST /api/sessions failed: ${createResp.status()}`).toBeTruthy();
    const created = await createResp.json();
    sessionId = created.session_id || created.id;
    expect(sessionId).toBeTruthy();

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
  });

  test("shows 'Make periodic' in the context menu for a regular session", async ({
    page,
    timeouts,
  }) => {
    // Open context menu via right-click on the session item.
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    // "Make periodic" must be present.
    const makePeriodicBtn = menu.locator("button").filter({ hasText: "Make periodic" });
    await expect(makePeriodicBtn).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("clicking 'Make periodic' converts the conversation and opens the periodic editor", async ({
    page,
    timeouts,
  }) => {
    // Open context menu and click "Make periodic".
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makePeriodicBtn = menu.locator("button").filter({ hasText: "Make periodic" });
    await expect(makePeriodicBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makePeriodicBtn.click();

    // The menu should close after selection.
    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // The periodic_updated WebSocket event flips periodic_enabled=true.
    // PeriodicFrequencyPanel renders when periodicEnabled=true in ChatInput.
    const periodicPanel = page.locator('[data-testid="periodic-frequency-panel"]');
    await expect(periodicPanel).toBeVisible({ timeout: timeouts.appReady });
  });
});
