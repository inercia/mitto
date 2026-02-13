import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * Message Reliability Tests
 *
 * These tests verify that messages are reliably delivered and ordered correctly
 * in various scenarios that can cause message loss or misordering:
 * - Page reload
 * - Visibility change (mobile sleep/wake)
 * - Conversation switching
 * - Streaming interruption
 * - WebSocket reconnection
 *
 * Uses testWithCleanup to automatically clean up old sessions before each test,
 * preventing the 32-session limit from being hit during test runs.
 * The fixture also clears localStorage to ensure test isolation.
 */

test.describe("Message Ordering After Page Reload", () => {
  // Delete all sessions before these tests to ensure complete isolation
  test.beforeAll(async ({ request }) => {
    const { apiUrl } = await import("../utils/selectors");
    // Get all sessions and delete them
    const sessionsRes = await request.get(apiUrl("/api/sessions"));
    if (sessionsRes.ok()) {
      const sessions = await sessionsRes.json();
      for (const session of sessions) {
        await request.delete(apiUrl(`/api/sessions/${session.session_id}`));
      }
    }
  });

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
  });

  test("should preserve exact message order after reload", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {

    // Create a fresh session for complete isolation
    const sessionId = await helpers.createFreshSession(page);
    expect(sessionId).toBeTruthy();

    // Send multiple messages with distinct content
    const messages = [
      helpers.uniqueMessage("First"),
      helpers.uniqueMessage("Second"),
      helpers.uniqueMessage("Third"),
    ];

    for (const msg of messages) {
      await helpers.sendMessageAndWait(page, msg);
    }

    // Capture all message texts in order
    const getOrderedMessages = async () => {
      const userMessages = await page.locator(selectors.userMessage).all();
      const texts: string[] = [];
      for (const el of userMessages) {
        const text = await el.textContent();
        if (text) texts.push(text.trim());
      }
      return texts;
    };

    const orderBefore = await getOrderedMessages();
    console.log("Messages before reload:", orderBefore);
    expect(orderBefore.length).toBe(3);

    // Navigate to the same session for reliable reload
    await helpers.navigateToSession(page, sessionId);
    await helpers.waitForMessagesLoaded(page, messages.length);

    // Verify message order after reload
    const orderAfter = await getOrderedMessages();
    console.log("Messages after reload:", orderAfter);

    // Order should be exactly the same
    expect(orderAfter.length).toBe(orderBefore.length);
    for (let i = 0; i < orderBefore.length; i++) {
      expect(orderAfter[i]).toBe(orderBefore[i]);
    }
  });

  test("should show all agent responses after reload", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    // Create a fresh session for complete isolation
    const sessionId = await helpers.createFreshSession(page);
    expect(sessionId).toBeTruthy();

    // Send a message and wait for complete response
    const testMessage = helpers.uniqueMessage("Response test");
    await helpers.sendMessageAndWait(page, testMessage);

    // Count agent messages before reload
    const agentCountBefore = await page.locator(selectors.agentMessage).count();
    expect(agentCountBefore).toBeGreaterThanOrEqual(1);

    // Navigate to the same session for reliable reload
    await helpers.navigateToSession(page, sessionId);
    await helpers.waitForMessagesLoaded(page, 1);

    // Verify user message is visible
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: testMessage })
    ).toBeVisible({ timeout: timeouts.shortAction });

    // Count agent messages after reload
    const agentCountAfter = await page.locator(selectors.agentMessage).count();
    expect(agentCountAfter).toBe(agentCountBefore);
  });
});

test.describe("Conversation Switching", () => {
  test("should preserve messages when switching conversations", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create first session and send a message
    const sessionId1 = await helpers.createFreshSession(page);
    const msg1 = helpers.uniqueMessage("Conv1");
    await helpers.sendMessageAndWait(page, msg1);

    // Count messages in first conversation
    const msgCount1 = await page.locator(selectors.allMessages).count();

    // Create second session
    await helpers.createFreshSession(page);
    const msg2 = helpers.uniqueMessage("Conv2");
    await helpers.sendMessageAndWait(page, msg2);

    // Switch back to first conversation using session ID
    await helpers.navigateToSession(page, sessionId1);
    await helpers.waitForMessagesLoaded(page, 1);

    // Verify first message is visible
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: msg1 }),
    ).toBeVisible({ timeout: timeouts.shortAction });

    // Message count should be the same
    const msgCountAfter = await page.locator(selectors.allMessages).count();
    expect(msgCountAfter).toBe(msgCount1);
  });

  test("should not show messages from wrong conversation", async ({
    page,
    helpers,
    selectors,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create first session with unique message
    await helpers.createFreshSession(page);
    const uniqueMarker1 = `UNIQUE_MARKER_A_${Date.now()}`;
    await helpers.sendMessageAndWait(page, uniqueMarker1);

    // Create second session with different unique message
    await helpers.createFreshSession(page);
    const uniqueMarker2 = `UNIQUE_MARKER_B_${Date.now()}`;
    await helpers.sendMessageAndWait(page, uniqueMarker2);

    // Verify second marker is visible, first is not
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: uniqueMarker2 }),
    ).toBeVisible();
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: uniqueMarker1 }),
    ).not.toBeVisible();
  });

  test("should handle rapid conversation switching", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create first session with message
    const sessionId1 = await helpers.createFreshSession(page);
    const msg1 = helpers.uniqueMessage("RapidSwitch1");
    await helpers.sendMessageAndWait(page, msg1);

    // Create second session with message
    const sessionId2 = await helpers.createFreshSession(page);
    const msg2 = helpers.uniqueMessage("RapidSwitch2");
    await helpers.sendMessageAndWait(page, msg2);

    // Rapidly switch between conversations using session IDs
    for (let i = 0; i < 3; i++) {
      await helpers.navigateToSession(page, sessionId1);
      await helpers.waitForMessagesLoaded(page, 1);

      await helpers.navigateToSession(page, sessionId2);
      await helpers.waitForMessagesLoaded(page, 1);
    }

    // Should still be functional
    await expect(page.locator(selectors.chatInput)).toBeEnabled();
  });
});

test.describe("Visibility Change Sync", () => {
  test("should not lose messages during visibility change", async ({
    page,
    helpers,
    selectors,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create fresh session
    await helpers.createFreshSession(page);

    const msg1 = helpers.uniqueMessage("Visibility1");
    await helpers.sendMessageAndWait(page, msg1);

    // Count USER messages before visibility change
    const userCountBefore = await page.locator(selectors.userMessage).count();
    const agentCountBefore = await page.locator(selectors.agentMessage).count();

    // Simulate visibility change (phone lock/unlock)
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        writable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await page.waitForTimeout(100);
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        value: "visible",
        writable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });

    // Wait for sync by polling message count stability
    await expect.poll(
      async () => page.locator(selectors.userMessage).count(),
      { timeout: 5000 }
    ).toBe(userCountBefore);

    // Count USER messages after - should be same (no duplicates or losses)
    const userCountAfter = await page.locator(selectors.userMessage).count();
    const agentCountAfter = await page.locator(selectors.agentMessage).count();
    expect(userCountAfter).toBe(userCountBefore);
    expect(agentCountAfter).toBeGreaterThanOrEqual(agentCountBefore);

    // Message should still be visible
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: msg1 }),
    ).toBeVisible();
  });

  test("should maintain message order after multiple visibility changes", async ({
    page,
    helpers,
    selectors,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create fresh session
    await helpers.createFreshSession(page);

    const messages = [
      helpers.uniqueMessage("Order1"),
      helpers.uniqueMessage("Order2"),
    ];

    for (const msg of messages) {
      await helpers.sendMessageAndWait(page, msg);
    }

    // Get message order
    const getOrder = async () => {
      const els = await page.locator(selectors.userMessage).all();
      const texts: string[] = [];
      for (const el of els) {
        texts.push((await el.textContent()) || "");
      }
      return texts;
    };

    const orderBefore = await getOrder();

    // Multiple visibility changes
    for (let i = 0; i < 3; i++) {
      await page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", {
          value: "hidden",
          writable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await page.waitForTimeout(50);
      await page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await page.waitForTimeout(100);
    }

    // Wait for message count stability
    await expect.poll(
      async () => page.locator(selectors.userMessage).count(),
      { timeout: 5000 }
    ).toBe(orderBefore.length);

    // Order should be preserved
    const orderAfter = await getOrder();
    expect(orderAfter.length).toBe(orderBefore.length);
    for (let i = 0; i < orderBefore.length; i++) {
      expect(orderAfter[i]).toBe(orderBefore[i]);
    }
  });
});

test.describe("Streaming Interruption", () => {
  test("should preserve messages after cancel during streaming", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create fresh session
    await helpers.createFreshSession(page);

    // Send a message
    const msg = helpers.uniqueMessage("Cancel test");
    await helpers.sendMessage(page, msg);

    // Wait briefly for streaming to start
    await page.waitForTimeout(100);

    // Try to cancel if stop button is available
    const stopBtn = page.locator(selectors.stopButton);
    if ((await stopBtn.count()) > 0) {
      await stopBtn.click();
    }

    // Wait for either stop button to disappear or response to complete
    await helpers.waitForStreamingComplete(page);

    // User message should still be visible
    await expect(
      page.locator(selectors.userMessage).filter({ hasText: msg }),
    ).toBeVisible();

    // Should be able to send another message
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.shortAction,
    });
  });
});

test.describe("Session Isolation Under Load", () => {
  // Delete all sessions before these tests to ensure complete isolation
  test.beforeAll(async ({ request }) => {
    const { apiUrl } = await import("../utils/selectors");
    const sessionsRes = await request.get(apiUrl("/api/sessions"));
    if (sessionsRes.ok()) {
      const sessions = await sessionsRes.json();
      for (const session of sessions) {
        await request.delete(apiUrl(`/api/sessions/${session.session_id}`));
      }
    }
  });

  test("should isolate messages when many sessions exist", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Use a unique test run ID to identify our sessions
    const testRunId = Date.now();

    // Create multiple sessions with unique markers and track their IDs
    const sessionData: Array<{ id: string; marker: string }> = [];
    const numSessions = 3;

    for (let i = 0; i < numSessions; i++) {
      const sessionId = await helpers.createFreshSession(page);
      const marker = `ISO_${testRunId}_${i}`;
      sessionData.push({ id: sessionId, marker });
      await helpers.sendMessageAndWait(page, marker);
    }

    // After creating all sessions, we're on the last one
    const userMessages = page.locator(selectors.userMessage);
    const lastMarker = sessionData[numSessions - 1].marker;

    // Current session should show its marker
    await expect(userMessages.filter({ hasText: lastMarker })).toBeVisible();

    // Current session should NOT show other markers from THIS test run
    for (let i = 0; i < numSessions - 1; i++) {
      const otherMarker = sessionData[i].marker;
      const count = await userMessages.filter({ hasText: otherMarker }).count();
      expect(count).toBe(0);
    }

    // Switch to first session using session ID
    const firstData = sessionData[0];
    await helpers.navigateToSession(page, firstData.id);
    await helpers.waitForMessagesLoaded(page, 1);

    // Should show the first marker
    await expect(userMessages.filter({ hasText: firstData.marker })).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Should NOT show other markers from this test run
    for (let i = 1; i < numSessions; i++) {
      const otherMarker = sessionData[i].marker;
      const count = await userMessages
        .filter({ hasText: otherMarker })
        .count();
      expect(count).toBe(0);
    }
  });

  test("should not bleed messages after page reload with many sessions", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create 3 sessions with unique markers
    const sessionData: Array<{ id: string; marker: string }> = [];
    for (let i = 0; i < 3; i++) {
      const sessionId = await helpers.createFreshSession(page);
      const marker = `RELOAD_BLEED_${i}_${Date.now()}`;
      sessionData.push({ id: sessionId, marker });
      await helpers.sendMessageAndWait(page, marker);
    }

    // Navigate to the last session explicitly
    const lastSession = sessionData[sessionData.length - 1];
    await helpers.navigateToSession(page, lastSession.id);
    await helpers.waitForMessagesLoaded(page, 1);

    // After reload, verify only the last marker is visible
    const userMessages = page.locator(selectors.userMessage);

    // The last marker should be visible
    await expect(userMessages.filter({ hasText: lastSession.marker })).toBeVisible();

    // Other markers should NOT be visible
    for (let i = 0; i < sessionData.length - 1; i++) {
      const otherMarker = sessionData[i].marker;
      const hasOtherMarker = await userMessages
        .filter({ hasText: otherMarker })
        .count();
      expect(hasOtherMarker).toBe(0);
    }
  });
});

test.describe("Sequence Number Tracking", () => {
  test("should track sequence numbers in API responses", async ({
    page,
    request,
    apiUrl,
    helpers,
    selectors,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create fresh session and send a message
    await helpers.createFreshSession(page);
    const msg = helpers.uniqueMessage("SeqTrack");
    await helpers.sendMessageAndWait(page, msg);

    // Get the session list to find our session
    const sessionsRes = await request.get(apiUrl("/api/sessions"));
    expect(sessionsRes.ok()).toBeTruthy();
    const sessions = await sessionsRes.json();

    // Find a session with events
    let sessionWithEvents = null;
    for (const s of sessions) {
      if (s.event_count > 1) {
        sessionWithEvents = s;
        break;
      }
    }

    if (sessionWithEvents) {
      // Get events for this session (field is session_id, not id)
      const eventsRes = await request.get(
        apiUrl(`/api/sessions/${sessionWithEvents.session_id}/events`),
      );
      expect(eventsRes.ok()).toBeTruthy();
      const events = await eventsRes.json();

      // Verify events have seq numbers and are ordered
      if (events.length > 1) {
        for (let i = 1; i < events.length; i++) {
          expect(events[i].seq).toBeGreaterThan(events[i - 1].seq);
        }
      }
    }
  });

  test("should return events with limit and order parameters", async ({
    page,
    request,
    apiUrl,
    helpers,
    selectors,
  }) => {
    
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Create fresh session and send multiple messages
    await helpers.createFreshSession(page);

    const msg1 = helpers.uniqueMessage("SeqLimit1");
    await helpers.sendMessageAndWait(page, msg1);

    const msg2 = helpers.uniqueMessage("SeqLimit2");
    await helpers.sendMessageAndWait(page, msg2);

    // Get the session list (sorted by updated_at, most recent first)
    const sessionsRes = await request.get(apiUrl("/api/sessions"));
    expect(sessionsRes.ok()).toBeTruthy();
    const sessions = await sessionsRes.json();
    expect(sessions.length).toBeGreaterThan(0);

    // Use the most recently updated session (first in list)
    const recentSession = sessions[0];

    // Get all events
    const allRes = await request.get(
      apiUrl(`/api/sessions/${recentSession.session_id}/events`),
    );
    expect(allRes.ok()).toBeTruthy();
    const allEvents = await allRes.json();

    // Should have at least 3 events
    expect(allEvents.length).toBeGreaterThanOrEqual(3);

    // Test limit parameter - request only 2 events
    const limitedRes = await request.get(
      apiUrl(`/api/sessions/${recentSession.session_id}/events?limit=2`),
    );
    expect(limitedRes.ok()).toBeTruthy();
    const limitedEvents = await limitedRes.json();
    expect(limitedEvents.length).toBe(2);

    // Test order parameter - request in descending order
    const descRes = await request.get(
      apiUrl(`/api/sessions/${recentSession.session_id}/events?order=desc`),
    );
    expect(descRes.ok()).toBeTruthy();
    const descEvents = await descRes.json();

    // First event in desc order should have higher seq than last
    if (descEvents.length > 1) {
      expect(descEvents[0].seq).toBeGreaterThan(
        descEvents[descEvents.length - 1].seq,
      );
    }
  });
});

