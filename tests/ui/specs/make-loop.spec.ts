import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * "Loop" context menu action tests.
 *
 * Verifies that right-clicking a regular (non-loop, non-child) conversation
 * and selecting "Loop" from the context menu:
 *   1. Sends PUT /api/sessions/{id}/loop with the draft body.
 *   2. The loop_updated broadcast triggers the frontend to flip
 *      session.loop_enabled=true.
 *   3. The LoopFrequencyPanel (data-testid="loop-frequency-panel") becomes
 *      visible in the ChatInput, confirming the inline editor opened.
 *
 * Setup mirrors conversation-context-menu.spec.ts (right-click the session item).
 */

// daisyUI context menus render as fixed-position <ul class="menu fixed z-50 …">
const MENU = ".menu.fixed.shadow-xl";

test.describe("Loop — context menu action", () => {
  let sessionId: string;

  test.beforeEach(async ({ page, request, helpers }) => {
    // Create a fresh regular (non-loop) top-level session.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Make Loop Test ${Date.now()}` },
    });
    expect(
      createResp.ok(),
      `POST /api/sessions failed: ${createResp.status()}`,
    ).toBeTruthy();
    const created = await createResp.json();
    sessionId = created.session_id || created.id;
    expect(sessionId).toBeTruthy();

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
  });

  test("shows 'Loop' in the context menu for a regular session", async ({
    page,
    timeouts,
  }) => {
    // Open context menu via right-click on the session item.
    const sessionItem = page
      .locator(`[data-session-id="${sessionId}"]`)
      .first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    // "Loop" must be present.
    const makeLoopBtn = menu.locator("button").filter({ hasText: /^Loop$/ });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("clicking 'Loop' converts the conversation and opens the loop editor", async ({
    page,
    timeouts,
  }) => {
    // Open context menu and click "Loop".
    const sessionItem = page
      .locator(`[data-session-id="${sessionId}"]`)
      .first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeLoopBtn = menu.locator("button").filter({ hasText: /^Loop$/ });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makeLoopBtn.click();

    // The menu should close after selection.
    await expect(page.locator(MENU)).toHaveCount(0, {
      timeout: timeouts.shortAction,
    });

    // Conversion opens the new Loop tab directly. Compact controls stay in the
    // composer while the full staged editor lives in the side panel.
    const panel = page.locator('[data-testid="session-panel"]');
    await expect(panel).toBeVisible({ timeout: timeouts.appReady });
    await expect(panel.locator('label[aria-label="Loop"] input')).toBeChecked();
    await expect(
      panel.locator('[data-testid="loop-settings-tab"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="loop-control-bar"]'),
    ).toBeVisible();
    await expect(panel.locator('[role="tablist"] label.tab')).toHaveCount(4);
  });

  test("clicking 'Make non-loop' reverts the conversation and hides the loop editor", async ({
    page,
    timeouts,
  }) => {
    // Step 1: Convert to loop via "Loop" (reuse existing flow).
    const sessionItem = page
      .locator(`[data-session-id="${sessionId}"]`)
      .first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    let menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeLoopBtn = menu.locator("button").filter({ hasText: /^Loop$/ });
    await expect(makeLoopBtn).toBeVisible({ timeout: timeouts.shortAction });
    await makeLoopBtn.click();

    await expect(page.locator(MENU)).toHaveCount(0, {
      timeout: timeouts.shortAction,
    });

    // Wait for the compact controls and full editor (confirms conversion succeeded).
    const loopBar = page.locator('[data-testid="loop-control-bar"]');
    const panel = page.locator('[data-testid="session-panel"]');
    await expect(loopBar).toBeVisible({ timeout: timeouts.appReady });
    await expect(
      panel.locator('[data-testid="loop-settings-tab"]'),
    ).toBeVisible();

    // Step 2: Right-click again — now "Make non-loop" should be visible
    // and "Loop" should be gone (they are mutually exclusive).
    await sessionItem.click({ button: "right" });
    menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const makeNonLoopBtn = menu
      .locator("button")
      .filter({ hasText: "Make non-loop" });
    await expect(makeNonLoopBtn).toBeVisible({ timeout: timeouts.shortAction });

    // "Loop" must NOT appear for an already-loop session.
    await expect(
      menu.locator("button").filter({ hasText: /^Loop$/ }),
    ).toHaveCount(0);

    // Step 3: Click "Make non-loop" and confirm loop-only UI disappears.
    await makeNonLoopBtn.click();
    await expect(page.locator(MENU)).toHaveCount(0, {
      timeout: timeouts.shortAction,
    });

    // The open panel falls back to Properties rather than retaining a tab that
    // no longer exists for this regular conversation.
    await expect(loopBar).toHaveCount(0, { timeout: timeouts.appReady });
    await expect(panel.locator('label[aria-label="Loop"]')).toHaveCount(0);
    await expect(
      panel.locator('label[aria-label="Properties"] input'),
    ).toBeChecked();
  });
});
