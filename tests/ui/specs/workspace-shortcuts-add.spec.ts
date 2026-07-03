import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Reproduction + regression lock for the "+ Add shortcut" seeding across all
 * three shortcut sections of the WorkspacesDialog "Cuts" (shortcuts) tab.
 *
 * The reported bug: adding a shortcut under "Conversation" / "Tasks list"
 * produced an unusable row (empty prompt selector, icon grid dominating the
 * view), while "Beads issue" worked. addShortcutRow() must be symmetric — a
 * freshly-added row in EVERY section must be seeded with the first available
 * prompt for that section (so the <select> has a non-empty value), which
 * requires sectionPrompts[section] to be populated for all three.
 *
 * The project-alpha fixture ships prompts tagged for each menu:
 *   - menus: beadsList   → tasksList section
 *   - menus: conversation → conversations section
 *   - menus: beadsIssues → beadsIssue section
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const FOLDER_NAME = "project-alpha";

const SECTIONS = ["tasksList", "conversations", "beadsIssue"] as const;

const dialog = (page: Page) =>
  page.locator('[data-testid="workspaces-dialog"]');

async function openDialog(page: Page) {
  await page.locator('button[data-testid="workspaces-btn"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
}

async function selectFolderHeader(page: Page) {
  await page.locator(`[data-folder-name="${FOLDER_NAME}"]`).click();
}

async function openShortcutsTab(page: Page) {
  await page.locator('[data-testid="ws-tab-shortcuts"]').click();
  // Wait for the shortcuts panel to finish loading its prompt lists: the
  // first section's "+ Add shortcut" button becomes enabled.
  await expect(
    page.locator('[data-testid="shortcut-add-tasksList"]'),
  ).toBeVisible({ timeout: 5000 });
}

test.describe("WorkspacesDialog shortcuts — Add shortcut seeding", () => {
  // Skip in Docker — requires a host-local workspace path.
  test.beforeEach(() => {
    test.skip(
      !!process.env.MITTO_EXTERNAL_SERVER,
      "Requires host-local workspace path unavailable in Docker",
    );
  });

  test.beforeAll(async ({ request, apiUrl }) => {
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: "mock-acp", working_dir: WORKSPACE_ALPHA },
    });
  });

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("adds a seeded row in every section (not just Beads issue)", async ({
    page,
  }) => {
    await openDialog(page);
    await selectFolderHeader(page);
    await openShortcutsTab(page);

    for (const section of SECTIONS) {
      const addBtn = page.locator(`[data-testid="shortcut-add-${section}"]`);
      await expect(addBtn).toBeEnabled();
      await addBtn.click();

      const row = page.locator(`[data-testid="shortcut-row-${section}-0"]`);
      await expect(row).toBeVisible({ timeout: 5000 });

      // The freshly-added row must be seeded with a real prompt: the <select>
      // value is non-empty (the reported bug left this empty for two sections).
      const select = row.locator("select");
      const value = await select.inputValue();
      expect(
        value,
        `section "${section}" should seed the first available prompt`,
      ).not.toBe("");

      // The icon-picker grid must collapse when the dropdown is not focused.
      // daisyUI collapses it via display:none; a `display` utility (flex) placed
      // directly on .dropdown-content would land in the utilities layer and
      // override that rule, leaving the grid permanently laid out (invisible but
      // click-intercepting — it would swallow clicks on the controls below it,
      // e.g. the next section's "+ Add shortcut"). Assert it is truly hidden.
      const grid = row.locator(".dropdown-content");
      await expect(grid).toBeHidden();
    }
  });
});
