import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Beads issue linkification tests.
 *
 * Verifies that beads issue IDs appearing in agent messages are automatically
 * converted to clickable .beads-link anchors, and that clicking one opens the
 * standalone BeadsIssueView for that issue.
 *
 * Strategy:
 *  - Mock /api/issues so the ID set is populated without the `bd` binary.
 *  - Mock /api/issues/{id} so BeadsIssueView can render without the binary.
 *  - Send a prompt that triggers the mock ACP to respond with "mitto-aaa" in
 *    the message text (the beads-issue-task.json fixture matches this).
 *  - Assert the linkified <a class="beads-link"> appears in the agent message.
 *  - Click the link and assert the BeadsIssueView is shown.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

const MOCK_ISSUE = {
  id: "mitto-aaa",
  title: "Test Beads Issue",
  description: "A test issue for linkification.",
  status: "open",
  priority: 1,
  issue_type: "feature",
  assignee: "",
  owner: "",
  created_at: "2026-06-01T10:00:00Z",
  updated_at: "2026-06-01T10:00:00Z",
};

testWithCleanup.describe("Beads issue linkification", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock the beads list so useBeadsKnownIds populates the cache.
    await page.route(/\/api\/issues(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([MOCK_ISSUE]),
      });
    });

    // Mock the beads show endpoint so BeadsIssueView can render.
    await page.route(/\/api\/issues\/[^/?]+/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUE),
      });
    });

    // Ensure the workspace exists.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  testWithCleanup(
    "agent message containing a known beads ID gets linkified",
    async ({ page, helpers, timeouts }) => {
      // Send a prompt matching the beads-issue-task fixture pattern.
      // The mock ACP responds: "Issue mitto-aaa is currently open. ..."
      await helpers.sendMessage(page, "mitto-aaa");
      await helpers.waitForAgentResponse(page);

      // The beads-ids-updated event fires after the /api/issues fetch.
      // Wait for the link to appear (linkify runs after ids are cached).
      const beadsLink = page.locator('a.beads-link[data-beads-id="mitto-aaa"]');
      await expect(beadsLink.first()).toBeVisible({
        timeout: timeouts.agentResponse,
      });
    },
  );

  testWithCleanup(
    "clicking a beads link opens the BeadsIssueView",
    async ({ page, helpers, timeouts }) => {
      await helpers.sendMessage(page, "mitto-aaa");
      await helpers.waitForAgentResponse(page);

      const beadsLink = page.locator('a.beads-link[data-beads-id="mitto-aaa"]');
      await expect(beadsLink.first()).toBeVisible({
        timeout: timeouts.agentResponse,
      });

      // Click the link; globalHandlers.js routes it to window.mittoOpenBeadsIssue.
      await beadsLink.first().click();

      // BeadsIssueView fetches /api/issues/{id} and renders the issue title.
      const issuePanel = page.locator(
        'div.properties-panel:has(h2:has-text("Test Beads Issue"))',
      );
      await expect(issuePanel).toBeVisible({ timeout: timeouts.agentResponse });

      // Regression guard: the viewer was opened from an auto-detected link in the
      // conversation body (not from the properties panel's linked-issue link), so
      // closing it must return to the conversation WITHOUT popping the properties
      // panel. (Previously the same origin was reused for both entry points,
      // causing the properties panel to open unexpectedly on close.)
      await issuePanel.locator('button[data-tip="Close"]').click();

      const convPanel = page.locator(
        'div.properties-panel:has(h2:has-text("Conversation"))',
      );
      await expect(issuePanel).toHaveCount(0, { timeout: timeouts.shortAction });
      await expect(convPanel).toHaveCount(0);
    },
  );
});
