// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Session management tests for Mitto Web UI.
 *
 * These tests verify that session lifecycle operations work correctly:
 * creating, listing, switching, renaming, and deleting sessions.
 */

test.describe('Session Management', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for app to fully load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should create a new session when clicking New button', async ({ page }) => {
    // Find and click the new session button (plus icon)
    const newButton = page.locator('button[title="New Session"]');
    await expect(newButton).toBeVisible({ timeout: 10000 });

    // Click to create new session
    await newButton.click();

    // Wait a bit for the session to be created
    await page.waitForTimeout(1000);

    // A system message should appear indicating connection (use specific selector)
    const systemMessage = page.locator('.text-gray-500.bg-slate-800\\/50').filter({ hasText: /Connected to/ });
    await expect(systemMessage).toBeVisible({ timeout: 10000 });
  });

  test('should display sessions in the sidebar', async ({ page }) => {
    // The sessions sidebar should have the Sessions heading
    const sessionsHeader = page.getByRole('heading', { name: 'Sessions' });
    await expect(sessionsHeader).toBeVisible({ timeout: 10000 });

    // The sidebar should be visible (contains the Sessions heading)
    const sidebar = sessionsHeader.locator('xpath=ancestor::div[contains(@class, "flex-col")]');
    await expect(sidebar.first()).toBeVisible({ timeout: 5000 });
  });

  test('should show session details in sidebar items', async ({ page }) => {
    // Session items should show the session name
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    
    // Should contain text content (session name)
    const sessionName = sessionItem.locator('.font-medium');
    await expect(sessionName).toBeVisible();
    await expect(sessionName).not.toBeEmpty();
  });

  test('should show rename and delete buttons on hover', async ({ page }) => {
    // Find a session item
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    
    // Hover over the session item
    await sessionItem.hover();
    
    // Rename button should appear
    const renameButton = sessionItem.locator('button[title="Rename"]');
    await expect(renameButton).toBeVisible({ timeout: 5000 });
    
    // Delete button should appear
    const deleteButton = sessionItem.locator('button[title="Delete"]');
    await expect(deleteButton).toBeVisible({ timeout: 5000 });
  });
});

test.describe('Session Rename', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should open rename dialog when clicking rename button', async ({ page }) => {
    // Find a session item and hover
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.hover();
    
    // Click rename button
    const renameButton = sessionItem.locator('button[title="Rename"]');
    await renameButton.click();
    
    // Rename dialog should appear
    const dialog = page.locator('text=Rename Session');
    await expect(dialog).toBeVisible({ timeout: 5000 });
    
    // Input field should be visible
    const input = page.locator('input[placeholder="Session name"]');
    await expect(input).toBeVisible();
  });

  test('should close rename dialog when clicking Cancel', async ({ page }) => {
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.hover();
    
    // Open rename dialog
    const renameButton = sessionItem.locator('button[title="Rename"]');
    await renameButton.click();
    await expect(page.locator('text=Rename Session')).toBeVisible({ timeout: 5000 });
    
    // Click Cancel
    const cancelButton = page.locator('button:has-text("Cancel")');
    await cancelButton.click();
    
    // Dialog should close
    await expect(page.locator('text=Rename Session')).toBeHidden();
  });

  test('should rename session when submitting new name', async ({ page }) => {
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.hover();
    
    // Open rename dialog
    await sessionItem.locator('button[title="Rename"]').click();
    await expect(page.locator('text=Rename Session')).toBeVisible({ timeout: 5000 });
    
    // Enter new name
    const newName = `Test Session ${Date.now()}`;
    const input = page.locator('input[placeholder="Session name"]');
    await input.fill(newName);
    
    // Click Save
    await page.locator('button:has-text("Save")').click();
    
    // Dialog should close
    await expect(page.locator('text=Rename Session')).toBeHidden();
    
    // Session name should be updated in the sidebar (use first() to avoid strict mode violation)
    await expect(page.locator(`text=${newName}`).first()).toBeVisible({ timeout: 5000 });
  });
});

test.describe('Session Delete', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should open delete confirmation dialog when clicking delete button', async ({ page }) => {
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]').first();
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.hover();
    
    // Click delete button
    await sessionItem.locator('button[title="Delete"]').click();
    
    // Delete dialog should appear
    const dialog = page.locator('text=Delete Session');
    await expect(dialog).toBeVisible({ timeout: 5000 });
  });
});

