import { test as base, expect } from "@playwright/test";
import { selectors, timeouts, apiUrl } from "../utils/selectors";
import * as helpers from "../utils/helpers";

/**
 * Extended test fixtures for Mitto UI tests.
 * Provides common setup and utilities for all tests.
 */

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
};

/**
 * Extended test with Mitto-specific fixtures
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
});

export { expect };
