import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Named Prompt Menu Send — Playwright coverage
 *
 * Epic mitto-xw6 unified all menu-driven prompt sends onto a single
 * "named-prompt" mechanism: menus seed a conversation by PROMPT NAME
 * (rendered as a NamedPromptPill), NOT the full prompt body.
 *
 * This spec proves each menu surface seeds by name, not body text:
 *   1. Conversation context menu   (menus: conversation)
 *   2. Beads issues menu           (menus: beadsIssues + ISSUE_ID substitution)
 *   3. Beads list / Tasks menu     (menus: beadsList)
 *   4. Cmd+/ predefined prompts    (regression: still works as before)
 *
 * Fixtures used:
 *   - Prompt: context-menu-prompt.md        (name: "Context Menu Test", menus: conversation)
 *   - Prompt: beads-issue-prompt.md         (name: "Beads Issue Task", menus: beadsIssues)
 *   - Prompt: beads-list-prompt.md          (name: "Beads List Review", menus: beadsList)
 *   - Prompt: greeting.md                   (name: "Hello Greeting", no menus gate)
 *   - Mock ACP: beads-issue-task.json       (matches "mitto-aaa" → echoes issue ID)
 *   - Mock ACP: simple-greeting.json        (matches "hello" → greeting reply)
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// daisyUI fixed context menus share this class combination.
const MENU = ".menu.fixed.z-50.shadow-xl";

// Issue used for beadsIssues surface; id must match beads-issue-task.json pattern.
const MOCK_ISSUES = [
  {
    id: "mitto-aaa",
    title: "Alpha issue",
    description: "Test issue for named-prompt menu sends.",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

/**
 * Opens the Beads view from the project-alpha folder button.
 *
 * The Tasks entry is rendered as `<div role="button" title="Beads issues: …">`
 * (not a `<button>`), so we use the attribute selector `[title^="Beads issues:"]`
 * which matches any element regardless of tag.
 */
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
  // Use attribute selector (not tag) because the element is <div role="button">,
  // not a native <button>.
  await folderDetails
    .locator('[title^="Beads issues:"]')
    .first()
    .click();
}

// ─────────────────────────────────────────────────────────────────────────────
// Surface 1: conversation context menu
// ─────────────────────────────────────────────────────────────────────────────

testWithCleanup.describe(
  "Named Prompt Menu Sends — Surface 1: conversation context menu",
  () => {
    let sessionId: string;

    testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
      await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
      });
      const resp = await request.post(apiUrl("/api/sessions"), {
        data: { name: `NPMM-Ctx-${Date.now()}`, working_dir: WORKSPACE_ALPHA },
      });
      expect(resp.ok()).toBeTruthy();
      const created = await resp.json();
      sessionId = created.session_id || created.id;
      expect(sessionId).toBeTruthy();

      await helpers.navigateAndWait(page);
      await helpers.navigateToSession(page, sessionId);
    });

    testWithCleanup(
      "context menu seeds by prompt name — pill visible, body text absent",
      async ({ page, timeouts }) => {
        // Open the session's context menu via right-click on the sidebar item.
        const sessionItem = page
          .locator(`[data-session-id="${sessionId}"]`)
          .first();
        await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
        await sessionItem.click({ button: "right" });

        const menu = page.locator(MENU).first();
        await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

        // Hover the "Workflow" group to reveal the nested prompt.
        const menuButtons = page.locator(`${MENU} button`);
        const groupItem = menuButtons.filter({ hasText: "Workflow" });
        await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });
        await groupItem.hover();

        const promptItem = menuButtons.filter({ hasText: "Context Menu Test" });
        await expect(promptItem).toBeVisible({ timeout: timeouts.shortAction });
        await promptItem.click();

        // Toast confirms the named prompt was sent to the queue.
        await expect(
          page.getByText('Sent "Context Menu Test" to conversation'),
        ).toBeVisible({ timeout: timeouts.appReady });

        // Best-effort: check queue dropdown label while item is pending.
        // The item may dispatch very quickly on an idle session; if so, skip.
        try {
          const queueToggle = page.locator('[data-queue-toggle]');
          await queueToggle.waitFor({ state: "visible", timeout: 3_000 });
          await queueToggle.click();
          const queueItem = page.locator('[data-testid="queue-item"]').first();
          await expect(
            queueItem.locator(".queue-item-text"),
          ).toContainText("Context Menu Test", { timeout: 2_000 });
          await queueToggle.click(); // close
        } catch {
          // Item already dispatched before queue check — acceptable.
        }

        // Primary: NamedPromptPill must appear in the transcript.
        await expect(
          page
            .locator('[data-testid="named-prompt-pill"]')
            .filter({ hasText: "Context Menu Test" }),
        ).toBeVisible({ timeout: 15_000 });

        // The full body text must NOT appear as a user message bubble.
        await expect(
          page.getByText(
            "This prompt is used by the conversation context-menu UI test.",
          ),
        ).toHaveCount(0);
      },
    );
  },
);

// ─────────────────────────────────────────────────────────────────────────────
// Surface 2: beads issues menu (menus: beadsIssues) + ISSUE_ID substitution
// ─────────────────────────────────────────────────────────────────────────────

testWithCleanup.describe(
  "Named Prompt Menu Sends — Surface 2: beads issues menu",
  () => {
    testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
      // Mock the beads list so the table renders without the external `bd` binary.
      await page.route("**/api/beads/list**", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(MOCK_ISSUES),
        });
      });

      await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
      });
      const resp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `NPMM-Beads-${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(resp.ok()).toBeTruthy();
      const seedId = (await resp.json()).session_id;

      // Pre-select the seed session so the app focuses on project-alpha on load,
      // which causes the folder to auto-expand and the Tasks (Beads) button to render.
      await page.addInitScript((sid) => {
        localStorage.setItem("mitto_last_session_id", sid);
        localStorage.removeItem("mitto_conversation_filter_tab");
      }, seedId);

      await helpers.navigateAndWait(page);
    });

    testWithCleanup(
      "starts new conversation seeded with pill; ISSUE_ID substituted in agent response",
      async ({ page, timeouts }) => {
        await clickBeadsButton(page, timeouts);
        // Wait for the issue row to appear.
        await expect(page.getByText("Alpha issue").first()).toBeVisible({
          timeout: timeouts.appReady,
        });

        // Open the per-issue context menu via the "..." button.
        const issueMenuBtn = page
          .locator('[data-testid="beads-issue-menu"]')
          .first();
        await expect(issueMenuBtn).toBeVisible({ timeout: timeouts.shortAction });
        await issueMenuBtn.click();

        const menu = page.locator(MENU).first();
        await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

        // "Task" submenu item appears once the beadsIssues prompts fetch resolves.
        const taskItem = page
          .locator(`${MENU} button`)
          .filter({ hasText: "Task" });
        await expect(taskItem).toBeVisible({ timeout: timeouts.appReady });

        // Dispatch mouseenter so the submenu opens without pointer-event conflicts.
        await taskItem.dispatchEvent("mouseenter");
        const submenu = page.locator(MENU).nth(1);
        await expect(submenu).toBeVisible({ timeout: timeouts.shortAction });

        const promptItem = submenu
          .locator("button")
          .filter({ hasText: "Beads Issue Task" });
        await expect(promptItem).toBeVisible({ timeout: timeouts.shortAction });
        await promptItem.click();

        // Toast confirms the conversation was created for the issue.
        await expect(
          page.getByText('Started "Beads Issue Task" for mitto-aaa'),
        ).toBeVisible({ timeout: timeouts.appReady });

        // The app switches to conversation view on the new session.
        // NamedPromptPill must appear in the new session's transcript.
        await expect(
          page
            .locator('[data-testid="named-prompt-pill"]')
            .filter({ hasText: "Beads Issue Task" }),
        ).toBeVisible({ timeout: 15_000 });

        // The full prompt body text must NOT appear as a user message.
        await expect(
          page.getByText(
            "Analyze the beads issue mitto-aaa and summarize the current status.",
          ),
        ).toHaveCount(0);

        // Agent response echoes the substituted ISSUE_ID, proving substitution.
        // The mock ACP responds to "mitto-aaa" with a message containing that ID.
        await expect(
          page.locator(".prose, .bg-mitto-agent").filter({ hasText: "mitto-aaa" }).first(),
        ).toBeVisible({ timeout: timeouts.agentResponse });
      },
    );
  },
);

// ─────────────────────────────────────────────────────────────────────────────
// Surface 3: beads list / Tasks menu (menus: beadsList)
// ─────────────────────────────────────────────────────────────────────────────

testWithCleanup.describe(
  "Named Prompt Menu Sends — Surface 3: beads list menu",
  () => {
    testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
      await page.route("**/api/beads/list**", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(MOCK_ISSUES),
        });
      });

      await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
      });
      const resp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `NPMM-BList-${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(resp.ok()).toBeTruthy();
      const seedId = (await resp.json()).session_id;

      // Pre-select the seed session so the folder auto-expands on load.
      await page.addInitScript((sid) => {
        localStorage.setItem("mitto_last_session_id", sid);
        localStorage.removeItem("mitto_conversation_filter_tab");
      }, seedId);

      await helpers.navigateAndWait(page);
    });

    testWithCleanup(
      "starts new conversation seeded with pill (beadsList prompt)",
      async ({ page, timeouts }) => {
        await clickBeadsButton(page, timeouts);
        await expect(page.getByText("Alpha issue").first()).toBeVisible({
          timeout: timeouts.appReady,
        });

        // Footer list-prompts button opens the beadsList dropdown.
        const listPromptsBtn = page.locator(
          'button[title="Run a prompt over the issue list in a new conversation"]',
        );
        await expect(listPromptsBtn).toBeVisible({
          timeout: timeouts.shortAction,
        });
        await listPromptsBtn.click();

        // "Beads List Review" appears once the beadsList prompts fetch resolves.
        const promptItem = page
          .locator("button")
          .filter({ hasText: "Beads List Review" });
        await expect(promptItem).toBeVisible({ timeout: timeouts.appReady });
        await promptItem.click();

        // Toast confirms the conversation was started.
        await expect(
          page.getByText('Started "Beads List Review"'),
        ).toBeVisible({ timeout: timeouts.appReady });

        // NamedPromptPill must appear in the new session's transcript.
        await expect(
          page
            .locator('[data-testid="named-prompt-pill"]')
            .filter({ hasText: "Beads List Review" }),
        ).toBeVisible({ timeout: 15_000 });

        // Full body text must NOT appear as a user bubble.
        await expect(
          page.getByText(
            "Review the current beads issue list and provide a summary of all work items.",
          ),
        ).toHaveCount(0);
      },
    );
  },
);

// ─────────────────────────────────────────────────────────────────────────────
// Surface 4: Cmd+/ predefined prompts menu (regression — still shows pill)
// ─────────────────────────────────────────────────────────────────────────────

testWithCleanup.describe(
  "Named Prompt Menu Sends — Surface 4: ChatInput predefined prompts dropup",
  () => {
    let sessionId: string;

    testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
      await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
      });
      const resp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `NPMM-PredP-${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(resp.ok()).toBeTruthy();
      const created = await resp.json();
      sessionId = created.session_id || created.id;
      expect(sessionId).toBeTruthy();

      // Pre-set localStorage so the app opens directly into this session,
      // which loads prompts from project-alpha (including "Hello Greeting").
      await page.addInitScript((sid) => {
        localStorage.setItem("mitto_last_session_id", sid);
        localStorage.removeItem("mitto_conversation_filter_tab");
      }, sessionId);

      await helpers.navigateAndWait(page);
    });

    testWithCleanup(
      "selecting a predefined prompt shows a pill — body text absent",
      async ({ page, timeouts }) => {
        // Wait for the chat textarea to be ready.
        await expect(page.locator("textarea")).toBeEnabled({
          timeout: timeouts.appReady,
        });

        // The "Insert predefined prompt" toggle only renders when prompts are
        // loaded for the active workspace; wait up to appReady.
        const promptsToggle = page.locator(
          'button[title="Insert predefined prompt"]',
        );
        await expect(promptsToggle).toBeVisible({ timeout: timeouts.appReady });
        await promptsToggle.click();

        // "Hello Greeting" is a workspace prompt from project-alpha/greeting.md.
        const promptItem = page
          .locator("button")
          .filter({ hasText: "Hello Greeting" });
        await expect(promptItem).toBeVisible({ timeout: timeouts.appReady });
        await promptItem.click();

        // NamedPromptPill must appear — this is the primary assertion.
        await expect(
          page
            .locator('[data-testid="named-prompt-pill"]')
            .filter({ hasText: "Hello Greeting" }),
        ).toBeVisible({ timeout: 15_000 });

        // The full body text must NOT appear as a user message bubble.
        await expect(
          page
            .locator(".bg-mitto-user, .bg-blue-600")
            .filter({ hasText: "Hello! How are you doing today?" }),
        ).toHaveCount(0);
      },
    );
  },
);
