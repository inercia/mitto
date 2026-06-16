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

// Context menus render as fixed-position daisyUI menus; this matches both the
// main menu and any open submenu while avoiding dialogs (which use different
// classes). The menu chrome is the daisyUI `menu` component on a fixed-position
// <ul> (bg-base-200 rounded-box shadow-xl fixed z-50).
const MENU = ".menu.fixed.z-50.shadow-xl";

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
    // Folder headers are daisyUI <summary> rows carrying data-has-context-menu;
    // session items do not carry it, so this targets the folder header specifically.
    const folderHeader = page
      .locator('summary[data-has-context-menu="true"]')
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

  testWithCleanup(
    "moves a folder to a new group via the 'Move to group' submenu",
    async ({ page, request, apiUrl, timeouts }) => {
      await openGroupMenu(page, timeouts);

      const menuButtons = page.locator(`${MENU} button`);
      const moveItem = menuButtons.filter({ hasText: "Move to group" });
      await expect(moveItem).toBeVisible({ timeout: timeouts.shortAction });

      // The submenu (existing groups + "New group…") is not rendered until hover.
      await moveItem.hover();
      const newGroupItem = menuButtons.filter({ hasText: "New group" });
      await expect(newGroupItem).toBeVisible({ timeout: timeouts.shortAction });
      await newGroupItem.click();

      // The new-group dialog opens and the context menu closes.
      const dialog = page.locator('[data-testid="new-group-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });
      await expect(page.locator(MENU)).toHaveCount(0);

      await page
        .locator('[data-testid="new-group-name-input"]')
        .fill("Operations");
      await page.locator('[data-testid="new-group-create-btn"]').click();

      // The folder's group is persisted (reflected by the config API).
      await expect
        .poll(
          async () => {
            const resp = await request.get(apiUrl("/api/config"));
            const cfg = await resp.json();
            const wss = Array.isArray(cfg.workspaces) ? cfg.workspaces : [];
            const ws = wss.find((w) => w.working_dir === WORKSPACE_ALPHA);
            return (ws && ws.group) || "";
          },
          { timeout: timeouts.appReady },
        )
        .toBe("Operations");

      // The sidebar regroups: an "Operations" group section header appears.
      await expect(
        page
          .locator(".folder-group-section")
          .filter({ hasText: "Operations" }),
      ).toBeVisible({ timeout: timeouts.appReady });
    },
  );

  testWithCleanup(
    "keeps the 'Move to group' flyout anchored to the right when reopened",
    async ({ page, timeouts }) => {
      // Regression: reopening the flyout on the same anchor used to leave it
      // parked at the viewport's left edge. The reposition effect parks the
      // flyout at left:8px to measure its true width, then computes the final
      // position. On the second open the computed value equalled the value
      // persisted from the first open, so the setState was a no-op, Preact
      // bailed out of re-rendering, and the imperatively-parked left:8px was
      // never overwritten — stranding the flyout far from its parent item.
      await openGroupMenu(page, timeouts);

      const menuButtons = page.locator(`${MENU} button`);
      const moveItem = menuButtons.filter({ hasText: "Move to group" });
      await expect(moveItem).toBeVisible({ timeout: timeouts.shortAction });
      const newGroupItem = menuButtons.filter({ hasText: "New group" });

      // Reads the open flyout <ul> and its parent <li.relative> anchor geometry.
      const readGeom = () =>
        page.evaluate(() => {
          const menus = Array.from(
            document.querySelectorAll(".menu.fixed.z-50.shadow-xl"),
          );
          const sub = menus.find((m) => m.closest("li.relative"));
          const li = sub ? sub.closest("li.relative") : null;
          if (!sub || !li) return null;
          const s = sub.getBoundingClientRect();
          const a = li.getBoundingClientRect();
          return { subLeft: s.left, subRight: s.right, anchorLeft: a.left };
        });

      // First hover: the flyout should open to the RIGHT of its anchor item,
      // not collapse toward the left edge.
      await moveItem.hover();
      await expect(newGroupItem).toBeVisible({ timeout: timeouts.shortAction });
      await page.waitForTimeout(150);
      const first = await readGeom();
      expect(first).not.toBeNull();
      expect(first.subLeft).toBeGreaterThan(first.anchorLeft);

      // Move away so the flyout closes, then reopen on the SAME anchor.
      await page.mouse.move(5, 5);
      await expect(newGroupItem).toBeHidden({ timeout: timeouts.shortAction });
      await page.waitForTimeout(100);

      await moveItem.hover();
      await expect(newGroupItem).toBeVisible({ timeout: timeouts.shortAction });
      await page.waitForTimeout(150);
      const second = await readGeom();
      expect(second).not.toBeNull();
      // The flyout must re-anchor to the right exactly like the first open and
      // must NOT be stranded at the parked left edge (~8px).
      expect(second.subLeft).toBeGreaterThan(second.anchorLeft);
      expect(Math.abs(second.subLeft - first.subLeft)).toBeLessThan(2);
    },
  );

  testWithCleanup(
    "keeps 'Move to group' available on external connections while gating Configure Workspace",
    async ({ page, helpers, timeouts }) => {
      // Simulate an external (e.g. iPhone over the network) connection. The
      // server injects window.mittoIsExternal = true for the external listener,
      // which makes the frontend treat the config as read-only. The inline HTML
      // script assigns the value during parsing, so define it as a locked getter
      // (registered before any page script) that swallows that assignment.
      await page.addInitScript(() => {
        Object.defineProperty(window, "mittoIsExternal", {
          configurable: true,
          get: () => true,
          set: () => {},
        });
      });

      // External connections normally load some vendor libs from jsdelivr; block
      // the CDN so the loader falls back to the bundled local files immediately
      // (keeps the test fast and offline-safe).
      await page.route("**://cdn.jsdelivr.net/**", (route) => route.abort());

      await helpers.navigateAndWait(page);

      const menu = await openGroupMenu(page, timeouts);
      const menuButtons = menu.locator("button");

      // The fix: folder grouping is local organizational metadata, so it stays
      // available even when configReadonly is true (external connection).
      await expect(
        menuButtons.filter({ hasText: "Move to group" }),
      ).toBeVisible({ timeout: timeouts.shortAction });

      // Guard: genuinely host-sensitive items remain gated, confirming
      // configReadonly is actually active in this scenario.
      await expect(
        menuButtons.filter({ hasText: "Configure Workspace" }),
      ).toHaveCount(0);
    },
  );
});
