import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * Structural safety net for the SettingsDialog daisyUI conversion (mitto-i1r.4).
 *
 * SettingsDialog has NO prior Playwright coverage. Like WorkspacesDialog it uses
 * a bespoke 70vw shell (daisyUI .modal-box caps width at 32rem, which would
 * collapse it). This spec locks down the load-bearing structure the conversion
 * must preserve:
 *   1. The dialog opens at its full (~70vw) size, not collapsed.
 *   2. Every sidebar panel (servers/runners/permissions/web/ui) renders
 *      non-empty content (catches render crashes / blank panels).
 *   3. The Save button is present.
 *
 * Selectors are anchored on stable data-testids so they survive the restyle.
 */

const NAV_IDS = ["servers", "runners", "permissions", "web", "ui"] as const;

const dialog = (page: Page) => page.locator('[data-testid="settings-dialog"]');
const content = (page: Page) => page.locator('[data-testid="settings-content"]');

async function openDialog(page: Page) {
  await page.locator('button[data-testid="settings-btn"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
}

// Click a nav item and assert its panel renders at least one element.
// Auto-waits, tolerating lazily-loaded panels while still catching a blanked
// subtree from a render crash.
async function assertPanelRenders(page: Page, navId: string) {
  await page.locator(`[data-testid="settings-nav-${navId}"]`).click();
  await expect(content(page).locator("> *").first()).toBeVisible({
    timeout: 5000,
  });
}

test.describe("SettingsDialog structure (daisyUI conversion safety net)", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("opens at full size (not collapsed to modal-box max-width)", async ({
    page,
  }) => {
    await openDialog(page);
    const box = await dialog(page).boundingBox();
    expect(box).not.toBeNull();
    // .modal-box caps width at 32rem (~512px); this dialog must be ~70vw.
    expect(box!.width).toBeGreaterThan(600);
    expect(box!.height).toBeGreaterThan(300);
  });

  test("all sidebar panels render content", async ({ page }) => {
    await openDialog(page);

    for (const id of NAV_IDS) {
      await expect(
        page.locator(`[data-testid="settings-nav-${id}"]`),
      ).toBeVisible();
    }

    for (const id of NAV_IDS) {
      await assertPanelRenders(page, id);
    }

    await expect(page.locator('[data-testid="settings-save"]')).toBeVisible();
  });
});
