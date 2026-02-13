import { test, expect } from "../fixtures/test-fixtures";

/**
 * File link handling tests for Mitto Web UI.
 *
 * These tests verify that file links are handled correctly:
 * - HTML files should open directly in a new browser tab (rendered)
 * - Other files should open in the syntax-highlighted viewer
 */

test.describe("File Link Handling", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  test("HTML files should open directly via API (not in viewer)", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for an HTML file
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

    // Verify the URL is the direct API endpoint (not viewer.html)
    const url = newPage.url();
    expect(url).toContain("/api/files?");
    expect(url).toContain("path=test-page.html");
    expect(url).not.toContain("viewer.html");

    // Verify the HTML is rendered (not shown as source code)
    // The test-page.html has a specific marker we can check
    await expect(newPage.locator("#html-test-marker")).toBeVisible();
    await expect(newPage.locator("#html-test-marker")).toHaveText(
      "This is a test HTML file for Playwright tests."
    );

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

  test("Markdown files should include render=html in the link URL", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Markdown file (simulating what the file link detection produces)
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      // This is the URL format that file link detection should produce for Markdown files
      testDiv.innerHTML = `
        <a id="md-file-link" href="/mitto/api/files?ws=${wsUUID}&path=README.md&render=html" class="file-link" style="color: #4af;">Markdown File</a>
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

    // Verify the URL contains render=html
    const url = newPage.url();
    expect(url).toContain("/api/files?");
    expect(url).toContain("path=README.md");
    expect(url).toContain("render=html");
    expect(url).not.toContain("viewer.html");

    // Verify the Markdown is rendered as HTML (should contain h1 header)
    // README.md contains "# Project Alpha"
    await expect(newPage.locator("h1")).toBeVisible();
    await expect(newPage.locator("h1")).toContainText("Project Alpha");

    await newPage.close();
  });

  test("Markdown files without render=html should have it added automatically (backward compatibility)", async ({
    page,
    context,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Inject a test link for a Markdown file WITHOUT render=html
    // (simulating an old recording that predates the render=html feature)
    await page.evaluate((wsUUID) => {
      const testDiv = document.createElement("div");
      testDiv.id = "test-file-links";
      testDiv.style.cssText =
        "position: fixed; bottom: 100px; left: 50%; transform: translateX(-50%); background: #333; padding: 20px; border-radius: 8px; z-index: 9999;";
      // Old URL format without render=html
      testDiv.innerHTML = `
        <a id="old-md-file-link" href="/mitto/api/files?ws=${wsUUID}&path=README.md" class="file-link" style="color: #4af;">Old Markdown Link</a>
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

    // Verify the URL had render=html added automatically
    const url = newPage.url();
    expect(url).toContain("/api/files?");
    expect(url).toContain("path=README.md");
    expect(url).toContain("render=html");
    expect(url).not.toContain("viewer.html");

    // Verify the Markdown is rendered as HTML (should contain h1 header)
    // README.md contains "# Project Alpha"
    await expect(newPage.locator("h1")).toBeVisible();
    await expect(newPage.locator("h1")).toContainText("Project Alpha");

    await newPage.close();
  });

  test("Markdown API endpoint with render=html returns rendered HTML", async ({
    page,
  }) => {
    // Get the workspace UUID from the API
    const response = await page.request.get("/mitto/api/workspaces");
    const data = await response.json();
    const workspaceUUID = data.workspaces?.[0]?.uuid;
    expect(workspaceUUID).toBeTruthy();

    // Navigate directly to the Markdown file via API with render=html
    await page.goto(
      `/mitto/api/files?ws=${workspaceUUID}&path=README.md&render=html`
    );

    // Verify the content is rendered as HTML (not raw Markdown)
    // README.md contains "# Project Alpha" which should become <h1>Project Alpha</h1>
    await expect(page.locator("h1")).toBeVisible();
    await expect(page.locator("h1")).toContainText("Project Alpha");

    // Also check that a bullet point list is rendered as HTML
    // README.md contains "- `main.go` - Entry point" which should become <li>
    await expect(page.locator("li").first()).toBeVisible();
    await expect(page.locator("li").first()).toContainText("main.go");
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

