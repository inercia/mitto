import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * "Make loop" context menu action tests.
 *
 * Verifies that right-clicking a regular (non-loop, non-child) conversation
 * and selecting "Make loop" from the context menu:
 *   1. Sends PUT /api/sessions/{id}/loop with the draft body.
 *   2. The loop_updated broadcast triggers the frontend to flip
 *      session.loop_enabled=true.
 *   3. The LoopFrequencyPanel (data-testid="loop-frequency-panel") becomes
 *      visible in the ChatInput, confirming the inline editor opened.
 *
 * Setup mirrors conversation-context-menu.spec.ts (right-click the session item).
 */

// daisyUI context menus render as fixed-position <ul class="menu fixed z-50 …">
const MENU = ".menu.fixed.z-50.shadow-xl";

test.describe("Make loop — context menu action", () => {
  let sessionId: string;

  test.beforeEach(async ({ page, request, helpers }) => {
    // Create a fresh regular (non-loop) top-level session.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Make Loop Test ${Date.now()}` },
    });
    expect(createResp.ok(), `POST /api/sessions failed: ${createResp.status()}`).toBeTruthy();
    const created = await createResp.json();
    sessionId = created.session_id || created.id;
    expect(sessionId).toBeTruthy();

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
  });

  test("shows 'Make loop' in the context menu for a regular session", async ({
    page,
    timeouts,
  }) => {
    // Open context menu via right-click on the session item.
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    // "Make loop" must be present.
    const makeLoopBtn = menu.locator("button").filter({ hasText: "Make loop" });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("clicking 'Make loop' converts the conversation and opens the loop editor", async ({
    page,
    timeouts,
  }) => {
    // Open context menu and click "Make loop".
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeLoopBtn = menu.locator("button").filter({ hasText: "Make loop" });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makeLoopBtn.click();

    // The menu should close after selection.
    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // The loop_updated WebSocket event flips loop_enabled=true.
    // LoopFrequencyPanel renders when loopEnabled=true in ChatInput.
    const loopPanel = page.locator('[data-testid="loop-frequency-panel"]');
    await expect(loopPanel).toBeVisible({ timeout: timeouts.appReady });
  });

  test("clicking 'Make non-loop' reverts the conversation and hides the loop editor", async ({
    page,
    timeouts,
  }) => {
    // Step 1: Convert to loop via "Make loop" (reuse existing flow).
    const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    let menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeLoopBtn = menu.locator("button").filter({ hasText: "Make loop" });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makeLoopBtn.click();

    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // Wait for the loop editor to appear (confirms conversion succeeded).
    const loopPanel = page.locator('[data-testid="loop-frequency-panel"]');
    await expect(loopPanel).toBeVisible({ timeout: timeouts.appReady });

    // Step 2: Right-click again — now "Make non-loop" should be visible
    // and "Make loop" should be gone (they are mutually exclusive).
    await sessionItem.click({ button: "right" });
    menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeNonLoopBtn = menu.locator("button").filter({ hasText: "Make non-loop" });
    await expect(makeNonLoopBtn).toBeVisible({ timeout: timeouts.shortAction });

    // "Make loop" must NOT appear for an already-loop session.
    await expect(menu.locator("button").filter({ hasText: "Make loop" })).toHaveCount(0);

    // Step 3: Click "Make non-loop" and confirm the editor disappears.
    await makeNonLoopBtn.click();
    await expect(page.locator(MENU)).toHaveCount(0, { timeout: timeouts.shortAction });

    // The loop_updated broadcast (nil) flips loop_enabled=false.
    // LoopFrequencyPanel stays in the DOM but collapses to h-0/opacity-0
    // (CSS transition), so Playwright sees it as not visible.
    await expect(loopPanel).not.toBeVisible({ timeout: timeouts.appReady });
  });
});
