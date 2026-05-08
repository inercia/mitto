/**
 * Mitto UI Screenshot Capture Script
 *
 * Captures screenshots of Mitto's web UI for documentation purposes.
 * Requires Mitto running at http://127.0.0.1:8099 with a fresh config.
 *
 * Usage:
 *   node docs/config/screenshots/capture-screenshots.mjs
 *
 * Screenshots are saved to docs/config/screenshots/
 */

import { chromium } from 'playwright';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCREENSHOT_DIR = __dirname;
const BASE_URL = 'http://127.0.0.1:8099/mitto';

async function screenshot(page, filename) {
  const path = join(SCREENSHOT_DIR, filename);
  await page.screenshot({ path, fullPage: false });
  console.log(`  ✓ ${filename}`);
  return path;
}

async function waitForSettle(page, ms = 600) {
  await page.waitForTimeout(ms);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });

  try {
    // ─── Phase 1: Agent Discovery ───────────────────────────────────────────
    console.log('\n📸 Phase 1: Agent Discovery Flow');

    await page.goto(BASE_URL, { waitUntil: 'networkidle' });
    await waitForSettle(page, 1500);

    // Wait for agent discovery dialog to appear
    const discoveryDialog = page.locator('[data-testid="agent-discovery-dialog"]');
    try {
      await discoveryDialog.waitFor({ state: 'visible', timeout: 8000 });
    } catch {
      console.log('  ℹ Agent discovery dialog not visible, checking if already configured...');
    }
    await waitForSettle(page);
    await screenshot(page, '01-agent-discovery-initial.png');

    // Click "Scan for Agents"
    const scanBtn = page.locator('[data-testid="agent-discovery-scan"]');
    if (await scanBtn.isVisible()) {
      await scanBtn.click();
      console.log('  → Scanning for agents...');
      // Wait for scanning to finish (spinner disappears, results appear)
      await page.waitForFunction(() => {
        const spinner = document.querySelector('.animate-spin');
        const buttons = [...document.querySelectorAll('button')];
        const confirmBtn = buttons.find(b => b.dataset.testid === 'agent-discovery-confirm');
        const skipBtn = buttons.find(b => b.innerText.includes('Skip') || b.innerText.includes('Configure Manually'));
        return (!spinner || !spinner.closest('[data-testid="agent-discovery-dialog"]')) && (confirmBtn || skipBtn);
      }, { timeout: 30000 }).catch(() => {});
      await waitForSettle(page, 1000);
      await screenshot(page, '01-agent-discovery-results.png');
    } else {
      console.log('  ⚠ Scan button not found, skipping scan screenshot');
    }

    // Close agent discovery (click skip/configure manually)
    const skipBtn = page.locator('[data-testid="agent-discovery-skip"]');
    if (await skipBtn.isVisible()) {
      await skipBtn.click();
      await waitForSettle(page);
    }

    // ─── Setup: Save settings via API + write workspaces file ───────────────
    console.log('\n⚙  Setting up sample data via API...');

    const configData = {
      acp_servers: [
        { name: 'Auggie', command: 'auggie --acp --allow-indexing', type: 'auggie', tags: ['coding', 'ai-assistant'] },
        { name: 'Claude Code', command: 'npx -y @agentclientprotocol/claude-agent-acp@latest', type: 'claude-code' },
        { name: 'Gemini', command: 'npx -y @google/gemini-cli@latest -- --acp', type: 'gemini' },
      ],
      workspaces: [
        { uuid: '11111111-1111-1111-1111-111111111111', working_dir: '/Users/alvaro/Development/inercia/mitto', acp_server: 'Auggie', name: 'Mitto' },
        { uuid: '22222222-2222-2222-2222-222222222222', working_dir: '/Users/alvaro/Development/other-project', acp_server: 'Claude Code', name: 'Other Project' },
      ],
      web: { theme: 'v2' },
    };

    const saveResult = await page.evaluate(async (data) => {
      // Get CSRF token first
      const csrfResp = await fetch('/mitto/api/csrf-token');
      const csrfData = await csrfResp.json();
      const csrfToken = csrfData.token;
      const r = await fetch('/mitto/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
        body: JSON.stringify(data),
      });
      return { ok: r.ok, status: r.status, body: await r.text() };
    }, configData);

    if (!saveResult.ok) {
      console.log(`  ⚠ Settings save returned ${saveResult.status}: ${saveResult.body}`);
    } else {
      console.log('  ✓ Settings saved via API');
    }

    // Reload page to apply settings
    await page.reload({ waitUntil: 'networkidle' });
    await waitForSettle(page, 2000);

    // If agent discovery appears again (save didn't work), dismiss it
    const discoveryBackdrop2 = page.locator('[data-testid="agent-discovery-backdrop"]');
    if (await discoveryBackdrop2.isVisible({ timeout: 2000 }).catch(() => false)) {
      console.log('  ℹ Dismissing post-reload agent discovery dialog...');
      const skipBtn2 = page.locator('[data-testid="agent-discovery-skip"]');
      if (await skipBtn2.isVisible()) await skipBtn2.click();
      await waitForSettle(page);
    }

    // ─── Phase 2: Settings Dialog ────────────────────────────────────────────
    console.log('\n📸 Phase 2: Settings Dialog');

    // Open settings: first check if settings dialog is already open (force-open mode)
    const settingsDialogVisible = await page.locator('[role="dialog"]:has-text("Settings")').isVisible({ timeout: 2000 }).catch(() => false);
    if (!settingsDialogVisible) {
      // Open settings via gear icon (title="Settings")
      const settingsBtn = page.locator('button[title="Settings"]');
      await settingsBtn.waitFor({ state: 'visible', timeout: 5000 });
      await settingsBtn.click();
      await waitForSettle(page, 1000);
    } else {
      console.log('  ℹ Settings dialog already open (force-open mode)');
      await waitForSettle(page, 500);
    }

    // Helper to click a nav tab and screenshot
    const captureTab = async (label, filename) => {
      const navBtn = page.locator(`nav button:has-text("${label}")`);
      if (await navBtn.isVisible()) {
        await navBtn.click();
        await waitForSettle(page, 700);
      }
      await screenshot(page, filename);
    };

    await captureTab('ACP Servers', '02-settings-servers.png');
    await captureTab('Runners', '02-settings-runners.png');
    await captureTab('Conversations', '02-settings-conversations.png');
    await captureTab('Web', '02-settings-web.png');
    await captureTab('UI', '02-settings-ui.png');

    // Close settings dialog: try close button, then Escape
    const closeBtn = page.locator('[role="dialog"]:has-text("Settings") button:has([class*="close"]), [role="dialog"]:has-text("Settings") button:has(svg)').last();
    const dialogCloseBtn = page.locator('[role="dialog"]:has-text("Settings") .p-1\\.5 button, [role="dialog"] button.p-1\\.5').first();
    try {
      // Try clicking outside the dialog to close it (only works if canClose=true)
      await page.mouse.click(50, 50);
      await waitForSettle(page, 500);
    } catch {}
    // Press Escape as fallback
    await page.keyboard.press('Escape');
    await waitForSettle(page);

    // ─── Phase 3: Workspaces Dialog ──────────────────────────────────────────
    console.log('\n📸 Phase 3: Workspaces Dialog');

    // Settings dialog may still be open if force-open; navigate to workspaces via button
    const settingsStillOpen = await page.locator('[role="dialog"]:has-text("Settings")').isVisible({ timeout: 1000 }).catch(() => false);
    if (settingsStillOpen) {
      console.log('  ℹ Settings dialog still open, reloading to access workspaces button...');
      await page.reload({ waitUntil: 'networkidle' });
      await waitForSettle(page, 2000);
    }

    const workspacesBtn = page.locator('button[title="Workspaces"]');
    await workspacesBtn.waitFor({ state: 'visible', timeout: 5000 });
    await workspacesBtn.click();
    await waitForSettle(page, 1000);
    await screenshot(page, '03-workspaces-overview.png');

    // ─── Phase 4: Workspace Folder Tabs ──────────────────────────────────────
    console.log('\n📸 Phase 4: Workspace Folder Tabs');

    // Click on the "Mitto" folder header to select it (shows folder-level tabs)
    // The folder header is a div.cursor-pointer with the folder name inside the .fixed dialog
    const folderHeader = page.locator('.workspaces-dialog div.cursor-pointer:has-text("Mitto")').first();
    if (await folderHeader.isVisible({ timeout: 3000 }).catch(() => false)) {
      await folderHeader.click();
      await waitForSettle(page, 1000);

      // Capture General tab (default)
      await screenshot(page, '04-workspace-general.png');

      // Helper to click folder-level tabs
      const captureFolderTab = async (label, filename) => {
        const tab = page.locator('.workspaces-dialog button:has-text("' + label + '")');
        if (await tab.isVisible({ timeout: 2000 }).catch(() => false)) {
          await tab.click();
          await waitForSettle(page, 1000);
          await screenshot(page, filename);
        } else {
          console.log('  ⚠ Tab "' + label + '" not found');
        }
      };

      await captureFolderTab('Metadata', '04-workspace-metadata.png');
      await captureFolderTab('Prompts', '04-workspace-prompts.png');
      await captureFolderTab('Processors', '04-workspace-processors.png');
      await captureFolderTab('Children', '04-workspace-children.png');
    } else {
      console.log('  ⚠ No workspace folder found to expand');
    }

    // ─── Phase 5: Close workspaces dialog ──────────────────────────────────
    console.log('\n📸 Phase 5: Closing dialogs');

    // Close workspaces dialog by clicking outside or pressing Escape
    await page.mouse.click(10, 10);
    await waitForSettle(page, 500);
    // If still visible, try Escape
    const stillOpen = await page.locator('.workspaces-dialog').isVisible({ timeout: 500 }).catch(() => false);
    if (stillOpen) {
      await page.keyboard.press('Escape');
      await waitForSettle(page, 500);
    }

  } catch (err) {
    console.error('\n❌ Error:', err.message);
    await screenshot(page, 'error-state.png');
    process.exitCode = 1;
  } finally {
    await browser.close();
    console.log('\nDone. Screenshots saved to:', SCREENSHOT_DIR);
  }
})();
