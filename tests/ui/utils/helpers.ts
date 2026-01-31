import { Page, expect } from '@playwright/test';
import { selectors, timeouts } from './selectors';

/**
 * Wait for the Mitto app to be fully loaded and ready
 */
export async function waitForAppReady(page: Page): Promise<void> {
  // Wait for chat input to be visible (may be disabled if no session)
  // This is a more reliable indicator that the app is ready than checking for spinner
  await expect(page.locator(selectors.chatInput)).toBeVisible({
    timeout: timeouts.appReady,
  });
}

/**
 * Wait for an active session with enabled chat input
 */
export async function waitForActiveSession(page: Page): Promise<void> {
  // Wait for chat input to be visible and enabled
  const textarea = page.locator(selectors.chatInput);
  await expect(textarea).toBeVisible({ timeout: timeouts.appReady });
  await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
}

/**
 * Ensure there's an active session by creating one if needed
 */
export async function ensureActiveSession(page: Page): Promise<void> {
  const textarea = page.locator(selectors.chatInput);

  // Check if textarea is disabled (no active session)
  const isDisabled = await textarea.isDisabled();
  if (isDisabled) {
    // Create a new session
    const newButton = page.locator(selectors.newSessionButton);
    await expect(newButton).toBeVisible({ timeout: timeouts.shortAction });
    await newButton.click();

    // Wait for session to be ready
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
  }
}

/**
 * Send a message in the chat
 */
export async function sendMessage(page: Page, message: string): Promise<void> {
  const textarea = page.locator(selectors.chatInput);
  await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
  await textarea.fill(message);
  await page.locator(selectors.sendButton).click();
}

/**
 * Wait for a user message to appear in the chat
 */
export async function waitForUserMessage(
  page: Page,
  message: string
): Promise<void> {
  await expect(page.locator(`text=${message}`)).toBeVisible({
    timeout: timeouts.shortAction,
  });
}

/**
 * Wait for an agent response to appear
 * Uses .first() to handle cases where multiple agent messages exist
 */
export async function waitForAgentResponse(page: Page): Promise<void> {
  await expect(page.locator(selectors.agentMessage).first()).toBeVisible({
    timeout: timeouts.agentResponse,
  });
}

/**
 * Create a new session via the UI
 */
export async function createNewSession(page: Page): Promise<void> {
  await page.locator(selectors.newSessionButton).click();
  // Wait for the new session to be ready
  await expect(page.locator(selectors.chatInput)).toBeEnabled({
    timeout: timeouts.shortAction,
  });
}

/**
 * Switch to a session by name
 */
export async function switchToSession(
  page: Page,
  sessionName: string
): Promise<void> {
  const sessionItem = page.locator(selectors.sessionItem(sessionName));
  await expect(sessionItem).toBeVisible({ timeout: timeouts.shortAction });
  await sessionItem.click();
  await expect(page.locator(selectors.chatInput)).toBeEnabled({
    timeout: timeouts.shortAction,
  });
}

/**
 * Generate a unique test message
 */
export function uniqueMessage(prefix: string = 'Test'): string {
  return `${prefix} ${Date.now()}`;
}

/**
 * Navigate to the app and wait for it to be ready
 */
export async function navigateAndWait(page: Page): Promise<void> {
  await page.goto('/');
  await waitForAppReady(page);
}

/**
 * Navigate to the app, wait for it to be ready, and ensure there's an active session
 */
export async function navigateAndEnsureSession(page: Page): Promise<void> {
  await page.goto('/');
  await waitForAppReady(page);
  await ensureActiveSession(page);
}

