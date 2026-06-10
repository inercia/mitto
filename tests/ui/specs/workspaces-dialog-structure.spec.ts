import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Structural safety net for the WorkspacesDialog daisyUI conversion (mitto-i1r.5).
 *
 * The existing auxiliary-model-selection spec only asserts visibility/behavior,
 * NOT layout. daisyUI's .modal-box caps width at 32rem (~512px), so routing this
 * 70vw dialog through it would silently collapse the UI while passing those tests.
 *
 * This spec asserts the load-bearing structure the conversion must preserve:
 *   1. The dialog opens at its full (~70vw) size, not collapsed.
 *   2. Every workspace tab (general/runner/mcp) renders non-empty content.
 *   3. Every folder tab (general/metadata/beads/prompts/processors/children)
 *      renders non-empty content (catches render crashes / blank panels).
 *   4. The dialog-level Save button is present.
 *
 * Selectors are anchored on stable data-testids so they survive the restyle.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const FOLDER_NAME = "project-alpha";

const dialog = (page: Page) => page.locator('[data-testid="workspaces-dialog"]');
const tabContent = (page: Page) => page.locator('[data-testid="ws-tab-content"]');

async function openDialog(page: Page) {
  await page.locator('button[title="Workspaces"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
}

async function selectWorkspaceChild(page: Page) {
  const folderGroup = page
    .locator(`[data-folder-name="${FOLDER_NAME}"]`)
    .locator("..");
  await expect(folderGroup).toBeVisible({ timeout: 5000 });
  // Click the workspace child (not the folder header).
  await folderGroup.locator(".ml-4 > div").first().click();
}

async function selectFolderHeader(page: Page) {
  await page.locator(`[data-folder-name="${FOLDER_NAME}"]`).click();
}

// Click a tab and assert its content panel renders at least one element.
// Auto-waits, so it tolerates lazy-loaded panels (beads/mcp) while still
// catching a blanked subtree from a render crash.
async function assertTabRenders(page: Page, tabId: string) {
  await page.locator(`[data-testid="ws-tab-${tabId}"]`).click();
  await expect(tabContent(page).locator("> *").first()).toBeVisible({
    timeout: 5000,
  });
}

test.describe("WorkspacesDialog structure (daisyUI conversion safety net)", () => {
  // Skip in Docker — requires a host-local workspace path.
  test.beforeEach(() => {
    test.skip(
      !!process.env.MITTO_EXTERNAL_SERVER,
      "Requires host-local workspace path unavailable in Docker",
    );
  });

  // Ensure the project-alpha workspace exists before the tests run.
  test.beforeAll(async ({ request, apiUrl }) => {
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: "mock-acp", working_dir: WORKSPACE_ALPHA },
    });
  });

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

  test("workspace tabs all render content", async ({ page }) => {
    await openDialog(page);
    await selectWorkspaceChild(page);

    for (const id of ["general", "runner", "mcp"]) {
      await expect(page.locator(`[data-testid="ws-tab-${id}"]`)).toBeVisible();
    }

    await assertTabRenders(page, "runner");
    await assertTabRenders(page, "mcp");
    await assertTabRenders(page, "general");

    await expect(page.locator('[data-testid="ws-save"]')).toBeVisible();
  });

  test("folder tabs all render content", async ({ page }) => {
    await openDialog(page);
    await selectFolderHeader(page);

    const folderTabs = [
      "general",
      "metadata",
      "beads",
      "prompts",
      "processors",
      "children",
    ];
    for (const id of folderTabs) {
      await expect(page.locator(`[data-testid="ws-tab-${id}"]`)).toBeVisible();
    }

    // Visit each panel (end on general) and assert it renders.
    for (const id of [...folderTabs.slice(1), "general"]) {
      await assertTabRenders(page, id);
    }

    await expect(page.locator('[data-testid="ws-save"]')).toBeVisible();
  });
});
