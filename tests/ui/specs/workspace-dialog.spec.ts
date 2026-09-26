import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Workspace Dialog tests for Mitto Web UI.
 *
 * Tests the enhanced workspace selection dialog with filtering
 * when there are more than WORKSPACE_FILTER_THRESHOLD workspaces configured.
 */

const projectRoot = path.resolve(__dirname, "../../..");

// This should match WORKSPACE_FILTER_THRESHOLD in web/static/app.js
const WORKSPACE_FILTER_THRESHOLD = 5;

// Create 7 test workspace paths (more than 5 to trigger filter UI)
const TEST_WORKSPACES = [
  { name: "project-alpha", path: path.join(projectRoot, "tests/fixtures/workspaces/project-alpha") },
  { name: "project-beta", path: path.join(projectRoot, "tests/fixtures/workspaces/project-beta") },
  { name: "empty-project", path: path.join(projectRoot, "tests/fixtures/workspaces/empty-project") },
  // Use subdirectories of the project as additional "workspaces" for testing
  { name: "cmd", path: path.join(projectRoot, "cmd") },
  { name: "internal", path: path.join(projectRoot, "internal") },
  { name: "web", path: path.join(projectRoot, "web") },
  { name: "docs", path: path.join(projectRoot, "docs") },
];

/**
 * Opens the "Select Workspace" picker via the global `mittoNewConversation`
 * hook — the same call the native Cmd+N menu makes.
 *
 * The sidebar's per-folder "new conversation" button (selectors.newSessionButton)
 * now creates a session DIRECTLY when its folder has exactly one matching
 * workspace (see handleNewSessionInFolder in SessionList.js, mitto-n0qj) —
 * which is the common case for these single-ACP-server test workspaces — so
 * it no longer opens this picker. `mittoNewConversation` unconditionally
 * shows the full picker whenever more than one workspace is configured,
 * regardless of the currently selected folder, so it reliably reaches the
 * dialog under test here.
 */
async function openWorkspaceDialog(page: Page) {
  await page.evaluate(() => {
    (window as any).mittoNewConversation?.();
  });
  const dialog = page.locator(".modal-box").filter({ hasText: "Select Workspace" });
  await expect(dialog).toBeVisible({ timeout: 5000 });
  return dialog;
}

test.describe("Workspace Dialog", () => {
  // Skip entire suite in Docker — requires 7 host-local workspace paths
  test.beforeEach(() => {
    test.skip(!!process.env.MITTO_EXTERNAL_SERVER,
      'Requires host-local workspace paths unavailable in Docker');
  });

  // Setup: Add all test workspaces before tests
  test.beforeAll(async ({ request, apiUrl }) => {
    for (const ws of TEST_WORKSPACES) {
      await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: "mock-acp", working_dir: ws.path },
      });
    }
  });

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should show filter input when more than 5 workspaces", async ({ page }) => {
    await openWorkspaceDialog(page);

    // Filter input should be visible (only shown when > 5 workspaces)
    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();

    // Filter input should be focused
    await expect(filterInput).toBeFocused();
  });

  test("should filter workspaces by name", async ({ page }) => {
    const dialog = await openWorkspaceDialog(page);

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();

    // Type to filter - should match "project-alpha" and "project-beta"
    await filterInput.fill("project");

    // Wait for filtering to take effect (increased timeout for reliability)
    await page.waitForTimeout(300);

    // Should see exactly 3 workspaces (empty-project, project-alpha, project-beta)
    // All contain "project" in their path
    const workspaceButtons = dialog.locator("button").filter({
      has: page.locator("div[title*='project']")
    });
    await expect(workspaceButtons).toHaveCount(3);

    // Verify the workspaces are the correct ones by checking for their paths in title attributes
    await expect(dialog.locator("div[title*='project-alpha']").first()).toBeVisible();
    await expect(dialog.locator("div[title*='project-beta']").first()).toBeVisible();
    await expect(dialog.locator("div[title*='empty-project']").first()).toBeVisible();

    // Should NOT see other workspaces (cmd, internal, web, docs)
    await expect(dialog.locator("div[title*='/cmd']").first()).not.toBeVisible();
    await expect(dialog.locator("div[title*='/internal']").first()).not.toBeVisible();
  });

  test("should show 'no match' message when filter has no results", async ({ page }) => {
    await openWorkspaceDialog(page);

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await filterInput.fill("nonexistent-workspace-xyz");

    await expect(page.locator("text=No workspaces match your filter")).toBeVisible();
  });

  test("should select workspace with number key when filter is empty", async ({ page, selectors }) => {
    const dialog = await openWorkspaceDialog(page);

    // Wait for dialog to be fully ready (filter input may capture focus)
    await page.waitForTimeout(200);

    // Click on the dialog body to ensure focus is not on the filter input
    await page.locator("text=Select Workspace").click();

    // Press "1" to select first workspace
    await page.keyboard.press("1");

    // Dialog should close and session should be created
    await expect(dialog).toBeHidden({ timeout: 10000 });

    // Chat input should be enabled (session created)
    await expect(page.locator(selectors.chatInput)).toBeEnabled({ timeout: 10000 });
  });

  test("should close dialog with Escape key", async ({ page }) => {
    const dialog = await openWorkspaceDialog(page);

    // Wait for dialog to be fully ready (filter input may capture focus)
    await page.waitForTimeout(200);

    // Press Escape to close
    await page.keyboard.press("Escape");

    // Dialog should close
    await expect(dialog).toBeHidden({ timeout: 5000 });
  });

  test(`should show numeric prefixes only for first ${WORKSPACE_FILTER_THRESHOLD} items`, async ({ page }) => {
    const dialog = await openWorkspaceDialog(page);

    // Check that numbers 1-N are visible as badges (N = WORKSPACE_FILTER_THRESHOLD).
    // The numeric-prefix badge div uses class "rounded" (NOT "rounded-lg" —
    // that belongs to the WorkspaceBadge abbreviation chip rendered next to it).
    for (let i = 1; i <= WORKSPACE_FILTER_THRESHOLD; i++) {
      const badge = dialog.locator(`div.rounded:has-text("${i}")`).first();
      await expect(badge).toBeVisible();
    }
  });

  test("should select workspace with number key even when filter has text", async ({ page }) => {
    const dialog = await openWorkspaceDialog(page);

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');

    // Type some text first to filter workspaces
    await filterInput.fill("pro");

    // Now type a number - it should select the first filtered result
    await filterInput.press("1");

    // Dialog should be closed because a workspace was selected
    await expect(dialog).not.toBeVisible({ timeout: 5000 });
  });

  test("should focus filter input when opened via mittoNewConversation", async ({ page, selectors }) => {
    // First create a session directly so we have a chat input to focus. The
    // sidebar's per-folder button now creates the session immediately (its
    // folder has exactly one matching workspace) rather than opening the
    // picker, which suits this precondition fine. By this point in the suite
    // several folders may already have sessions (each earlier test creates
    // one via its own number-key selection), so multiple per-folder buttons
    // can be present — any one of them works here, hence `.first()`.
    await page.locator(selectors.newSessionButton).first().click();
    const chatInput = page.locator(selectors.chatInput);
    await expect(chatInput).toBeEnabled({ timeout: 10000 });

    // Focus the chat input (simulating user typing)
    await chatInput.focus();
    await expect(chatInput).toBeFocused();

    // Now trigger mittoNewConversation (same as Cmd+N from native menu). With
    // more than one workspace configured this opens the full picker dialog.
    await openWorkspaceDialog(page);

    // The filter input should receive focus (not the chat input)
    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();
    await expect(filterInput).toBeFocused({ timeout: 2000 });
  });
});

