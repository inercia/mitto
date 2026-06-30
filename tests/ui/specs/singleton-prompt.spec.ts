import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Singleton Prompts — Playwright coverage (mitto-4mb.5)
 *
 * A prompt declared `singleton: true` must not have more than one
 * non-archived conversation per working dir + origin prompt name. Running
 * the same singleton prompt a second time from the menu must route to the
 * EXISTING conversation (reused: true) instead of creating a duplicate.
 *
 * Modeled on "Surface 3: beads list menu" in named-prompt-menu-send.spec.ts.
 *
 * Fixtures used:
 *   - Prompt: singleton-list-prompt.prompt.yaml (name: "Singleton List Review",
 *     menus: beadsList, singleton: true)
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// daisyUI fixed context menus share this class combination.
const MENU = ".menu.fixed.z-50.shadow-xl";

const MOCK_ISSUES = [
  {
    id: "mitto-aaa",
    title: "Alpha issue",
    description: "Test issue for singleton prompt menu sends.",
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
 * which matches any element regardless of tag. Idempotent: expands the
 * project-alpha folder if needed, then clicks the Tasks/Beads button.
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
  await folderDetails.locator('[title^="Beads issues:"]').first().click();
}

testWithCleanup.describe("Singleton Prompts — beads list menu", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route(/\/api\/issues(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Test isolation: delete any conversation left over from a previous run
    // with this origin prompt — otherwise the singleton find-or-route would
    // reuse it on the FIRST click below, turning the "first run" assertion
    // ("Started ...") into a spurious "Reusing existing ..." failure.
    const existingResp = await request.get(apiUrl("/api/sessions"));
    if (existingResp.ok()) {
      const existingSessions = await existingResp.json();
      for (const s of existingSessions) {
        if (s.origin_prompt_name === "Singleton List Review") {
          await request.delete(apiUrl(`/api/sessions/${s.session_id}`));
        }
      }
    }
    const resp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Singleton-BList-${Date.now()}`,
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
    "singleton prompt: second run reuses the same conversation (no duplicate)",
    async ({ page, request, apiUrl, timeouts }) => {
      // --- First run: creates a NEW conversation ---
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText("Alpha issue").first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      const listPromptsBtn = page.locator(
        'button[data-tip="Run a prompt over the issue list in a new conversation"]',
      );
      await expect(listPromptsBtn).toBeVisible({
        timeout: timeouts.shortAction,
      });
      await listPromptsBtn.click();

      const promptItem = page
        .locator("button")
        .filter({ hasText: "Singleton List Review" });
      await expect(promptItem).toBeVisible({ timeout: timeouts.appReady });
      await promptItem.click();

      // Toast confirms a fresh conversation was started.
      await expect(
        page.getByText('Started "Singleton List Review"'),
      ).toBeVisible({ timeout: timeouts.appReady });

      // NamedPromptPill must appear in the new session's transcript.
      await expect(
        page
          .locator('[data-testid="named-prompt-pill"]')
          .filter({ hasText: "Singleton List Review" }),
      ).toBeVisible({ timeout: 15_000 });

      // --- Second run: re-open the Tasks view and run the SAME prompt again
      // (UI-driven, not via API) — must REUSE the existing conversation. ---
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText("Alpha issue").first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      const listPromptsBtn2 = page.locator(
        'button[data-tip="Run a prompt over the issue list in a new conversation"]',
      );
      await expect(listPromptsBtn2).toBeVisible({
        timeout: timeouts.shortAction,
      });
      await listPromptsBtn2.click();

      const promptItem2 = page
        .locator("button")
        .filter({ hasText: "Singleton List Review" });
      await expect(promptItem2).toBeVisible({ timeout: timeouts.appReady });
      await promptItem2.click();

      // Key singleton signal: reuse toast instead of "Started ...".
      await expect(
        page.getByText('Reusing existing "Singleton List Review" conversation'),
      ).toBeVisible({ timeout: timeouts.appReady });

      // --- No-duplicate assertion: exactly ONE conversation has this origin. ---
      const listResp = await request.get(apiUrl("/api/sessions"));
      expect(listResp.ok()).toBeTruthy();
      const sessions = await listResp.json();
      const matches = sessions.filter(
        (s) => s.origin_prompt_name === "Singleton List Review",
      );
      expect(matches.length).toBe(1);
    },
  );
});
