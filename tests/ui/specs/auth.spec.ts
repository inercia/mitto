import { test, expect } from '../fixtures/test-fixtures';

/**
 * Authentication tests for Mitto Web UI.
 *
 * These tests verify the login flow when authentication is enabled.
 * Set MITTO_TEST_AUTH=1 to enable these tests.
 */

// Skip auth tests unless explicitly enabled
const authEnabled = process.env.MITTO_TEST_AUTH === '1';
const testUsername = process.env.MITTO_TEST_USERNAME || 'testuser';
const testPassword = process.env.MITTO_TEST_PASSWORD || 'testpass123';

test.describe('Authentication - Login Page', () => {
  test.skip(!authEnabled, 'Auth tests disabled. Set MITTO_TEST_AUTH=1 to enable.');

  test('should show login page when not authenticated', async ({ page }) => {
    // Clear any existing cookies
    await page.context().clearCookies();

    await page.goto('/');

    // Should redirect to login page
    await expect(page).toHaveURL(/_auth\.html/);
    await expect(page).toHaveTitle(/Mitto.*Login/);
  });

  test('should display login form elements', async ({ page }) => {
    await page.goto('/_auth.html');

    // Check for login form elements
    await expect(page.locator('h1:has-text("Mitto")')).toBeVisible();
    await expect(page.locator('h2:has-text("Sign In")')).toBeVisible();
    await expect(page.locator('input#username')).toBeVisible();
    await expect(page.locator('input#password')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test('should show error for empty credentials', async ({ page }) => {
    await page.goto('/_auth.html');

    // Try to submit empty form
    await page.locator('button[type="submit"]').click();

    // Browser validation should prevent submission
    const usernameInput = page.locator('input#username');
    await expect(usernameInput).toHaveAttribute('required', '');
  });

  test('should show error for invalid credentials', async ({ page }) => {
    await page.goto('/_auth.html');

    // Enter invalid credentials
    await page.locator('input#username').fill('wronguser');
    await page.locator('input#password').fill('wrongpass');
    await page.locator('button[type="submit"]').click();

    // Should show error message
    const errorDiv = page.locator('#error');
    await expect(errorDiv).toBeVisible({ timeout: 5000 });
    await expect(errorDiv).toContainText(/invalid/i);
  });

  test('should login successfully with valid credentials', async ({ page, selectors, timeouts }) => {
    await page.goto('/_auth.html');

    // Enter valid credentials
    await page.locator('input#username').fill(testUsername);
    await page.locator('input#password').fill(testPassword);
    await page.locator('button[type="submit"]').click();

    // Should redirect to main app
    await expect(page).toHaveURL(/^(?!.*login).*$/);

    // Main app should be visible
    await expect(page.locator(selectors.app)).toBeVisible();
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
  });

  test('should maintain session after login', async ({ page, selectors, timeouts }) => {
    // Login first
    await page.goto('/_auth.html');
    await page.locator('input#username').fill(testUsername);
    await page.locator('input#password').fill(testPassword);
    await page.locator('button[type="submit"]').click();

    // Wait for redirect to main app
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });

    // Refresh the page
    await page.reload();

    // Should still be logged in (not redirected to login)
    await expect(page).not.toHaveURL(/_auth\.html/);
    await expect(page.locator(selectors.chatInput)).toBeVisible({
      timeout: timeouts.appReady,
    });
  });
});

test.describe('Authentication - API Protection', () => {
  test.skip(!authEnabled, 'Auth tests disabled. Set MITTO_TEST_AUTH=1 to enable.');

  test('should return 401 for unauthenticated API requests', async ({ request }) => {
    const response = await request.get('/api/sessions', {
      headers: {
        Cookie: '', // Clear any cookies
      },
    });

    expect(response.status()).toBe(401);
  });

  test('should allow API requests with valid session', async ({ page, request }) => {
    // Login via the UI first
    await page.goto('/_auth.html');
    await page.locator('input#username').fill(testUsername);
    await page.locator('input#password').fill(testPassword);
    await page.locator('button[type="submit"]').click();
    await expect(page.locator('textarea')).toBeVisible({ timeout: 10000 });

    // Get cookies from the page context
    const cookies = await page.context().cookies();
    const sessionCookie = cookies.find((c) => c.name === 'mitto_session');

    expect(sessionCookie).toBeDefined();

    // API request with the session cookie should work
    const response = await request.get('/api/sessions', {
      headers: {
        Cookie: `mitto_session=${sessionCookie?.value}`,
      },
    });

    expect(response.status()).toBe(200);
  });
});

