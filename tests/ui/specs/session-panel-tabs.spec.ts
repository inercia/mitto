import { test, expect } from "../fixtures/test-fixtures";
import type { Page, Locator } from "@playwright/test";

/**
 * Regression guard for the Conversation properties panel tab glitch.
 *
 * Root cause (fixed): the three tab <label>s in SessionPanel combined daisyUI's
 * `.tab` (from `tabs-lift`) AND `.tooltip` on the SAME element. Both utilities
 * render into the element's ::before/::after pseudo-elements, so they collided.
 * When a tab's (visually hidden) radio gained focus after selection,
 * `.tooltip:has(:focus-visible)` made the tooltip ::before visible; merged with
 * `tabs-lift`'s active-tab corner pseudo-element (and shifted left by the
 * tooltip's own translateX(-50%)), it painted a stray grey rounded bar under the
 * tabs. The fix drops the daisyUI `tooltip`/`data-tip` in favour of a native
 * `title` attribute (matching WorkspacesDialog's tabs).
 *
 * This spec locks that in: with the panel open, no tab may carry the tooltip
 * utility, and neither the ::before nor the ::after of any tab may render as a
 * visible filled bar — checked in the exact condition that triggered the bug
 * (the tab's radio focused/active).
 */

const openSessionPanel = async (page: Page): Promise<Locator> => {
  await page.locator('[data-testid="header-session-details"]').click();
  const panel = page.locator('[data-testid="session-panel"]');
  await expect(panel).toBeVisible({ timeout: 5000 });
  return panel;
};

// Read the computed ::before / ::after of a tab label, plus the structural
// markers that identify the (removed) daisyUI tooltip. Runs while the tab's
// hidden radio is focused, reproducing the state that surfaced the glitch.
async function inspectTab(tab: Locator) {
  return tab.evaluate((el) => {
    const radio = el.querySelector("input");
    if (radio) (radio as HTMLElement).focus();
    const before = getComputedStyle(el, "::before");
    const after = getComputedStyle(el, "::after");
    return {
      hasTooltipClass: el.classList.contains("tooltip"),
      hasDataTip: el.hasAttribute("data-tip"),
      beforeBg: before.backgroundColor,
      beforeContent: before.content,
      afterBg: after.backgroundColor,
      afterContent: after.content,
    };
  });
}

const TRANSPARENT = "rgba(0, 0, 0, 0)";

test.describe("SessionPanel tabs (no stray pseudo-element on focus)", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("renders exactly three tabs without the daisyUI tooltip utility", async ({
    page,
  }) => {
    const panel = await openSessionPanel(page);
    const tabs = panel.locator('[role="tablist"] label.tab');
    await expect(tabs).toHaveCount(3);

    // Each tab uses a native `title` (accessible hover hint) and NOT the
    // daisyUI `tooltip` class — the class was the root cause of the collision.
    for (let i = 0; i < 3; i++) {
      const tab = tabs.nth(i);
      await expect(tab).not.toHaveClass(/\btooltip\b/);
      await expect(tab).toHaveAttribute("title", /.+/);
      await expect(tab).not.toHaveAttribute("data-tip", /.+/);
    }
  });

  test("no tab paints a stray filled pseudo-element bar when focused", async ({
    page,
  }) => {
    const panel = await openSessionPanel(page);
    const tabs = panel.locator('[role="tablist"] label.tab');
    await expect(tabs).toHaveCount(3);

    for (let i = 0; i < 3; i++) {
      const tab = tabs.nth(i);
      // Activate the tab (this also focuses its hidden radio — the exact
      // condition under which the old tooltip ::before became visible).
      await tab.click();
      const res = await inspectTab(tab);

      // Structural: the tooltip utility must be gone (it owned ::before/::after).
      expect(res.hasTooltipClass, `tab ${i} must not have .tooltip`).toBe(false);
      expect(res.hasDataTip, `tab ${i} must not have data-tip`).toBe(false);

      // The daisyUI tooltip arrow lived on ::after with content:"" (never
      // 'none'). After the fix `tabs-lift` uses no ::after, so it must not exist.
      expect(res.afterContent, `tab ${i} ::after must not render`).toBe("none");
      expect(res.afterBg, `tab ${i} ::after must be transparent`).toBe(
        TRANSPARENT,
      );

      // The stray bar was the tooltip bubble body: a solid neutral-coloured,
      // rounded ::before. `tabs-lift`'s legit active-tab corner uses only a
      // background-IMAGE (transparent background-color), so a non-transparent
      // ::before background here means the bubble regressed.
      expect(res.beforeBg, `tab ${i} ::before must not be a filled bar`).toBe(
        TRANSPARENT,
      );
    }
  });
});
