import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * Structural regression net for the sidebar toolbar button row (New
 * Conversation / Add Folder / Filter / Density / Search / Workspaces /
 * Settings).
 *
 * The toolbar is now rendered by the portable Toolbar component
 * (web/static/components/Toolbar.js) as a segmented "pill": a single bordered,
 * rounded container (`.mitto-toolbar`) holding borderless ghost icon buttons.
 * This spec locks the invariants of that shape:
 *   1. PRESENCE: all items are present and visible (anchored on stable
 *      data-testids so selectors survive restyles).
 *   2. HEIGHT: every item shares the same height (all btn-sm) so the row reads
 *      as one continuous surface. Widths intentionally differ now — the
 *      Filter/Density dropdowns carry a caret and are wider than plain icon
 *      buttons — so width equality is NOT asserted.
 *   3. BORDERS: the single pill container carries the visible border; the
 *      individual items are borderless at rest (the global per-<button> resting
 *      border is suppressed for `.mitto-toolbar > button`). This documents the
 *      restyle and guards against a regression back to per-item boxes.
 */

const ITEM_IDS = [
  "add-folder-btn",
  "category-filter-btn",
  "density-btn",
  "search-btn",
  "workspaces-btn",
  "settings-btn",
] as const;

const toolbar = (page: Page) =>
  page.locator('[data-testid="sidebar-toolbar"]').first();

const pill = (page: Page) =>
  page.locator('[data-testid="sidebar-toolbar"] .mitto-toolbar').first();

// Collect per-item box metrics + resting border-top width/style. The mouse
// defaults to (0,0) so no item is hovered; we assert the resting appearance.
async function collectItemMetrics(page: Page) {
  const tb = toolbar(page);
  const metrics: {
    id: string;
    w: number;
    h: number;
    borderWidth: number;
    borderStyle: string;
  }[] = [];
  for (const id of ITEM_IDS) {
    const el = tb.locator(`[data-testid="${id}"]`).first();
    await expect(el).toBeVisible();
    const m = await el.evaluate((node) => {
      const r = node.getBoundingClientRect();
      const cs = getComputedStyle(node);
      return {
        w: r.width,
        h: r.height,
        borderWidth: parseFloat(cs.borderTopWidth) || 0,
        borderStyle: cs.borderTopStyle,
      };
    });
    metrics.push({ id, ...m });
  }
  return metrics;
}

test.describe("Sidebar toolbar structure (segmented pill)", () => {
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

  test("all toolbar items share the same height", async ({ page }) => {
    const metrics = await collectItemMetrics(page);
    const heights = metrics.map((m) => m.h);
    const heightSpread = Math.max(...heights) - Math.min(...heights);
    expect(
      heightSpread,
      `toolbar item heights must match (got ${JSON.stringify(
        metrics.map((m) => [m.id, Math.round(m.h)]),
      )})`,
    ).toBeLessThanOrEqual(1.5);
  });

  test("pill container is bordered and rounded", async ({ page }) => {
    const el = pill(page);
    await expect(el).toBeVisible();
    const box = await el.evaluate((node) => {
      const cs = getComputedStyle(node);
      return {
        borderWidth: parseFloat(cs.borderTopWidth) || 0,
        borderStyle: cs.borderTopStyle,
        radius: parseFloat(cs.borderTopLeftRadius) || 0,
      };
    });
    expect(box.borderStyle).not.toBe("none");
    expect(box.borderWidth).toBeGreaterThan(0);
    expect(box.radius).toBeGreaterThan(0);
  });

  test("toolbar items are borderless at rest", async ({ page }) => {
    const metrics = await collectItemMetrics(page);
    for (const m of metrics) {
      const borderless = m.borderStyle === "none" || m.borderWidth === 0;
      expect(
        borderless,
        `${m.id} must be borderless at rest (style=${m.borderStyle}, width=${m.borderWidth})`,
      ).toBe(true);
    }
  });
});
