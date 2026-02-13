import { Page, expect } from "@playwright/test";
import { selectors, timeouts, apiUrl } from "./selectors";

/**
 * Clear all localStorage state to ensure test isolation.
 * Call this before each test to prevent session pollution.
 */
export async function clearLocalStorage(page: Page): Promise<void> {
  await page.evaluate(() => {
    localStorage.removeItem("mitto_last_session_id");
    localStorage.removeItem("mitto_sidebar_collapsed");
    localStorage.removeItem("mitto_sidebar_width");
    localStorage.removeItem("mitto_queue_collapsed");
  });
}

/**
 * Create a fresh session and wait for it to be ready.
 * This ensures the test starts with a clean, isolated session.
 * Returns the session ID.
 */
export async function createFreshSession(page: Page): Promise<string> {
  // Clear any stored session ID first
  await page.evaluate(() => {
    localStorage.removeItem("mitto_last_session_id");
  });

  // Click new session button
  await page.locator(selectors.newSessionButton).click();

  // Wait for the textarea to be enabled (indicates session is ready)
  await expect(page.locator(selectors.chatInput)).toBeEnabled({
    timeout: timeouts.appReady,
  });

  // Wait for WebSocket connection to stabilize
  await waitForWebSocketReady(page);

  // Get and return the session ID
  const sessionId = await page.evaluate(() => {
    return localStorage.getItem("mitto_last_session_id") || "";
  });

  return sessionId;
}

/**
 * Wait for the WebSocket connection to be established and ready.
 * This is more reliable than fixed timeouts.
 */
export async function waitForWebSocketReady(page: Page): Promise<void> {
  // Wait for the chat input to be enabled as a proxy for WebSocket readiness
  await expect(page.locator(selectors.chatInput)).toBeEnabled({
    timeout: timeouts.appReady,
  });

  // Give WebSocket a moment to fully stabilize after the UI updates
  // This is a minimal delay to handle async state propagation
  await page.waitForTimeout(200);
}

/**
 * Wait for all streaming to complete by checking that no messages are actively updating.
 */
export async function waitForStreamingComplete(page: Page): Promise<void> {
  // Wait for any agent response to appear first
  await expect(page.locator(selectors.agentMessage).first()).toBeVisible({
    timeout: timeouts.agentResponse,
  });

  // Wait for the stop button to disappear (indicates streaming is done)
  // If stop button doesn't appear (fast response), that's fine
  try {
    await page.locator(selectors.stopButton).waitFor({
      state: "hidden",
      timeout: timeouts.agentResponse,
    });
  } catch {
    // Stop button may never appear for fast responses
  }

  // Give UI a moment to settle after streaming ends
  await page.waitForTimeout(200);
}

/**
 * Wait for messages to be loaded/synced after navigation or reload.
 * Uses actual DOM state rather than fixed timeouts.
 *
 * @param page - The Playwright page
 * @param expectedUserMessages - Expected number of user messages (not total messages)
 */
export async function waitForMessagesLoaded(
  page: Page,
  expectedUserMessages: number = 0
): Promise<void> {
  if (expectedUserMessages > 0) {
    // Wait for at least the expected number of user messages to appear
    // This is more reliable than counting all messages since agent responses vary
    await expect(
      page.locator(selectors.userMessage)
    ).toHaveCount(expectedUserMessages, { timeout: timeouts.appReady });
  }

  // Wait for any loading indicators to disappear
  await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
    timeout: timeouts.appReady,
  });
}

/**
 * Navigate to a specific session by ID.
 * Uses the data-session-id attribute on session items for reliable navigation.
 */
export async function navigateToSession(
  page: Page,
  sessionId: string
): Promise<void> {
  // Store current session ID to verify the switch
  const currentSessionId = await page.evaluate(() => {
    return localStorage.getItem("mitto_last_session_id");
  });

  // If already on the target session, just reload the page
  if (currentSessionId === sessionId) {
    await page.reload();
    await waitForAppReady(page);
    await waitForWebSocketReady(page);
    return;
  }

  // Use the data-session-id attribute for reliable session selection
  const sessionItem = page.locator(`[data-session-id="${sessionId}"]`);

  // Wait for the session item to appear (might take a moment if list is loading)
  await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });

  // Click on the session item
  await sessionItem.click();

  // Wait for localStorage to update with the new session ID
  await expect
    .poll(
      async () => {
        return await page.evaluate(() => {
          return localStorage.getItem("mitto_last_session_id");
        });
      },
      { timeout: timeouts.appReady }
    )
    .toBe(sessionId);

  // Wait for app and session to load
  await waitForAppReady(page);
  await waitForWebSocketReady(page);
}

/**
 * Wait for the Mitto app to be fully loaded and ready
 */
export async function waitForAppReady(page: Page): Promise<void> {
  // Wait for chat input to be visible (may be disabled if no session)
  // This is a more reliable indicator that the app is ready than checking for spinner
  await expect(page.locator(selectors.chatInput)).toBeVisible({
    timeout: timeouts.appReady,
  });
}

/**
 * Wait for an active session with enabled chat input
 */
export async function waitForActiveSession(page: Page): Promise<void> {
  // Wait for chat input to be visible and enabled
  const textarea = page.locator(selectors.chatInput);
  await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
}

/**
 * Ensure there's an active session by creating one if needed.
 * Returns the session ID.
 */
export async function ensureActiveSession(page: Page): Promise<string> {
  const textarea = page.locator(selectors.chatInput);

  // Check if textarea is disabled (no active session)
  const isDisabled = await textarea.isDisabled();
  if (isDisabled) {
    // Create a new session
    const newButton = page.locator(selectors.newSessionButton);
    await expect(newButton).toBeVisible({ timeout: timeouts.shortAction });
    await newButton.click();

    // Wait for session to be ready
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });

    // Wait for WebSocket connection to stabilize
    await waitForWebSocketReady(page);
  }

  // Return the current session ID
  const sessionId = await page.evaluate(() => {
    return localStorage.getItem("mitto_last_session_id") || "";
  });
  return sessionId;
}

/**
 * Send a message in the chat
 */
export async function sendMessage(page: Page, message: string): Promise<void> {
  const textarea = page.locator(selectors.chatInput);
  await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
  await textarea.fill(message);
  await page.locator(selectors.sendButton).click();
}

/**
 * Send a message and wait for the complete response.
 * This is more reliable than separate send + waitForResponse calls.
 */
export async function sendMessageAndWait(
  page: Page,
  message: string
): Promise<void> {
  await sendMessage(page, message);
  await waitForUserMessage(page, message);
  await waitForStreamingComplete(page);
}

/**
 * Wait for a user message to appear in the chat
 */
export async function waitForUserMessage(
  page: Page,
  message: string,
): Promise<void> {
  await expect(page.locator(`text=${message}`)).toBeVisible({
    timeout: timeouts.shortAction,
  });
}

/**
 * Wait for an agent response to appear
 * Uses .first() to handle cases where multiple agent messages exist
 */
export async function waitForAgentResponse(page: Page): Promise<void> {
  await expect(page.locator(selectors.agentMessage).first()).toBeVisible({
    timeout: timeouts.agentResponse,
  });
}

/**
 * Generate a unique test message
 */
export function uniqueMessage(prefix: string = "Test"): string {
  return `${prefix} ${Date.now()}`;
}

/**
 * Navigate to the app and wait for it to be ready
 */
export async function navigateAndWait(page: Page): Promise<void> {
  await page.goto("/");
  await waitForAppReady(page);
}

/**
 * Navigate to the app, wait for it to be ready, and ensure there's an active session
 */
export async function navigateAndEnsureSession(page: Page): Promise<void> {
  await page.goto("/");
  await waitForAppReady(page);
  await ensureActiveSession(page);
}

/**
 * Delete sessions until we're under a threshold
 * Keeps the most recent sessions and deletes older ones
 */
export async function cleanupSessionsToLimit(
  request: import("@playwright/test").APIRequestContext,
  maxSessions: number = 20,
  apiPrefix: string = "/mitto",
): Promise<number> {
  try {
    // Get all sessions
    const sessionsResponse = await request.get(`${apiPrefix}/api/sessions`);
    if (!sessionsResponse.ok()) {
      return 0;
    }

    const sessions = await sessionsResponse.json();
    if (!sessions || !Array.isArray(sessions) || sessions.length <= maxSessions) {
      return 0;
    }

    // Sessions are sorted by updated_at (most recent first)
    // Delete the oldest ones (at the end of the array)
    const sessionsToDelete = sessions.slice(maxSessions);
    let deletedCount = 0;

    for (const session of sessionsToDelete) {
      const deleteResponse = await request.delete(
        `${apiPrefix}/api/sessions/${session.session_id}`,
      );
      if (deleteResponse.ok()) {
        deletedCount++;
      }
    }

    return deletedCount;
  } catch (error) {
    console.warn("Error during session cleanup:", error);
    return 0;
  }
}
