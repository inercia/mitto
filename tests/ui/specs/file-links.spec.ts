import { test, expect } from "../fixtures/test-fixtures";

/**
 * File link handling tests for Mitto Web UI.
 *
 * These tests verify that file links are handled correctly:
 * - HTML files open in the unified viewer (which renders them in an iframe)
 * - All other files (including markdown) open in the unified viewer
 * - The unified viewer handles both code (syntax-highlighted) and markdown (rendered prose)
 *
 * Note: /api/files? links are intercepted by app.js (backward-compat handler) and
 * converted to viewer.html URLs before opening in a new tab.
 */

test.describe("File Link Handling", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  test("HTML files should open in viewer (rendered in iframe)", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for an HTML file using the /api/files? URL format.
    // app.js backward-compat handler intercepts this and converts it to viewer.html.
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      testDiv.innerHTML = `
        <a id="html-file-link" href="/mitto/api/files?ws=${wsUUID}&path=test-page.html" class="file-link" style="color: #4af;">HTML File</a>
      `;
      document.body.appendChild(testDiv);
    }, workspaceUUID);

    // Wait for a new page to open when clicking the HTML link
    const [newPage] = await Promise.all([
      context.waitForEvent("page"),
      page.click("#html-file-link"),
    ]);

    // Wait for the new page to load
    await newPage.waitForLoadState("domcontentloaded");

    // The app.js backward-compat handler converts /api/files? URLs to viewer.html URLs.
    // Verify the URL went through viewer.html and includes the file path.
    const url = newPage.url();
    expect(url).toContain("viewer.html");
    expect(url).toContain("path=test-page.html");

    await newPage.close();
  });

  test("Non-HTML files should open in viewer", async ({ page, context }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Go file
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      testDiv.innerHTML = `
        <a id="go-file-link" href="/mitto/api/files?ws=${wsUUID}&path=main.go" class="file-link" style="color: #4af;">Go File</a>
      `;
      document.body.appendChild(testDiv);
    }, workspaceUUID);

    // Wait for a new page to open when clicking the Go file link
    const [newPage] = await Promise.all([
      context.waitForEvent("page"),
      page.click("#go-file-link"),
    ]);

    // Wait for the new page to load
    await newPage.waitForLoadState("domcontentloaded");

    // Verify the URL is the viewer page (not direct API)
    const url = newPage.url();
    expect(url).toContain("viewer.html");
    expect(url).toContain("path=main.go");

    // Verify the viewer page loaded (it has a file-path element)
    await expect(newPage.locator("#filePath")).toBeVisible();

    await newPage.close();
  });

  test("HTML file API endpoint renders HTML correctly", async ({ page }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Navigate directly to the HTML file via API
    await page.goto(
      `/mitto/api/files?ws=${workspaceUUID}&path=test-page.html`
    );

    // Verify the HTML is rendered with styles applied
    // The test-page.html has a green test-marker class
    const marker = page.locator("#html-test-marker");
    await expect(marker).toBeVisible();
    await expect(marker).toHaveCSS("color", "rgb(0, 128, 0)"); // green

    // Verify the title is set correctly
    await expect(page).toHaveTitle("Test HTML Page");
  });

  test("Markdown files should open in the unified viewer", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Markdown file
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      testDiv.innerHTML = `
        <a id="md-file-link" href="/mitto/api/files?ws=${wsUUID}&path=README.md" class="file-link" style="color: #4af;">Markdown File</a>
      `;
      document.body.appendChild(testDiv);
    }, workspaceUUID);

    // Wait for a new page to open when clicking the Markdown link
    const [newPage] = await Promise.all([
      context.waitForEvent("page"),
      page.click("#md-file-link"),
    ]);

    // Wait for the new page to load
    await newPage.waitForLoadState("domcontentloaded");

    // Verify the URL is the unified viewer page
    const url = newPage.url();
    expect(url).toContain("viewer.html");
    expect(url).toContain("path=README.md");

    // Verify the viewer page loaded with the file path
    await expect(newPage.locator("#filePath")).toBeVisible();
    await expect(newPage.locator("#filePath")).toContainText("README.md");

    // Verify the Markdown is rendered as HTML (should contain h1 header)
    // README.md contains "# Project Alpha"
    await expect(newPage.locator("h1")).toBeVisible();
    await expect(newPage.locator("h1")).toContainText("Project Alpha");

    // Verify the markdown is displayed in the article container (not code block)
    await expect(newPage.locator("#markdownContent")).toBeVisible();

    // Verify font size buttons are hidden for markdown mode
    await expect(newPage.locator("#decreaseFontBtn")).toBeHidden();
    await expect(newPage.locator("#increaseFontBtn")).toBeHidden();

    await newPage.close();
  });

  test("Markdown files with render=html param should also open in viewer (backward compatibility)", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Markdown file WITH render=html
    // (old recordings may have links with this parameter from before the unified viewer)
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      testDiv.innerHTML = `
        <a id="old-md-file-link" href="/mitto/api/files?ws=${wsUUID}&path=README.md&render=html" class="file-link" style="color: #4af;">Old Markdown Link</a>
      `;
      document.body.appendChild(testDiv);
    }, workspaceUUID);

    // Wait for a new page to open when clicking the Markdown link
    const [newPage] = await Promise.all([
      context.waitForEvent("page"),
      page.click("#old-md-file-link"),
    ]);

    // Wait for the new page to load
    await newPage.waitForLoadState("domcontentloaded");

    // Verify it opens in the unified viewer (not directly)
    const url = newPage.url();
    expect(url).toContain("viewer.html");
    expect(url).toContain("path=README.md");

    // Verify the viewer page loaded and renders markdown
    await expect(newPage.locator("#filePath")).toBeVisible();
    await expect(newPage.locator("h1")).toBeVisible();
    await expect(newPage.locator("h1")).toContainText("Project Alpha");

    await newPage.close();
  });

  test("Markdown API endpoint with render=html returns self-contained HTML page", async ({
    page,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Fetch the rendered markdown directly via API
    const apiResponse = await page.request.get(
      `/mitto/api/files?ws=${workspaceUUID}&path=README.md&render=html`
    );

    expect(apiResponse.ok()).toBeTruthy();
    expect(apiResponse.headers()["content-type"]).toContain("text/html");

    // Verify no-cache headers are set
    expect(apiResponse.headers()["cache-control"]).toContain("no-cache");

    const body = await apiResponse.text();

    // The response is a full HTML document (viewable directly in the browser)
    expect(body).toContain("<!DOCTYPE html>");
    expect(body).toContain("<article>");

    // It should contain rendered markdown content inside the <article>
    expect(body).toContain("Project Alpha");
    expect(body).toContain("<h1");
    expect(body).toContain("<li");
    expect(body).toContain("main.go");

    // It should include the mermaid.js loader script
    expect(body).toContain("mermaid");
  });

  test("Old links without API prefix should be fixed (backward compatibility)", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Go file WITHOUT the /mitto prefix
    // This simulates old recordings that may have links without the API prefix
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      testDiv.innerHTML = `
        <a id="old-go-file-link" href="/api/files?ws=${wsUUID}&path=main.go" class="file-link" style="color: #4af;">Old Go File Link</a>
      `;
      document.body.appendChild(testDiv);
    }, workspaceUUID);

    // Wait for a new page to open when clicking the Go file link
    const [newPage] = await Promise.all([
      context.waitForEvent("page"),
      page.click("#old-go-file-link"),
    ]);

    // Wait for the new page to load
    await newPage.waitForLoadState("domcontentloaded");

    // Verify the URL is the viewer page WITH the /mitto prefix (fixed)
    const url = newPage.url();
    expect(url).toContain("/mitto/viewer.html");
    expect(url).toContain("path=main.go");

    // Verify the viewer page loaded (it has a file-path element)
    await expect(newPage.locator("#filePath")).toBeVisible();

    await newPage.close();
  });
});



/**
 * Unified Viewer UI tests.
 *
 * These tests navigate directly to viewer.html to verify the viewer's own UI:
 * - Close button works (no CSP violation from inline onclick)
 * - Download button is present
 * - Font size buttons shown for code, hidden for markdown
 * - Mermaid diagrams render in the viewer
 * - Source code shows with line numbers
 * - No CSP violations in the console
 */
test.describe("Unified Viewer UI", () => {
  // Helper: get the workspace UUID from the API
  async function getWorkspaceUUID(page: any): Promise<string> {
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const uuid = data.workspaces?.[0]?.uuid;
    expect(uuid).toBeTruthy();
    return uuid;
  }

  // Helper: navigate to viewer.html for a given file
  async function openViewer(page: any, wsUUID: string, filePath: string) {
    await page.goto(
      `/mitto/viewer.html?ws=${wsUUID}&path=${encodeURIComponent(filePath)}`
    );
    await page.waitForLoadState("domcontentloaded");
  }

  // Helper: collect CSP violations from console
  function collectCSPViolations(page: any): string[] {
    const violations: string[] = [];
    page.on("console", (msg: any) => {
      const text = msg.text();
      if (
        text.includes("Content Security Policy") ||
        text.includes("Refused to execute") ||
        text.includes("violates the following Content Security Policy")
      ) {
        violations.push(text);
      }
    });
    return violations;
  }

  test("Source code viewer shows close button, download, and font size buttons", async ({
    page,
  }) => {
    const wsUUID = await getWorkspaceUUID(page);
    const cspViolations = collectCSPViolations(page);

    await openViewer(page, wsUUID, "main.go");

    // Verify file path is displayed
    await expect(page.locator("#filePath")).toBeVisible();
    await expect(page.locator("#filePath")).toContainText("main.go");

    // Verify file size is shown
    await expect(page.locator("#fileSize")).toBeVisible();

    // Verify Close button is present and visible
    await expect(page.locator("#closeBtn")).toBeVisible();
    await expect(page.locator("#closeBtn")).toContainText("Close");

    // Verify Download button is present and has correct title attribute
    // (button is icon-only with an SVG, uses title="Download file" for accessibility)
    await expect(page.locator("#downloadBtn")).toBeVisible();
    await expect(page.locator("#downloadBtn")).toHaveAttribute("title", "Download file");

    // Verify font size buttons are visible (shown for code files)
    await expect(page.locator("#decreaseFontBtn")).toBeVisible();
    await expect(page.locator("#increaseFontBtn")).toBeVisible();

    // Verify code block is visible (not markdown article)
    await expect(page.locator("#codeBlock")).toBeVisible();
    await expect(page.locator("#markdownContent")).toBeHidden();

    // No CSP violations
    expect(cspViolations).toHaveLength(0);
  });

  test("Close button works without CSP violation", async ({ page }) => {
    const wsUUID = await getWorkspaceUUID(page);
    const cspViolations = collectCSPViolations(page);

    await openViewer(page, wsUUID, "main.go");

    // Verify close button is visible
    await expect(page.locator("#closeBtn")).toBeVisible();

    // Click the close button — should not throw a CSP error
    // (window.close() will emit a warning in non-popup windows, but that's expected)
    await page.locator("#closeBtn").click();

    // Verify no CSP violations were triggered
    expect(cspViolations).toHaveLength(0);
  });

  test("Markdown viewer shows close/download but hides font size buttons", async ({
    page,
  }) => {
    const wsUUID = await getWorkspaceUUID(page);
    const cspViolations = collectCSPViolations(page);

    await openViewer(page, wsUUID, "README.md");

    // Verify file path is displayed
    await expect(page.locator("#filePath")).toBeVisible();
    await expect(page.locator("#filePath")).toContainText("README.md");

    // Verify Close button is present
    await expect(page.locator("#closeBtn")).toBeVisible();
    await expect(page.locator("#closeBtn")).toContainText("Close");

    // Verify Download button is present
    await expect(page.locator("#downloadBtn")).toBeVisible();

    // Verify font size buttons are HIDDEN for markdown
    await expect(page.locator("#decreaseFontBtn")).toBeHidden();
    await expect(page.locator("#increaseFontBtn")).toBeHidden();

    // Verify markdown content is visible (not code block)
    await expect(page.locator("#markdownContent")).toBeVisible();
    await expect(page.locator("#codeBlock")).toBeHidden();

    // Verify markdown is actually rendered (not raw text)
    await expect(page.locator("#markdownContent h1")).toBeVisible();
    await expect(page.locator("#markdownContent h1")).toContainText(
      "Project Alpha"
    );

    // No CSP violations
    expect(cspViolations).toHaveLength(0);
  });

  test("Markdown viewer close button works without CSP violation", async ({
    page,
  }) => {
    const wsUUID = await getWorkspaceUUID(page);
    const cspViolations = collectCSPViolations(page);

    await openViewer(page, wsUUID, "README.md");

    await expect(page.locator("#closeBtn")).toBeVisible();
    await page.locator("#closeBtn").click();

    // No CSP violations
    expect(cspViolations).toHaveLength(0);
  });

  test("Markdown with mermaid diagrams renders SVG in viewer", async ({
    page,
  }) => {
    const wsUUID = await getWorkspaceUUID(page);
    const cspViolations = collectCSPViolations(page);

    // Mock the mermaid CDN to return a mock mermaid object.
    // Route must be set up BEFORE navigating to the page.
    await page.route(
      "**/cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js",
      async (route: any) => {
        await route.fulfill({
          contentType: "application/javascript",
          // The mock mermaid extracts labels from [Label] and {Label} patterns
          // and produces an SVG with text nodes for each label.
          body: `
            window.mermaid = {
              initialize: function() {},
              render: async function(id, def) {
                var labels = [];
                var re = /[\\[{]([^\\]{}]+)[\\}\\]]/g;
                var m;
                while ((m = re.exec(def)) !== null) labels.push(m[1].trim());
                var texts = labels.map(function(l, i) {
                  return '<text x="10" y="' + (20 + i * 25) + '">' +
                    l.replace(/[<>&"]/g, '') + '</text>';
                }).join('');
                return {
                  svg: '<svg xmlns="http://www.w3.org/2000/svg" width="200" height="' +
                    (labels.length * 25 + 20) + '" id="' + id +
                    '"><g>' + texts + '</g></svg>'
                };
              }
            };
          `,
        });
      }
    );

    await openViewer(page, wsUUID, "ARCHITECTURE.md");

    // Verify the viewer loaded with correct file
    await expect(page.locator("#filePath")).toBeVisible();
    await expect(page.locator("#filePath")).toContainText("ARCHITECTURE.md");

    // Verify markdown content is displayed
    await expect(page.locator("#markdownContent")).toBeVisible();
    await expect(page.locator("#markdownContent h1")).toContainText(
      "Architecture"
    );

    // Wait for mermaid to load (via our CDN mock) and render the diagram.
    // The <pre class="mermaid"> should be replaced by <div class="mermaid-diagram">.
    const mermaidDiagram = page.locator(".mermaid-diagram");
    await expect(mermaidDiagram.first()).toBeVisible({ timeout: 15000 });

    // Verify the SVG is present inside the mermaid diagram
    const svg = mermaidDiagram.first().locator("svg");
    await expect(svg).toBeVisible();

    // Verify the diagram contains expected flowchart node labels
    const diagramText = await mermaidDiagram.first().textContent();
    expect(diagramText).toContain("Client");
    expect(diagramText).toContain("API Gateway");
    expect(diagramText).toContain("Database");

    // The raw mermaid code block should NOT be visible
    const rawMermaidBlock = page.locator(
      '#markdownContent pre.mermaid'
    );
    expect(await rawMermaidBlock.count()).toBe(0);

    // No CSP violations
    expect(cspViolations).toHaveLength(0);
  });

  test("Dynamically created mermaid script receives correct CSP nonce", async ({
    page,
  }) => {
    const wsUUID = await getWorkspaceUUID(page);

    // Track nonce values: capture the nonce from the page's inline script,
    // then verify the dynamically created mermaid script gets the same nonce.
    let capturedPageNonce = "";
    let capturedMermaidScriptNonce = "";

    // Mock the mermaid CDN — we don't need real mermaid, just need the script
    // element to be created so we can inspect its nonce attribute.
    await page.route(
      "**/cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js",
      async (route: any) => {
        await route.fulfill({
          contentType: "application/javascript",
          body: "window.mermaid = { initialize: function(){}, render: async function(id, def) { return { svg: '<svg></svg>' }; } };",
        });
      }
    );

    await openViewer(page, wsUUID, "ARCHITECTURE.md");

    // Wait for mermaid diagram to be processed
    await page.waitForTimeout(2000);

    // Check the nonce on the page's inline script vs the dynamically created mermaid script
    const nonces = await page.evaluate(() => {
      // Get the nonce from the page's main inline script (the one with our code)
      const inlineScript = document.querySelector('script[nonce]');
      const pageNonce = inlineScript ? (inlineScript as HTMLScriptElement).nonce : "";

      // Get the nonce from the dynamically created mermaid script
      const mermaidScript = document.querySelector(
        'script[src*="mermaid"]'
      );
      const mermaidNonce = mermaidScript
        ? (mermaidScript as HTMLScriptElement).nonce ||
          mermaidScript.getAttribute("nonce") ||
          ""
        : "NOT_FOUND";

      return { pageNonce, mermaidNonce };
    });

    // The page should have a nonce (set by CSP middleware)
    expect(nonces.pageNonce).toBeTruthy();
    expect(nonces.pageNonce.length).toBeGreaterThan(0);

    // The mermaid script should have the SAME nonce as the page's inline script
    expect(nonces.mermaidNonce).toBe(nonces.pageNonce);
  });
});