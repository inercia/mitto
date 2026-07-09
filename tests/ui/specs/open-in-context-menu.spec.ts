import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * "Open In" folder context-menu tests (mitto-bbi.5).
 *
 * Verifies the collapsed folder context-menu "Open ▸" submenu built from
 * ui.mac.open_in.targets: only entries with enabled === true appear, ordering
 * matches the fixture, and clicking one fires the /api/badge-click endpoint
 * with the {workspace_path, action:"open", target_id} envelope wired in .4.
 *
 * Mirrors the workspace bootstrap and locator conventions of
 * group-context-menu.spec.ts. Uses `.menu.fixed.shadow-xl` (not `…z-50…`)
 * because ContextMenu.js switched to inline `z-index: 9999` in a later
 * refactor; the old `z-50` class is no longer present in the DOM.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);

const AGENT_NAME = "mock-acp";

// Matches both the top-level context menu <ul> and any open submenu <ul>
// rendered by ContextMenu.js (shared "menu bg-base-200 rounded-box shadow-xl
// fixed …" class list).
const MENU = ".menu.fixed.shadow-xl";

// Fixture: two enabled + one disabled entry. Used to prove the submenu
// filters by enabled === true and preserves the configured order.
const FIXTURE_OPEN_IN_TARGETS = [
  {
    id: "finder",
    label: "Finder",
    icon: "finder",
    command: "open -a Finder ${MITTO_WORKING_DIR}",
    enabled: true,
    builtin: true,
  },
  {
    id: "vscode",
    label: "Visual Studio Code",
    icon: "vscode",
    command: 'open -a "Visual Studio Code" ${MITTO_WORKING_DIR}',
    enabled: true,
    builtin: true,
  },
  {
    id: "cursor",
    label: "Cursor",
    icon: "cursor",
    command: "open -a Cursor ${MITTO_WORKING_DIR}",
    enabled: false,
    builtin: true,
  },
];

// Patch the running server's global config with the Open In fixture via a
// GET-then-POST round trip on /api/config. The web section is intentionally
// omitted (it is a pointer on ConfigSaveRequest, so nil == "preserve existing
// web/auth"); the sanitized password from GET would otherwise clobber auth.
async function patchOpenInTargets(request, apiUrl, targets) {
  const getResp = await request.get(apiUrl("/api/config"));
  expect(getResp.ok()).toBeTruthy();
  const cfg = await getResp.json();

  const uiBase = cfg.ui || {};
  const macBase = uiBase.mac || {};
  const patchedUI = {
    ...uiBase,
    mac: { ...macBase, open_in: { targets } },
  };

  const body: Record<string, unknown> = {
    workspaces: cfg.workspaces || [],
    acp_servers: cfg.acp_servers || [],
    prompts: cfg.prompts || [],
    ui: patchedUI,
  };
  if (cfg.conversations) body.conversations = cfg.conversations;
  if (cfg.session) body.session = cfg.session;
  if (cfg.permissions) body.permissions = cfg.permissions;
  if (Array.isArray(cfg.models) && cfg.models.length > 0) {
    body.models = cfg.models;
  }

  const postResp = await request.post(apiUrl("/api/config"), { data: body });
  expect(postResp.ok()).toBeTruthy();
}

// Simulate the native macOS app so isNativeApp() (utils/native.js) returns
// true. isNativeApp() probes typeof window.mittoPickFolder === "function", so
// defining the property before page load flips the check.
//
// This is applied per-test (NOT in beforeEach) because the last test in the
// describe block verifies the non-native path and must navigate WITHOUT the
// stub — otherwise addInitScript in beforeEach would run on every navigation
// including the non-native case.
async function stubNativeApp(page) {
  await page.addInitScript(() => {
    Object.defineProperty(window, "mittoPickFolder", {
      configurable: true,
      get: () => () => {},
      set: () => {},
    });
  });
}

testWithCleanup.describe("Open In folder context-menu (mitto-bbi.5)", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl }) => {
    // Ensure the project-alpha workspace exists so its folder header renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation so the folder group has visible content.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Open In Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();

    // Install the Open In fixture BEFORE the initial page load so the
    // frontend's fetchConfig() on mount reads the patched targets. Individual
    // tests handle the native-app stub and navigation themselves.
    await patchOpenInTargets(request, apiUrl, FIXTURE_OPEN_IN_TARGETS);
  });

  async function openGroupMenu(page, timeouts) {
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
    "shows only enabled Open targets in configured order, hides disabled Cursor",
    async ({ page, timeouts, helpers }) => {
      await stubNativeApp(page);
      await helpers.navigateAndWait(page);

      const menu = await openGroupMenu(page, timeouts);

      // "Open" is the collapsed submenu entry (mitto-bbi.4). It must be
      // present because the fixture has at least one enabled target.
      const menuButtons = menu.locator("button");
      const openItem = menuButtons.filter({ hasText: /^Open$/ });
      await expect(openItem).toBeVisible({ timeout: timeouts.shortAction });

      // Submenu is hover-only; before hover it must not be in the DOM.
      const allButtons = page.locator(`${MENU} button`);
      await expect(allButtons.filter({ hasText: "Finder" })).toHaveCount(0);

      await openItem.hover();

      // Wait for the submenu (second matching <ul>) to render.
      await expect(page.locator(MENU)).toHaveCount(2, {
        timeout: timeouts.shortAction,
      });
      const submenu = page.locator(MENU).nth(1);
      await expect(submenu).toBeVisible();

      // The submenu must contain exactly Finder and Visual Studio Code, in
      // that order, and must NOT contain the disabled Cursor entry.
      const subButtons = submenu.locator("button");
      await expect(subButtons).toHaveCount(2, { timeout: timeouts.shortAction });

      const labels = (await subButtons.allTextContents()).map((t) => t.trim());
      expect(labels).toEqual(["Finder", "Visual Studio Code"]);
      expect(labels).not.toContain("Cursor");
    },
  );

  testWithCleanup(
    "clicking Finder POSTs {action:'open', target_id:'finder'} to /api/badge-click",
    async ({ page, timeouts, apiUrl, helpers }) => {
      await stubNativeApp(page);
      await helpers.navigateAndWait(page);

      // apiUrl returns the prefixed path (e.g. "/mitto/api/badge-click"); the
      // browser resolves that to an absolute URL, so match by pathname suffix.
      const badgeClickPath = apiUrl("/api/badge-click");
      const isBadgeClickURL = (url: string) => {
        try {
          return new URL(url).pathname === badgeClickPath;
        } catch {
          return url.endsWith(badgeClickPath);
        }
      };

      // Install the route BEFORE any click so we cannot lose the request to a
      // race between the button-click handler and page.route() registration.
      await page.route(
        (u) => isBadgeClickURL(u.toString()),
        (route) =>
          route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true }),
          }),
      );

      const menu = await openGroupMenu(page, timeouts);

      await menu
        .locator("button")
        .filter({ hasText: /^Open$/ })
        .hover();

      const submenu = page.locator(MENU).nth(1);
      await expect(submenu).toBeVisible({ timeout: timeouts.shortAction });

      const finderBtn = submenu
        .locator("button")
        .filter({ hasText: /^Finder$/ });
      await expect(finderBtn).toBeVisible({ timeout: timeouts.shortAction });

      const [req] = await Promise.all([
        page.waitForRequest(
          (r) => r.method() === "POST" && isBadgeClickURL(r.url()),
          { timeout: timeouts.appReady },
        ),
        finderBtn.click(),
      ]);

      const body = JSON.parse(req.postData() || "{}");
      expect(body).toMatchObject({
        workspace_path: WORKSPACE_ALPHA,
        action: "open",
        target_id: "finder",
      });

      // The context menu closes after a submenu item is clicked.
      await expect(page.locator(MENU)).toHaveCount(0, {
        timeout: timeouts.shortAction,
      });
    },
  );

  testWithCleanup(
    "hides the Open submenu entirely in a non-native (browser) context (mitto-k0l)",
    async ({ page, timeouts, helpers }) => {
      // NO stubNativeApp() here: window.mittoPickFolder stays undefined so
      // isNativeApp() returns false, and SessionList.js must omit the entire
      // "Open ▸" submenu block from the folder context menu.
      await helpers.navigateAndWait(page);

      const menu = await openGroupMenu(page, timeouts);

      // The rest of the folder context menu (Rename, Delete, ...) still
      // renders, but the "Open" collapsed entry must be absent.
      const openItem = menu.locator("button").filter({ hasText: /^Open$/ });
      await expect(openItem).toHaveCount(0);
    },
  );
});
