/**
 * Tests for correct rendering of markdown when streamed in chunks.
 *
 * These tests verify that markdown is correctly rendered even when
 * the markdown syntax is split across multiple streaming chunks.
 * This is a common edge case when AI agents stream responses.
 */

import { test, expect } from "../fixtures/test-fixtures";

test.describe("Markdown Chunk Rendering", () => {
  // Use serial mode to ensure tests run one at a time
  // Each test creates a fresh session for isolation
  test.describe.configure({ mode: "serial" });

  test.beforeEach(async ({ page, helpers, selectors }) => {
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    // Create a fresh session for each test to ensure isolation
    await helpers.createFreshSession(page);
    // Ensure the messages container is visible (session content is shown)
    await expect(page.locator(selectors.messagesContainer)).toBeVisible();
  });

  test("should render code block split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    // Trigger the markdown-code-block scenario
    await helpers.sendMessage(page, "TEST:code-block-split");

    // Wait for the response to complete
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered (chunks may arrive after prompt completes)
    await page.waitForTimeout(1000);

    // Wait for the code block to appear anywhere in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);
    const codeBlock = messagesContainer.locator("pre code");
    await expect(codeBlock).toBeVisible({ timeout: timeouts.agentResponse });

    // Verify the code content is correct
    const codeText = await codeBlock.textContent();
    expect(codeText).toContain("def hello():");
    expect(codeText).toContain('print("Hello, World!")');

    // Verify surrounding text is rendered in the agent message
    const agentMessage = page.locator(selectors.agentMessage).last();
    await expect(agentMessage).toContainText("Here's some code:");
    await expect(agentMessage).toContainText("That's the code.");
  });

  test("should render nested list split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:nested-list-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for the list in the messages container (more reliable than agent message selector)
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify ordered list exists
    const orderedList = messagesContainer.locator("ol").first();
    await expect(orderedList).toBeVisible({ timeout: timeouts.agentResponse });

    // Verify nested unordered list
    const nestedBullets = messagesContainer.locator("ol > li > ul > li");
    expect(await nestedBullets.count()).toBeGreaterThanOrEqual(2);

    // Verify content
    await expect(messagesContainer).toContainText("First item");
    await expect(messagesContainer).toContainText("Nested bullet");
    await expect(messagesContainer).toContainText("Second item");
    await expect(messagesContainer).toContainText("That's the list.");
  });

  test("should render inline code split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:inline-code-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered (chunks may arrive after prompt completes)
    await page.waitForTimeout(1000);

    const agentMessage = page.locator(selectors.agentMessage).last();

    // Verify inline code elements
    const inlineCode = agentMessage.locator("code:not(pre code)");
    await expect(inlineCode.first()).toBeVisible({ timeout: timeouts.agentResponse });
    expect(await inlineCode.count()).toBeGreaterThanOrEqual(2);

    // Verify specific code content
    await expect(agentMessage.locator("code")).toContainText(["console.log"]);
    await expect(agentMessage).toContainText("for errors");
  });

  test("should render bold and italic split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:bold-italic-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    const agentMessage = page.locator(selectors.agentMessage).last();

    // Verify bold text
    const boldText = agentMessage.locator("strong");
    await expect(boldText.first()).toContainText("bold text");

    // Verify italic text
    const italicText = agentMessage.locator("em");
    await expect(italicText.first()).toContainText("italic text");
  });

  test("should render link split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:link-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    const agentMessage = page.locator(selectors.agentMessage).last();

    // Verify link is rendered
    const link = agentMessage.locator('a[href="https://example.com"]');
    await expect(link).toBeVisible({ timeout: timeouts.agentResponse });
    await expect(link).toContainText("this link");
  });

  test("should render table split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:table-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for the table in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify table is rendered
    const table = messagesContainer.locator("table");
    await expect(table).toBeVisible({ timeout: timeouts.agentResponse });

    // Verify table content
    await expect(table).toContainText("Name");
    await expect(table).toContainText("Age");
    await expect(table).toContainText("Alice");
    await expect(table).toContainText("30");
    await expect(messagesContainer).toContainText("That's the table.");
  });

  test("should render heading split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:heading-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for headings in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify h1 heading
    const h1 = messagesContainer.locator("h1");
    await expect(h1).toBeVisible({ timeout: timeouts.agentResponse });
    await expect(h1).toContainText("Main Title");

    // Verify h2 heading
    const h2 = messagesContainer.locator("h2");
    await expect(h2).toBeVisible();
    await expect(h2).toContainText("Subsection");
  });

  test("should render blockquote split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:blockquote-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for blockquote in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify blockquote is rendered
    const blockquote = messagesContainer.locator("blockquote");
    await expect(blockquote).toBeVisible({ timeout: timeouts.agentResponse });
    await expect(blockquote).toContainText("blockquote");
    await expect(messagesContainer).toContainText("End of quote.");
  });

  test("should render code block with backticks split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:backticks-split");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for code block in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify code block is rendered (backticks were split: ` + `` = ```)
    const codeBlock = messagesContainer.locator("pre code");
    await expect(codeBlock).toBeVisible({ timeout: timeouts.agentResponse });

    // Verify the code content
    const codeText = await codeBlock.textContent();
    expect(codeText).toContain("def foo():");
    expect(codeText).toContain("pass");
  });

  test("should render mixed formatting split across chunks", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.sendMessage(page, "TEST:mixed-formatting");
    await helpers.waitForAgentResponse(page);

    // Wait for all content to be rendered
    await page.waitForTimeout(1000);

    // Look for elements in the messages container
    const messagesContainer = page.locator(selectors.messagesContainer);

    // Verify heading
    const h1 = messagesContainer.locator("h1");
    await expect(h1).toContainText("Getting Started");

    // Verify inline code
    await expect(messagesContainer.locator("code")).toContainText([
      "myFunction",
    ]);

    // Verify code blocks
    const codeBlocks = messagesContainer.locator("pre code");
    expect(await codeBlocks.count()).toBeGreaterThanOrEqual(2);

    // Verify bold text
    const boldText = messagesContainer.locator("strong");
    await expect(boldText.first()).toContainText("First");

    // Verify blockquote with note
    const blockquote = messagesContainer.locator("blockquote");
    await expect(blockquote).toContainText("Note");

    // Verify link
    const link = messagesContainer.locator('a[href="https://example.com"]');
    await expect(link).toBeVisible();
    await expect(link).toContainText("documentation");
  });

  /**
   * Regression test for: https://github.com/inercia/mitto/issues/22
   * Tests that text AFTER a code block with language identifier is NOT rendered as monospace.
   * The issue reported: "an empty monospace block appears before the code,
   * and the text after the closing fence is rendered as monospace too"
   */
  test("should not render text after code block as monospace (issue #22)", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    // Trigger the markdown-code-block scenario which has text after the code block
    await helpers.sendMessage(page, "TEST:code-block-split");
    await helpers.waitForAgentResponse(page);
    await page.waitForTimeout(1000);

    const messagesContainer = page.locator(selectors.messagesContainer);
    const agentMessage = page.locator(selectors.agentMessage).last();

    // 1. Verify the code block exists and has proper structure
    const codeBlock = messagesContainer.locator("pre code");
    await expect(codeBlock).toBeVisible({ timeout: timeouts.agentResponse });

    // 2. Verify there are no empty pre or code elements (the "garbled output" bug)
    const emptyPre = await messagesContainer.locator("pre:empty").count();
    const emptyCode = await messagesContainer.locator("code:empty").count();
    expect(emptyPre).toBe(0);
    expect(emptyCode).toBe(0);

    // 3. Verify "That's the code." (text after code block) is NOT inside pre/code
    // Get the HTML content of the agent message
    const agentHtml = await agentMessage.innerHTML();

    // The text "That's the code." should be in a <p> tag, not inside <pre> or <code>
    // Parse the HTML to check structure
    const textAfterCode = "That's the code.";

    // Verify the text exists
    await expect(agentMessage).toContainText(textAfterCode);

    // Get all <p> tags and verify the text is in one of them
    const paragraphs = agentMessage.locator("p");
    const paragraphCount = await paragraphs.count();
    let foundInParagraph = false;
    for (let i = 0; i < paragraphCount; i++) {
      const text = await paragraphs.nth(i).textContent();
      if (text && text.includes(textAfterCode)) {
        foundInParagraph = true;
        break;
      }
    }
    expect(foundInParagraph).toBe(true);

    // 4. Verify the language identifier "python" is NOT visible as text content
    const fullText = await agentMessage.textContent();
    expect(fullText).not.toContain("```python");
    expect(fullText).not.toContain("```\npython");

    // 5. Verify text before code block ("Here's some code:") is also in a <p> tag
    const textBeforeCode = "Here's some code:";
    await expect(agentMessage).toContainText(textBeforeCode);

    let foundBeforeInParagraph = false;
    for (let i = 0; i < paragraphCount; i++) {
      const text = await paragraphs.nth(i).textContent();
      if (text && text.includes(textBeforeCode)) {
        foundBeforeInParagraph = true;
        break;
      }
    }
    expect(foundBeforeInParagraph).toBe(true);
  });
});

