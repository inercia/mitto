import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * Structural regression net for the sidebar toolbar button row (the join group
 * holding New Conversation / Workspaces / Filter / Density / Search / Settings).
 *
 * Locks down two bugs that were fixed separately:
 *   1. SIZING: the Filter/Density triggers are <summary> elements inside a
 *      <details>, and a <details> silently ignores flex-grow when flex-basis is
 *      0 (the `flex-1` utility). That left those two items at content width
 *      (~44px) while the plain <button> items grew to ~70px. The fix switches
 *      all children to `flex-auto` (flex-basis auto) so every item gets an
 *      equal share. This spec asserts all items share the same width/height.
 *   2. BORDER: the global per-<button> border rule never reached the <summary>
 *      triggers, leaving Filter/Density borderless. A scoped styles-v2.css rule
 *      mirrors the border onto the toolbar's summaries. This spec asserts all
 *      items share the same border-top color.
 *
 * Selectors are anchored on stable data-testids so they survive restyles.
 */

const ITEM_IDS = [
  "new-conversation-btn",
  "workspaces-btn",
  "category-filter-btn",
  "density-btn",
  "search-btn",
  "settings-btn",
] as const;

const toolbar = (page: Page) =>
  page.locator('[data-testid="sidebar-toolbar"]').first();

// Collect per-item box metrics + resting border-top color. The mouse defaults
// to (0,0) so no item is hovered; ghost buttons only show their border at rest
// via the app's global/scoped border rules, which is exactly what we assert.
async function collectItemMetrics(page: Page) {
  const tb = toolbar(page);
  const metrics: { id: string; w: number; h: number; border: string }[] = [];
  for (const id of ITEM_IDS) {
    const el = tb.locator(`[data-testid="${id}"]`).first();
    await expect(el).toBeVisible();
    const m = await el.evaluate((node) => {
      const r = node.getBoundingClientRect();
      const cs = getComputedStyle(node);
      return { w: r.width, h: r.height, border: cs.borderTopColor };
    });
    metrics.push({ id, ...m });
  }
  return metrics;
}

test.describe("Sidebar toolbar structure (sizing + border regression)", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    await expect(toolbar(page)).toBeVisible({ timeout: 5000 });
  });

  test("all toolbar items are present and visible", async ({ page }) => {
    const tb = toolbar(page);
    for (const id of ITEM_IDS) {
      await expect(tb.locator(`[data-testid="${id}"]`).first()).toBeVisible();
    }
  });

  test("all toolbar items share identical width and height", async ({
    page,
  }) => {
    const metrics = await collectItemMetrics(page);

    const widths = metrics.map((m) => m.w);
    const heights = metrics.map((m) => m.h);

    // Sub-pixel flex rounding can differ by a fraction of a pixel between
    // items; allow a 1.5px spread but nothing close to the old 70 vs 44 gap.
    const widthSpread = Math.max(...widths) - Math.min(...widths);
    const heightSpread = Math.max(...heights) - Math.min(...heights);

    expect(
      widthSpread,
      `toolbar item widths must match (got ${JSON.stringify(
        metrics.map((m) => [m.id, Math.round(m.w)]),
      )})`,
    ).toBeLessThanOrEqual(1.5);

    expect(
      heightSpread,
      `toolbar item heights must match (got ${JSON.stringify(
        metrics.map((m) => [m.id, Math.round(m.h)]),
      )})`,
    ).toBeLessThanOrEqual(1.5);
  });

  test("all toolbar items share the same border-top color", async ({
    page,
  }) => {
    const metrics = await collectItemMetrics(page);
    const colors = new Set(metrics.map((m) => m.border));
    expect(
      colors.size,
      `toolbar item borders must match (got ${JSON.stringify(
        metrics.map((m) => [m.id, m.border]),
      )})`,
    ).toBe(1);
  });
});
