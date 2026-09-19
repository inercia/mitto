import { defineConfig, devices } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// mitto-sus.2: the WebKit leg of the Chromium-vs-WebKit-vs-WKWebView A/B
// responsiveness profile. Added to `projects` ONLY when explicitly requested
// via PERF_BROWSER=webkit (see Makefile's bench-ui-webkit /
// bench-ui-webkit-baseline targets), so the default `projects` list — and
// therefore `make test-ui` / `make bench-ui` — is byte-identical to before.
// Chromium-only metrics (collectPaintLayoutStats via CDP, DOMStats's
// usedJSHeapBytes) return null on this leg by design; see
// docs/devel/ui-responsiveness-benchmarks.md.
const projects = [
  {
    name: "chromium",
    use: { ...devices["Desktop Chrome"] },
  },
];
if (process.env.PERF_BROWSER === "webkit") {
  projects.push({
    name: "webkit",
    use: { ...devices["Desktop Safari"] },
  });
}

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

  // Configure projects for browsers. See the `projects` const above (only
  // chromium by default; webkit is added when PERF_BROWSER=webkit).
  projects,

  // Global timeout for each test
  timeout: 30000,

  // Expect timeout
  expect: {
    timeout: 5000,
  },

  // Output folder for test artifacts
  outputDir: "./test-results",

  // Web server configuration - starts mitto automatically for tests
  // Note: The webServer starts BEFORE globalSetup runs, so we use a helper script
  // to create the settings file before starting mitto.
  // Set MITTO_EXTERNAL_SERVER=1 to disable auto-start (e.g. when testing against Docker).
  webServer: process.env.MITTO_EXTERNAL_SERVER ? undefined : {
    command:
      'bash -c "cd ../.. && make build build-mock-acp && ./tests/ui/start-test-server.sh"',
    port: 8089,
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
    env: {
      MITTO_TEST_MODE: "1",
      MITTO_DIR: process.env.MITTO_DIR || "/tmp/mitto-test",
    },
  },
});
