import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Auxiliary Model Selection tests for Mitto Web UI.
 *
 * Auxiliary sessions now always run on the workspace's own ACP server. The
 * workspace editor exposes a shared "Model Selection" control (match mode +
 * pattern) that, when set, switches auxiliary sessions to a specific model.
 *
 * These tests verify the UI round-trip for that control:
 *   1. Setting a match mode + pattern persists to the workspace config.
 *   2. The values survive a page reload.
 *   3. Clearing the match mode removes the configuration entirely.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const FOLDER_NAME = "project-alpha";

// Locate the ModelSelection match-mode <select> by an option unique to it.
function modeSelect(page: Page) {
  return page
    .locator(".workspaces-dialog select")
    .filter({ has: page.locator('option[value="lookAlike"]') });
}

function patternInput(page: Page) {
  return page.locator('.workspaces-dialog input[placeholder="e.g., Opus 4.6"]');
}

// Open the Workspaces dialog and select the project-alpha workspace (General tab).
async function openWorkspaceGeneralTab(page: Page) {
  await page.locator('button[data-testid="workspaces-btn"]').first().click();
  await expect(page.locator(".workspaces-dialog")).toBeVisible({ timeout: 5000 });

  const folderGroup = page
    .locator(`[data-folder-name="${FOLDER_NAME}"]`)
    .locator("..");
  await expect(folderGroup).toBeVisible({ timeout: 5000 });
  // Click the workspace child (not the folder header) to load workspace edits.
  await folderGroup.locator(".ml-4 > div").first().click();

  // General tab is selected by default; the model-selection control is present.
  await expect(modeSelect(page)).toBeVisible({ timeout: 5000 });
}

async function saveDialog(page: Page) {
  await page
    .locator(".workspaces-dialog")
    .getByRole("button", { name: "Save", exact: true })
    .click();
  await expect(page.getByText("Workspaces saved")).toBeVisible({ timeout: 5000 });
}

test.describe("Auxiliary Model Selection", () => {
  // Skip in Docker — requires a host-local workspace path.
  test.beforeEach(() => {
    test.skip(!!process.env.MITTO_EXTERNAL_SERVER,
      "Requires host-local workspace path unavailable in Docker");
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

  test("should persist auxiliary model selection and survive reload", async ({
    page,
    request,
    apiUrl,
  }) => {
    await openWorkspaceGeneralTab(page);

    // Set match mode + pattern, then save.
    await modeSelect(page).selectOption("contains");
    await expect(patternInput(page)).toBeEnabled();
    await patternInput(page).fill("Opus");
    await saveDialog(page);

    // Verify persistence via the config API (authoritative check).
    await expect
      .poll(async () => {
        const cfg = await (await request.get(apiUrl("/api/config"))).json();
        const ws = cfg.workspaces?.find(
          (w: any) => w.working_dir === WORKSPACE_ALPHA,
        );
        return ws?.auxiliary_model_selection ?? null;
      }, { timeout: 5000 })
      .toEqual({ matchMode: "contains", pattern: "Opus" });

    // Verify the values are restored after a full page reload.
    await page.reload();
    await page.locator('button[data-testid="workspaces-btn"]').first().waitFor();
    await openWorkspaceGeneralTab(page);
    await expect(modeSelect(page)).toHaveValue("contains");
    await expect(patternInput(page)).toHaveValue("Opus");
  });

  test("should remove auxiliary model selection when match mode is cleared", async ({
    page,
    request,
    apiUrl,
  }) => {
    // Seed a selection through the UI so this test is independent.
    await openWorkspaceGeneralTab(page);
    await modeSelect(page).selectOption("exact");
    await expect(patternInput(page)).toBeEnabled();
    await patternInput(page).fill("Sonnet");
    await saveDialog(page);
    // Save keeps the dialog open; wait for the seed toast to clear so the next
    // save produces a fresh "saved" toast rather than matching the stale one.
    await expect(page.getByText("Workspaces saved")).toBeHidden({ timeout: 5000 });

    // Now clear the match mode back to "-- None --" and save (dialog still open).
    await expect(modeSelect(page)).toHaveValue("exact");
    await modeSelect(page).selectOption("");
    // Pattern input becomes disabled once the match mode is cleared.
    await expect(patternInput(page)).toBeDisabled();
    await saveDialog(page);

    // The auxiliary_model_selection field must be omitted entirely.
    await expect
      .poll(async () => {
        const cfg = await (await request.get(apiUrl("/api/config"))).json();
        const ws = cfg.workspaces?.find(
          (w: any) => w.working_dir === WORKSPACE_ALPHA,
        );
        return ws?.auxiliary_model_selection ?? null;
      }, { timeout: 5000 })
      .toBeNull();
  });
});
