import { defineConfig, devices } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Playwright configuration for Mitto Web UI tests.
 *
 * These tests use a mock ACP server for deterministic, repeatable testing.
 * The test server is started automatically via webServer configuration.
 */
export default defineConfig({
  testDir: "./specs",
  testMatch: "**/*.spec.ts",

  // Run tests serially (ACP state is shared per session)
  fullyParallel: false,
  workers: 1,

  // CI configuration
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",

  // Global setup/teardown
  globalSetup: path.resolve(__dirname, "./global-setup.ts"),
  globalTeardown: path.resolve(__dirname, "./global-teardown.ts"),

  use: {
    // Base URL for the test server
    baseURL: process.env.MITTO_TEST_URL || "http://127.0.0.1:8089",

    // Collect trace when retrying the failed test
    trace: "on-first-retry",

    // Take screenshot on failure
    screenshot: "only-on-failure",

    // Record video on first retry
    video: "on-first-retry",

    // Default timeout for actions
    actionTimeout: 10000,
  },

  // Configure projects for browsers
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
    // Uncomment to test on more browsers
    // {
    //   name: 'firefox',
    //   use: { ...devices['Desktop Firefox'] },
    // },
    // {
    //   name: 'webkit',
    //   use: { ...devices['Desktop Safari'] },
    // },
  ],

  // Global timeout for each test
  timeout: 30000,

  // Expect timeout
  expect: {
    timeout: 5000,
  },

  // Output folder for test artifacts
  outputDir: "./test-results",

  // Web server configuration - starts mitto automatically for tests
  webServer: {
    command:
      'bash -c "cd ../.. && make build build-mock-acp && ./mitto web --port 8089 --dir mock-acp:tests/fixtures/workspaces/project-alpha"',
    port: 8089,
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
    env: {
      MITTO_TEST_MODE: "1",
      MITTO_DIR: process.env.MITTO_DIR || "/tmp/mitto-test",
    },
  },
});
