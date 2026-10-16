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
 * workspace editor lets a workspace select a named model profile for auxiliary
 * sessions.
 *
 * These tests verify the profile-selection round-trip:
 *   1. A selected profile persists to workspace config and survives reload.
 *   2. Clearing the profile removes it from workspace config.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const FOLDER_NAME = "project-alpha";
const PROFILE_NAME = "Auxiliary UI Test Profile";

let originalConfig: { models: any[]; workspaces: any[] } | null = null;

// Locate the profile selector (the first selector in the auxiliary-selection
// row) from the field's own container.
function auxiliaryProfileSelect(page: Page) {
  return page
    .getByText("Auxiliary Model Selection (optional)", { exact: true })
    .locator("..")
    .locator("select")
    .first();
}

async function getConfig(request, apiUrl) {
  const response = await request.get(apiUrl("/api/config"));
  expect(response.ok()).toBeTruthy();
  return response.json();
}

// Patch only the supported settings fields, keeping the existing config intact
// and deliberately omitting web/auth, whose GET values are sanitized.
async function saveConfig(request, apiUrl, cfg, models, workspaces) {
  const body: Record<string, unknown> = {
    workspaces,
    acp_servers: cfg.acp_servers || [],
    prompts: cfg.prompts || [],
    ui: cfg.ui || {},
    models,
  };
  if (cfg.conversations) body.conversations = cfg.conversations;
  if (cfg.session) body.session = cfg.session;
  if (cfg.permissions) body.permissions = cfg.permissions;

  const response = await request.post(apiUrl("/api/config"), { data: body });
  expect(response.ok()).toBeTruthy();
}

// Open the Workspaces dialog and select the project-alpha workspace (General tab).
async function openWorkspaceGeneralTab(page: Page) {
  await page.locator('button[data-testid="workspaces-btn"]').first().click();
  await expect(page.locator(".workspaces-dialog")).toBeVisible({
    timeout: 5000,
  });

  const folderGroup = page
    .locator(`[data-folder-name="${FOLDER_NAME}"]`)
    .locator("..");
  await expect(folderGroup).toBeVisible({ timeout: 5000 });
  // Click the workspace child (not the folder header) to load workspace edits.
  await folderGroup.locator(".ml-4 > div").first().click();

  // General tab is selected by default; the profile selector is present.
  await expect(auxiliaryProfileSelect(page)).toBeVisible({ timeout: 5000 });
}

async function saveDialog(page: Page) {
  await page
    .locator(".workspaces-dialog")
    .getByRole("button", { name: "Save", exact: true })
    .click();
  await expect(page.getByText("Workspaces saved")).toBeVisible({
    timeout: 5000,
  });
}

test.describe("Auxiliary Model Selection", () => {
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

  test.beforeEach(async ({ request, apiUrl, page, helpers }) => {
    const cfg = await getConfig(request, apiUrl);
    originalConfig = {
      models: JSON.parse(JSON.stringify(cfg.models || [])),
      workspaces: JSON.parse(JSON.stringify(cfg.workspaces || [])),
    };

    const models = originalConfig.models.filter(
      (profile) => profile.name !== PROFILE_NAME,
    );
    models.push({ name: PROFILE_NAME });
    await saveConfig(request, apiUrl, cfg, models, originalConfig.workspaces);

    await helpers.navigateAndWait(page);
  });

  test.afterEach(async ({ request, apiUrl }) => {
    if (!originalConfig) return;

    try {
      const cfg = await getConfig(request, apiUrl);
      await saveConfig(
        request,
        apiUrl,
        cfg,
        originalConfig.models,
        originalConfig.workspaces,
      );
    } finally {
      originalConfig = null;
    }
  });

  test("should persist auxiliary model profile and survive reload", async ({
    page,
    request,
    apiUrl,
  }) => {
    await openWorkspaceGeneralTab(page);

    await auxiliaryProfileSelect(page).selectOption(PROFILE_NAME);
    await saveDialog(page);

    // Verify persistence via the config API (authoritative check).
    await expect
      .poll(
        async () => {
          const cfg = await getConfig(request, apiUrl);
          const ws = cfg.workspaces?.find(
            (w: any) => w.working_dir === WORKSPACE_ALPHA,
          );
          return ws?.auxiliary_model_profile ?? null;
        },
        { timeout: 5000 },
      )
      .toBe(PROFILE_NAME);

    // Verify the selected profile is restored after a full page reload.
    await page.reload();
    await page
      .locator('button[data-testid="workspaces-btn"]')
      .first()
      .waitFor();
    await openWorkspaceGeneralTab(page);
    await expect(auxiliaryProfileSelect(page)).toHaveValue(PROFILE_NAME);
  });

  test("should remove auxiliary model profile when selection is cleared", async ({
    page,
    request,
    apiUrl,
  }) => {
    await openWorkspaceGeneralTab(page);
    await auxiliaryProfileSelect(page).selectOption(PROFILE_NAME);
    await saveDialog(page);
    // Save keeps the dialog open; wait for the seed toast to clear so the next
    // save produces a fresh "saved" toast rather than matching the stale one.
    await expect(page.getByText("Workspaces saved")).toBeHidden({
      timeout: 5000,
    });

    // Clear the profile back to "-- None --" and save (dialog still open).
    await expect(auxiliaryProfileSelect(page)).toHaveValue(PROFILE_NAME);
    await auxiliaryProfileSelect(page).selectOption("");
    await saveDialog(page);

    // The auxiliary_model_profile field must be omitted entirely.
    await expect
      .poll(
        async () => {
          const cfg = await getConfig(request, apiUrl);
          const ws = cfg.workspaces?.find(
            (w: any) => w.working_dir === WORKSPACE_ALPHA,
          );
          return ws?.auxiliary_model_profile ?? null;
        },
        { timeout: 5000 },
      )
      .toBeNull();
  });
});
