import { test, expect } from "../fixtures/test-fixtures";

/**
 * File link handling tests for Mitto Web UI.
 *
 * These tests verify that file links are handled correctly:
 * - HTML files should open directly in a new browser tab (rendered)
 * - All other files (including markdown) should open in the unified viewer
 * - The unified viewer handles both code (syntax-highlighted) and markdown (rendered prose)
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

  test("Markdown API endpoint with render=html returns HTML fragment", async ({
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

    // The response should be an HTML fragment (not a full document)
    expect(body).not.toContain("<!DOCTYPE html>");
    expect(body).not.toContain("<html");

    // But it should contain rendered markdown content
    expect(body).toContain("Project Alpha");
    expect(body).toContain("<h1");
    expect(body).toContain("<li");
    expect(body).toContain("main.go");
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

