import { test, expect } from "../fixtures/test-fixtures";

/**
 * "Copy as Markdown" tests for the Mitto Web UI.
 *
 * Covers the three copy entry points added to the chat UI:
 *   1. Per-message hover button on a USER bubble (copies the raw text).
 *   2. Per-message hover button on an AGENT bubble (serializes the rendered
 *      HTML back to Markdown via htmlToMarkdown).
 *   3. The conversation header three-dot menu → "Copy as Markdown", which
 *      copies the whole conversation (role headers + `---` separators).
 *
 * Clipboard strategy: instead of relying on the headless browser's clipboard
 * permissions/focus (which are flaky), we override navigator.clipboard.writeText
 * via an init script that records every written value into window.__copiedText.
 * The override still calls the original so copyToClipboard() resolves and the UI
 * shows its "Copied!" confirmation, but the assertion reads the captured value
 * deterministically.
 */

// Runs in the page before any app code; captures clipboard writes.
const CLIPBOARD_CAPTURE = () => {
  (window as any).__copiedText = [];
  if (navigator.clipboard) {
    const orig = navigator.clipboard.writeText
      ? navigator.clipboard.writeText.bind(navigator.clipboard)
      : null;
    navigator.clipboard.writeText = async (t: string) => {
      (window as any).__copiedText.push(t);
      if (orig) {
        try {
          await orig(t);
        } catch (_) {
          /* ignore permission errors in headless */
        }
      }
    };
  }
};

async function lastCopied(page): Promise<string | null> {
  return await page.evaluate(() => {
    const arr = (window as any).__copiedText || [];
    return arr.length ? arr[arr.length - 1] : null;
  });
}

test.describe("Copy as Markdown", () => {
  test.beforeEach(async ({ page, helpers, context }) => {
    // Belt-and-suspenders: grant clipboard so the original writeText doesn't
    // throw (Chromium only — the test project is chromium).
    await context
      .grantPermissions(["clipboard-read", "clipboard-write"])
      .catch(() => {});
    await page.addInitScript(CLIPBOARD_CAPTURE);
    await helpers.navigateAndWait(page);
    await helpers.ensureActiveSession(page);
  });

  test("copies a user message as Markdown via the hover button", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const msg = helpers.uniqueMessage("Copy user");
    await helpers.sendMessage(page, msg);
    await helpers.waitForUserMessage(page, msg);

    const bubble = page
      .locator(selectors.userMessage)
      .filter({ hasText: msg })
      .first();
    const copyBtn = bubble.locator(selectors.copyMessageMarkdown);

    // The button is opacity-0 until its message group is hovered.
    await page.mouse.move(0, 0);
    const opacity = () =>
      copyBtn.evaluate((el) => Number(getComputedStyle(el).opacity));
    expect(await opacity()).toBeLessThan(0.5);

    await bubble.hover();
    await expect.poll(opacity, { timeout: timeouts.shortAction }).toBeGreaterThan(0.9);

    await copyBtn.click();

    // UI confirmation: the title flips to "Copied!" for ~1.5s.
    await expect(copyBtn).toHaveAttribute("title", "Copied!", {
      timeout: timeouts.shortAction,
    });

    // The user copy is the raw, unmodified text.
    await expect.poll(() => lastCopied(page)).toBe(msg);
  });

  test("copies an agent message as Markdown via the hover button", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    await helpers.sendMessageAndWait(page, "Hello");

    const bubble = page.locator(selectors.agentMessage).last();
    await expect(bubble).toBeVisible({ timeout: timeouts.agentResponse });
    const copyBtn = bubble.locator(selectors.copyMessageMarkdown);

    await bubble.hover();
    await copyBtn.click();

    await expect(copyBtn).toHaveAttribute("title", "Copied!", {
      timeout: timeouts.shortAction,
    });

    // The agent copy is serialized from rendered HTML to Markdown: it must be
    // non-empty and must not contain raw HTML tags.
    const copied = await lastCopied(page);
    expect(copied).toBeTruthy();
    expect((copied as string).length).toBeGreaterThan(0);
    expect(copied).not.toContain("<div");
    expect(copied).not.toContain("</p>");
  });

  test("copies the whole conversation from the header menu", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const msg = helpers.uniqueMessage("Convo copy");
    await helpers.sendMessageAndWait(page, msg);

    await page.locator(selectors.headerConversationMenu).click();
    const menu = page.locator(selectors.contextMenu).first();
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const copyItem = page
      .locator(`${selectors.contextMenu} button`)
      .filter({ hasText: "Copy as Markdown" });
    await expect(copyItem).toBeVisible({ timeout: timeouts.shortAction });
    await copyItem.click();

    // Success toast and the menu closes.
    await expect(page.getByText("Conversation copied as Markdown")).toBeVisible({
      timeout: timeouts.appReady,
    });
    await expect(page.locator(selectors.contextMenu)).toHaveCount(0);

    // The whole-conversation copy includes role headers, the user message, and
    // the `---` turn separator.
    const copied = await lastCopied(page);
    expect(copied).toBeTruthy();
    expect(copied).toContain("## 🧑 User");
    expect(copied).toContain(msg);
    expect(copied).toContain("## 🤖 Assistant");
    expect(copied).toContain("---");
  });
});
