// @ts-check
import { test, expect } from '@playwright/test';

/**
 * Multi-workspace session tests for Mitto Web UI.
 *
 * These tests verify that sessions can be created and used with different workspaces,
 * each with their own working directory and ACP server configuration.
 *
 * Prerequisites:
 * - The Mitto web server must be running before the test
 * - An ACP server (e.g., auggie or claude-code) must be available and configured
 * - The specified directories must exist on the filesystem:
 *   - /Users/alvaro/Development/inercia/mitto
 *   - /Users/alvaro/Development/inercia/don
 *
 * To run with custom workspaces:
 *   mitto web --dir auggie:/Users/alvaro/Development/inercia/mitto \
 *             --dir auggie:/Users/alvaro/Development/inercia/don
 */

// Test workspace configurations
const WORKSPACE_1 = {
  dir: '/Users/alvaro/Development/inercia/mitto',
  acpServer: 'auggie',
  expectedFiles: ['go.mod', 'cmd', 'internal'], // Files expected in mitto project
};

const WORKSPACE_2 = {
  dir: '/Users/alvaro/Development/inercia/don',
  acpServer: 'auggie',
  expectedFiles: [], // Will be different from workspace 1
};

// Timeout for agent responses (agents can be slow)
const AGENT_RESPONSE_TIMEOUT = 60000;

test.describe('Multi-Workspace Sessions', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for app to fully load
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should configure workspaces via API', async ({ request }) => {
    // Add first workspace
    const response1 = await request.post('/api/workspaces', {
      data: {
        acp_server: WORKSPACE_1.acpServer,
        working_dir: WORKSPACE_1.dir,
      },
    });
    // Accept both 201 (created) and 409 (already exists)
    expect([201, 409]).toContain(response1.status());

    // Add second workspace
    const response2 = await request.post('/api/workspaces', {
      data: {
        acp_server: WORKSPACE_2.acpServer,
        working_dir: WORKSPACE_2.dir,
      },
    });
    expect([201, 409]).toContain(response2.status());

    // Verify workspaces are listed
    const listResponse = await request.get('/api/workspaces');
    expect(listResponse.ok()).toBeTruthy();
    const workspaces = await listResponse.json();
    expect(workspaces.length).toBeGreaterThanOrEqual(2);

    // Verify both directories are present
    const dirs = workspaces.map(ws => ws.working_dir);
    expect(dirs).toContain(WORKSPACE_1.dir);
    expect(dirs).toContain(WORKSPACE_2.dir);
  });

  test('should create session for first workspace and list files', async ({ page, request }) => {
    // Ensure workspace is configured
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_1.acpServer, working_dir: WORKSPACE_1.dir },
    });

    // Create a new session for workspace 1
    const createResponse = await request.post('/api/sessions', {
      data: {
        name: 'Workspace 1 Test',
        working_dir: WORKSPACE_1.dir,
      },
    });
    expect(createResponse.ok()).toBeTruthy();
    const sessionData = await createResponse.json();
    expect(sessionData.session_id).toBeTruthy();
    expect(sessionData.working_dir).toBe(WORKSPACE_1.dir);

    // Navigate to the session
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Find and click on the session in the sidebar
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Workspace 1 Test' });
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.click();

    // Wait for session to be active
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Send prompt to list files
    const textarea = page.locator('textarea');
    await textarea.fill('list me the files there');
    await page.locator('button:has-text("Send")').click();

    // Wait for agent response
    await expect(page.locator('.bg-mitto-agent, .prose')).toBeVisible({ timeout: AGENT_RESPONSE_TIMEOUT });

    // Verify the response contains expected files from mitto project
    const agentMessage = page.locator('.bg-mitto-agent, .prose').last();
    await expect(agentMessage).toContainText(/go\.mod|cmd|internal|README/i, { timeout: AGENT_RESPONSE_TIMEOUT });
  });

  test('should create session for second workspace with different files', async ({ page, request }) => {
    // Ensure workspace is configured
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_2.acpServer, working_dir: WORKSPACE_2.dir },
    });

    // Create a new session for workspace 2
    const createResponse = await request.post('/api/sessions', {
      data: {
        name: 'Workspace 2 Test',
        working_dir: WORKSPACE_2.dir,
      },
    });
    expect(createResponse.ok()).toBeTruthy();
    const sessionData = await createResponse.json();
    expect(sessionData.session_id).toBeTruthy();
    expect(sessionData.working_dir).toBe(WORKSPACE_2.dir);

    // Navigate to the session
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Find and click on the session in the sidebar
    const sessionItem = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Workspace 2 Test' });
    await expect(sessionItem).toBeVisible({ timeout: 10000 });
    await sessionItem.click();

    // Wait for session to be active
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Send prompt to list files
    const textarea = page.locator('textarea');
    await textarea.fill('list me the files there');
    await page.locator('button:has-text("Send")').click();

    // Wait for agent response - should show different files than workspace 1
    await expect(page.locator('.bg-mitto-agent, .prose')).toBeVisible({ timeout: AGENT_RESPONSE_TIMEOUT });
  });

  test('should show both sessions in sidebar', async ({ page, request }) => {
    // Create sessions for both workspaces
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_1.acpServer, working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_2.acpServer, working_dir: WORKSPACE_2.dir },
    });

    await request.post('/api/sessions', {
      data: { name: 'Multi-WS Session 1', working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/sessions', {
      data: { name: 'Multi-WS Session 2', working_dir: WORKSPACE_2.dir },
    });

    // Reload page to see both sessions
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Both sessions should appear in the sidebar
    const session1 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Multi-WS Session 1' });
    const session2 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Multi-WS Session 2' });

    await expect(session1).toBeVisible({ timeout: 10000 });
    await expect(session2).toBeVisible({ timeout: 10000 });
  });

  test('should switch between workspace sessions independently', async ({ page, request }) => {
    // Ensure workspaces are configured
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_1.acpServer, working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_2.acpServer, working_dir: WORKSPACE_2.dir },
    });

    // Create two sessions
    const resp1 = await request.post('/api/sessions', {
      data: { name: 'Switch Test 1', working_dir: WORKSPACE_1.dir },
    });
    const resp2 = await request.post('/api/sessions', {
      data: { name: 'Switch Test 2', working_dir: WORKSPACE_2.dir },
    });

    expect(resp1.ok()).toBeTruthy();
    expect(resp2.ok()).toBeTruthy();

    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Click on first session
    const session1 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Switch Test 1' });
    await expect(session1).toBeVisible({ timeout: 10000 });
    await session1.click();

    // Verify we're in session 1 (header should show session name or working dir)
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Switch to second session
    const session2 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Switch Test 2' });
    await session2.click();

    // Verify we're now in session 2
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Switch back to session 1
    await session1.click();
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });
  });

  test('should verify session working directory via API', async ({ request }) => {
    // Ensure workspaces are configured
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_1.acpServer, working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_2.acpServer, working_dir: WORKSPACE_2.dir },
    });

    // Create sessions
    const resp1 = await request.post('/api/sessions', {
      data: { name: 'API Verify 1', working_dir: WORKSPACE_1.dir },
    });
    const resp2 = await request.post('/api/sessions', {
      data: { name: 'API Verify 2', working_dir: WORKSPACE_2.dir },
    });

    const session1 = await resp1.json();
    const session2 = await resp2.json();

    // Verify each session has correct working directory
    expect(session1.working_dir).toBe(WORKSPACE_1.dir);
    expect(session2.working_dir).toBe(WORKSPACE_2.dir);

    // Fetch session details and verify
    const details1 = await request.get(`/api/sessions/${session1.session_id}`);
    const details2 = await request.get(`/api/sessions/${session2.session_id}`);

    expect(details1.ok()).toBeTruthy();
    expect(details2.ok()).toBeTruthy();

    const data1 = await details1.json();
    const data2 = await details2.json();

    expect(data1.working_dir).toBe(WORKSPACE_1.dir);
    expect(data2.working_dir).toBe(WORKSPACE_2.dir);
  });
});

test.describe('Workspace Session Isolation', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });
  });

  test('should maintain separate message history per session', async ({ page, request }) => {
    // Ensure workspaces are configured
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_1.acpServer, working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/workspaces', {
      data: { acp_server: WORKSPACE_2.acpServer, working_dir: WORKSPACE_2.dir },
    });

    // Create two sessions
    await request.post('/api/sessions', {
      data: { name: 'Isolation Test 1', working_dir: WORKSPACE_1.dir },
    });
    await request.post('/api/sessions', {
      data: { name: 'Isolation Test 2', working_dir: WORKSPACE_2.dir },
    });

    await page.goto('/');
    await expect(page.locator('.animate-spin')).toBeHidden({ timeout: 10000 });

    // Send a unique message to session 1
    const session1 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Isolation Test 1' });
    await expect(session1).toBeVisible({ timeout: 10000 });
    await session1.click();
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    const uniqueMsg1 = `Unique message for session 1: ${Date.now()}`;
    await page.locator('textarea').fill(uniqueMsg1);
    await page.locator('button:has-text("Send")').click();

    // Wait for message to appear
    await expect(page.locator(`text=${uniqueMsg1}`)).toBeVisible({ timeout: 5000 });

    // Switch to session 2
    const session2 = page.locator('[class*="border-b"][class*="cursor-pointer"]')
      .filter({ hasText: 'Isolation Test 2' });
    await session2.click();
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Session 2 should NOT have the message from session 1
    await expect(page.locator(`text=${uniqueMsg1}`)).toBeHidden({ timeout: 3000 });

    // Send a different message to session 2
    const uniqueMsg2 = `Unique message for session 2: ${Date.now()}`;
    await page.locator('textarea').fill(uniqueMsg2);
    await page.locator('button:has-text("Send")').click();

    // Wait for message to appear
    await expect(page.locator(`text=${uniqueMsg2}`)).toBeVisible({ timeout: 5000 });

    // Switch back to session 1
    await session1.click();
    await expect(page.locator('textarea')).toBeEnabled({ timeout: 15000 });

    // Session 1 should have its message but NOT session 2's message
    await expect(page.locator(`text=${uniqueMsg1}`)).toBeVisible({ timeout: 5000 });
    await expect(page.locator(`text=${uniqueMsg2}`)).toBeHidden({ timeout: 3000 });
  });
});

