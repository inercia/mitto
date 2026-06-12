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

  test("clicking 'Make non-periodic' reverts the conversation and hides the periodic editor", async ({
    page,
    timeouts,
  }) => {
    // Step 1: Convert to periodic via "Make periodic" (reuse existing flow).
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    let menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makePeriodicBtn = menu.locator("button").filter({ hasText: "Make periodic" });
    await expect(makePeriodicBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makePeriodicBtn.click();

    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // Wait for the periodic editor to appear (confirms conversion succeeded).
    const periodicPanel = page.locator('[data-testid="periodic-frequency-panel"]');
    await expect(periodicPanel).toBeVisible({ timeout: timeouts.appReady });

    // Step 2: Right-click again — now "Make non-periodic" should be visible
    // and "Make periodic" should be gone (they are mutually exclusive).
    await sessionItem.click({ button: "right" });
    menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeNonPeriodicBtn = menu.locator("button").filter({ hasText: "Make non-periodic" });
    await expect(makeNonPeriodicBtn).toBeVisible({ timeout: timeouts.shortAction });

    // "Make periodic" must NOT appear for an already-periodic session.
    await expect(menu.locator("button").filter({ hasText: "Make periodic" })).toHaveCount(0);

    // Step 3: Click "Make non-periodic" and confirm the editor disappears.
    await makeNonPeriodicBtn.click();
    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // The periodic_updated broadcast (nil) flips periodic_enabled=false.
    // PeriodicFrequencyPanel stays in the DOM but collapses to h-0/opacity-0
    // (CSS transition), so Playwright sees it as not visible.
    await expect(periodicPanel).not.toBeVisible({ timeout: timeouts.appReady });
  });
});
