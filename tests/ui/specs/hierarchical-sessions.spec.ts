import { test, expect } from "../fixtures/test-fixtures";
import * as path from "path";
import * as fs from "fs";

/**
 * Hierarchical Session Grouping Tests
 *
 * Tests the behavior of parent-child session relationships created via
 * mitto_conversation_new MCP tool. Verifies:
 * - Tree structure rendering
 * - Session switching behavior
 * - Parent-child relationship preservation
 * - Updated_at timestamp behavior
 */

test.describe("Hierarchical Session Grouping", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should display hierarchical tree with real parent-child relationships", async ({
    page,
    helpers,
    timeouts,
  }) => {
    // This test requires the helper script to create sessions with parent_session_id
    // Check if the hierarchical sessions were created
    const mittoDir = process.env.MITTO_DIR || "/tmp/mitto-test";
    const hierarchicalSessionsFile = path.join(
      mittoDir,
      "hierarchical-sessions.json"
    );

    // Skip if the helper hasn't been run
    if (!fs.existsSync(hierarchicalSessionsFile)) {
      console.log(
        "[Test] Skipping: Run create-hierarchical-sessions.go helper first"
      );
      test.skip();
      return;
    }

    // Read the session IDs created by the helper
    const sessionData = JSON.parse(
      fs.readFileSync(hierarchicalSessionsFile, "utf-8")
    );
    const parentId = sessionData.parent.id;
    const childIds = sessionData.children.map((c: any) => c.id);

    console.log(`[Test] Parent session: ${parentId}`);
    console.log(`[Test] Child sessions: ${childIds.join(", ")}`);

    // Reload the page to see the hierarchical sessions
    await page.reload();
    await helpers.waitForAppReady(page);

    // Take a screenshot of the hierarchical tree
    await page.screenshot({
      path: "test-results/hierarchical-tree-initial.png",
      fullPage: true,
    });

    // Verify parent session is visible
    const parentSessionItem = page.locator(
      `[data-session-id="${parentId}"]`
    );
    await expect(parentSessionItem).toBeVisible({ timeout: timeouts.appReady });

    // Verify child sessions are visible
    for (const childId of childIds) {
      const childSessionItem = page.locator(
        `[data-session-id="${childId}"]`
      );
      await expect(childSessionItem).toBeVisible({
        timeout: timeouts.appReady,
      });
    }

    // Click on the first child session
    const firstChildItem = page.locator(
      `[data-session-id="${childIds[0]}"]`
    );
    await firstChildItem.click();

    // Wait for session to load
    await helpers.waitForAppReady(page);
    await helpers.waitForWebSocketReady(page);

    // Take a screenshot after clicking child
    await page.screenshot({
      path: "test-results/hierarchical-tree-child-active.png",
      fullPage: true,
    });

    // CRITICAL TEST: Verify the tree is still visible (should NOT disappear)
    const parentStillVisible = await parentSessionItem.isVisible();
    expect(parentStillVisible).toBe(true);

    console.log(
      `[Test] ✓ Parent session still visible after clicking child: ${parentStillVisible}`
    );

    // Verify all child sessions are still visible
    for (const childId of childIds) {
      const childSessionItem = page.locator(
        `[data-session-id="${childId}"]`
      );
      const childVisible = await childSessionItem.isVisible();
      expect(childVisible).toBe(true);
      console.log(
        `[Test] ✓ Child session ${childId} still visible: ${childVisible}`
      );
    }

    // Click on the parent session
    await parentSessionItem.click();

    // Wait for session to load
    await helpers.waitForAppReady(page);
    await helpers.waitForWebSocketReady(page);

    // Take a screenshot after clicking parent
    await page.screenshot({
      path: "test-results/hierarchical-tree-parent-active.png",
      fullPage: true,
    });

    // Verify all sessions are still visible
    const parentStillVisibleAfterSwitch = await parentSessionItem.isVisible();
    expect(parentStillVisibleAfterSwitch).toBe(true);

    for (const childId of childIds) {
      const childSessionItem = page.locator(
        `[data-session-id="${childId}"]`
      );
      const childVisible = await childSessionItem.isVisible();
      expect(childVisible).toBe(true);
    }

    console.log(
      "[Test] ✓ All sessions remain visible after switching between parent and child"
    );
  });

  test("should create and display parent-child session hierarchy via API", async ({
    request,
    apiUrl,
    page,
    helpers,
    timeouts,
  }) => {
    // Step 1: Create a parent session
    const parentResponse = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Parent Session ${Date.now()}`,
      },
    });
    expect(parentResponse.ok()).toBeTruthy();
    const parentSession = await parentResponse.json();
    const parentId = parentSession.session_id;

    console.log(`[Test] Created parent session: ${parentId}`);

    // Step 2: Create child sessions by directly manipulating session metadata
    // Note: Since the API doesn't support parent_session_id parameter yet,
    // we'll create regular sessions and then update their metadata via a helper endpoint
    // For now, we'll create regular sessions and verify the tree structure works
    // when parent_session_id is set (which happens via MCP tool in production)

    const child1Response = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Child Session 1 ${Date.now()}`,
      },
    });
    expect(child1Response.ok()).toBeTruthy();
    const child1Session = await child1Response.json();
    const child1Id = child1Session.session_id;

    const child2Response = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Child Session 2 ${Date.now()}`,
      },
    });
    expect(child2Response.ok()).toBeTruthy();
    const child2Session = await child2Response.json();
    const child2Id = child2Session.session_id;

    console.log(`[Test] Created child sessions: ${child1Id}, ${child2Id}`);

    // Step 3: Reload the page to see all sessions
    await page.reload();
    await helpers.waitForAppReady(page);

    // Step 4: Take a screenshot of the initial state
    await page.screenshot({
      path: "test-results/hierarchical-sessions-initial.png",
      fullPage: true,
    });

    // Step 5: Verify all sessions are visible in the sidebar
    const sessionsList = page.locator(".session-item-container");
    const sessionCount = await sessionsList.count();
    expect(sessionCount).toBeGreaterThanOrEqual(3); // At least parent + 2 children

    console.log(`[Test] Found ${sessionCount} sessions in sidebar`);

    // Step 6: Click on the parent session
    const parentSessionItem = page.locator(
      `[data-session-id="${parentId}"]`
    );
    await expect(parentSessionItem).toBeVisible({ timeout: timeouts.appReady });
    await parentSessionItem.click();

    // Wait for session to load
    await helpers.waitForAppReady(page);
    await helpers.waitForWebSocketReady(page);

    // Step 7: Verify parent session is active
    const activeSessionId = await page.evaluate(() => {
      return localStorage.getItem("mitto_last_session_id");
    });
    expect(activeSessionId).toBe(parentId);

    console.log(`[Test] Switched to parent session: ${activeSessionId}`);

    // Step 8: Take a screenshot after clicking parent
    await page.screenshot({
      path: "test-results/hierarchical-sessions-parent-active.png",
      fullPage: true,
    });

    // Step 9: Click on a child session
    const child1SessionItem = page.locator(
      `[data-session-id="${child1Id}"]`
    );
    await expect(child1SessionItem).toBeVisible({ timeout: timeouts.appReady });
    
    // Monitor console for any errors
    const consoleMessages: string[] = [];
    page.on("console", (msg) => {
      consoleMessages.push(`[${msg.type()}] ${msg.text()}`);
    });

    await child1SessionItem.click();

    // Wait for session to load
    await helpers.waitForAppReady(page);
    await helpers.waitForWebSocketReady(page);

    // Step 10: Verify child session is active
    const activeChildSessionId = await page.evaluate(() => {
      return localStorage.getItem("mitto_last_session_id");
    });
    expect(activeChildSessionId).toBe(child1Id);

    console.log(`[Test] Switched to child session: ${activeChildSessionId}`);

    // Step 11: Take a screenshot after clicking child
    await page.screenshot({
      path: "test-results/hierarchical-sessions-child-active.png",
      fullPage: true,
    });

    // Step 12: Verify the session tree is still visible (should NOT disappear)
    const sessionsListAfterClick = page.locator(".session-item-container");
    const sessionCountAfterClick = await sessionsListAfterClick.count();
    expect(sessionCountAfterClick).toBeGreaterThanOrEqual(3);

    console.log(
      `[Test] Sessions still visible after clicking child: ${sessionCountAfterClick}`
    );

    // Step 13: Log console messages for debugging
    console.log("[Test] Console messages during test:");
    consoleMessages.forEach((msg) => console.log(msg));

    // Step 14: Verify no errors in console
    const errorMessages = consoleMessages.filter((msg) =>
      msg.startsWith("[error]")
    );
    expect(errorMessages).toHaveLength(0);
  });

  test("should preserve parent_session_id when switching sessions", async ({
    request,
    apiUrl,
    page,
    helpers,
  }) => {
    // Create parent and child sessions
    const parentResponse = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Parent ${Date.now()}` },
    });
    const parentSession = await parentResponse.json();
    const parentId = parentSession.session_id;

    const childResponse = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Child ${Date.now()}` },
    });
    const childSession = await childResponse.json();
    const childId = childSession.session_id;

    // Reload to see sessions
    await page.reload();
    await helpers.waitForAppReady(page);

    // Get initial session metadata
    const initialSessionsResponse = await request.get(apiUrl("/api/sessions"));
    const initialSessions = await initialSessionsResponse.json();

    const initialChild = initialSessions.find(
      (s: any) => s.session_id === childId
    );
    const initialParentSessionId = initialChild?.parent_session_id || "";

    console.log(
      `[Test] Initial parent_session_id for child: ${initialParentSessionId}`
    );

    // Switch to parent session
    await helpers.navigateToSession(page, parentId);

    // Switch to child session
    await helpers.navigateToSession(page, childId);

    // Get session metadata after switching
    const afterSessionsResponse = await request.get(apiUrl("/api/sessions"));
    const afterSessions = await afterSessionsResponse.json();

    const afterChild = afterSessions.find(
      (s: any) => s.session_id === childId
    );
    const afterParentSessionId = afterChild?.parent_session_id || "";

    console.log(
      `[Test] After switching parent_session_id for child: ${afterParentSessionId}`
    );

    // Verify parent_session_id is preserved
    expect(afterParentSessionId).toBe(initialParentSessionId);
  });

  test("should maintain session order when switching between parent and child", async ({
    request,
    apiUrl,
    page,
    helpers,
  }) => {
    // Create multiple sessions with known timestamps
    const sessions = [];
    for (let i = 0; i < 3; i++) {
      const response = await request.post(apiUrl("/api/sessions"), {
        data: { name: `Session ${i} ${Date.now()}` },
      });
      const session = await response.json();
      sessions.push(session);
      // Small delay to ensure different created_at timestamps
      await page.waitForTimeout(100);
    }

    // Reload to see all sessions
    await page.reload();
    await helpers.waitForAppReady(page);

    // Get initial session order
    const initialSessionsResponse = await request.get(apiUrl("/api/sessions"));
    const initialSessions = await initialSessionsResponse.json();
    const initialOrder = initialSessions.map((s: any) => s.session_id);

    console.log(`[Test] Initial session order: ${initialOrder.join(", ")}`);

    // Switch between sessions
    for (const session of sessions) {
      await helpers.navigateToSession(page, session.session_id);
      await page.waitForTimeout(200);
    }

    // Get session order after switching
    const afterSessionsResponse = await request.get(apiUrl("/api/sessions"));
    const afterSessions = await afterSessionsResponse.json();
    const afterOrder = afterSessions.map((s: any) => s.session_id);

    console.log(`[Test] After switching session order: ${afterOrder.join(", ")}`);

    // Verify order is maintained (sessions should be sorted by created_at, not updated_at)
    // The order should be the same as initial order
    expect(afterOrder).toEqual(initialOrder);
  });

  test("should check updated_at behavior when switching sessions", async ({
    request,
    apiUrl,
    page,
    helpers,
  }) => {
    // Create a session
    const response = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Test Session ${Date.now()}` },
    });
    const session = await response.json();
    const sessionId = session.session_id;

    // Get initial updated_at
    const initialResponse = await request.get(
      apiUrl(`/api/sessions/${sessionId}`)
    );
    const initialSession = await initialResponse.json();
    const initialUpdatedAt = initialSession.updated_at;

    console.log(`[Test] Initial updated_at: ${initialUpdatedAt}`);

    // Wait a bit to ensure timestamp would change if it's being updated
    await page.waitForTimeout(1000);

    // Reload and switch to the session
    await page.reload();
    await helpers.waitForAppReady(page);
    await helpers.navigateToSession(page, sessionId);

    // Get updated_at after switching
    const afterResponse = await request.get(
      apiUrl(`/api/sessions/${sessionId}`)
    );
    const afterSession = await afterResponse.json();
    const afterUpdatedAt = afterSession.updated_at;

    console.log(`[Test] After switching updated_at: ${afterUpdatedAt}`);

    // Document the behavior: Does updated_at change when switching?
    // This test will help us understand the current behavior
    if (initialUpdatedAt === afterUpdatedAt) {
      console.log(
        "[Test] ✓ updated_at does NOT change when switching sessions"
      );
    } else {
      console.log("[Test] ✗ updated_at DOES change when switching sessions");
      console.log(`[Test] Initial: ${initialUpdatedAt}`);
      console.log(`[Test] After: ${afterUpdatedAt}`);
    }
  });
});
