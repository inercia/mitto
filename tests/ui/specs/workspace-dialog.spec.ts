import { test, expect } from "../fixtures/test-fixtures";
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

test.describe("Workspace Dialog", () => {
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

  test("should show filter input when more than 5 workspaces", async ({ page, selectors }) => {
    // Click new session button to open workspace dialog
    await page.locator(selectors.newSessionButton).click();

    // Wait for dialog to appear
    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Filter input should be visible (only shown when > 5 workspaces)
    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();

    // Filter input should be focused
    await expect(filterInput).toBeFocused();
  });

  test("should filter workspaces by name", async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();

    // Type to filter - should match "project-alpha" and "project-beta"
    await filterInput.fill("project");

    // Wait for filtering to take effect
    await page.waitForTimeout(100);

    // Should see project-alpha and project-beta (use .first() to avoid strict mode violation)
    await expect(page.locator("div.font-medium:has-text('project-alpha')").first()).toBeVisible();
    await expect(page.locator("div.font-medium:has-text('project-beta')").first()).toBeVisible();

    // Should NOT see other workspaces (they don't contain "project" in basename)
    // Note: cmd, internal, web, docs should be hidden
    await expect(page.locator("div.font-medium:has-text('cmd')")).toBeHidden();
  });

  test("should show 'no match' message when filter has no results", async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await filterInput.fill("nonexistent-workspace-xyz");

    await expect(page.locator("text=No workspaces match your filter")).toBeVisible();
  });

  test("should select workspace with number key when filter is empty", async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Press "1" to select first workspace
    await page.keyboard.press("1");

    // Dialog should close and session should be created
    await expect(dialog).toBeHidden({ timeout: 5000 });

    // Chat input should be enabled (session created)
    await expect(page.locator(selectors.chatInput)).toBeEnabled({ timeout: 10000 });
  });

  test("should close dialog with Escape key", async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Press Escape to close
    await page.keyboard.press("Escape");

    // Dialog should close
    await expect(dialog).toBeHidden({ timeout: 5000 });
  });

  test(`should show numeric prefixes only for first ${WORKSPACE_FILTER_THRESHOLD} items`, async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Check that numbers 1-N are visible as badges (N = WORKSPACE_FILTER_THRESHOLD)
    for (let i = 1; i <= WORKSPACE_FILTER_THRESHOLD; i++) {
      const badge = dialog.locator(`div.rounded-lg:has-text("${i}")`).first();
      await expect(badge).toBeVisible();
    }
  });

  test("should select workspace with number key even when filter has text", async ({ page, selectors }) => {
    await page.locator(selectors.newSessionButton).click();

    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');

    // Type some text first to filter workspaces
    await filterInput.fill("pro");

    // Now type a number - it should select the first filtered result
    await filterInput.press("1");

    // Dialog should be closed because a workspace was selected
    await expect(dialog).not.toBeVisible({ timeout: 5000 });
  });

  test("should focus filter input when opened via mittoNewConversation", async ({ page, selectors }) => {
    // First create a session so we have a chat input to focus
    await page.locator(selectors.newSessionButton).click();
    const dialog = page.locator("text=Select Workspace").locator("..");
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // Select first workspace to create session
    await page.keyboard.press("1");
    await expect(dialog).toBeHidden({ timeout: 5000 });

    // Wait for session to be created and chat input to be enabled
    const chatInput = page.locator(selectors.chatInput);
    await expect(chatInput).toBeEnabled({ timeout: 10000 });

    // Focus the chat input (simulating user typing)
    await chatInput.focus();
    await expect(chatInput).toBeFocused();

    // Now trigger mittoNewConversation (same as Cmd+N from native menu)
    await page.evaluate(() => {
      (window as any).mittoNewConversation?.();
    });

    // Wait for dialog to appear
    await expect(dialog).toBeVisible({ timeout: 5000 });

    // The filter input should receive focus (not the chat input)
    const filterInput = page.locator('input[placeholder="Filter workspaces..."]');
    await expect(filterInput).toBeVisible();
    await expect(filterInput).toBeFocused({ timeout: 2000 });
  });
});

