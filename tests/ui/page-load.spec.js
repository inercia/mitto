// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Basic page load and initial state tests for Mitto Web UI.
 *
 * These tests verify that the application loads correctly and
 * displays the expected initial interface elements.
 */

test.describe('Page Load', () => {
  test('should load the main page', async ({ page }) => {
    await page.goto('/');
    
    // Verify the page title
    await expect(page).toHaveTitle(/Mitto/);
  });

  test('should show the app container', async ({ page }) => {
    await page.goto('/');
    
    // Wait for the app to load (the loading spinner should disappear)
    await expect(page.locator('#app')).toBeVisible();
    
    // The loading spinner should eventually disappear
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should have proper viewport and styling', async ({ page }) => {
    await page.goto('/');
    
    // Wait for app to load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    
    // The body should have the dark theme background
    const body = page.locator('body');
    await expect(body).toHaveClass(/bg-mitto-bg/);
  });
});

test.describe('Initial UI Elements', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for app to fully load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should display the chat input area', async ({ page }) => {
    // Look for the textarea input
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
  });

  test('should display the send button', async ({ page }) => {
    // The send button with "Send" text
    const sendButton = page.locator('button:has-text("Send")');
    await expect(sendButton).toBeVisible({ timeout: 10000 });
  });

  test('should have a sessions sidebar or toggle', async ({ page }) => {
    // Look for "Sessions" heading in the sidebar
    const sessionsHeader = page.getByRole('heading', { name: 'Sessions' });
    await expect(sessionsHeader).toBeVisible({ timeout: 10000 });
  });

  test('should have a new session button', async ({ page }) => {
    // Look for the plus icon button for creating new sessions
    const newButton = page.locator('button[title="New Session"]');
    await expect(newButton).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Responsive Behavior', () => {
  test('should work on mobile viewport', async ({ page }) => {
    // Set mobile viewport
    await page.setViewportSize({ width: 375, height: 667 });
    await page.goto('/');
    
    // Wait for app to load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    
    // Chat input should still be visible
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
  });

  test('should work on tablet viewport', async ({ page }) => {
    // Set tablet viewport
    await page.setViewportSize({ width: 768, height: 1024 });
    await page.goto('/');
    
    // Wait for app to load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
    
    // Chat input should be visible
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
  });

  test('should work on desktop viewport', async ({ page }) => {
    // Set desktop viewport
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/');

    // Wait for app to load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Sessions sidebar should be visible on desktop
    const sessionsHeader = page.getByRole('heading', { name: 'Sessions' });
    await expect(sessionsHeader).toBeVisible({ timeout: 10000 });

    // Chat input should be visible
    const textarea = page.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 10000 });
  });
});

