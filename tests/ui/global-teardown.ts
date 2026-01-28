import { FullConfig } from '@playwright/test';
import fs from 'fs/promises';

/**
 * Global teardown for Playwright tests.
 * Runs once after all tests complete.
 */
async function globalTeardown(config: FullConfig): Promise<void> {
  console.log('🧹 Running global teardown...');

  const testDir = process.env.MITTO_DIR || '/tmp/mitto-test';

  // Clean up test directory (optional - keep for debugging)
  if (process.env.CI) {
    try {
      await fs.rm(testDir, { recursive: true, force: true });
      console.log(`✅ Cleaned up test directory: ${testDir}`);
    } catch (error) {
      console.warn(`⚠️ Could not clean up test directory: ${error}`);
    }
  } else {
    console.log(`ℹ️ Keeping test directory for debugging: ${testDir}`);
  }
}

export default globalTeardown;

