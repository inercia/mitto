import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * Loop "edit arguments" button tests (mitto-2eu).
 *
 * Covers the SlidersIcon button rendered next to the LoopPromptSelector in the
 * conversation side panel's Loop tab:
 *   - Enabled when the selected loop prompt declares parameters; clicking
 *     opens the shared PromptParameterDialog.
 *   - Submitting the dialog stages arguments; the Loop tab Save action PATCHes
 *     /api/sessions/:id/loop with { arguments }.
 *   - Reopening the dialog pre-seeds the previously-saved value (initialValues).
 *   - Disabled when the selected prompt declares no parameters.
 *
 * Fixtures (project-alpha workspace):
 *   loop-param-prompt.prompt.yaml  ("Loop Param Test", menus: promptsLoop, TASK: text optional)
 *   greeting.prompt.yaml               ("Hello Greeting", no parameters)
 *
 * Note: the loop selector only lists a prompt when menuSatisfies() holds for
 * the promptsLoop menu, which auto-supplies no parameter types. A prompt with
 * a REQUIRED text param would therefore be hidden from the selector entirely, so
 * the editable-args case necessarily uses an optional (or boolean) parameter.
 */

const PARAM_PROMPT = "Loop Param Test";
const NO_PARAM_PROMPT = "Hello Greeting";
const EDIT_ARGS_BTN = '[data-testid="loop-edit-args-button"]';
const DIALOG = '[data-testid="prompt-param-dialog"]';

async function apiCreateSession(
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  return (await resp.json()).session_id;
}

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
      max_iterations: 10,
      triggers: ["schedule"],
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
  await expect(page.locator('[data-testid="loop-control-bar"]')).toBeVisible({
    timeout,
  });
  await page.locator('[data-testid="loop-open-settings"]').click();
  await expect(page.locator('[data-testid="loop-settings-tab"]')).toBeVisible({
    timeout,
  });
}

test.describe("Loop edit-arguments button", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("opens dialog, saves arguments, and re-seeds them on reopen", async ({
    page,
    request,
    helpers,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(request);
    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
    await enableLoop(request, sessionId, PARAM_PROMPT);
    await openLoopSettings(page, t.agentResponse);

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

    // Dialog submission stages the argument inside the Loop editor.
    await page.locator('[data-testid="prompt-param-save-btn"]').click();
    await expect(page.locator(DIALOG)).not.toBeVisible({
      timeout: t.shortAction,
    });

    // Reopening before the Loop save pre-seeds the staged value.
    await editBtn.click();
    await expect(page.locator(DIALOG)).toBeVisible({ timeout: t.shortAction });
    await expect(page.locator(`${DIALOG} textarea`)).toHaveValue(
      "nightly cleanup",
    );
    await page.locator('[data-testid="prompt-param-save-btn"]').click();

    // The Loop tab Save action persists the staged arguments map.
    const [patchReq] = await Promise.all([
      page.waitForRequest(
        (req) =>
          req.url().includes(`/api/sessions/${sessionId}/loop`) &&
          req.method() === "PATCH",
        { timeout: t.appReady },
      ),
      page.locator('[data-testid="loop-save-button"]').click(),
    ]);
    const body = JSON.parse(patchReq.postData() || "{}");
    expect(body.arguments?.TASK).toBe("nightly cleanup");
  });

  test("button is disabled when the selected prompt has no parameters", async ({
    page,
    request,
    helpers,
    timeouts: t,
  }) => {
    const sessionId = await apiCreateSession(request);
    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
    await enableLoop(request, sessionId, NO_PARAM_PROMPT);
    await openLoopSettings(page, t.agentResponse);

    // The button renders but is disabled (Hello Greeting declares no params)
    const editBtn = page.locator(EDIT_ARGS_BTN);
    await expect(editBtn).toBeVisible({ timeout: t.shortAction });
    await expect(editBtn).toBeDisabled();

    // Clicking a disabled button must not open the dialog
    await editBtn.click({ force: true });
    await expect(page.locator(DIALOG)).not.toBeVisible({ timeout: 1500 });
  });
});
