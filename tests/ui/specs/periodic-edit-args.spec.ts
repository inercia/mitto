import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { timeouts, apiUrl, selectors } from "../utils/selectors";

/**
 * Periodic "edit arguments" button tests (mitto-2eu).
 *
 * Covers the SlidersIcon button rendered next to the PeriodicPromptSelector:
 *   - Enabled when the selected periodic prompt declares parameters; clicking
 *     opens the shared PromptParameterDialog.
 *   - Submitting the dialog PATCHes /api/sessions/:id/periodic with { arguments }.
 *   - Reopening the dialog pre-seeds the previously-saved value (initialValues).
 *   - Disabled when the selected prompt declares no parameters.
 *
 * Fixtures (project-alpha workspace):
 *   periodic-param-prompt.prompt.yaml  ("Periodic Param Test", menus: promptsPeriodic, TASK: text optional)
 *   greeting.prompt.yaml               ("Hello Greeting", no parameters)
 *
 * Note: the periodic selector only lists a prompt when menuSatisfies() holds for
 * the promptsPeriodic menu, which auto-supplies no parameter types. A prompt with
 * a REQUIRED text param would therefore be hidden from the selector entirely, so
 * the editable-args case necessarily uses an optional (or boolean) parameter.
 */

const PARAM_PROMPT = "Periodic Param Test";
const NO_PARAM_PROMPT = "Hello Greeting";
const EDIT_ARGS_BTN = '[data-testid="periodic-edit-args-button"]';
const DIALOG = '[data-testid="prompt-param-dialog"]';

async function apiCreateSession(
  page: import("@playwright/test").Page,
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  const id: string = (await resp.json()).session_id;
  await page.evaluate((sid) => {
    localStorage.setItem("mitto_last_session_id", sid);
    localStorage.removeItem("mitto_conversation_filter_tab");
  }, id);
  await page.reload();
  await expect(page.locator(selectors.chatInput)).toHaveAttribute(
    "placeholder",
    /Type your message/,
    { timeout: timeouts.agentResponse },
  );
  return id;
}

async function enablePeriodic(
  request: import("@playwright/test").APIRequestContext,
  sessionId: string,
  promptName: string,
): Promise<void> {
  const resp = await request.put(apiUrl(`/api/sessions/${sessionId}/periodic`), {
    data: { prompt_name: promptName, frequency: { value: 1, unit: "hours" }, enabled: true },
  });
  expect(resp.ok(), `PUT periodic failed: ${resp.status()} ${await resp.text()}`).toBe(true);
}

test.describe("Periodic edit-arguments button", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("opens dialog, saves arguments, and re-seeds them on reopen", async ({
    page,
    request,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(page, request);
    await enablePeriodic(request, sessionId, PARAM_PROMPT);

    await expect(page.locator('[data-testid="periodic-frequency-panel"]')).toBeVisible({
      timeout: t.agentResponse,
    });

    // Button is present and enabled (selected prompt declares a parameter)
    const editBtn = page.locator(EDIT_ARGS_BTN);
    await expect(editBtn).toBeVisible({ timeout: t.shortAction });
    await expect(editBtn).toBeEnabled();

    // Clicking opens the shared PromptParameterDialog, titled after the prompt
    await editBtn.click();
    await expect(page.locator(DIALOG)).toBeVisible({ timeout: t.shortAction });
    await expect(page.locator(DIALOG)).toContainText(PARAM_PROMPT);

    // type=text renders a textarea; it starts empty (no stored arguments yet)
    const taskField = page.locator(`${DIALOG} textarea`);
    await expect(taskField).toBeVisible({ timeout: t.shortAction });
    await expect(taskField).toHaveValue("");
    await taskField.fill("nightly cleanup");

    // Submitting PATCHes the periodic config with the arguments map
    const [patchReq] = await Promise.all([
      page.waitForRequest(
        (req) =>
          req.url().includes(`/api/sessions/${sessionId}/periodic`) &&
          req.method() === "PATCH",
        { timeout: t.appReady },
      ),
      page.locator('[data-testid="prompt-param-save-btn"]').click(),
    ]);
    const body = JSON.parse(patchReq.postData() || "{}");
    expect(body.arguments?.TASK).toBe("nightly cleanup");

    // Dialog closes after submit
    await expect(page.locator(DIALOG)).not.toBeVisible({ timeout: t.shortAction });

    // Reopening the dialog pre-seeds the previously-saved value (initialValues)
    await editBtn.click();
    await expect(page.locator(DIALOG)).toBeVisible({ timeout: t.shortAction });
    await expect(page.locator(`${DIALOG} textarea`)).toHaveValue("nightly cleanup");
  });

  test("button is disabled when the selected prompt has no parameters", async ({
    page,
    request,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(page, request);
    await enablePeriodic(request, sessionId, NO_PARAM_PROMPT);

    await expect(page.locator('[data-testid="periodic-frequency-panel"]')).toBeVisible({
      timeout: t.agentResponse,
    });

    // The button renders but is disabled (Hello Greeting declares no params)
    const editBtn = page.locator(EDIT_ARGS_BTN);
    await expect(editBtn).toBeVisible({ timeout: t.shortAction });
    await expect(editBtn).toBeDisabled();

    // Clicking a disabled button must not open the dialog
    await editBtn.click({ force: true });
    await expect(page.locator(DIALOG)).not.toBeVisible({ timeout: 1500 });
  });
});
