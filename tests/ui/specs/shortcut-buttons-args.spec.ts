import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Shortcut buttons — prompt argument support (mitto-6v7m).
 *
 * Locks in the "shortcut button click opens PromptParameterDialog when the
 * linked prompt declares required parameters the shortcut cannot auto-supply,
 * else dispatches directly" behavior at both surviving shortcut sites:
 *
 *   1. Conversation-header shortcut (testId `conversation-shortcut-btn-*`)
 *      → uses handleSendPromptToConversation → seedConversationWithPrompt
 *        (POST /api/sessions/:id/queue).
 *   2. Tasks-view shortcut (testId `beads-shortcut-btn-*`)
 *      → uses handleRunBeadsListPrompt → startConversationWithPrompt
 *        (POST /api/sessions). This is the mitto-cwf5 regression lock.
 *
 * The beadsIssue-detail shortcut (`beads-issue-shortcut-btn-*`) shares the same
 * handleRunBeadsPrompt path exercised by prompt-param-dialog.spec.ts (the
 * per-issue context menu), which already covers the auto-fill + dialog +
 * merged-args behavior; adding a separate detail-panel path here would only
 * duplicate that coverage against a different entry point.
 *
 * Fixtures (project-alpha workspace):
 *   context-menu-param-prompt.prompt.yaml ("Convo Param Test", menus: conversation, TASK required)
 *   beads-list-prompt.prompt.yaml         ("List Prompt Test", menus: beadsList, no params)
 *   loop-param-prompt.prompt.yaml         ("Loop Param Test", menus: promptsLoop, TASK optional)
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

const DIALOG = '[data-testid="prompt-param-dialog"]';
const CONVO_PARAM_PROMPT = "Convo Param Test";
const LIST_PARAM_PROMPT = "Beads List Param Test";

// Mocked issue list so the Tasks view mounts without shelling out to `bd`.
const MOCK_ISSUES = [
  {
    id: "mitto-aaa",
    title: "Alpha issue",
    description: "Test issue for shortcut arg dialog.",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

// =============================================================================
// Conversation-header shortcut button
// =============================================================================

testWithCleanup.describe("Conversation shortcut button — prompt arguments", () => {
  let sessionId: string;

  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Register a single conversations-section folder shortcut pointing at the
    // param-prompt fixture. The frontend merges global + folder shortcuts;
    // global is intentionally empty so `conversation-shortcut-btn-0` maps to
    // the folder entry we just registered.
    await page.route(/\/api\/folders\/shortcuts(\?|$)/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          sections: {
            conversations: [{ prompt: CONVO_PARAM_PROMPT, icon: "" }],
          },
        }),
      });
    });
    await page.route(/\/api\/global\/shortcuts(\?|$)/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ sections: { conversations: [] } }),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Shortcut-Conv-${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();
    sessionId = (await createResp.json()).session_id;

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
  });

  testWithCleanup(
    "clicking a shortcut with a required text param opens the dialog and dispatches with the entered argument",
    async ({ page, timeouts }) => {
      const shortcutBtn = page
        .locator('[data-testid="conversation-shortcut-btn-0"]')
        .first();
      await expect(shortcutBtn).toBeVisible({ timeout: timeouts.appReady });
      await expect(shortcutBtn).not.toBeDisabled();

      await shortcutBtn.click();

      await expect(page.locator(DIALOG)).toBeVisible({ timeout: timeouts.shortAction });
      await expect(page.locator(DIALOG)).toContainText(CONVO_PARAM_PROMPT);

      // Only the missing TASK field is rendered (one fieldset).
      const fieldsets = page.locator(`${DIALOG} fieldset`);
      await expect(fieldsets).toHaveCount(1);

      const taskField = page.locator(`${DIALOG} textarea`).first();
      await taskField.fill("shortcut-supplied task");

      const [queueRequest] = await Promise.all([
        page.waitForRequest(
          (req) =>
            req.url().includes(`/api/sessions/${sessionId}/queue`) &&
            req.method() === "POST",
          { timeout: timeouts.appReady },
        ),
        page.locator('[data-testid="prompt-param-save-btn"]').click(),
      ]);

      const body = JSON.parse(queueRequest.postData() || "{}");
      expect(body.prompt_name).toBe(CONVO_PARAM_PROMPT);
      expect(body.arguments?.TASK).toBe("shortcut-supplied task");

      // Dialog closes after submit.
      await expect(page.locator(DIALOG)).not.toBeVisible({
        timeout: timeouts.shortAction,
      });
    },
  );

  testWithCleanup(
    "cancelling the shortcut param dialog does not dispatch",
    async ({ page, timeouts }) => {
      const shortcutBtn = page
        .locator('[data-testid="conversation-shortcut-btn-0"]')
        .first();
      await expect(shortcutBtn).toBeVisible({ timeout: timeouts.appReady });
      await shortcutBtn.click();

      await expect(page.locator(DIALOG)).toBeVisible({ timeout: timeouts.shortAction });

      let dispatched = false;
      page.on("request", (req) => {
        if (
          req.url().includes(`/api/sessions/${sessionId}/queue`) &&
          req.method() === "POST"
        ) {
          dispatched = true;
        }
      });

      await page.locator('[data-testid="prompt-param-close-btn"]').click();
      await expect(page.locator(DIALOG)).not.toBeVisible({
        timeout: timeouts.shortAction,
      });

      await page.waitForTimeout(1500);
      expect(dispatched).toBe(false);
    },
  );
});

// =============================================================================
// Tasks-view (beads list) shortcut button — mitto-cwf5 regression lock
// =============================================================================

async function clickBeadsButton(page, timeouts) {
  const folderHeader = page
    .locator('summary[data-has-context-menu="true"]')
    .filter({ hasText: "project-alpha" })
    .first();
  await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });
  const folderDetails = folderHeader.locator("xpath=ancestor::details[1]");
  if (!(await folderDetails.evaluate((el: HTMLDetailsElement) => el.open))) {
    await folderHeader.click();
  }
  await folderDetails
    .locator('[role="button"][title^="Beads issues:"]')
    .first()
    .click();
}

testWithCleanup.describe("Tasks-view shortcut button — prompt arguments", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route(/\/api\/issues(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES),
      });
    });

    // Register a tasksList folder shortcut pointing at the param-prompt fixture.
    await page.route(/\/api\/folders\/shortcuts(\?|$)/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          sections: {
            tasksList: [{ prompt: LIST_PARAM_PROMPT, icon: "" }],
          },
        }),
      });
    });
    await page.route(/\/api\/global\/shortcuts(\?|$)/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ sections: { tasksList: [] } }),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Shortcut-List-${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "tasksList shortcut with a required param opens the dialog and dispatches with the entered argument (mitto-cwf5)",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText("Alpha issue").first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      const shortcutBtn = page
        .locator('[data-testid="beads-shortcut-btn-0"]')
        .first();
      await expect(shortcutBtn).toBeVisible({ timeout: timeouts.shortAction });
      await expect(shortcutBtn).not.toBeDisabled();
      await shortcutBtn.click();

      await expect(page.locator(DIALOG)).toBeVisible({ timeout: timeouts.shortAction });
      await expect(page.locator(DIALOG)).toContainText(LIST_PARAM_PROMPT);

      // Only the missing FOCUS field is rendered.
      const fieldsets = page.locator(`${DIALOG} fieldset`);
      await expect(fieldsets).toHaveCount(1);

      await page.locator(`${DIALOG} textarea`).first().fill("high priority tasks");

      // tasksList shortcuts spawn a NEW root conversation via
      // startConversationWithPrompt → POST /api/sessions with { arguments }.
      const [sessionRequest] = await Promise.all([
        page.waitForRequest(
          (req) => req.url().includes("/api/sessions") && req.method() === "POST",
          { timeout: timeouts.appReady },
        ),
        page.locator('[data-testid="prompt-param-save-btn"]').click(),
      ]);

      const body = JSON.parse(sessionRequest.postData() || "{}");
      expect(body.arguments?.FOCUS).toBe("high priority tasks");

      await expect(page.locator(DIALOG)).not.toBeVisible({
        timeout: timeouts.shortAction,
      });
    },
  );
});
