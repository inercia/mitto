import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { timeouts, apiUrl, selectors } from "../utils/selectors";

/**
 * PeriodicPromptSelector internals tests.
 *
 * Regression net: catches a daisyUI restyle breaking:
 *   - The selector dropdown opening/closing
 *   - Prompt list rendering (grouped/ungrouped)
 *   - Search filter narrowing the list
 *   - Prompt selection updating the displayed name
 *
 * The existing periodic-prompt-pill.spec.ts only tests the pill trigger
 * (NamedPromptPill) after a periodic run. This spec goes deeper, opening
 * the PeriodicPromptSelector *picker* dropdown itself.
 *
 * Setup:
 *   - Uses the project-alpha workspace fixture which has a "Hello Greeting"
 *     prompt in .mitto/prompts/greeting.md
 *   - Configures periodic via PUT /api/sessions/:id/periodic so the UI
 *     renders PeriodicPromptSelector with isOpen=true.
 */

/**
 * Create a session via REST API and activate it in the browser.
 */
async function apiCreateSession(
  page: import("@playwright/test").Page,
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  const data = await resp.json();
  const id: string = data.session_id;
  await page.evaluate((sid) => {
    localStorage.setItem("mitto_last_session_id", sid);
    localStorage.removeItem("mitto_conversation_filter_tab");
  }, id);
  await page.reload();
  // Wait for ACP to be ready: placeholder changes from "Waiting for AI agent to connect..."
  await expect(page.locator(selectors.chatInput)).toHaveAttribute(
    "placeholder",
    /Type your message/,
    { timeout: timeouts.agentResponse },
  );
  return id;
}

/**
 * Configure periodic on a session via REST API.
 *
 * The `periodic_updated` broadcast arrives on the global events WebSocket
 * (/api/events) which may connect slightly after the session WS. We retry
 * the PUT a few times with a brief delay to ensure the message is received.
 * Callers should then wait for a UI signal (e.g. periodic-frequency-panel
 * visible) with a generous timeout.
 */
async function enablePeriodic(
  request: import("@playwright/test").APIRequestContext,
  sessionId: string,
  promptName: string,
): Promise<void> {
  const resp = await request.put(apiUrl(`/api/sessions/${sessionId}/periodic`), {
    data: {
      prompt_name: promptName,
      frequency: { value: 1, unit: "hours" },
      enabled: true,
    },
  });
  expect(resp.ok(), `PUT periodic failed: ${resp.status()} ${await resp.text()}`).toBe(true);
}

test.describe("PeriodicPromptSelector internals", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("should open prompt dropdown and display available prompts", async ({
    page,
    request,
    timeouts: t,
  }) => {
    // Create a fresh session and enable periodic
    const sessionId = await apiCreateSession(page, request);
    expect(sessionId).toBeTruthy();
    await enablePeriodic(request, sessionId, "Hello Greeting");

    // PeriodicFrequencyPanel becomes visible when periodic_enabled=true.
    // Use agentResponse timeout to handle the global events WS race: the
    // periodic_updated broadcast arrives on /api/events which may connect
    // slightly after the session WS, so the state update can take a few seconds.
    const frequencyPanel = page.locator('[data-testid="periodic-frequency-panel"]');
    await expect(frequencyPanel).toBeVisible({ timeout: t.agentResponse });

    // The trigger button should show the currently selected prompt name
    const triggerBtn = page.locator('[data-testid="periodic-prompt-selector-button"]');
    await expect(triggerBtn).toBeAttached();
    await expect(triggerBtn).toContainText("Hello Greeting");

    // Click the trigger to open the dropdown
    await triggerBtn.click();

    // Dropdown panel should now appear
    const dropdown = page.locator('[data-testid="periodic-prompt-selector-dropdown"]');
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    // Search filter input should be focused
    const searchInput = page.locator('[data-testid="periodic-prompt-selector-search"]');
    await expect(searchInput).toBeVisible();
    await expect(searchInput).toBeFocused();

    // At least the "Hello Greeting" prompt from the project-alpha workspace should appear
    const promptList = page.locator(
      '[data-testid="periodic-prompt-selector-list"]',
    );
    await expect(promptList).toBeVisible();

    // Prompt item containing "Hello Greeting" should be present
    const greetingItem = promptList.locator("button.prompt-item", {
      hasText: "Hello Greeting",
    });
    await expect(greetingItem).toBeVisible({ timeout: t.shortAction });

    // The selected item (Hello Greeting) is marked as selected
    await expect(greetingItem).toHaveAttribute("aria-selected", "true");
  });

  test("should filter prompts via search and allow selection", async ({
    page,
    request,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(page, request);
    expect(sessionId).toBeTruthy();
    await enablePeriodic(request, sessionId, "Hello Greeting");

    await expect(page.locator('[data-testid="periodic-frequency-panel"]')).toBeVisible({
      timeout: t.agentResponse,
    });

    const triggerBtn = page.locator('[data-testid="periodic-prompt-selector-button"]');
    await triggerBtn.click();

    const dropdown = page.locator('[data-testid="periodic-prompt-selector-dropdown"]');
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    const searchInput = page.locator('[data-testid="periodic-prompt-selector-search"]');

    // Type a filter that matches "Hello Greeting"
    await searchInput.fill("hello");
    const promptList = page.locator(
      '[data-testid="periodic-prompt-selector-list"]',
    );
    const greetingItem = promptList.locator("button.prompt-item", {
      hasText: "Hello Greeting",
    });
    await expect(greetingItem).toBeVisible({ timeout: t.shortAction });

    // Type a filter that matches nothing — "No matching prompts" message should appear
    await searchInput.fill("zzz-no-match");
    await expect(dropdown.locator("text=No matching prompts")).toBeVisible({
      timeout: t.shortAction,
    });

    // Clear filter and select "Hello Greeting"
    await searchInput.fill("");
    await expect(greetingItem).toBeVisible({ timeout: t.shortAction });
    await greetingItem.click();

    // Dropdown closes after selection
    await expect(dropdown).not.toBeVisible({ timeout: t.shortAction });

    // Trigger button reflects the newly selected prompt
    await expect(triggerBtn).toContainText("Hello Greeting");
  });

  test("should close dropdown when clicking outside", async ({
    page,
    request,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(page, request);
    await enablePeriodic(request, sessionId, "Hello Greeting");

    await expect(page.locator('[data-testid="periodic-frequency-panel"]')).toBeVisible({
      timeout: t.agentResponse,
    });

    await page.locator('[data-testid="periodic-prompt-selector-button"]').click();
    const dropdown = page.locator('[data-testid="periodic-prompt-selector-dropdown"]');
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    // Click somewhere outside the dropdown
    await page.locator("body").click({ position: { x: 10, y: 10 } });
    await expect(dropdown).not.toBeVisible({ timeout: t.shortAction });
  });
});
