import { test, expect } from "../fixtures/test-fixtures";
import { selectors, timeouts, apiUrl } from "../utils/selectors";

/**
 * QueueDropdown internals tests.
 *
 * Regression net: catches a daisyUI restyle breaking the queue dropdown's
 * anchored positioning, item rows, or move-up/move-down/delete actions.
 *
 * Strategy:
 *   1. Create a fresh session via REST API (bypasses the sidebar "New
 *      Conversation" button, which may be hidden when the sidebar CSS is
 *      not fully compiled — e.g. md:drawer-open is absent on this branch).
 *   2. Trigger a slow-streaming response (pattern: "slow response") so the
 *      agent keeps responding for ~4 seconds, giving us time to populate the
 *      queue and interact with the dropdown before it auto-sends items.
 *   3. Add three items to the queue via API while the agent is busy.
 *   4. Click the queue-toggle button (appears when queueLength > 0).
 *   5. Assert the dropdown is open, item rows are visible, and action buttons
 *      (move-up, move-down, delete) are present.
 *   6. Delete an item and assert the count decreases.
 */

/**
 * Create a fresh session via REST API and activate it in the browser.
 * Returns the session ID.
 */
async function createSessionViaAPI(
  page: import("@playwright/test").Page,
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  const data = await resp.json();
  const sessionId: string = data.session_id;
  expect(sessionId).toBeTruthy();

  // Activate the session in the browser via localStorage then reload
  await page.evaluate((id) => {
    localStorage.setItem("mitto_last_session_id", id);
    localStorage.removeItem("mitto_conversation_filter_tab");
  }, sessionId);
  await page.reload();

  // Wait for the textarea placeholder to indicate ACP is ready.
  // When acpReady=false the placeholder says "Waiting for AI agent to connect...";
  // when ready it switches to "Type your message...".
  await expect(page.locator(selectors.chatInput)).toHaveAttribute(
    "placeholder",
    /Type your message/,
    { timeout: timeouts.agentResponse },
  );
  return sessionId;
}

test.describe("QueueDropdown internals", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("should open dropdown and show item rows with action buttons", async ({
    page,
    request,
  }) => {
    // Create a fresh session via API (bypasses hidden sidebar button)
    const sessionId = await createSessionViaAPI(page, request);

    // Send a slow-streaming message so the agent keeps responding
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await textarea.fill("slow response please");
    await page.locator(selectors.sendButton).click();

    // Wait for agent to start responding
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Add 3 messages to queue via API while agent is busy
    for (const msg of ["Queued msg 1", "Queued msg 2", "Queued msg 3"]) {
      const resp = await request.post(apiUrl(`/api/sessions/${sessionId}/queue`), {
        data: { message: msg },
      });
      expect(resp.ok(), `Queue POST failed: ${resp.status()}`).toBe(true);
    }

    // Queue toggle button appears when queueLength > 0.
    // Use agentResponse timeout since the queue_updated WebSocket message can
    // take a few seconds when the ACP session is still warming up.
    const toggleBtn = page.locator(selectors.queueToggleButton);
    await expect(toggleBtn).toBeVisible({ timeout: timeouts.agentResponse });

    // Click toggle to open the dropdown
    await toggleBtn.click();

    // Dropdown panel should be open
    const dropdown = page.locator('[data-testid="queue-dropdown"]');
    await expect(dropdown).toHaveAttribute("data-is-open", "true");

    // Three item rows should be visible
    const items = page.locator('[data-testid="queue-item"]');
    await expect(items).toHaveCount(3, { timeout: timeouts.shortAction });

    // Item text should be visible
    await expect(items.first().locator(".queue-item-text")).toContainText("Queued msg 1");

    // Action buttons: hover over the first item to reveal them
    await items.first().hover();

    // Move-up on first item is disabled (index 0)
    const moveUp = items.first().locator(".queue-item-move-up");
    await expect(moveUp).toBeVisible();
    await expect(moveUp).toBeDisabled();

    // Move-down on first item is enabled
    const moveDown = items.first().locator(".queue-item-move-down");
    await expect(moveDown).toBeVisible();
    await expect(moveDown).toBeEnabled();

    // Delete button is always enabled
    const deleteBtn = items.first().locator(".queue-item-delete");
    await expect(deleteBtn).toBeVisible();
    await expect(deleteBtn).toBeEnabled();

    // Delete the last item and assert count drops to 2
    const lastItem = items.last();
    await lastItem.hover();
    await lastItem.locator(".queue-item-delete").click();

    // Wait for count to decrease to 2
    const remainingItems = page.locator('[data-testid="queue-item"]');
    await expect(remainingItems).toHaveCount(2, {
      timeout: timeouts.shortAction,
    });

    // Also verify the NEW last item (index 1): move-up enabled, move-down disabled
    const newLastItem = remainingItems.last();
    await newLastItem.hover();
    await expect(newLastItem.locator(".queue-item-move-up")).toBeEnabled();
    await expect(newLastItem.locator(".queue-item-move-down")).toBeDisabled();
  });
});
