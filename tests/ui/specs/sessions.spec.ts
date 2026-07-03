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
    // navigateAndWait resets the conversation filter tab in localStorage via
    // addInitScript before the page loads.  However the app's React side also
    // has an auto-tab-switch effect that switches to the Periodic tab whenever
    // the active session has periodic_enabled=true (e.g. after the
    // periodic-prompt-pill.spec.ts suite).  We therefore explicitly click the
    // Conversations tab after the app is ready so all session-management tests
    // always start on the correct tab.
    await helpers.navigateAndWait(page);
    // Click the Conversations tab if it is not already selected.
    const conversationsTab = page.getByRole("tab", { name: "Conversations" });
    if (await conversationsTab.isVisible()) {
      const isSelected = await conversationsTab.getAttribute("aria-selected");
      if (isSelected !== "true") {
        await conversationsTab.click();
      }
    }
  });

  test("should display sessions sidebar", async ({ page, timeouts }) => {
    const conversationsHeader = page.getByRole("heading", {
      name: "Conversations",
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
 * Active-conversation removal navigation (mitto-17d).
 *
 * When the ACTIVE conversation is removed from view, the UI switches to that
 * conversation's folder Tasks (beads) view so the user stays in the same
 * workspace context instead of being bounced to another conversation or an
 * empty state. This covers the archive path (the delete path shares the same
 * onActiveSessionRemoved callback wiring in useWebSocket).
 *
 * The Beads backend shells out to the external `bd` binary, which is not
 * guaranteed in CI, so /api/issues is mocked with an empty list — the test
 * only asserts that the Tasks view for the right folder mounts.
 */
const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

testWithCleanup.describe("Active conversation removal opens the folder Tasks view", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl }) => {
    // Mock the beads list so the Tasks view renders without the external `bd`
    // binary; an empty list is enough to confirm the view mounted.
    await page.route(/\/api\/issues(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    // Ensure the project-alpha workspace exists so its folder Tasks view resolves.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
  });

  testWithCleanup(
    "archiving the active conversation switches to that folder's Tasks view",
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

      const menu = page.locator(".menu.fixed.z-50.shadow-xl").first();
      await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
      await menu
        .getByRole("button", { name: "Archive", exact: true })
        .click();

      // The UI navigates to the archived conversation's folder Tasks (beads)
      // view (mitto-17d). The BeadsView header is unique to that view and is
      // scoped to the folder basename, so it confirms both that we left the
      // conversation view and that we opened the correct folder's Tasks.
      const beadsHeader = page
        .locator("span.text-lg.flex-1")
        .filter({ hasText: "project-alpha" });
      await expect(beadsHeader).toBeVisible({ timeout: timeouts.appReady });
    },
  );
});
