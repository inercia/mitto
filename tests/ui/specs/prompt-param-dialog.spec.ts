import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Prompt Parameter Dialog — Playwright E2E coverage (mitto-hcf.3)
 *
 * Tests the beadsIssues → PromptParameterDialog → dispatch flow:
 *   1. A prompt with auto-filled beadsId + required text param opens the dialog;
 *      only the free-text field is shown (not the auto-filled one).
 *   2. Submitting the dialog dispatches with merged auto+user arguments.
 *   3. A prompt with NO missing params dispatches directly without a dialog.
 *   4. Cancelling (Close) does NOT dispatch.
 *
 * Fixtures:
 *   beads-issue-prompt.prompt.yaml   (group: Task, no parameters → no dialog)
 *   beads-issue-param-prompt.prompt.yaml (group: Param, beadsId + text → dialog)
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// ContextMenu.js renders the beads context menu with these classes (inline z-index, no z-50).
const BEADS_MENU = ".menu.rounded-box.shadow-xl.fixed";

const MOCK_ISSUES = [
  {
    id: "mitto-aaa",
    title: "Alpha issue",
    description: "Test issue for param dialog E2E.",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

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
  await folderDetails.locator('[title^="Beads issues:"]').first().click();
}

/** Opens the context menu for the first issue row and selects a prompt by group + name. */
async function selectBeadsPrompt(page, timeouts, groupText: string, promptText: string) {
  await expect(page.getByText("Alpha issue").first()).toBeVisible({ timeout: timeouts.appReady });

  const issueMenuBtn = page.locator('[data-testid="beads-issue-menu"]').first();
  await expect(issueMenuBtn).toBeVisible({ timeout: timeouts.shortAction });
  await issueMenuBtn.click();

  const mainMenu = page.locator(BEADS_MENU).first();
  await expect(mainMenu).toBeVisible({ timeout: timeouts.shortAction });

  const groupBtn = page.locator(`${BEADS_MENU} button`).filter({ hasText: groupText }).first();
  await expect(groupBtn).toBeVisible({ timeout: timeouts.appReady });
  await groupBtn.dispatchEvent("mouseenter");

  const submenu = page.locator(BEADS_MENU).nth(1);
  await expect(submenu).toBeVisible({ timeout: timeouts.shortAction });

  const promptBtn = submenu.locator("button").filter({ hasText: promptText }).first();
  await expect(promptBtn).toBeVisible({ timeout: timeouts.shortAction });
  await promptBtn.click();
}

testWithCleanup.describe("PromptParameterDialog — beadsIssues invocation flow", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(MOCK_ISSUES) });
    });
    await request.post(apiUrl("/api/workspaces"), { data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA } });
    const resp = await request.post(apiUrl("/api/sessions"), { data: { name: `PPD-${Date.now()}`, working_dir: WORKSPACE_ALPHA } });
    expect(resp.ok()).toBeTruthy();
    const seedId = (await resp.json()).session_id;
    await page.addInitScript((sid) => {
      localStorage.setItem("mitto_last_session_id", sid);
      localStorage.removeItem("mitto_conversation_filter_tab");
    }, seedId);
    await helpers.navigateAndWait(page);
  });

  // ── Test 1: dialog opens for prompt with missing params ───────────────────
  testWithCleanup(
    "opens PromptParameterDialog for prompt with required text param",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await selectBeadsPrompt(page, timeouts, "Param", "Beads Param Test");

      // Dialog must appear
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      // The free-text CONDITION field must be present (textarea for type=text)
      const conditionField = page.locator('[data-testid="prompt-param-dialog"] textarea');
      await expect(conditionField).toBeVisible({ timeout: timeouts.shortAction });

      // The dialog title reflects the prompt name
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toContainText("Beads Param Test");

      // The auto-filled ISSUE_ID field must NOT appear (beadsId is auto-filled by the menu)
      // The dialog receives only the MISSING params, so only CONDITION is shown
      const fieldsets = page.locator('[data-testid="prompt-param-dialog"] fieldset');
      await expect(fieldsets).toHaveCount(1);
    },
  );

  // ── Test 2: submit dispatches with merged auto + user args ────────────────
  testWithCleanup(
    "submitting the dialog dispatches with merged auto-filled and user-entered arguments",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await selectBeadsPrompt(page, timeouts, "Param", "Beads Param Test");

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Fill the CONDITION textarea
      const conditionField = page.locator('[data-testid="prompt-param-dialog"] textarea');
      await conditionField.fill("must be high priority");

      // Intercept POST /api/sessions and capture the request body
      const [sessionRequest] = await Promise.all([
        page.waitForRequest(
          (req) => req.url().includes("/api/sessions") && req.method() === "POST",
          { timeout: timeouts.appReady },
        ),
        page.locator('[data-testid="prompt-param-save-btn"]').click(),
      ]);

      const body = JSON.parse(sessionRequest.postData() || "{}");
      // Both auto-filled ISSUE_ID and user-entered CONDITION must be present
      expect(body.arguments?.ISSUE_ID).toBe("mitto-aaa");
      expect(body.arguments?.CONDITION).toBe("must be high priority");

      // Dialog should close after submit
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Toast confirms dispatch
      await expect(
        page.getByText('Started "Beads Param Test" for mitto-aaa'),
      ).toBeVisible({ timeout: timeouts.appReady });
    },
  );

  // ── Test 3: prompt with no missing params dispatches without dialog ────────
  testWithCleanup(
    "prompt with no missing params dispatches directly — no dialog shown",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);

      // Intercept POST /api/sessions to verify dispatch happens
      const sessionRequestPromise = page.waitForRequest(
        (req) => req.url().includes("/api/sessions") && req.method() === "POST",
        { timeout: timeouts.appReady },
      );

      // "Beads Issue Task" has no parameters — no dialog should open
      await selectBeadsPrompt(page, timeouts, "Task", "Beads Issue Task");

      // Dialog must NOT appear
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: 2000,
      });

      // Dispatch should happen automatically
      await sessionRequestPromise;

      // Toast confirms dispatch
      await expect(
        page.getByText('Started "Beads Issue Task" for mitto-aaa'),
      ).toBeVisible({ timeout: timeouts.appReady });
    },
  );

  // ── Test 4: cancelling the dialog does not dispatch ───────────────────────
  testWithCleanup(
    "cancelling the dialog (Close) does not dispatch",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await selectBeadsPrompt(page, timeouts, "Param", "Beads Param Test");

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Track whether any POST /api/sessions fires after cancel
      let sessionDispatched = false;
      page.on("request", (req) => {
        if (req.url().includes("/api/sessions") && req.method() === "POST") {
          sessionDispatched = true;
        }
      });

      // Click Close (dismiss)
      await page.locator('[data-testid="prompt-param-close-btn"]').click();

      // Dialog closes
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Wait a bit to ensure no delayed dispatch fires
      await page.waitForTimeout(1500);
      expect(sessionDispatched).toBe(false);
    },
  );
});

// =============================================================================
// Conversation-menu invocation flow
// =============================================================================

/**
 * Conversation context menu (right-click) → PromptParameterDialog → dispatch
 *
 * Fixtures:
 *   context-menu-prompt.prompt.yaml       (group: Workflow, no parameters → no dialog)
 *   context-menu-param-prompt.prompt.yaml (group: ConvoParam, TASK: text → dialog)
 */

// ContextMenu renders as a fixed daisyUI menu with inline z-index (no z-50 class).
const CONVO_MENU = ".menu.rounded-box.shadow-xl.fixed";
const CONVO_PARAM_GROUP = "ConvoParam";
const CONVO_PARAM_PROMPT = "Convo Param Test";
const CONVO_NO_PARAM_GROUP = "Workflow";
const CONVO_NO_PARAM_PROMPT = "Context Menu Test";

/** Right-clicks a session item by sessionId, returns the menu locator. */
async function openConvoMenu(page, timeouts, sessionId: string) {
  const sessionItem = page.locator(`[data-session-id="${sessionId}"]`).first();
  await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
  await sessionItem.click({ button: "right" });
  const menu = page.locator(CONVO_MENU).first();
  await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
  return menu;
}

/** Hovers a group button and clicks the named prompt in the submenu. */
async function selectConvoPrompt(page, timeouts, groupText: string, promptText: string) {
  const menuButtons = page.locator(`${CONVO_MENU} button`);
  const groupItem = menuButtons.filter({ hasText: groupText }).first();
  await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });
  await groupItem.hover();
  const submenu = page.locator(CONVO_MENU).nth(1);
  await expect(submenu).toBeVisible({ timeout: timeouts.shortAction });
  const promptBtn = submenu.locator("button").filter({ hasText: promptText }).first();
  await expect(promptBtn).toBeVisible({ timeout: timeouts.shortAction });
  await promptBtn.click();
}

testWithCleanup.describe("PromptParameterDialog — conversation-menu invocation flow", () => {
  let sessionId: string;

  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `PPD-Conv-${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();
    sessionId = (await createResp.json()).session_id;

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);
  });

  // ── Test 1: dialog opens for conversation-menu prompt with missing params ──
  testWithCleanup(
    "opens dialog for conversation-menu prompt with a required text param",
    async ({ page, timeouts }) => {
      await openConvoMenu(page, timeouts, sessionId);
      await selectConvoPrompt(page, timeouts, CONVO_PARAM_GROUP, CONVO_PARAM_PROMPT);

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      // One fieldset (only TASK is missing; it has type=text)
      const fieldsets = page.locator('[data-testid="prompt-param-dialog"] fieldset');
      await expect(fieldsets).toHaveCount(1);

      // Dialog title reflects the prompt name
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toContainText(CONVO_PARAM_PROMPT);
    },
  );

  // ── Test 2: submit dispatches queue POST with arguments ───────────────────
  testWithCleanup(
    "submitting the dialog dispatches a queue POST including arguments",
    async ({ page, timeouts }) => {
      await openConvoMenu(page, timeouts, sessionId);
      await selectConvoPrompt(page, timeouts, CONVO_PARAM_GROUP, CONVO_PARAM_PROMPT);

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Fill the TASK textarea
      const taskField = page.locator('[data-testid="prompt-param-dialog"] textarea');
      await taskField.fill("review the PR");

      // Intercept POST to the session queue
      const [queueRequest] = await Promise.all([
        page.waitForRequest(
          (req) => req.url().includes(`/api/sessions/${sessionId}/queue`) && req.method() === "POST",
          { timeout: timeouts.appReady },
        ),
        page.locator('[data-testid="prompt-param-save-btn"]').click(),
      ]);

      const body = JSON.parse(queueRequest.postData() || "{}");
      expect(body.prompt_name).toBe(CONVO_PARAM_PROMPT);
      expect(body.arguments?.TASK).toBe("review the PR");

      // Dialog closes
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: timeouts.shortAction,
      });

      // Success toast
      await expect(
        page.getByText(`Sent "${CONVO_PARAM_PROMPT}" to conversation`),
      ).toBeVisible({ timeout: timeouts.appReady });
    },
  );

  // ── Test 3: no-missing-params prompt dispatches directly, no dialog ────────
  testWithCleanup(
    "conversation-menu prompt with no missing params dispatches directly — no dialog shown",
    async ({ page, timeouts }) => {
      await openConvoMenu(page, timeouts, sessionId);

      // Intercept the queue POST before clicking so we don't miss it
      const queueRequestPromise = page.waitForRequest(
        (req) => req.url().includes(`/api/sessions/${sessionId}/queue`) && req.method() === "POST",
        { timeout: timeouts.appReady },
      );

      await selectConvoPrompt(page, timeouts, CONVO_NO_PARAM_GROUP, CONVO_NO_PARAM_PROMPT);

      // Dialog must NOT appear
      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: 2000,
      });

      // Queue POST fires without dialog
      await queueRequestPromise;

      // Success toast
      await expect(
        page.getByText(`Sent "${CONVO_NO_PARAM_PROMPT}" to conversation`),
      ).toBeVisible({ timeout: timeouts.appReady });
    },
  );

  // ── Test 4: cancelling the dialog does not dispatch ───────────────────────
  testWithCleanup(
    "cancelling the conversation-menu param dialog does not dispatch",
    async ({ page, timeouts }) => {
      await openConvoMenu(page, timeouts, sessionId);
      await selectConvoPrompt(page, timeouts, CONVO_PARAM_GROUP, CONVO_PARAM_PROMPT);

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).toBeVisible({
        timeout: timeouts.shortAction,
      });

      let dispatched = false;
      page.on("request", (req) => {
        if (req.url().includes(`/api/sessions/${sessionId}/queue`) && req.method() === "POST") {
          dispatched = true;
        }
      });

      await page.locator('[data-testid="prompt-param-close-btn"]').click();

      await expect(page.locator('[data-testid="prompt-param-dialog"]')).not.toBeVisible({
        timeout: timeouts.shortAction,
      });

      await page.waitForTimeout(1500);
      expect(dispatched).toBe(false);
    },
  );
});
