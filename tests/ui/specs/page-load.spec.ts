import { test, expect } from "../fixtures/test-fixtures";

/**
 * Basic page load and initial state tests for Mitto Web UI.
 *
 * These tests verify that the application loads correctly and
 * displays the expected initial interface elements.
 */

test.describe("Page Load", () => {
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
