import { chromium } from 'playwright';

const PORT = process.env.TEST_PORT || '49997';
const URL = `http://127.0.0.1:${PORT}`;

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

  console.log(`Navigating to ${URL}...`);
  await page.goto(URL, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  const newBtn = page.locator('[title="New Conversation"]');
  await newBtn.first().click();
  await page.waitForTimeout(500);

  // Screenshot: before filtering (all entries visible with group headers)
  await page.screenshot({ path: '/tmp/ws-clean-01-all.png' });
  console.log('Screenshot: /tmp/ws-clean-01-all.png');

  const filterInput = page.locator('input[placeholder="Filter workspaces..."]');

  // Filter to "proj" - matches 4 entries across 4 groups
  await filterInput.fill('proj');
  await page.waitForTimeout(300);
  await page.screenshot({ path: '/tmp/ws-clean-02-proj.png' });
  console.log('Screenshot: /tmp/ws-clean-02-proj.png');

  // Filter to "blog" - matches 1 entry in 1 group (no group header)
  await filterInput.fill('blog');
  await page.waitForTimeout(300);
  await page.screenshot({ path: '/tmp/ws-clean-03-single.png' });
  console.log('Screenshot: /tmp/ws-clean-03-single.png');

  // Clear filter to show all again
  await filterInput.fill('');
  await page.waitForTimeout(300);
  await page.screenshot({ path: '/tmp/ws-clean-04-restored.png' });
  console.log('Screenshot: /tmp/ws-clean-04-restored.png');

  // Verify entries have correct info
  const entries = page.locator('button.w-full.p-3');
  const count = await entries.count();
  console.log(`\nAll entries (${count}):`);
  for (let i = 0; i < count; i++) {
    const text = (await entries.nth(i).innerText()).replace(/\n/g, ' | ');
    console.log(`  ${i}: ${text}`);
  }

  await browser.close();
  console.log('\nDone');
})();
