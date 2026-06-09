import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Group context menu tests for the Mitto Web UI.
 *
 * Verifies the right-click context menu on a conversation group (folder) header,
 * focusing on the "New" entry that exposes a hover-expandable submenu listing the
 * workspaces/agents available for that folder (mirroring the "+" button).
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);

// The mock ACP server name configured in tests/ui/global-setup.ts. This is the
// label shown for the agent entry inside the "New" submenu.
const AGENT_NAME = "mock-acp";

// Context menus render as fixed-position panels; this matches both the main menu
// and any open submenu while avoiding dialogs (which use different classes).
const MENU = ".fixed.z-50.bg-slate-800.shadow-xl";

testWithCleanup.describe("Group Context Menu - New submenu", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Ensure the project-alpha workspace exists so its folder group renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation in project-alpha so the folder group header appears.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Submenu Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();

    // Folder grouping is the default for the Conversations tab.
    await helpers.navigateAndWait(page);
  });

  // Opens the project-alpha group context menu and returns its locator.
  async function openGroupMenu(page, timeouts) {
    // Sticky group/folder headers carry data-has-context-menu; session items do
    // not use the sticky class, so this targets the folder header specifically.
    const folderHeader = page
      .locator('div.sticky.top-0[data-has-context-menu="true"]')
      .filter({ hasText: "project-alpha" })
      .first();
    await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });
    await folderHeader.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
    return menu;
  }

  testWithCleanup(
    "shows a New item whose agent submenu stays collapsed until hovered",
    async ({ page, timeouts }) => {
      await openGroupMenu(page, timeouts);

      // Standard items remain present alongside the new "New" entry.
      const menuButtons = page.locator(`${MENU} button`);
      await expect(
        menuButtons.filter({ hasText: "Configure Workspace" }),
      ).toBeVisible();
      const newItem = menuButtons.filter({ hasText: "New" });
      await expect(newItem).toBeVisible();

      // "New" must be the first entry, ahead of the workspace actions. The
      // submenu is not rendered until hover, so these are the top-level items.
      const labels = await menuButtons.allTextContents();
      const newIdx = labels.findIndex((t) => t.includes("New"));
      const configureIdx = labels.findIndex((t) =>
        t.includes("Configure Workspace"),
      );
      expect(newIdx).toBe(0);
      expect(newIdx).toBeLessThan(configureIdx);

      // The agent entry lives in a submenu that is not rendered until hover.
      const agentItem = menuButtons.filter({ hasText: AGENT_NAME });
      await expect(agentItem).toHaveCount(0);

      // Hovering "New" expands the submenu and reveals the agent entry.
      await newItem.hover();
      await expect(agentItem).toBeVisible({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "creates a new conversation when an agent submenu entry is clicked",
    async ({ page, request, apiUrl, timeouts }) => {
      await openGroupMenu(page, timeouts);

      const menuButtons = page.locator(`${MENU} button`);
      await menuButtons.filter({ hasText: "New" }).hover();

      const agentItem = menuButtons.filter({ hasText: AGENT_NAME });
      await expect(agentItem).toBeVisible({ timeout: timeouts.shortAction });

      // Record the conversation count before triggering creation.
      const beforeResp = await request.get(apiUrl("/api/sessions"));
      const before = await beforeResp.json();
      const beforeCount = Array.isArray(before) ? before.length : 0;

      await agentItem.click();

      // The submenu action should create exactly one new conversation.
      await expect
        .poll(
          async () => {
            const resp = await request.get(apiUrl("/api/sessions"));
            const list = await resp.json();
            return Array.isArray(list) ? list.length : 0;
          },
          { timeout: timeouts.appReady },
        )
        .toBe(beforeCount + 1);

      // The new conversation in project-alpha should be selectable/usable.
      await expect(page.locator(MENU)).toHaveCount(0);
    },
  );
});
