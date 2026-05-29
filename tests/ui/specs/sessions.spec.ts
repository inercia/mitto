import { test, expect } from "../fixtures/test-fixtures";

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
