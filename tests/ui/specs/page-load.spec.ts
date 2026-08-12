import { test, expect } from "../fixtures/test-fixtures";

/**
 * Basic page load and initial state tests for Mitto Web UI.
 *
 * These tests verify that the application loads correctly and
 * displays the expected initial interface elements.
 */

test.describe("Page Load", () => {
  test("first service-worker install does not reload during API bootstrap", async ({
    page,
  }) => {
    const navigations: string[] = [];
    const failedApiRequests: string[] = [];

    page.on("framenavigated", (frame) => {
      if (frame === page.mainFrame()) navigations.push(frame.url());
    });
    page.on("requestfailed", (request) => {
      if (new URL(request.url()).pathname.includes("/api/")) {
        failedApiRequests.push(request.url());
      }
    });

    await page.goto("/");
    await expect(
      page.getByText("project-alpha", { exact: true }).last(),
    ).toBeVisible();
    await page.waitForTimeout(500);

    expect(navigations).toHaveLength(1);
    expect(failedApiRequests).toEqual([]);
  });

  test("session stream opens with browser-native timers", async ({ page }) => {
    await page.goto("/");

    const opened = await page.evaluate(async () => {
      // @ts-expect-error Browser-served JavaScript module has no TS declaration.
      const { SessionStream } = await import("/sdk/realtime/session-stream.js");
      // @ts-expect-error Browser-served JavaScript module has no TS declaration.
      const { resolveConfig } = await import("/sdk/core/config.js");
      const sockets: any[] = [];
      class FakeWebSocket {
        static OPEN = 1;
        readyState = 0;
        onopen: (() => void) | null = null;
        onclose: ((event: object) => void) | null = null;
        onmessage = null;
        onerror = null;
        constructor() {
          sockets.push(this);
        }
        send() {}
        close() {
          this.onclose?.({ code: 1000, reason: "", wasClean: true });
        }
      }
      const stream = new SessionStream(
        resolveConfig({
          baseUrl: location.origin,
          fetch: window.fetch.bind(window),
          WebSocket: FakeWebSocket,
        }),
        "existing-session",
      );
      let didOpen = false;
      stream.on("open", () => (didOpen = true));
      stream.connect();
      sockets[0].readyState = FakeWebSocket.OPEN;
      sockets[0].onopen();
      stream.close();
      return didOpen;
    });

    expect(opened).toBe(true);
  });

  test("should load the main page", async ({ page }) => {
    await page.goto("/");

    // Verify the page title
    await expect(page).toHaveTitle(/Mitto/);
  });

  test("should show the app container", async ({
    page,
    selectors,
    timeouts,
  }) => {
    await page.goto("/");

    // Wait for the app to load (the loading spinner should disappear)
    await expect(page.locator(selectors.app)).toBeVisible();

    // The loading spinner should eventually disappear
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });
  });

  test("should have proper viewport and styling", async ({
    page,
    selectors,
    timeouts,
  }) => {
    await page.goto("/");

    // Wait for app to load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // The body should have the dark theme background
    const body = page.locator(selectors.body);
    await expect(body).toHaveClass(/bg-mitto-bg/);
  });
});

test.describe("Initial UI Elements", () => {
  test.beforeEach(async ({ page, selectors, timeouts }) => {
    await page.goto("/");
    // Wait for app to fully load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });
  });

  test("should display the chat input area", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Look for the textarea input
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  });

  test("should display the send button", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // The send button (icon-only, paper plane)
    const sendButton = page.locator(selectors.sendButton);
    await expect(sendButton).toBeVisible({ timeout: timeouts.appReady });
  });

  test("should have a sessions sidebar or toggle", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Look for "Conversations" heading in the sidebar
    const sessionsHeader = page.locator(selectors.sessionsHeader);
    await expect(sessionsHeader).toBeVisible({ timeout: timeouts.appReady });
  });

  test("should have a new session button", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Look for the plus icon button for creating new sessions
    const newButton = page.locator(selectors.newSessionButton);
    await expect(newButton).toBeVisible({ timeout: timeouts.appReady });
  });
});

test.describe("Responsive Behavior", () => {
  test("should work on mobile viewport", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Set mobile viewport
    await page.setViewportSize({ width: 375, height: 667 });
    await page.goto("/");

    // Wait for app to load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // Chat input should still be visible
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  });

  test("should work on tablet viewport", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Set tablet viewport
    await page.setViewportSize({ width: 768, height: 1024 });
    await page.goto("/");

    // Wait for app to load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // Chat input should be visible
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  });

  test("should work on desktop viewport", async ({
    page,
    selectors,
    timeouts,
  }) => {
    // Set desktop viewport
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/");

    // Wait for app to load
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // Sessions sidebar should be visible on desktop
    const sessionsHeader = page.locator(selectors.sessionsHeader);
    await expect(sessionsHeader).toBeVisible({ timeout: timeouts.appReady });

    // Chat input should be visible
    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  });
});
