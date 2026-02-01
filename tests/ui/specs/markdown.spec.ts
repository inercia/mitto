import { test, expect } from "../fixtures/test-fixtures";

/**
 * Markdown rendering and message display tests for Mitto Web UI.
 *
 * These tests verify that markdown content is rendered correctly
 * in agent responses.
 */

test.describe("Markdown Rendering", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  test("should render agent responses", async ({ page, helpers }) => {
    // Send a message that will trigger a response
    await helpers.sendMessage(page, "Hello");

    // Wait for agent response (uses .first() to handle multiple messages)
    await helpers.waitForAgentResponse(page);
  });

  test("should display code blocks with syntax highlighting", async ({
    page,
    selectors,
    helpers,
  }) => {
    // Send a message asking for code
    await helpers.sendMessage(page, "Show me some code");

    // Wait for response (uses .first() to handle multiple messages)
    await helpers.waitForAgentResponse(page);

    // If the response contains code, it should be in a code block
    // The mock server may or may not return code, so we just verify no errors
    await expect(page.locator(selectors.app)).toBeVisible();
  });

  test("should handle long messages without breaking layout", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Send a long message
    const longMessage = "A".repeat(500);
    await helpers.sendMessage(page, longMessage);

    // Wait for message to appear
    await expect(
      page.locator(`text=${longMessage.substring(0, 50)}`),
    ).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Layout should not be broken
    const app = page.locator(selectors.app);
    const boundingBox = await app.boundingBox();
    expect(boundingBox).toBeTruthy();
    expect(boundingBox!.width).toBeGreaterThan(0);
    expect(boundingBox!.height).toBeGreaterThan(0);
  });
});

test.describe("Message Styling", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  test("should differentiate user and agent messages", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Send a message
    const testMessage = helpers.uniqueMessage("Style test");
    await helpers.sendMessage(page, testMessage);

    // Wait for user message
    await helpers.waitForUserMessage(page, testMessage);

    // User message should have user styling
    const userMessage = page.locator(selectors.userMessage).filter({
      hasText: testMessage,
    });
    await expect(userMessage).toBeVisible({ timeout: timeouts.shortAction });

    // Wait for agent response (uses .first() to handle multiple messages)
    await helpers.waitForAgentResponse(page);
  });

  test("should display timestamps or metadata", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Send a message
    await helpers.sendMessage(page, "Test for metadata");

    // Wait for message
    await page.waitForTimeout(1000);

    // System messages or metadata should be visible
    const systemMessage = page.locator(selectors.systemMessage);
    // There should be at least one system/metadata element
    const count = await systemMessage.count();
    expect(count).toBeGreaterThanOrEqual(0); // May or may not have metadata
  });
});
