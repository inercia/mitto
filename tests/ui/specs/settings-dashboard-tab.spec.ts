import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * Settings › Dashboard tab (mitto-w6b).
 *
 * Covers the acceptance criteria for the Phase 2 UI on top of the Phase 1
 * persistence layer:
 *   1. The Dashboard nav item opens a panel that lists every chart from the
 *      `DASHBOARD_CHARTS` registry with a checkbox row.
 *   2. Unchecking a chart persists into localStorage under the
 *      `mitto-dashboard-hidden-charts` key (the deterministic anchor that
 *      `storage.js` writes before firing the debounced PUT).
 *   3. Attempting to uncheck the last visible chart is refused: the guard
 *      message renders (data-testid="dashboard-hiding-all-error"), the
 *      checkbox snaps back to checked, and localStorage is not mutated to
 *      an all-hidden list.
 *   4. A cross-tab `mitto-dashboard-hidden-charts-changed` CustomEvent fired
 *      by another actor updates the checkbox state without a reload.
 *
 * Selectors are anchored on data-testids set on the checkboxes
 * (`dashboard-chart-toggle-<id>`) and on the alert
 * (`dashboard-hiding-all-error`).
 */

const DASHBOARD_KEY = "mitto-dashboard-hidden-charts";
const CHART_IDS = [
  "tokens",
  "tool_calls",
  "prompts_vs_turns",
  "beads_activity",
  "beads_cycle_time",
] as const;

const dialog = (page: Page) => page.locator('[data-testid="settings-dialog"]');
const chart = (page: Page, id: string) =>
  page.locator(`[data-testid="dashboard-chart-toggle-${id}"]`);
const guard = (page: Page) =>
  page.locator('[data-testid="dashboard-hiding-all-error"]');

async function openDashboardTab(page: Page) {
  await page.locator('button[data-testid="settings-btn"]').first().click();
  await expect(dialog(page)).toBeVisible({ timeout: 5000 });
  await page.locator('[data-testid="settings-nav-dashboard"]').click();
  await expect(chart(page, CHART_IDS[0])).toBeVisible({ timeout: 5000 });
}

async function readHiddenCharts(page: Page): Promise<string[]> {
  const raw = await page.evaluate(
    (k) => localStorage.getItem(k),
    DASHBOARD_KEY,
  );
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

test.describe("Settings › Dashboard tab (mitto-w6b)", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
    // Start every test from a clean chart-visibility state so ordering does
    // not leak between cases.
    await page.evaluate((k) => localStorage.removeItem(k), DASHBOARD_KEY);
  });

  test("renders every registered chart with a visible checkbox", async ({
    page,
  }) => {
    await openDashboardTab(page);
    for (const id of CHART_IDS) {
      const c = chart(page, id);
      await expect(c, `chart ${id} should be listed`).toBeVisible();
      // Fresh state: nothing hidden, so every checkbox starts checked.
      await expect(c).toBeChecked();
    }
    await expect(guard(page)).toBeHidden();
  });

  test("unchecking a chart persists into localStorage", async ({ page }) => {
    await openDashboardTab(page);
    await chart(page, "tokens").uncheck();
    // storage.js writes localStorage synchronously (before the debounced
    // PUT), so we can assert without waiting on the network.
    await expect
      .poll(() => readHiddenCharts(page), { timeout: 2000 })
      .toEqual(["tokens"]);
    // The other charts stay checked.
    await expect(chart(page, "tool_calls")).toBeChecked();
    await expect(chart(page, "prompts_vs_turns")).toBeChecked();
  });

  test("refuses to hide the last visible chart", async ({ page }) => {
    await openDashboardTab(page);
    // Hide all but the last of the five; the last must remain visible.
    const allButLast = CHART_IDS.slice(0, -1);
    const last = CHART_IDS[CHART_IDS.length - 1];
    for (const id of allButLast) {
      await chart(page, id).uncheck();
    }
    await expect
      .poll(() => readHiddenCharts(page), { timeout: 2000 })
      .toEqual(allButLast);

    // Attempt to uncheck the last one — must be refused.
    await chart(page, last).uncheck();

    // Guard message appears.
    await expect(guard(page)).toBeVisible();
    await expect(guard(page)).toContainText("At least one chart");

    // Checkbox snaps back to checked (the guard rejects the state change).
    await expect(chart(page, last)).toBeChecked();

    // localStorage is NOT mutated into an all-hidden list.
    expect(await readHiddenCharts(page)).toEqual(allButLast);
  });

  test("adopts external change events without a reload", async ({ page }) => {
    await openDashboardTab(page);
    // Simulate a peer tab / storage sync writing a new hidden list and firing
    // the CustomEvent that SettingsDialog subscribes to.
    await page.evaluate(() => {
      window.dispatchEvent(
        new CustomEvent("mitto-dashboard-hidden-charts-changed", {
          detail: { ids: ["tool_calls"] },
        }),
      );
    });
    await expect(chart(page, "tool_calls")).not.toBeChecked();
    await expect(chart(page, "tokens")).toBeChecked();
    await expect(chart(page, "prompts_vs_turns")).toBeChecked();
  });
});
