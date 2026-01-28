// @ts-check
import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for Mitto Web UI tests.
 *
 * These tests require a running Mitto web server.
 * Start the server with: go run ./cmd/mitto web --port 8080
 *
 * Run tests with: npm run test:ui
 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.js',

  // Run tests in parallel but be conservative since we're talking to a real server
  fullyParallel: false,
  workers: 1,

  // Fail the build on CI if you accidentally left test.only in the source code
  forbidOnly: !!process.env.CI,

  // Retry on CI only
  retries: process.env.CI ? 2 : 0,

  // Reporter to use
  reporter: process.env.CI ? 'dot' : 'list',

  // Shared settings for all the projects below
  use: {
    // Base URL to use in actions like `await page.goto('/')`
    baseURL: process.env.MITTO_TEST_URL || 'http://127.0.0.1:8080',

    // Collect trace when retrying the failed test
    trace: 'on-first-retry',

    // Take screenshot on failure
    screenshot: 'only-on-failure',

    // Record video on first retry
    video: 'on-first-retry',
  },

  // Configure projects for major browsers
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Global timeout for each test
  timeout: 30000,

  // Expect timeout
  expect: {
    timeout: 5000,
  },

  // Output folder for test artifacts
  outputDir: './test-results',
});

