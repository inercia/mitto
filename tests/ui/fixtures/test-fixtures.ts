import { test as base, expect } from "@playwright/test";
import { selectors, timeouts, apiUrl } from "../utils/selectors";
import * as helpers from "../utils/helpers";

/**
 * Extended test fixtures for Mitto UI tests.
 * Provides common setup and utilities for all tests.
 *
 * Test Isolation Strategy:
 * - Each test gets a fresh localStorage state
 * - Session cleanup happens automatically via testWithCleanup
 * - Use createFreshSession() for complete session isolation
 */

// Maximum sessions to keep during cleanup (leaves room for new sessions)
// Lower threshold to ensure tests have a clean environment
const MAX_SESSIONS_THRESHOLD = 10;

// Define custom fixtures
type MittoFixtures = {
  /** Helper functions for common operations */
  helpers: typeof helpers;
  /** Centralized selectors */
  selectors: typeof selectors;
  /** Common timeouts */
  timeouts: typeof timeouts;
  /** API URL builder with prefix */
  apiUrl: typeof apiUrl;
  /** Cleanup sessions before test to avoid hitting limits */
  cleanupSessions: () => Promise<number>;
  /** Session ID for the current test (set after createFreshSession) */
  testSessionId: string | null;
};

/**
 * Extended test with Mitto-specific fixtures.
 * NOTE: localStorage cleanup is now handled in individual tests that need it
 * via clearLocalStorage() helper, to avoid interfering with tests that
 * rely on navigateAndWait() for proper setup.
 */
export const test = base.extend<MittoFixtures>({
  helpers: async ({}, use) => {
    await use(helpers);
  },
  selectors: async ({}, use) => {
    await use(selectors);
  },
  timeouts: async ({}, use) => {
    await use(timeouts);
  },
  apiUrl: async ({}, use) => {
    await use(apiUrl);
  },
  cleanupSessions: async ({ request }, use) => {
    // Provide a cleanup function that tests can call
    const cleanup = async () => {
      return await helpers.cleanupSessionsToLimit(
        request,
        MAX_SESSIONS_THRESHOLD,
      );
    };
    await use(cleanup);
  },
  testSessionId: async ({}, use) => {
    // Placeholder - set by individual tests using createFreshSession
    await use(null);
  },
});

/**
 * Test with automatic session cleanup before each test.
 * Use this for tests that create many sessions.
 * NOTE: Does NOT navigate or clear localStorage automatically to avoid
 * interfering with tests that have their own setup.
 */
export const testWithCleanup = base.extend<MittoFixtures>({
  helpers: async ({}, use) => {
    await use(helpers);
  },
  selectors: async ({}, use) => {
    await use(selectors);
  },
  timeouts: async ({}, use) => {
    await use(timeouts);
  },
  apiUrl: async ({}, use) => {
    await use(apiUrl);
  },
  cleanupSessions: async ({ request }, use) => {
    // Cleanup BEFORE the test runs
    const deletedCount = await helpers.cleanupSessionsToLimit(
      request,
      MAX_SESSIONS_THRESHOLD,
    );
    if (deletedCount > 0) {
      console.log(`[Cleanup] Deleted ${deletedCount} old sessions before test`);
    }

    // Provide the cleanup function for manual use during test
    const cleanup = async () => {
      return await helpers.cleanupSessionsToLimit(
        request,
        MAX_SESSIONS_THRESHOLD,
      );
    };
    await use(cleanup);
  },
  testSessionId: async ({}, use) => {
    await use(null);
  },
});

export { expect };
