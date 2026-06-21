import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Mobile outside-click dismissal for BOTH side panels (mitto-cdf follow-up).
 *
 * On phone-sized viewports the dimming .drawer-overlay backdrop is display:none
 * for both the left conversations sidebar (.sidebar-shell) and the right
 * properties panel (.drawer-dock): a full-area composited overlay dropped the
 * conversation's GPU backing store on pointer-move. Each panel therefore detects
 * outside clicks with a document `mousedown` listener instead of a DOM backdrop.
 *
 * These tests lock in that an outside click (NOT the X button) dismisses each
 * panel:
 *   - Left sidebar:  clicking the conversation peek to the panel's RIGHT.
 *   - Right panel:   clicking the conversation peek to the panel's LEFT.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// The mobile sidebar drawer container (z-40). Modal dialogs use z-50, so this is
// unambiguous. Its open state is reflected by the #sidebar-drawer checkbox.
const SIDEBAR_OVERLAY = ".drawer-side.z-40";
const SIDEBAR_TOGGLE = "#sidebar-drawer";
// The right properties panel is the dock-mode Drawer with this testid.
const SESSION_PANEL = '[data-testid="session-panel"]';
const MOBILE_VIEWPORT = { width: 390, height: 844 };
const SEED_NAME_PREFIX = "Outside Click Seed";

testWithCleanup.describe("Side panels - mobile outside-click dismissal", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Ensure the project-alpha workspace exists and seed a conversation so the
    // sidebar (and a selectable conversation) is available.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `${SEED_NAME_PREFIX} ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
    // Mobile breakpoint: the hamburger is md:hidden and the overlay backdrops are
    // suppressed, so the document-listener dismissal path is the one under test.
    await page.setViewportSize(MOBILE_VIEWPORT);
  });

  // Opens the mobile sidebar via the header hamburger and returns its container.
  async function openSidebar(page, timeouts) {
    const hamburger = page.locator('button[aria-label="Show conversations"]');
    await expect(hamburger).toBeVisible({ timeout: timeouts.appReady });
    await hamburger.click();

    const overlay = page.locator(SIDEBAR_OVERLAY);
    await expect(overlay).toBeVisible({ timeout: timeouts.shortAction });
    await expect(page.locator(SIDEBAR_TOGGLE)).toBeChecked();
    return overlay;
  }

  testWithCleanup(
    "left sidebar: clicking outside the panel closes it",
    async ({ page, timeouts }) => {
      await openSidebar(page, timeouts);

      // Click the conversation peek to the RIGHT of the sidebar panel (outside
      // .drawer-side). The X button is deliberately NOT used: this exercises the
      // document mousedown listener that replaces the suppressed backdrop.
      await page.mouse.click(
        MOBILE_VIEWPORT.width - 6,
        MOBILE_VIEWPORT.height / 2,
      );

      await expect(page.locator(SIDEBAR_TOGGLE)).not.toBeChecked();
      await expect(page.locator(SIDEBAR_OVERLAY)).not.toBeVisible();
    },
  );

  testWithCleanup(
    "right properties panel: clicking outside the panel closes it",
    async ({ page, timeouts }) => {
      const overlay = await openSidebar(page, timeouts);

      // Select the seeded conversation: this closes the sidebar and shows the
      // conversation view (so the header title can open the properties panel).
      const conversation = overlay
        .locator("div[data-session-id]")
        .filter({ hasText: SEED_NAME_PREFIX })
        .first();
      await expect(conversation).toBeVisible({ timeout: timeouts.shortAction });
      await conversation.click();
      await expect(page.locator(SIDEBAR_TOGGLE)).not.toBeChecked();

      // Open the properties panel via the header title ("Click to view
      // properties"). It is the first level-1 heading (conversation view).
      const title = page.getByRole("heading", { level: 1 }).first();
      await expect(title).toBeVisible({ timeout: timeouts.appReady });
      await title.click();

      const panel = page.locator(SESSION_PANEL);
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // The dock panel occupies the right ~85vw; click the conversation peek to
      // its LEFT (outside .drawer-dock). The X button is deliberately NOT used.
      await page.mouse.click(6, MOBILE_VIEWPORT.height / 2);

      await expect(panel).not.toBeVisible({ timeout: timeouts.shortAction });
    },
  );
});
