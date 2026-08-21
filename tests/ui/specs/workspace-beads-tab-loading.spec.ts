import { test, expect } from "../fixtures/test-fixtures";
import type { Page, ConsoleMessage } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

/**
 * Regression test for mitto-xdqx (WorkspacesDialog Tasks/beads tab stuck on
 * "Loading…").
 *
 * The bug: after a successful config fetch, the beads tab's render function
 * referenced icon components (TrashIcon/PlusIcon/SlidersIcon) that were not
 * imported. The render threw a ReferenceError, Preact aborted the commit, and
 * the DOM remained on the previous frame — the loading spinner. The test
 * exercises switching folders, closing+reopening the dialog, and clicking
 * away+back to catch any repeat of this render-crash-latches-spinner pattern.
 *
 * The project-alpha fixture has NO .beads dir (matches the reported failing
 * case: bd surfaces integration keys that trigger the delete-button render
 * path).
 */

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const FOLDER_NAME = "project-alpha";

const LOAD_TIMEOUT_MS = 3000;

const dialog = (page: Page) => page.locator('[data-testid="workspaces-dialog"]');
const tabContent = (page: Page) => page.locator('[data-testid="ws-tab-content"]');
const beadsLoading = (page: Page) =>
  tabContent(page).locator('[data-testid="beads-loading"]');

async function openDialog(page: Page) {
  await page.locator('button[data-testid="workspaces-btn"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
}

async function closeDialog(page: Page) {
  await page.keyboard.press("Escape");
  await expect(dialog(page)).toBeHidden({ timeout: 5000 });
}

async function selectFolderHeader(page: Page) {
  await page.locator(`[data-folder-name="${FOLDER_NAME}"]`).click();
}

async function clickTab(page: Page, tabId: string) {
  await page.locator(`[data-testid="ws-tab-${tabId}"]`).click();
}

// Assert the "Loading…" spinner clears within LOAD_TIMEOUT_MS AND the tab
// body renders one of the loaded states (empty-state hint, editable rows, or
// error banner). The second assertion is the key catch for mitto-xdqx: a
// silent render crash after fetch success leaves the spinner in the DOM even
// though beadsConfigLoading is false, so a spinner-only assertion could
// spuriously pass while the panel is still broken.
async function assertBeadsLoadClears(page: Page, label: string) {
  await expect(
    beadsLoading(page),
    `[${label}] spinner should clear within ${LOAD_TIMEOUT_MS}ms`,
  ).toHaveCount(0, { timeout: LOAD_TIMEOUT_MS });
  const loadedState = tabContent(page).locator(
    'p:has-text("No integration keys set yet."), input[readonly], [role="alert"]',
  );
  await expect(
    loadedState.first(),
    `[${label}] tab body should render loaded state (empty/rows/error)`,
  ).toBeVisible({ timeout: 2000 });
}

test.describe("WorkspacesDialog beads tab loading (mitto-xdqx)", () => {
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

  test("beads tab clears Loading across switch/close-reopen/re-click paths", async ({
    page,
  }, testInfo) => {
    // Capture uncaught page errors — a silent ReferenceError in the render
    // function is what caused the original hang, so surface any such error in
    // the test report even if the assertions somehow pass.
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => {
      pageErrors.push(err.message);
    });
    page.on("console", (msg: ConsoleMessage) => {
      if (msg.type() === "error") pageErrors.push(`[console] ${msg.text()}`);
    });

    try {
      // ---- Path 1: fresh open → beads tab ---------------------------------
      await openDialog(page);
      await selectFolderHeader(page);
      await clickTab(page, "beads");
      await assertBeadsLoadClears(page, "path1: fresh open");

      // ---- Path 2: switch tab away and back -------------------------------
      await clickTab(page, "general");
      await clickTab(page, "beads");
      await assertBeadsLoadClears(page, "path2: away-and-back");

      // ---- Path 3: close dialog and re-open on same folder ---------------
      await closeDialog(page);
      await openDialog(page);
      await selectFolderHeader(page);
      await clickTab(page, "beads");
      await assertBeadsLoadClears(page, "path3: close-reopen");
    } finally {
      if (pageErrors.length > 0) {
        await testInfo.attach("page-errors.txt", {
          body: pageErrors.join("\n"),
          contentType: "text/plain",
        });
      }
    }

    expect(
      pageErrors,
      "no uncaught page errors during beads tab interactions",
    ).toEqual([]);
  });
});
