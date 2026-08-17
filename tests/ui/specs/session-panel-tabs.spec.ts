import { test, expect } from "../fixtures/test-fixtures";
import type { Page, Locator } from "@playwright/test";
import { apiUrl } from "../utils/selectors";

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
      expect(res.hasTooltipClass, `tab ${i} must not have .tooltip`).toBe(
        false,
      );
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

  test("adds the Loop tab without exposing callback state as a loop trigger", async ({
    page,
    request,
    timeouts,
  }) => {
    const sessionId = await page.evaluate(() =>
      localStorage.getItem("mitto_last_session_id"),
    );
    expect(sessionId).toBeTruthy();

    const loopUrl = apiUrl(`/api/sessions/${sessionId}/loop`);
    const configure = await request.put(loopUrl, {
      data: {
        prompt: "Session panel coverage",
        frequency: { value: 1, unit: "hours" },
        delay_seconds: 30,
        enabled: true,
        max_iterations: 3,
        triggers: ["schedule", "onCompletion"],
      },
    });
    expect(configure.ok()).toBeTruthy();

    const bar = page.locator('[data-testid="loop-control-bar"]');
    await expect(bar).toBeVisible({ timeout: timeouts.appReady });
    await bar.locator('[data-testid="loop-open-settings"]').click();

    const panel = page.locator('[data-testid="session-panel"]');
    const tabs = panel.locator('[role="tablist"] label.tab');
    await expect(tabs).toHaveCount(4);
    await expect(panel.locator('label[aria-label="Loop"] input')).toBeChecked();

    for (const trigger of ["schedule", "onCompletion", "onTasks", "onChild"]) {
      await expect(
        panel.locator(`[data-testid="loop-settings-${trigger}"]`),
      ).toBeVisible();
    }

    const callback = panel.locator('[data-testid="callback-trigger-section"]');
    await expect(callback).toBeVisible();
    expect(
      await panel
        .locator(
          '[data-testid="loop-settings-onChild"], [data-testid="callback-trigger-section"]',
        )
        .evaluateAll((nodes) =>
          nodes.map((node) => node.getAttribute("data-testid")),
        ),
    ).toEqual(["loop-settings-onChild", "callback-trigger-section"]);

    let savedPayload: Record<string, unknown> | null = null;
    await page.route(`**${loopUrl}`, async (route) => {
      if (route.request().method() === "PUT") {
        savedPayload = route.request().postDataJSON();
      }
      await route.continue();
    });

    await panel
      .locator('[data-testid="loop-settings-trigger-onChild"]')
      .check();
    await panel.getByRole("checkbox", { name: "Any child loop stops" }).check();
    await panel.locator('[data-testid="loop-save-button"]').click();
    await expect.poll(() => savedPayload).not.toBeNull();
    expect(savedPayload?.triggers).toEqual([
      "schedule",
      "onCompletion",
      "onChild",
    ]);
    expect(savedPayload?.child_events).toEqual([
      "anyEndResponse",
      "anyDeleted",
      "anyLoopStopped",
    ]);
    expect(savedPayload).not.toHaveProperty("callback");
    expect(savedPayload).not.toHaveProperty("callback_url");

    await panel.locator('label[aria-label="Advanced"]').click();
    await expect(callback).toHaveCount(0);

    await panel.locator('label[aria-label="Loop"]').click();
    const detach = await request.delete(loopUrl);
    expect(detach.ok()).toBeTruthy();
    await expect(panel.locator('label[aria-label="Loop"]')).toHaveCount(0);
    await expect(
      panel.locator('label[aria-label="Properties"] input'),
    ).toBeChecked();
  });
});
