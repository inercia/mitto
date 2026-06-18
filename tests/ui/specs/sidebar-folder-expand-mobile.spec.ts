import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Mobile sidebar folder-expansion behavior (mitto-2du).
 *
 * Regression: on a phone-sized viewport, tapping a *collapsed* folder header to
 * expand it used to immediately close the sidebar drawer (because expanding a
 * folder auto-focuses the group's last conversation / Tasks view, and that
 * selection closed the drawer). Expanding a folder must now keep the drawer
 * open — the conversation / Tasks view loads underneath. A *direct*
 * conversation or Tasks click still closes the drawer (covered elsewhere).
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// The mobile sidebar drawer container (z-40). Modal dialogs use z-50, so this
// is unambiguous. Its open state is reflected by the #sidebar-drawer checkbox.
const MOBILE_OVERLAY = ".drawer-side.z-40";
const SIDEBAR_TOGGLE = "#sidebar-drawer";
const MOBILE_VIEWPORT = { width: 390, height: 844 };

testWithCleanup.describe("Sidebar - mobile folder expansion", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Ensure the project-alpha workspace exists so its folder header renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation so the project-alpha folder header appears.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Folder Expand Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  // Opens the mobile sidebar via the header hamburger and returns its summary.
  async function openSidebarAndGetFolder(page, timeouts) {
    await page.setViewportSize(MOBILE_VIEWPORT);

    const hamburger = page.locator('button[aria-label="Show conversations"]');
    await expect(hamburger).toBeVisible({ timeout: timeouts.appReady });
    await hamburger.click();

    const overlay = page.locator(MOBILE_OVERLAY);
    await expect(overlay).toBeVisible({ timeout: timeouts.shortAction });

    const folderHeader = overlay
      .locator('summary[data-has-context-menu="true"]')
      .filter({ hasText: "project-alpha" })
      .first();
    await expect(folderHeader).toBeVisible({ timeout: timeouts.shortAction });
    return { overlay, folderHeader };
  }

  testWithCleanup(
    "expanding a collapsed folder keeps the sidebar drawer open",
    async ({ page, timeouts }) => {
      const { folderHeader } = await openSidebarAndGetFolder(page, timeouts);

      // Folders default to expanded. Collapse first (toggling closed does not
      // auto-select, so the drawer stays open) to reach the collapsed state.
      const details = folderHeader.locator("xpath=..");
      if (await details.evaluate((el) => (el as HTMLDetailsElement).open)) {
        await folderHeader.click();
        await expect(details).not.toHaveAttribute("open", /.*/);
      }

      // The drawer is still open after collapsing.
      await expect(page.locator(SIDEBAR_TOGGLE)).toBeChecked();

      // Expand the collapsed folder: this auto-focuses the group's conversation
      // / Tasks view, but the drawer must remain open.
      await folderHeader.click();
      await expect(details).toHaveAttribute("open", /.*/);

      // Regression assertion: the sidebar drawer is still open.
      await expect(page.locator(SIDEBAR_TOGGLE)).toBeChecked();
      await expect(page.locator(MOBILE_OVERLAY)).toBeVisible();
    },
  );

  testWithCleanup(
    "directly clicking a conversation still closes the sidebar drawer",
    async ({ page, timeouts }) => {
      const { overlay } = await openSidebarAndGetFolder(page, timeouts);

      // A direct conversation click (not a folder expand) should close the
      // mobile drawer so the conversation is shown.
      const conversation = overlay
        .locator("div[data-session-id]")
        .filter({ hasText: "Folder Expand Seed" })
        .first();
      await expect(conversation).toBeVisible({ timeout: timeouts.shortAction });
      await conversation.click();

      await expect(page.locator(SIDEBAR_TOGGLE)).not.toBeChecked();
    },
  );
});
