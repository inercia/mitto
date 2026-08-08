import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Suppressed archive affordances for protected (`no_archive`) conversations
 * (mitto-yvel.6).
 *
 * A conversation created via a prompt with `target.noArchive: true` is marked
 * non-archivable server-side (mitto-yvel.2/.3) and the frontend must suppress
 * every archive affordance for it (mitto-yvel.4):
 *   - The context/kebab menu's "Archive" item is DISABLED (not hidden) with a
 *     "This conversation is protected from archiving" tooltip; "Delete" stays
 *     enabled since deletion is the only way to remove a protected conversation.
 *   - The mobile swipe-left gesture does not reveal the archive affordance at
 *     all — the gesture must not *start*.
 * An unprotected control conversation in the same list must behave normally
 * in both cases, so the test fails loudly if the flag leaks too broadly.
 *
 * Fixture: tests/fixtures/workspaces/project-alpha/.mitto/prompts/no-archive-prompt.prompt.yaml
 *   → name: "No Archive Test", target.noArchive: true, no `menus:` key.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);

// The mock ACP server name configured in tests/ui/global-setup.ts.
const AGENT_NAME = "mock-acp";
const NO_ARCHIVE_PROMPT_NAME = "No Archive Test";
const PROTECTED_REASON = "This conversation is protected from archiving";
const MOBILE_VIEWPORT = { width: 390, height: 844 };

// Context menus render as fixed-position daisyUI menus, matching both the
// main menu and any open submenu (ContextMenu.js's outer <ul> and the
// per-item submenu <ul> both carry these classes; the stacking z-index is
// applied inline rather than via a `z-50` class).
const MENU = ".menu.fixed.shadow-xl";

testWithCleanup.describe("No-archive affordance suppression", () => {
  let protectedId: string;
  let controlId: string;

  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    const ts = Date.now();
    // Protected: created via the no-archive prompt (origin_prompt_name
    // resolves target.noArchive server-side at create time).
    const protectedResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `NoArchive Protected ${ts}`,
        working_dir: WORKSPACE_ALPHA,
        origin_prompt_name: NO_ARCHIVE_PROMPT_NAME,
      },
    });
    expect(protectedResp.ok()).toBeTruthy();
    const protectedCreated = await protectedResp.json();
    protectedId = protectedCreated.session_id || protectedCreated.id;
    expect(protectedId).toBeTruthy();
    expect(protectedCreated.no_archive).toBe(true);

    // Control: plain create, no prompt → no_archive stays false.
    const controlResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `NoArchive Control ${ts}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(controlResp.ok()).toBeTruthy();
    const controlCreated = await controlResp.json();
    controlId = controlCreated.session_id || controlCreated.id;
    expect(controlId).toBeTruthy();
    expect(controlCreated.no_archive).toBeFalsy();

    await helpers.navigateAndWait(page);
  });

  // Guard: the transport seam (GET /api/sessions) must actually carry the
  // flag for both rows, so a UI-only failure below is never ambiguous
  // between "suppression broken" and "flag never set".
  testWithCleanup(
    "GET /api/sessions reports no_archive for the protected session only",
    async ({ request, apiUrl }) => {
      const listResp = await request.get(apiUrl("/api/sessions"));
      expect(listResp.ok()).toBeTruthy();
      const sessions = await listResp.json();
      const protectedRow = sessions.find(
        (s) => s.session_id === protectedId,
      );
      const controlRow = sessions.find((s) => s.session_id === controlId);
      expect(protectedRow?.no_archive).toBe(true);
      expect(controlRow?.no_archive).toBeFalsy();
    },
  );

  async function openSessionMenu(page, timeouts, targetId) {
    const sessionItem = page
      .locator(`[data-session-id="${targetId}"]`)
      .first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
    return menu;
  }

  testWithCleanup(
    "protected row: Archive is disabled with the protection tooltip, Delete stays enabled",
    async ({ page, timeouts }) => {
      await openSessionMenu(page, timeouts, protectedId);
      const menuButtons = page.locator(`${MENU} button`);

      const archiveBtn = menuButtons.filter({ hasText: "Archive" });
      await expect(archiveBtn).toBeVisible({ timeout: timeouts.shortAction });
      await expect(archiveBtn).toBeDisabled();

      const archiveItem = archiveBtn.locator("xpath=ancestor::li[1]");
      await expect(archiveItem).toHaveAttribute("title", PROTECTED_REASON);

      // Deletion remains the only way to remove a protected conversation.
      const deleteBtn = menuButtons.filter({ hasText: "Delete" });
      await expect(deleteBtn).toBeVisible();
      await expect(deleteBtn).toBeEnabled();
    },
  );

  testWithCleanup(
    "control row: Archive is enabled and carries no protection tooltip",
    async ({ page, timeouts }) => {
      await openSessionMenu(page, timeouts, controlId);
      const menuButtons = page.locator(`${MENU} button`);

      const archiveBtn = menuButtons.filter({ hasText: "Archive" });
      await expect(archiveBtn).toBeVisible({ timeout: timeouts.shortAction });
      await expect(archiveBtn).toBeEnabled();

      const archiveItem = archiveBtn.locator("xpath=ancestor::li[1]");
      const title = await archiveItem.getAttribute("title");
      expect(title).not.toBe(PROTECTED_REASON);
    },
  );

  // Opens the mobile sidebar via the header hamburger and returns its
  // container (mirrors side-panels-outside-click-mobile.spec.ts).
  async function openMobileSidebar(page, timeouts) {
    const hamburger = page.locator('button[aria-label="Show conversations"]');
    await expect(hamburger).toBeVisible({ timeout: timeouts.appReady });
    await hamburger.click();
    const overlay = page.locator(".drawer-side.z-40");
    await expect(overlay).toBeVisible({ timeout: timeouts.shortAction });
    return overlay;
  }

  // Drives a left swipe (mouse down/move/up) across a session row. The swipe
  // hook binds pointer (mouse) handlers in addition to touch handlers, so a
  // mouse-driven gesture exercises the same disabled/enabled branch without
  // requiring a touch-enabled Playwright project. `dx` is kept below the
  // row's 50%-width auto-trigger threshold (but above `revealWidth`=80px) so
  // a successful swipe only reveals the affordance rather than firing the
  // archive/delete action as a side effect.
  async function swipeLeft(page, content) {
    const box = await content.boundingBox();
    if (!box) throw new Error("session row has no bounding box");
    const startX = box.x + box.width - 10;
    const y = box.y + box.height / 2;
    await page.mouse.move(startX, y);
    await page.mouse.down();
    for (const dx of [20, 60, 100, 140]) {
      await page.mouse.move(startX - dx, y);
    }
    await page.mouse.up();
  }

  // The archive/delete affordance button lives in a sibling div of the
  // `[data-session-id]` content div (both children of `.session-item-container`;
  // see SessionItem.js), revealed by toggling that sibling's `opacity` style —
  // NOT by mounting/unmounting it. Playwright's `toBeVisible()` ignores
  // `opacity`, so assert the opacity style and the content div's translateX
  // directly rather than element visibility.
  function containerFor(content) {
    return content.locator(
      "xpath=ancestor::div[contains(@class,'session-item-container')][1]",
    );
  }

  testWithCleanup(
    "mobile: swipe-left on the protected row does not reveal the archive affordance",
    async ({ page, timeouts }) => {
      await page.setViewportSize(MOBILE_VIEWPORT);
      const overlay = await openMobileSidebar(page, timeouts);
      const content = overlay
        .locator(`[data-session-id="${protectedId}"]`)
        .first();
      await expect(content).toBeVisible({ timeout: timeouts.shortAction });
      const container = containerFor(content);

      await swipeLeft(page, content);

      // The gesture must never start: no translateX offset, and the archive
      // background stays fully transparent (opacity 0).
      const transform = await content.evaluate((el) => el.style.transform);
      expect(transform === "" || transform === "translateX(0px)").toBe(true);
      const opacity = await container
        .locator("> div")
        .first()
        .evaluate((el) => el.style.opacity);
      expect(opacity).toBe("0");
    },
  );

  testWithCleanup(
    "mobile: swipe-left on the control row reveals the archive affordance",
    async ({ page, timeouts }) => {
      await page.setViewportSize(MOBILE_VIEWPORT);
      const overlay = await openMobileSidebar(page, timeouts);
      const content = overlay
        .locator(`[data-session-id="${controlId}"]`)
        .first();
      await expect(content).toBeVisible({ timeout: timeouts.shortAction });
      const container = containerFor(content);

      await swipeLeft(page, content);

      // Proves the gesture harness itself works: without this, the protected
      // row's "not revealed" assertion above would pass vacuously.
      const transform = await content.evaluate((el) => el.style.transform);
      expect(transform).not.toBe("");
      expect(transform).not.toBe("translateX(0px)");
      const opacity = await container
        .locator("> div")
        .first()
        .evaluate((el) => el.style.opacity);
      expect(opacity).toBe("1");
      await expect(
        container.locator('[aria-label="Archive"]'),
      ).toBeVisible({ timeout: timeouts.shortAction });
    },
  );
});
