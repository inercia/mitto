import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import fs from "fs";
import os from "os";
import path from "path";

/**
 * "Add folder" dialog behavior when many hidden workspaces exist.
 *
 * Verifies the Portal-based compact dropdown introduced on AddFolderDialog.js:
 *  - The top-3 hidden workspaces render in the flat quick-list.
 *  - The "Show N more folders…" toggle opens a floating menu (Portal at
 *    document.body) sized to the toggle button, with `max-h-80` scrolling.
 *  - Clicking an entry inside the dropdown pins that workspace and closes the
 *    dialog (server-side pin state is persisted).
 *  - Escape closes only the dropdown, leaving the dialog open (capture-mode
 *    keydown handler with stopPropagation()).
 */

const AGENT_NAME = "mock-acp";
const TOTAL_WORKSPACES = 16;
const TOP_LIST_SIZE = 3;

async function unpinAll(request: any, apiUrl: any, dirs: string[]) {
  for (const dir of dirs) {
    await request.put(
      apiUrl(`/api/folders/pin?working_dir=${encodeURIComponent(dir)}`),
      { data: { pinned: false } },
    );
  }
}

testWithCleanup.describe("Add folder dropdown (many hidden workspaces)", () => {
  let tmpDirs: string[] = [];

  testWithCleanup.beforeEach(async ({ request, apiUrl }) => {
    tmpDirs = [];
    const base = os.tmpdir();
    for (let i = 0; i < TOTAL_WORKSPACES; i++) {
      const dir = path.join(base, `mitto-add-folder-repro-${i}`);
      fs.mkdirSync(dir, { recursive: true });
      tmpDirs.push(dir);
      // POST /api/workspaces; retry once with a short backoff if we hit the
      // scanner-defense rate limit (429). Accept 201/409 as terminal states.
      let resp = await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: AGENT_NAME, working_dir: dir },
      });
      if (resp.status() === 429) {
        await new Promise((r) => setTimeout(r, 250));
        resp = await request.post(apiUrl("/api/workspaces"), {
          data: { acp_server: AGENT_NAME, working_dir: dir },
        });
      }
      expect([201, 409]).toContain(resp.status());
    }
    await unpinAll(request, apiUrl, tmpDirs);
  });

  testWithCleanup.afterEach(async ({ request, apiUrl }) => {
    for (const dir of tmpDirs) {
      await request.delete(
        apiUrl(`/api/workspaces?working_dir=${encodeURIComponent(dir)}`),
      );
      try {
        fs.rmSync(dir, { recursive: true, force: true });
      } catch {
        // best-effort cleanup
      }
    }
    tmpDirs = [];
  });

  testWithCleanup(
    "dropdown reveals extra workspaces beyond the top-3",
    async ({ page, helpers, timeouts }) => {
      await helpers.navigateAndWait(page);

      await page.locator('[data-testid="add-folder-btn"]').click();
      const dialog = page.locator('[data-testid="add-folder-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

      // Top-3 flat list.
      const hiddenList = page.locator(
        '[data-testid="add-folder-hidden-list"]',
      );
      await expect(hiddenList).toBeVisible();
      const topPicks = hiddenList.locator(
        '[data-testid^="add-folder-pick-"]',
      );
      await expect(topPicks).toHaveCount(TOP_LIST_SIZE);

      // Toggle is present, list is NOT yet rendered.
      const toggle = page.locator('[data-testid="add-folder-more-toggle"]');
      await expect(toggle).toBeVisible();
      await expect(toggle).toContainText("Show ");
      await expect(toggle).toContainText("more folders");
      await expect(
        page.locator('[data-testid="add-folder-more-list"]'),
      ).toHaveCount(0);

      // Open dropdown (Portal-mounted at document.body).
      await toggle.click();
      const moreList = page.locator('[data-testid="add-folder-more-list"]');
      await expect(moreList).toBeVisible({ timeout: timeouts.shortAction });

      // Contains at least (TOTAL - TOP_LIST_SIZE) entries. The environment may
      // carry pre-existing hidden workspaces (e.g. from other suites), so we
      // assert a lower bound and check every tmp workspace is visible somewhere
      // (top-3 flat list OR the dropdown).
      const morePicks = moreList.locator(
        '[data-testid^="add-folder-pick-"]',
      );
      const moreCount = await morePicks.count();
      expect(moreCount).toBeGreaterThanOrEqual(
        TOTAL_WORKSPACES - TOP_LIST_SIZE,
      );
      for (const dir of tmpDirs) {
        await expect(
          page.locator(`[data-testid="add-folder-pick-${dir}"]`),
        ).toHaveCount(1);
      }

      // Width ≈ toggle button width (± 2px). Verifies the "sized to toggle"
      // Portal placement (inline style width = toggle rect width).
      const toggleBox = await toggle.boundingBox();
      const menuBox = await moreList.boundingBox();
      expect(toggleBox).not.toBeNull();
      expect(menuBox).not.toBeNull();
      expect(Math.abs(menuBox!.width - toggleBox!.width)).toBeLessThanOrEqual(2);

      // Height cap: `max-h-96` = 24rem = 384px, plus a couple of px of padding.
      // `max-h-96` IS present in the committed precompiled tailwind.css, so the
      // clamp is effective.
      expect(menuBox!.height).toBeLessThanOrEqual(400);

      await page.screenshot({
        path: "test-results/add-folder-dropdown-open.png",
        fullPage: false,
      });
    },
  );

  testWithCleanup(
    "clicking an entry in the dropdown pins that workspace",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      await helpers.navigateAndWait(page);

      await page.locator('[data-testid="add-folder-btn"]').click();
      const dialog = page.locator('[data-testid="add-folder-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

      await page.locator('[data-testid="add-folder-more-toggle"]').click();
      const moreList = page.locator('[data-testid="add-folder-more-list"]');
      await expect(moreList).toBeVisible({ timeout: timeouts.shortAction });

      // Pick an entry from the dropdown — a workspace whose working_dir is
      // known to fall outside the top-3 (choose one from the tail so it is
      // unambiguously in the dropdown). We do not depend on MRU ordering:
      // we just find any pick-* inside moreList and click the first one.
      const picks = moreList.locator('[data-testid^="add-folder-pick-"]');
      await expect(picks.first()).toBeVisible();
      const pickedTestId = await picks.first().getAttribute("data-testid");
      expect(pickedTestId).toBeTruthy();
      const pickedDir = pickedTestId!.replace(/^add-folder-pick-/, "");
      await picks.first().click();

      // Dialog closes.
      await expect(dialog).toHaveCount(0, { timeout: timeouts.shortAction });

      // Server-side pin state confirmed.
      const getResp = await request.get(
        apiUrl(
          `/api/folders/pin?working_dir=${encodeURIComponent(pickedDir)}`,
        ),
      );
      expect(getResp.status()).toBe(200);
      expect(await getResp.json()).toEqual({ pinned: true });
    },
  );

  testWithCleanup(
    "Escape closes only the dropdown, not the dialog",
    async ({ page, helpers, timeouts }) => {
      await helpers.navigateAndWait(page);

      await page.locator('[data-testid="add-folder-btn"]').click();
      const dialog = page.locator('[data-testid="add-folder-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

      await page.locator('[data-testid="add-folder-more-toggle"]').click();
      const moreList = page.locator('[data-testid="add-folder-more-list"]');
      await expect(moreList).toBeVisible({ timeout: timeouts.shortAction });

      await page.keyboard.press("Escape");

      // Dropdown gone.
      await expect(moreList).toHaveCount(0, {
        timeout: timeouts.shortAction,
      });
      // Dialog still visible (capture-mode stopPropagation()).
      await expect(dialog).toBeVisible();
    },
  );
});
