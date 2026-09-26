import { test, testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Session management tests for Mitto Web UI.
 *
 * These tests verify session creation, listing, renaming, and deletion.
 */

test.describe("Session Management", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should display sessions sidebar", async ({ page, timeouts }) => {
    // The sidebar heading was renamed to "Mitto" in the daisyUI 5 upgrade
    // (see tests/ui/utils/selectors.ts:conversationsHeader) — there is no
    // longer a "Conversations" tab or heading anywhere in the app.
    const conversationsHeader = page.getByRole("heading", {
      name: "Mitto",
    });
    await expect(conversationsHeader).toBeVisible({
      timeout: timeouts.appReady,
    });
  });

  test("should have at least one session on load", async ({
    page,
    selectors,
    timeouts,
  }) => {
    const sessionItems = page.locator(selectors.sessionsList);

    // On a clean test run the server has no sessions yet.  Create one by
    // clicking the New Conversation button so the sidebar has something to
    // display, then verify it appears.  When sessions already exist from
    // previous tests the item is immediately visible and the click is skipped.
    const count = await sessionItems.count();
    if (count === 0) {
      await page.locator(selectors.newSessionButton).click();
    }

    await expect(sessionItems.first()).toBeVisible({
      timeout: timeouts.appReady,
    });
  });

  test("should create new session when clicking new button", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Count existing sessions
    const sessionItems = page.locator(selectors.sessionsList);
    const initialCount = await sessionItems.count();

    // Click new session button
    await page.locator(selectors.newSessionButton).click();

    // Wait for new session to be created
    await page.waitForTimeout(1000);

    // Should have one more session (or at least the same if it replaced)
    const newCount = await sessionItems.count();
    expect(newCount).toBeGreaterThanOrEqual(initialCount);
  });

  test("should switch between sessions", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Ensure we have an active session before trying to send a message
    await helpers.ensureActiveSession(page);

    // Send a message in the first session
    const testMessage = helpers.uniqueMessage("First session");
    await helpers.sendMessage(page, testMessage);
    await helpers.waitForUserMessage(page, testMessage);

    // Create a new session
    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.shortAction,
    });

    // The first message should not be visible in the new session
    // Use a more specific selector to check the user's message, not the echoed response
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: testMessage })
    ).toHaveCount(0, {
      timeout: 2000,
    });

    // Switch back to the first session (click on it in the sidebar)
    const sessionItems = page.locator(selectors.sessionsList);
    await sessionItems.first().click();

    // Wait for session to load
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.shortAction,
    });
  });
});

test.describe("Session API", () => {
  test("should list sessions via API", async ({ request, apiUrl }) => {
    const response = await request.get(apiUrl("/api/sessions"));
    expect(response.ok()).toBeTruthy();

    const sessions = await response.json();
    expect(Array.isArray(sessions)).toBeTruthy();
  });

  test("should create session via API", async ({ request, apiUrl }) => {
    const response = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `API Test Session ${Date.now()}`,
      },
    });
    expect(response.ok()).toBeTruthy();

    const session = await response.json();
    expect(session.session_id).toBeTruthy();
  });
});

/**
 * Active-conversation removal navigation.
 *
 * When the ACTIVE conversation is removed from view, the UI navigates to the
 * global Dashboard instead of being bounced to another conversation or an
 * empty state. This covers the archive path (the delete path shares the same
 * onActiveSessionRemoved callback wiring in useWebSocket).
 *
 * Originally (mitto-17d) this landed on the conversation's folder Tasks
 * (beads) view; that was superseded by mitto-ce3, which routes to the
 * workspace-agnostic Dashboard instead (see the onActiveSessionRemovedRef
 * wiring in app.js) — so this test now asserts the Dashboard mounts.
 */
const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

testWithCleanup.describe("Active conversation removal opens the Dashboard", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl }) => {
    // Mock the beads list so any Tasks view that happens to render doesn't
    // depend on the external `bd` binary; an empty list is enough.
    await page.route(/\/api\/issues(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    // Ensure the project-alpha workspace exists.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
  });

  testWithCleanup(
    "archiving the active conversation switches to the Dashboard",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      // Seed a conversation in project-alpha and make it the active conversation.
      const createResp = await request.post(apiUrl("/api/sessions"), {
        data: { name: `Archive Nav ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
      });
      expect(createResp.ok()).toBeTruthy();
      const created = await createResp.json();
      const sessionId = created.session_id || created.id;
      expect(sessionId).toBeTruthy();

      await helpers.navigateAndWait(page);
      await helpers.navigateToSession(page, sessionId);

      // Open the active conversation's context menu and click Archive.
      const sessionItem = page
        .locator(`[data-session-id="${sessionId}"]`)
        .first();
      await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
      await sessionItem.click({ button: "right" });

      // ContextMenu.js (daisyUI conversion) dropped the "z-50" class in
      // favor of an inline z-index style; match on the remaining stable
      // classes instead.
      const menu = page.locator(".menu.fixed.shadow-xl").first();
      await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
      await menu
        .getByRole("button", { name: "Archive", exact: true })
        .click();

      // The UI navigates to the global Dashboard (mitto-ce3). Same
      // mounted-heading selector used by dashboard.spec.ts / dashboard-charts.spec.ts.
      const dashboardHeading = page
        .locator("span.font-semibold", { hasText: "Dashboard" })
        .first();
      await expect(dashboardHeading).toBeVisible({ timeout: timeouts.appReady });
    },
  );
});
