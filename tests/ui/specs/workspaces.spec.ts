import { test, testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Multi-workspace session tests for Mitto Web UI.
 *
 * These tests verify that sessions can be created and used with different workspaces,
 * each with their own working directory and ACP server configuration.
 */

// Get the project root for test workspace paths
const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const WORKSPACE_BETA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-beta",
);

// Timeout for agent responses (agents can be slow)
const AGENT_RESPONSE_TIMEOUT = 60000;

test.describe("Workspace API", () => {
  test("should list workspaces via API", async ({ request, apiUrl }) => {
    const response = await request.get(apiUrl("/api/workspaces"));
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty("workspaces");
    expect(Array.isArray(data.workspaces)).toBeTruthy();
  });

  test("should add workspace via API", async ({ request, apiUrl }) => {
    const response = await request.post(apiUrl("/api/workspaces"), {
      data: {
        acp_server: "mock-acp",
        working_dir: WORKSPACE_BETA,
      },
    });
    // Accept both 201 (created) and 409 (already exists)
    expect([201, 409]).toContain(response.status());
  });

  test("should list available ACP servers", async ({ request, apiUrl }) => {
    const response = await request.get(apiUrl("/api/workspaces"));
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty("acp_servers");
    expect(Array.isArray(data.acp_servers)).toBeTruthy();
  });
});

testWithCleanup.describe("Multi-Workspace Sessions", () => {
  testWithCleanup.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  testWithCleanup("should create session with workspace", async ({ request, apiUrl, cleanupSessions }) => {
    // Cleanup is triggered by including cleanupSessions in params
    void cleanupSessions; // Mark as used

    // Ensure workspace is configured
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: "mock-acp", working_dir: WORKSPACE_ALPHA },
    });

    // Create a session for the workspace
    const response = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Workspace Test ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(response.ok()).toBeTruthy();

    const session = await response.json();
    expect(session.session_id).toBeTruthy();
    expect(session.working_dir).toBe(WORKSPACE_ALPHA);
  });

  testWithCleanup("should show session in sidebar after creation", async ({
    page,
    request,
    selectors,
    timeouts,
    apiUrl,
    cleanupSessions,
  }) => {
    // Cleanup is triggered by including cleanupSessions in params
    void cleanupSessions; // Mark as used
    const sessionName = `UI Workspace Test ${Date.now()}`;

    // Create session via API
    await request.post(apiUrl("/api/sessions"), {
      data: {
        name: sessionName,
        working_dir: WORKSPACE_ALPHA,
      },
    });

    // Reload page to see the session
    await page.reload();
    await expect(page.locator(selectors.loadingSpinner)).toBeHidden({
      timeout: timeouts.appReady,
    });

    // Session should appear in sidebar
    const sessionItem = page.locator(selectors.sessionsList).filter({
      hasText: sessionName,
    });
    await expect(sessionItem).toBeVisible({ timeout: timeouts.shortAction });
  });
});

testWithCleanup.describe("Workspace Session Isolation", () => {
  testWithCleanup("should maintain separate sessions per workspace", async ({
    request,
    apiUrl,
    cleanupSessions,
  }) => {
    // Cleanup is triggered by including cleanupSessions in params
    void cleanupSessions; // Mark as used

    // Create sessions for different workspaces
    const session1 = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Isolation Test Alpha ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });

    const session2 = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Isolation Test Beta ${Date.now()}`,
        working_dir: WORKSPACE_BETA,
      },
    });

    expect(session1.ok()).toBeTruthy();
    expect(session2.ok()).toBeTruthy();

    const data1 = await session1.json();
    const data2 = await session2.json();

    // Sessions should have different IDs
    expect(data1.session_id).not.toBe(data2.session_id);

    // Sessions should have different working directories
    expect(data1.working_dir).toBe(WORKSPACE_ALPHA);
    expect(data2.working_dir).toBe(WORKSPACE_BETA);
  });

  testWithCleanup("should verify session working directory via API", async ({
    request,
    apiUrl,
    cleanupSessions,
  }) => {
    // Cleanup is triggered by including cleanupSessions in params
    void cleanupSessions; // Mark as used
    // Create a session
    const createResponse = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `API Verify ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResponse.ok()).toBeTruthy();

    const session = await createResponse.json();

    // Fetch session details
    const detailsResponse = await request.get(
      apiUrl(`/api/sessions/${session.session_id}`),
    );
    expect(detailsResponse.ok()).toBeTruthy();

    const details = await detailsResponse.json();
    expect(details.working_dir).toBe(WORKSPACE_ALPHA);
  });
});
