import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * Settings → Models: up/down reorder buttons (mitto-ex7.3).
 *
 * Backend already treats Config.Models slice order as priority
 * (SelectPreferredModel, ProfilesByTag); this spec covers the UI reorder
 * controls that expose that order to the user.
 */

const dialog = (page: Page) => page.locator('[data-testid="settings-dialog"]');

async function openModelsTab(page: Page) {
  await page.locator('button[data-testid="settings-btn"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
  await page.locator('[data-testid="settings-nav-models"]').click();
}

// Type a name into the currently expanded profile row's name input.
async function setExpandedProfileName(page: Page, name: string) {
  const nameInput = page.locator(
    '[data-testid="settings-dialog"] input[placeholder="e.g., Opus"]',
  );
  await nameInput.fill(name);
}

test.describe("Settings → Models reorder (mitto-ex7.3)", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("up/down buttons reorder profiles and honor boundaries", async ({
    page,
  }) => {
    await openModelsTab(page);

    // Add two profiles. Each Add Model click also expands the new row, so
    // the name input targets it directly.
    const addBtn = page.locator('[data-testid="add-model-profile"]');
    await addBtn.click();
    await setExpandedProfileName(page, "TestReorderA");
    await addBtn.click();
    await setExpandedProfileName(page, "TestReorderB");

    // Locate the two newly-added rows by name. We may have pre-existing
    // profiles from the default seed, so we assert on our two names by
    // finding their indices.
    const nameSpans = page.locator(
      '[data-testid="settings-dialog"] .font-medium.text-sm.flex-1.truncate',
    );
    const allNames = await nameSpans.allTextContents();
    const idxA = allNames.findIndex((s) => s.trim() === "TestReorderA");
    const idxB = allNames.findIndex((s) => s.trim() === "TestReorderB");
    expect(idxA).toBeGreaterThanOrEqual(0);
    expect(idxB).toBe(idxA + 1);

    // Boundary: first row's up button is disabled, last row's down button is
    // disabled.
    await expect(
      page.locator('[data-testid="model-profile-move-up-0"]'),
    ).toBeDisabled();
    const total = await nameSpans.count();
    await expect(
      page.locator(`[data-testid="model-profile-move-down-${total - 1}"]`),
    ).toBeDisabled();

    // Move TestReorderB (currently at idxB) up: swap with TestReorderA.
    await page
      .locator(`[data-testid="model-profile-move-up-${idxB}"]`)
      .click();

    const namesAfter = await nameSpans.allTextContents();
    expect(namesAfter[idxA].trim()).toBe("TestReorderB");
    expect(namesAfter[idxB].trim()).toBe("TestReorderA");
  });
});
