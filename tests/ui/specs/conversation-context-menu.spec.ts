import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Conversation context menu tests for the Mitto Web UI.
 *
 * Verifies the right-click context menu on an individual conversation (session
 * item). Beyond the standard Archive / Properties / Delete entries, prompts
 * whose `menus` list includes `conversation` in their front-matter are surfaced here,
 * grouped into submenus by their `group` attribute. Clicking a prompt enqueues
 * it to that conversation.
 *
 * The menu is evaluated per-conversation: each prompt's `enabledWhen` is checked
 * against the conversation that was right-clicked (not the active session), so a
 * conditional prompt only appears on the conversations where it applies.
 *
 * Fixtures used:
 *   - Prompt: tests/fixtures/workspaces/project-alpha/.mitto/prompts/context-menu-prompt.md
 *     → name: "Context Menu Test", group: "Workflow", menus: conversation
 *   - Prompt: tests/fixtures/workspaces/project-alpha/.mitto/prompts/context-menu-active-only.md
 *     → name: "Conditional Test", group: "Workflow", menus: conversation,
 *       enabledWhen: "permissions.canPromptUser"
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);

// The mock ACP server name configured in tests/ui/global-setup.ts.
const AGENT_NAME = "mock-acp";

// The fixture prompts surfaced in the conversation context menu.
const PROMPT_GROUP = "Workflow";
const PROMPT_NAME = "Context Menu Test";
// Conditional prompt: enabledWhen "permissions.canPromptUser" (default true).
const CONDITIONAL_PROMPT_NAME = "Conditional Test";

// Context menus render as fixed-position panels; this matches both the main menu
// and any open submenu while avoiding dialogs (which use different classes).
const MENU = ".fixed.z-50.bg-mitto-surface-2.shadow-xl";

testWithCleanup.describe("Conversation Context Menu - prompt submenus", () => {
  let sessionId: string;

  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Ensure the project-alpha workspace exists so its prompts (including the
    // menus:conversation fixture) are loaded for the active session.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation in project-alpha so a session item renders.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Ctx Menu Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    sessionId = created.session_id || created.id;
    expect(sessionId).toBeTruthy();

    await helpers.navigateAndWait(page);

    // Select the seeded session so the app fetches workspace prompts for
    // project-alpha (prompts load from the active session's working_dir).
    await helpers.navigateToSession(page, sessionId);
  });

  // Opens a session's context menu (defaults to the seeded session) and returns
  // its locator.
  async function openSessionMenu(page, timeouts, targetId = sessionId) {
    const sessionItem = page
      .locator(`[data-session-id="${targetId}"]`)
      .first();
    await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
    await sessionItem.click({ button: "right" });

    const menu = page.locator(MENU).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });
    return menu;
  }

  testWithCleanup(
    "shows the prompt group submenu before Properties/Archive/Delete",
    async ({ page, timeouts }) => {
      await openSessionMenu(page, timeouts);

      const menuButtons = page.locator(`${MENU} button`);

      // Standard conversation actions remain present.
      await expect(
        menuButtons.filter({ hasText: "Properties" }),
      ).toBeVisible();
      await expect(menuButtons.filter({ hasText: "Delete" })).toBeVisible();

      // The "Workflow" group submenu (from the menus:conversation prompt) appears
      // before the standard actions. It may take a moment to show because the
      // prompt list is fetched asynchronously once the session becomes active;
      // toBeVisible retries until the menu re-renders with the loaded prompts.
      const groupItem = menuButtons.filter({ hasText: PROMPT_GROUP });
      await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });

      // The group submenu must come before Properties and Delete.
      const labels = await menuButtons.allTextContents();
      const deleteIdx = labels.findIndex((t) => t.includes("Delete"));
      const groupIdx = labels.findIndex((t) => t.includes(PROMPT_GROUP));
      expect(groupIdx).toBeGreaterThanOrEqual(0);
      expect(deleteIdx).toBeGreaterThan(groupIdx);

      // The prompt itself lives in a submenu that is not rendered until hover.
      const promptItem = menuButtons.filter({ hasText: PROMPT_NAME });
      await expect(promptItem).toHaveCount(0);

      // Hovering the group expands the submenu and reveals the prompt entry.
      await groupItem.hover();
      await expect(promptItem).toBeVisible({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "enqueues the prompt to the conversation when clicked",
    async ({ page, timeouts }) => {
      await openSessionMenu(page, timeouts);

      const menuButtons = page.locator(`${MENU} button`);
      const groupItem = menuButtons.filter({ hasText: PROMPT_GROUP });
      await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });

      await groupItem.hover();
      const promptItem = menuButtons.filter({ hasText: PROMPT_NAME });
      await expect(promptItem).toBeVisible({ timeout: timeouts.shortAction });

      await promptItem.click();

      // Clicking enqueues the prompt and shows a success toast confirming it was
      // sent to the conversation. The menu closes after selection.
      await expect(
        page.getByText(`Sent "${PROMPT_NAME}" to conversation`),
      ).toBeVisible({ timeout: timeouts.appReady });
      await expect(page.locator(MENU)).toHaveCount(0);
    },
  );

  testWithCleanup(
    "evaluates enabledWhen against the right-clicked conversation, not the active one",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      // Create a second conversation in the same workspace and disable its
      // can_prompt_user permission (default true). permissions.canPromptUser is
      // therefore false for it and true for the seeded conversation, so the
      // conditional fixture prompt should only appear on the seeded one.
      const otherResp = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `Ctx Menu Restricted ${Date.now()}`,
          working_dir: WORKSPACE_ALPHA,
        },
      });
      expect(otherResp.ok()).toBeTruthy();
      const otherCreated = await otherResp.json();
      const restrictedId = otherCreated.session_id || otherCreated.id;
      expect(restrictedId).toBeTruthy();

      const patchResp = await request.patch(
        apiUrl(`/api/sessions/${restrictedId}/settings`),
        { data: { settings: { can_prompt_user: false } } },
      );
      expect(
        patchResp.ok(),
        `PATCH settings failed: ${patchResp.status()} ${await patchResp.text()}`,
      ).toBe(true);

      // Reload so the new conversation appears in the sidebar, then keep the
      // seeded (unrestricted) conversation active.
      await page.reload();
      await helpers.waitForAppReady(page);
      await helpers.navigateToSession(page, sessionId);

      const menuButtons = page.locator(`${MENU} button`);

      // Right-click the seeded (unrestricted) conversation: the conditional
      // prompt IS present because permissions.canPromptUser is true for it.
      await openSessionMenu(page, timeouts, sessionId);
      let groupItem = menuButtons.filter({ hasText: PROMPT_GROUP });
      await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });
      await groupItem.hover();
      await expect(
        menuButtons.filter({ hasText: CONDITIONAL_PROMPT_NAME }),
      ).toBeVisible({ timeout: timeouts.shortAction });
      // Close the menu before opening the next one.
      await page.keyboard.press("Escape");
      await expect(page.locator(MENU)).toHaveCount(0);

      // Right-click the restricted conversation while the unrestricted one is
      // still active. Before per-conversation evaluation this reused the active
      // session's prompt list and wrongly showed the conditional prompt; now
      // enabledWhen is evaluated for the restricted conversation, so it is hidden
      // — while the unconditional "Context Menu Test" prompt still appears.
      await openSessionMenu(page, timeouts, restrictedId);
      groupItem = menuButtons.filter({ hasText: PROMPT_GROUP });
      await expect(groupItem).toBeVisible({ timeout: timeouts.appReady });
      await groupItem.hover();
      await expect(
        menuButtons.filter({ hasText: PROMPT_NAME }),
      ).toBeVisible({ timeout: timeouts.shortAction });
      await expect(
        menuButtons.filter({ hasText: CONDITIONAL_PROMPT_NAME }),
      ).toHaveCount(0);
    },
  );
});
