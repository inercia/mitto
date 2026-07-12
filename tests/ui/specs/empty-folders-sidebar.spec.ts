import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Empty-folder (pinned) sidebar tests (mitto-8nv).
 *
 * Verifies:
 *  - A pinned workspace with no sessions still appears as a sidebar folder.
 *  - The "Add folder to sidebar" toolbar dialog lists hidden workspaces and
 *    pinning one from the dialog surfaces it in the sidebar.
 *  - The folder context menu offers "Remove from sidebar" on pinned empty
 *    folders and unpinning removes them (and returns them to the hidden pool).
 *  - "Remove from sidebar" is NOT offered on session-derived folders.
 *
 * NOTE: The first three tests are marked `.fixme` until mitto-662 lands —
 * writing folders.json via PUT /api/folders/pin does not re-project the
 * pinned flag onto in-memory workspaces (SyncConfigWorkspaces skips
 * ApplyFolderDefaults), so /api/workspaces still returns `pinned` absent
 * even after a successful PUT. Unit tests miss this because they reload
 * from disk. Un-fixme once mitto-662 is fixed. The fourth test (session-
 * derived folder — no pinning involved) runs today.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const WORKSPACE_BETA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-beta",
);

const AGENT_NAME = "mock-acp";
// The context menu renders (via Portal) as a fixed-position daisyUI menu; the
// z-index is applied as an inline style, not a Tailwind class, so we match on
// the stable class combination `menu.fixed.shadow-xl` only.
const MENU = ".menu.fixed.shadow-xl";

testWithCleanup.describe("Empty pinned folders in sidebar", () => {
  testWithCleanup.beforeEach(async ({ request, apiUrl }) => {
    // Best-effort reset: unpin both fixture workspaces so each test starts
    // from a known hidden state. Ignore failures (workspace may not exist
    // yet on this run — the endpoint validates dir membership).
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_BETA },
    });
    await request.put(
      apiUrl(`/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_ALPHA)}`),
      { data: { pinned: false } },
    );
    await request.put(
      apiUrl(`/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_BETA)}`),
      { data: { pinned: false } },
    );
  });

  testWithCleanup.fixme(
    "pinned empty workspace appears as a sidebar folder with a Tasks node and no conversations (blocked by mitto-662)",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      // Pin beta with no session in it.
      const pinResp = await request.put(
        apiUrl(
          `/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_BETA)}`,
        ),
        { data: { pinned: true } },
      );
      expect(pinResp.status()).toBe(200);

      await helpers.navigateAndWait(page);

      const folderHeader = page
        .locator('summary[data-has-context-menu="true"]')
        .filter({ hasText: "project-beta" })
        .first();
      await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });

      // Folders default to open; the enclosing <details> element wraps the
      // header and children. Ensure it's expanded (safety net) so the Tasks
      // node is in the rendered tree.
      const details = folderHeader.locator("xpath=ancestor::details[1]");
      const isOpen = await details.getAttribute("open");
      if (isOpen === null) {
        await folderHeader.click();
      }

      // The Tasks node lives inside the folder's <details>; scope by workingDir
      // via the id attribute the tasksNode carries (`tasks:<workingDir>`).
      const tasksNode = details.locator(
        `[id="tasks:${WORKSPACE_BETA}"], [data-testid="tasks:${WORKSPACE_BETA}"]`,
      );
      // Fallback: if neither id nor testid attributes exist on that node, the
      // visible "Tasks" text inside this folder's details is still the correct
      // locator. Match either — whichever the current UI renders.
      const tasksAny = tasksNode.or(
        details.getByText("Tasks", { exact: true }),
      );
      await expect(tasksAny.first()).toBeVisible({
        timeout: timeouts.shortAction,
      });
    },
  );

  testWithCleanup.fixme(
    "Add folder button opens dialog listing hidden workspaces; picking one pins it (blocked by mitto-662)",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      // Alpha becomes a session-derived (non-hidden) folder.
      const createResp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `Empty-Folder Alpha ${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(createResp.ok()).toBeTruthy();

      // Beta stays hidden (workspace configured in beforeEach; no session, not pinned).
      await helpers.navigateAndWait(page);

      // Beta must NOT be visible in the sidebar yet.
      await expect(
        page
          .locator('summary[data-has-context-menu="true"]')
          .filter({ hasText: "project-beta" }),
      ).toHaveCount(0);

      // Open the "Add folder to sidebar" dialog.
      await page.locator('[data-testid="add-folder-btn"]').click();
      const dialog = page.locator('[data-testid="add-folder-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

      const hiddenList = page.locator('[data-testid="add-folder-hidden-list"]');
      await expect(hiddenList).toBeVisible();

      const pickBeta = page.locator(
        `[data-testid="add-folder-pick-${WORKSPACE_BETA}"]`,
      );
      await expect(pickBeta).toBeVisible({ timeout: timeouts.shortAction });
      await pickBeta.click();

      // Dialog closes after pick.
      await expect(dialog).toHaveCount(0, { timeout: timeouts.shortAction });

      // Beta now appears in the sidebar as a folder header.
      await expect(
        page
          .locator('summary[data-has-context-menu="true"]')
          .filter({ hasText: "project-beta" })
          .first(),
      ).toBeVisible({ timeout: timeouts.appReady });

      // Pin state is persisted server-side.
      const getResp = await request.get(
        apiUrl(
          `/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_BETA)}`,
        ),
      );
      expect(getResp.status()).toBe(200);
      expect(await getResp.json()).toEqual({ pinned: true });
    },
  );

  testWithCleanup.fixme(
    "Remove from sidebar context-menu entry unpins a folder and removes it from the sidebar (blocked by mitto-662)",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      // Pin beta so the folder header exists (no sessions).
      const pinResp = await request.put(
        apiUrl(
          `/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_BETA)}`,
        ),
        { data: { pinned: true } },
      );
      expect(pinResp.status()).toBe(200);

      await helpers.navigateAndWait(page);

      const folderHeader = page
        .locator('summary[data-has-context-menu="true"]')
        .filter({ hasText: "project-beta" })
        .first();
      await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });

      await folderHeader.click({ button: "right" });
      const menu = page.locator(MENU).first();
      await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

      const removeEntry = menu
        .locator("button")
        .filter({ hasText: "Remove from sidebar" });
      await expect(removeEntry).toBeVisible({ timeout: timeouts.shortAction });
      await removeEntry.click();

      // Folder disappears from the sidebar.
      await expect(
        page
          .locator('summary[data-has-context-menu="true"]')
          .filter({ hasText: "project-beta" }),
      ).toHaveCount(0, { timeout: timeouts.appReady });

      // Pin state is persisted server-side (false).
      const getResp = await request.get(
        apiUrl(
          `/api/folders/pin?working_dir=${encodeURIComponent(WORKSPACE_BETA)}`,
        ),
      );
      expect(getResp.status()).toBe(200);
      expect(await getResp.json()).toEqual({ pinned: false });

      // Beta is back in the "hidden workspaces" pool exposed by Add folder.
      await page.locator('[data-testid="add-folder-btn"]').click();
      await expect(
        page.locator('[data-testid="add-folder-dialog"]'),
      ).toBeVisible({ timeout: timeouts.shortAction });
      await expect(
        page.locator(`[data-testid="add-folder-pick-${WORKSPACE_BETA}"]`),
      ).toBeVisible({ timeout: timeouts.shortAction });
      await page.locator('[data-testid="add-folder-cancel-btn"]').click();
      await expect(
        page.locator('[data-testid="add-folder-dialog"]'),
      ).toHaveCount(0, { timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "Remove from sidebar entry is absent on a folder that has active sessions",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      const createResp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `Session-Derived Alpha ${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(createResp.ok()).toBeTruthy();

      await helpers.navigateAndWait(page);

      const folderHeader = page
        .locator('summary[data-has-context-menu="true"]')
        .filter({ hasText: "project-alpha" })
        .first();
      await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });

      await folderHeader.click({ button: "right" });
      const menu = page.locator(MENU).first();
      await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

      // Guards the "session-derived folder is authoritative" invariant: the
      // "Remove from sidebar" entry only surfaces on pinned empty folders.
      await expect(
        menu.locator("button").filter({ hasText: "Remove from sidebar" }),
      ).toHaveCount(0);

      await page.keyboard.press("Escape");
      await expect(page.locator(MENU)).toHaveCount(0, {
        timeout: timeouts.shortAction,
      });
    },
  );
});
