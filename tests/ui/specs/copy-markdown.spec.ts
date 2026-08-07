import { test, expect } from "../fixtures/test-fixtures";

/**
 * "Copy as Markdown" tests for the Mitto Web UI.
 *
 * Covers the three copy entry points added to the chat UI:
 *   1. Per-message hover button on a USER bubble (copies the raw text).
 *   2. Per-message hover button on an AGENT bubble (serializes the rendered
 *      HTML back to Markdown via htmlToMarkdown).
 *   3. The conversation header "Copy" dropdown (mitto-a6v1), with four
 *      entries: conversation name, conversation ID, full contents as
 *      Markdown (role headers + `---` separators), and last response as
 *      Markdown.
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

    // UI confirmation: the daisyUI tooltip wrapper flips its data-tip to
    // "Copied!" (and force-opens) for ~1.5s.
    const userCopyTip = bubble.locator(
      '.tooltip:has([data-testid="copy-message-markdown"])',
    );
    await expect(userCopyTip).toHaveAttribute("data-tip", "Copied!", {
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

    const agentCopyTip = bubble.locator(
      '.tooltip:has([data-testid="copy-message-markdown"])',
    );
    await expect(agentCopyTip).toHaveAttribute("data-tip", "Copied!", {
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

  // Skipped: at desktop viewport width, clicks on items inside this dropdown
  // are intercepted by the sidebar (.drawer-side z-40) because the menu
  // (w-64, right-aligned to a narrow icon trigger) visually overlaps the
  // sidebar column. Pre-existing bug (predates mitto-a6v1.3), tracked in
  // mitto-nmiv. Re-enable once that's fixed.
  test.fixme("copies the whole conversation from the header Copy dropdown", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const msg = helpers.uniqueMessage("Convo copy");
    await helpers.sendMessageAndWait(page, msg);

    // "Copy as Markdown" is now a "Copy" dropdown in the conversation toolbar
    // pill (previously a single-action button; before that, the "…"
    // conversation-actions menu). Open the trigger, then the full-contents
    // entry (mitto-a6v1).
    const copyTrigger = page.locator(selectors.headerCopyMarkdown);
    await expect(copyTrigger).toBeVisible({ timeout: timeouts.shortAction });
    await copyTrigger.click();
    const copyMenu = page.locator(selectors.headerCopyMenu);
    await expect(copyMenu).toBeVisible({ timeout: timeouts.shortAction });
    await page.locator(selectors.headerCopyConversationMd).click();

    // Success toast appears and no context menu is left open.
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

  // Skipped: see mitto-nmiv (sidebar intercepts clicks on this dropdown's
  // items at desktop viewport width). Pre-existing bug, predates mitto-a6v1.3.
  test.fixme("copies the conversation name and ID from the header Copy dropdown", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    await helpers.sendMessageAndWait(page, helpers.uniqueMessage("Name/ID copy"));

    await page.locator(selectors.headerCopyMarkdown).click();
    await expect(page.locator(selectors.headerCopyMenu)).toBeVisible({
      timeout: timeouts.shortAction,
    });
    await page.locator(selectors.headerCopyId).click();
    await expect(page.getByText("Conversation ID copied")).toBeVisible({
      timeout: timeouts.appReady,
    });
    const copiedId = await lastCopied(page);
    expect(copiedId).toBeTruthy();

    await page.locator(selectors.headerCopyMarkdown).click();
    await expect(page.locator(selectors.headerCopyMenu)).toBeVisible({
      timeout: timeouts.shortAction,
    });
    await page.locator(selectors.headerCopyName).click();
    await expect(page.getByText("Conversation name copied")).toBeVisible({
      timeout: timeouts.appReady,
    });
  });

  // Skipped: see mitto-nmiv (sidebar intercepts clicks on this dropdown's
  // items at desktop viewport width). Pre-existing bug, predates mitto-a6v1.3.
  test.fixme("copies the last response from the header Copy dropdown", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    const msg = helpers.uniqueMessage("Last response copy");
    await helpers.sendMessageAndWait(page, msg);

    await page.locator(selectors.headerCopyMarkdown).click();
    await expect(page.locator(selectors.headerCopyMenu)).toBeVisible({
      timeout: timeouts.shortAction,
    });
    await page.locator(selectors.headerCopyLastResponseMd).click();

    await expect(
      page.getByText("Last response copied as Markdown"),
    ).toBeVisible({ timeout: timeouts.appReady });

    const copied = await lastCopied(page);
    expect(copied).toBeTruthy();
    expect(copied).not.toContain("## 🧑 User");
    expect(copied).not.toContain(msg);
  });

  test("header Copy dropdown lists four entries and enables 'last response' only after an agent reply", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    // Guaranteed-empty conversation: ensureActiveSession (used by beforeEach)
    // may reuse an existing session with prior messages, which would make the
    // "before any reply" assertions below flaky.
    await helpers.createFreshSession(page);

    // Before any reply: all four entries are present in one open of the
    // dropdown, and "Copy last response as Markdown" is disabled while
    // "Copy conversation ID" (always available once a session exists) is
    // enabled — disabled entries are greyed out, not hidden.
    await page.locator(selectors.headerCopyMarkdown).click();
    await expect(page.locator(selectors.headerCopyMenu)).toBeVisible({
      timeout: timeouts.shortAction,
    });
    await expect(page.locator(selectors.headerCopyName)).toBeVisible();
    await expect(page.locator(selectors.headerCopyId)).toBeVisible();
    await expect(
      page.locator(selectors.headerCopyConversationMd),
    ).toBeVisible();
    await expect(
      page.locator(selectors.headerCopyLastResponseMd),
    ).toBeVisible();

    await expect(page.locator(selectors.headerCopyId)).toBeEnabled();
    await expect(
      page.locator(selectors.headerCopyLastResponseMd),
    ).toBeDisabled();
    // Bonus: with no conversation content yet, the full-Markdown entry is
    // disabled too.
    await expect(
      page.locator(selectors.headerCopyConversationMd),
    ).toBeDisabled();

    // Close the dropdown without clicking a disabled entry.
    await page.keyboard.press("Escape");
    await expect(page.locator(selectors.headerCopyMenu)).toBeHidden({
      timeout: timeouts.shortAction,
    });

    // After a reply: reopen and confirm "Copy last response as Markdown" is
    // now enabled.
    await helpers.sendMessageAndWait(
      page,
      helpers.uniqueMessage("Dropdown state"),
    );

    await page.locator(selectors.headerCopyMarkdown).click();
    await expect(page.locator(selectors.headerCopyMenu)).toBeVisible({
      timeout: timeouts.shortAction,
    });
    await expect(
      page.locator(selectors.headerCopyLastResponseMd),
    ).toBeEnabled();
  });

  // mitto-nmiv: reproduces the geometric clipping bug directly (ground truth
  // via document.elementFromPoint, same technique used in the bead's own
  // report and in beads.spec.ts's dialog-covers-panel regression check).
  // The dropdown's w-64 menu is right-aligned to a narrow icon trigger, so
  // its left portion extends past the overflow:hidden main-content column
  // into the sidebar's screen region and is clipped there — NOT painted
  // under the sidebar by z-index, since clipping wins regardless of z-index.
  // Expected once fixed: every entry's own center is hit-tested to the entry
  // itself. Today the leftmost entries (name/ID) are clipped away and the
  // sidebar (or whatever sits behind) is hit instead.
  test("header Copy dropdown items are not clipped by the sidebar at desktop viewport", async ({
    page,
    selectors,
    helpers,
    timeouts,
  }) => {
    await helpers.sendMessageAndWait(
      page,
      helpers.uniqueMessage("Hit-test copy dropdown"),
    );

    await page.locator(selectors.headerCopyMarkdown).click();
    const menu = page.locator(selectors.headerCopyMenu);
    await expect(menu).toBeVisible({ timeout: timeouts.shortAction });

    const itemSelectors = [
      selectors.headerCopyName,
      selectors.headerCopyId,
      selectors.headerCopyConversationMd,
      selectors.headerCopyLastResponseMd,
    ];

    for (const sel of itemSelectors) {
      const item = page.locator(sel);
      const hit = await item.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const cx = r.left + r.width / 2;
        const cy = r.top + r.height / 2;
        const top = document.elementFromPoint(cx, cy);
        return {
          insideItem: !!top && el.contains(top),
          topTestId: top ? top.getAttribute("data-testid") : null,
          topClass: top ? String(top.className) : null,
          rect: { left: r.left, top: r.top, width: r.width, height: r.height },
        };
      });
      expect(
        hit.insideItem,
        `${sel} center (${JSON.stringify(hit.rect)}) is covered by: testid=${hit.topTestId} class=${hit.topClass}`,
      ).toBe(true);
    }
  });
});
