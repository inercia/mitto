import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { timeouts, apiUrl, selectors } from "../utils/selectors";

/**
 * LoopPromptSelector internals tests.
 *
 * Regression net: catches a daisyUI restyle breaking:
 *   - The selector dropdown opening/closing
 *   - Prompt list rendering (grouped/ungrouped)
 *   - Search filter narrowing the list
 *   - Prompt selection updating the displayed name
 *
 * The existing loop-prompt-pill.spec.ts only tests the pill trigger
 * (NamedPromptPill) after a loop run. This spec goes deeper, opening
 * the LoopPromptSelector *picker* dropdown itself.
 *
 * Setup:
 *   - Uses the project-alpha workspace fixture which has a "Hello Greeting"
 *     prompt in .mitto/prompts/greeting.md
 *   - Configures loop via PUT /api/sessions/:id/loop so the UI
 *     renders LoopPromptSelector with isOpen=true.
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
 * Configure a session loop with one REST request. The settings panel stays
 * collapsed until the user opens it through the LoopControlBar.
 */
async function enableLoop(
  request: import("@playwright/test").APIRequestContext,
  sessionId: string,
  promptName: string,
): Promise<void> {
  const resp = await request.put(apiUrl(`/api/sessions/${sessionId}/loop`), {
    data: {
      prompt_name: promptName,
      frequency: { value: 1, unit: "hours" },
      enabled: true,
    },
  });
  expect(
    resp.ok(),
    `PUT loop failed: ${resp.status()} ${await resp.text()}`,
  ).toBe(true);
}

async function openLoopSettings(
  page: import("@playwright/test").Page,
  timeout: number,
): Promise<void> {
  const settingsButton = page.getByTestId("loop-open-settings");
  await expect(settingsButton).toBeVisible({ timeout });
  await settingsButton.click();
}

test.describe("LoopPromptSelector internals", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("should open prompt dropdown and display available prompts", async ({
    page,
    request,
    timeouts: t,
  }) => {
    // Create a fresh session and enable loop
    const sessionId = await apiCreateSession(page, request);
    expect(sessionId).toBeTruthy();
    await enableLoop(request, sessionId, "Hello Greeting");
    await openLoopSettings(page, t.agentResponse);

    // The settings panel is open, so its prompt selector controls can be tested.
    const loopSettingsTab = page.getByTestId("loop-settings-tab");
    await expect(loopSettingsTab).toBeVisible({ timeout: t.agentResponse });

    // The trigger button should show the currently selected prompt name
    const triggerBtn = loopSettingsTab.getByTestId(
      "loop-settings-prompt-button",
    );
    await expect(triggerBtn).toBeAttached();
    await expect(triggerBtn).toContainText("Hello Greeting");

    // Click the trigger to open the dropdown
    await triggerBtn.click();

    // Dropdown panel should now appear
    const dropdown = loopSettingsTab.getByTestId(
      "loop-settings-prompt-dropdown",
    );
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    // Search filter input should be focused
    const searchInput = loopSettingsTab.getByTestId(
      "loop-settings-prompt-search",
    );
    await expect(searchInput).toBeVisible();
    await expect(searchInput).toBeFocused();

    // At least the "Hello Greeting" prompt from the project-alpha workspace should appear
    const promptList = loopSettingsTab.getByTestId("loop-settings-prompt-list");
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
    await enableLoop(request, sessionId, "Hello Greeting");
    await openLoopSettings(page, t.agentResponse);

    const loopSettingsTab = page.getByTestId("loop-settings-tab");
    await expect(loopSettingsTab).toBeVisible({
      timeout: t.agentResponse,
    });

    const triggerBtn = loopSettingsTab.getByTestId(
      "loop-settings-prompt-button",
    );
    await triggerBtn.click();

    const dropdown = loopSettingsTab.getByTestId(
      "loop-settings-prompt-dropdown",
    );
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    const searchInput = loopSettingsTab.getByTestId(
      "loop-settings-prompt-search",
    );

    // Type a filter that matches "Hello Greeting"
    await searchInput.fill("hello");
    const promptList = loopSettingsTab.getByTestId("loop-settings-prompt-list");
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
    await enableLoop(request, sessionId, "Hello Greeting");
    await openLoopSettings(page, t.agentResponse);

    const loopSettingsTab = page.getByTestId("loop-settings-tab");
    await expect(loopSettingsTab).toBeVisible({
      timeout: t.agentResponse,
    });

    await loopSettingsTab.getByTestId("loop-settings-prompt-button").click();
    const dropdown = loopSettingsTab.getByTestId(
      "loop-settings-prompt-dropdown",
    );
    await expect(dropdown).toBeVisible({ timeout: t.shortAction });

    // Click somewhere outside the dropdown
    await page.locator("body").click({ position: { x: 10, y: 10 } });
    await expect(dropdown).not.toBeVisible({ timeout: t.shortAction });
  });
});
