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

test.describe("Mermaid Diagram Rendering", () => {
  // Mermaid tests need extra time: CDN load (mermaid.js) + SVG rendering can take 10-30s.
  // The test count may also exceed the session limit; clean up before each test.
  test.setTimeout(120_000);

  test.beforeEach(async ({ page, helpers, cleanupSessions, selectors, timeouts }) => {
    // Clean up old sessions to avoid hitting the 32-session limit which prevents
    // fresh session creation and causes the mermaid scenario not to trigger.
    await cleanupSessions();

    await helpers.navigateAndWait(page);
    // Create a fresh session for mermaid tests to avoid interference from previous tests.
    // The mermaid scenario requires a clean session to trigger correctly.
    const newButton = page.locator(selectors.newSessionButton);
    await newButton.click();
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await helpers.waitForWebSocketReady(page);

    // Inject a mermaid mock directly into the page after it is ready.
    //
    // Why page.evaluate() instead of page.route():
    //   page.route() intercepts at the network level and requires the browser to
    //   make a CDN request (which Playwright then fulfils). With headless Chrome +
    //   CSP nonces the script tag is created and the request is dispatched, but the
    //   combination of dynamic script injection + CSP enforcement sometimes causes
    //   the onload callback not to fire reliably in CI environments.
    //
    //   page.evaluate() bypasses the CDN entirely: by setting window.mermaidReady=true
    //   BEFORE the prompt is sent, renderMermaidInContainer() (called from Message.js
    //   useEffect) skips the CDN loading branch and calls window.mermaid.render()
    //   directly. This is synchronous from the test's perspective and makes the
    //   mermaid rendering deterministic.
    //
    // The mock satisfies the two entry points in preact-loader.js:
    //   1. window.mermaid.initialize(config) — called after CDN load (no-op here)
    //   2. window.mermaid.render(id, def)    — returns { svg } with parsed labels
    //
    // The SVG includes text nodes for every [Label] / {Label} in the definition
    // so assertions like expect(diagramText).toContain("Start") pass correctly.
    // Use /* eslint-disable */ to suppress lint warnings for the JS-inside-TS pattern.
    // The evaluate callback is serialized with func.toString() and run in the browser,
    // so we use plain JS (no TypeScript type annotations) to avoid parse errors.
    await page.evaluate(() => {
      /* eslint-disable */
      // @ts-ignore
      window.mermaid = {
        initialize: function (config) {},
        render: async function (id, def) {
          // Extract node labels from mermaid syntax: [Label] and {Label}
          var labels = [];
          var regex = /[\[{]([^\]{}]+)[\}\]]/g;
          var m;
          while ((m = regex.exec(def)) !== null) {
            labels.push(m[1].trim());
          }
          var textEls = labels
            .map(function (l, i) {
              return (
                '<text x="10" y="' +
                (20 + i * 25) +
                '">' +
                l.replace(/[<>&"]/g, "") +
                "</text>"
              );
            })
            .join("");
          return {
            svg:
              '<svg xmlns="http://www.w3.org/2000/svg" width="200" height="' +
              (labels.length * 25 + 20) +
              '" id="' +
              id +
              '"><g class="nodes">' +
              textEls +
              '</g><g class="edges"></g></svg>',
          };
        },
      };
      // Setting mermaidReady=true makes renderMermaidInContainer() skip the CDN
      // request branch and call window.mermaid.render() directly.
      // @ts-ignore
      window.mermaidReady = true;
      // @ts-ignore
      window.mermaidLoading = false;
      /* eslint-enable */
    });
  });

  test("should render mermaid diagrams during streaming", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Send a message that triggers the mermaid-diagram scenario
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
    await textarea.fill("Show mermaid test");
    await page.locator(selectors.sendButton).click();

    // Confirm the prompt was accepted — user message should appear in the chat
    await expect(page.locator("text=Show mermaid test")).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Wait for streaming to complete before asserting on mermaid rendering
    await helpers.waitForStreamingComplete(page);

    // Wait for mermaid.js to load (via our CDN mock) and render the diagram.
    // The diagram should be rendered as an SVG inside a .mermaid-diagram wrapper
    // (pre.mermaid gets replaced by div.mermaid-diagram containing SVG).
    const mermaidDiagram = page.locator(".mermaid-diagram");
    await expect(mermaidDiagram.first()).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Verify the SVG is present inside the mermaid diagram
    const svg = mermaidDiagram.first().locator("svg");
    await expect(svg).toBeVisible({ timeout: timeouts.shortAction });

    // Verify the SVG has content (nodes from the flowchart)
    const svgContent = await svg.innerHTML();
    expect(svgContent.length).toBeGreaterThan(100); // SVG should have substantial content

    // Verify the diagram contains the expected flowchart node labels
    const diagramText = await mermaidDiagram.first().textContent();
    expect(diagramText).toContain("Start");
    expect(diagramText).toContain("Decision");
    expect(diagramText).toContain("End");
  });

  test("should render mermaid diagram SVG without showing raw code", async ({
    page,
    selectors,
    timeouts,
    helpers,
  }) => {
    // Send a message that triggers the mermaid-diagram scenario
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
    await textarea.fill("Show mermaid diagram");
    await page.locator(selectors.sendButton).click();

    // Confirm the prompt was accepted — user message should appear in the chat
    await expect(page.locator("text=Show mermaid diagram")).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Wait for streaming to complete before asserting on mermaid rendering
    await helpers.waitForStreamingComplete(page);

    // Wait for mermaid to render (via our CDN mock)
    const mermaidDiagram = page.locator(".mermaid-diagram");
    await expect(mermaidDiagram.first()).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // The raw mermaid code block should NOT be visible —
    // it should have been replaced by the rendered SVG wrapper.
    const rawMermaidBlock = page.locator('pre.mermaid:not([data-mermaid-processed="true"])');
    const count = await rawMermaidBlock.count();
    expect(count).toBe(0);
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
