import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

test.describe("Loop control bar and settings tab", () => {
  test("keeps compact actions and opens the full Loop editor", async ({
    page,
    request,
    helpers,
    timeouts,
  }) => {
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Loop Control Bar Test ${Date.now()}` },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    const sessionId = created.session_id || created.id;

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);

    const loopResp = await request.put(
      apiUrl(`/api/sessions/${sessionId}/loop`),
      {
        data: {
          prompt: "Compact control test",
          frequency: { value: 1, unit: "hours" },
          enabled: true,
          max_iterations: 3,
          triggers: ["schedule"],
        },
      },
    );
    expect(loopResp.ok()).toBeTruthy();

    let runNowCalls = 0;
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/loop/run-now`)}`,
      async (route) => {
        runNowCalls += 1;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: "{}",
        });
      },
    );

    const bar = page.locator('[data-testid="loop-control-bar"]');
    await expect(bar).toBeVisible({ timeout: timeouts.appReady });
    await expect(bar).toContainText("Running");
    await expect(
      page.locator('[data-testid="loop-frequency-panel"]'),
    ).toHaveCount(0);

    await bar.locator('[data-testid="loop-open-settings"]').click();
    const panel = page.locator('[data-testid="session-panel"]');
    await expect(panel.locator('label[aria-label="Loop"] input')).toBeChecked();
    await expect(
      panel.locator('[data-testid="loop-settings-tab"]'),
    ).toBeVisible();
    const callback = panel.locator('[data-testid="callback-trigger-section"]');
    const callbackToggle = callback.locator('[data-testid="callback-toggle"]');
    await expect(callback).toBeVisible();
    await expect(callbackToggle).toBeEnabled();
    await expect(callbackToggle).not.toBeChecked();
    await expect(callback).not.toContainText("Not configured");
    await expect(callback).not.toContainText("Generate callback URL");

    await callbackToggle.check();
    await expect(callbackToggle).toBeChecked();
    const callbackUrl = callback.locator('[data-testid="callback-url"]');
    await expect(callbackUrl).toBeVisible();
    await expect(callbackUrl).toHaveCSS("text-overflow", "ellipsis");
    await expect(
      callback.locator('[data-testid="callback-copy"]'),
    ).toBeVisible();

    await panel.getByRole("button", { name: "Close" }).click();
    await expect(panel).toHaveCount(0);

    await bar.locator('[data-testid="loop-pause-resume-button"]').click();
    await expect(bar).toContainText("Paused", {
      timeout: timeouts.shortAction,
    });

    await bar.locator('[data-testid="loop-pause-resume-button"]').click();
    await page.getByRole("button", { name: "Restore", exact: true }).click();
    await expect(bar).toContainText("Running", {
      timeout: timeouts.shortAction,
    });

    await bar.locator('[data-testid="loop-run-now-button"]').click();
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await expect.poll(() => runNowCalls).toBe(2);
  });

  // mitto-w7hh.2 follow-up: browser-level geometry regression for the narrow
  // Loop-tab layout fix (ToggleRow, General/onTasks numeric grids, Max
  // duration value+unit tracks, ~24rem dock width). Checks real bounding
  // boxes at a desktop-ish width (~1200px) and a narrow phone/small-window
  // width (~390px) rather than relying on brittle pixel-exact values.
  test("keeps Loop controls and wrapping copy inside the panel at 1200px and narrow phone widths", async ({
    page,
    request,
    helpers,
    timeouts,
  }) => {
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Loop Geometry Test ${Date.now()}` },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    const sessionId = created.session_id || created.id;

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);

    const loopResp = await request.put(
      apiUrl(`/api/sessions/${sessionId}/loop`),
      {
        data: {
          prompt: "Geometry regression test",
          frequency: { value: 1, unit: "hours" },
          enabled: true,
          max_iterations: 3,
          triggers: ["schedule"],
        },
      },
    );
    expect(loopResp.ok()).toBeTruthy();

    const bar = page.locator('[data-testid="loop-control-bar"]');
    await expect(bar).toBeVisible({ timeout: timeouts.appReady });
    await bar.locator('[data-testid="loop-open-settings"]').click();

    const panel = page.locator('[data-testid="session-panel"]');
    await expect(
      panel.locator('[data-testid="loop-settings-tab"]'),
    ).toBeVisible();
    await panel
      .locator('[data-testid="loop-settings-trigger-onTasks"]')
      .check();
    await panel
      .locator('[data-testid="loop-settings-trigger-onChild"]')
      .check();
    // The panel slides in via the 0.2s propertiesPanelSlideIn transform
    // animation (styles.css .properties-panel); wait for it to settle so
    // boundingBox() reads the final, resting geometry rather than a
    // mid-animation transform offset.
    await expect(async () => {
      const first = await panel.boundingBox();
      await page.waitForTimeout(50);
      const second = await panel.boundingBox();
      expect(first).toEqual(second);
    }).toPass({ timeout: timeouts.shortAction });

    // The "General" fieldset (Enabled / Fresh context / Run on start toggles
    // + Max runs / Max duration). Scoped by its legend text since the
    // "Prompt" fieldset shares the same outer classes.
    const generalFieldset = panel
      .locator('fieldset:has(legend:text-is("General"))')
      .first();
    await expect(generalFieldset).toBeVisible();

    const maxDurationValue = panel.locator(
      '[data-testid="loop-settings-max-duration-value"]',
    );
    const maxDurationUnit = panel.locator(
      '[data-testid="loop-settings-max-duration-unit"]',
    );
    const scheduleUnit = panel.locator(
      '[data-testid="loop-settings-schedule-unit"]',
    );
    const fireWhen = panel.locator('[data-testid="loop-settings-fire-when"]');
    const coalesceRow = panel.locator(
      '[data-testid="loop-settings-coalesce-row"]',
    );
    const onChildHeader = panel.locator(
      '[data-testid="loop-settings-trigger-header-onChild"]',
    );
    const onChildCard = panel.locator('[data-testid="loop-settings-onChild"]');
    const onSlackCard = panel.locator('[data-testid="loop-settings-onSlack"]');
    const callbackCard = panel.locator(
      '[data-testid="callback-trigger-section"]',
    );

    // Not pixel-perfect: enforce practical editor sizing and keep the unit
    // selector compact enough to read as a dropdown rather than a text field.
    const MIN_NUMBER_WIDTH = 80;
    const MIN_UNIT_WIDTH = 96;
    const MAX_UNIT_WIDTH = 128;
    // Small allowance for sub-pixel rounding between layout reads.
    const SLACK = 1;

    async function assertGeometryAtCurrentSize() {
      const viewport = page.viewportSize();
      expect(viewport).not.toBeNull();

      const panelBox = await panel.boundingBox();
      expect(panelBox).not.toBeNull();
      expect(panelBox!.x).toBeGreaterThanOrEqual(-SLACK);
      expect(panelBox!.x + panelBox!.width).toBeLessThanOrEqual(
        viewport!.width + SLACK,
      );
      expect(panelBox!.y).toBeGreaterThanOrEqual(-SLACK);
      expect(panelBox!.y + panelBox!.height).toBeLessThanOrEqual(
        viewport!.height + SLACK,
      );

      const fieldsetBox = await generalFieldset.boundingBox();
      expect(fieldsetBox).not.toBeNull();

      const toggleInputs = generalFieldset.locator(
        'input[type="checkbox"].toggle',
      );
      const toggleCount = await toggleInputs.count();
      // Enabled, Fresh context, Run on start.
      expect(toggleCount).toBeGreaterThanOrEqual(3);

      for (let i = 0; i < toggleCount; i++) {
        const toggleBox = await toggleInputs.nth(i).boundingBox();
        expect(toggleBox).not.toBeNull();
        expect(toggleBox!.x).toBeGreaterThanOrEqual(fieldsetBox!.x - SLACK);
        expect(toggleBox!.x + toggleBox!.width).toBeLessThanOrEqual(
          fieldsetBox!.x + fieldsetBox!.width + SLACK,
        );
        expect(toggleBox!.y).toBeGreaterThanOrEqual(fieldsetBox!.y - SLACK);
        expect(toggleBox!.y + toggleBox!.height).toBeLessThanOrEqual(
          fieldsetBox!.y + fieldsetBox!.height + SLACK,
        );
      }

      await expect(maxDurationValue).toBeVisible();
      await expect(maxDurationUnit).toBeVisible();
      const valueBox = await maxDurationValue.boundingBox();
      const unitBox = await maxDurationUnit.boundingBox();
      expect(valueBox).not.toBeNull();
      expect(unitBox).not.toBeNull();
      expect(valueBox!.width).toBeGreaterThanOrEqual(MIN_NUMBER_WIDTH);
      expect(unitBox!.width).toBeGreaterThanOrEqual(MIN_UNIT_WIDTH);
      expect(unitBox!.width).toBeLessThanOrEqual(MAX_UNIT_WIDTH);

      for (const [select, chevron] of [
        [
          maxDurationUnit,
          panel.locator(
            '[data-testid="loop-settings-max-duration-unit-chevron"]',
          ),
        ],
        [
          scheduleUnit,
          panel.locator('[data-testid="loop-settings-schedule-unit-chevron"]'),
        ],
        [
          fireWhen,
          panel.locator('[data-testid="loop-settings-fire-when-chevron"]'),
        ],
      ]) {
        await select.scrollIntoViewIfNeeded();
        await expect(select).toBeVisible();
        await expect(chevron).toBeVisible();
        const selectBox = await select.boundingBox();
        const chevronBox = await chevron.boundingBox();
        expect(selectBox).not.toBeNull();
        expect(chevronBox).not.toBeNull();
        expect(chevronBox!.x).toBeGreaterThan(selectBox!.x);
        expect(chevronBox!.x + chevronBox!.width).toBeLessThanOrEqual(
          selectBox!.x + selectBox!.width + SLACK,
        );
        expect(
          Math.abs(
            chevronBox!.y +
              chevronBox!.height / 2 -
              (selectBox!.y + selectBox!.height / 2),
          ),
        ).toBeLessThanOrEqual(SLACK);
      }

      async function assertWrappedCopy(
        row,
        copy,
        control,
        controlSide: "left" | "right",
      ) {
        await row.scrollIntoViewIfNeeded();
        await expect(copy).toHaveCSS("white-space", "normal");
        const [rowBox, copyBox, controlBox, overflow] = await Promise.all([
          row.boundingBox(),
          copy.boundingBox(),
          control.boundingBox(),
          copy.evaluate((node) => ({
            clientWidth: node.clientWidth,
            scrollWidth: node.scrollWidth,
          })),
        ]);
        expect(rowBox).not.toBeNull();
        expect(copyBox).not.toBeNull();
        expect(controlBox).not.toBeNull();
        expect(overflow.scrollWidth).toBeLessThanOrEqual(
          overflow.clientWidth + SLACK,
        );
        expect(copyBox!.x).toBeGreaterThanOrEqual(rowBox!.x - SLACK);
        expect(copyBox!.x + copyBox!.width).toBeLessThanOrEqual(
          rowBox!.x + rowBox!.width + SLACK,
        );
        if (controlSide === "right") {
          expect(copyBox!.x + copyBox!.width).toBeLessThanOrEqual(
            controlBox!.x + SLACK,
          );
        } else {
          expect(copyBox!.x).toBeGreaterThanOrEqual(
            controlBox!.x + controlBox!.width - SLACK,
          );
        }
      }

      await assertWrappedCopy(
        coalesceRow,
        coalesceRow.locator('[data-testid="loop-settings-coalesce-row-copy"]'),
        coalesceRow.locator(
          '[data-testid="loop-settings-coalesce-row-control"]',
        ),
        "right",
      );
      await assertWrappedCopy(
        onChildHeader,
        onChildHeader.locator(
          '[data-testid="loop-settings-trigger-copy-onChild"]',
        ),
        onChildHeader.locator('[data-testid="loop-settings-trigger-onChild"]'),
        "left",
      );

      const childEventRows = panel.locator(
        '[data-testid^="loop-settings-child-event-"]:not([data-testid*="-copy-"])',
      );
      for (let i = 0; i < (await childEventRows.count()); i++) {
        const row = childEventRows.nth(i);
        await assertWrappedCopy(
          row,
          row.locator('[data-testid*="loop-settings-child-event-copy-"]'),
          row.locator('input[type="checkbox"]'),
          "left",
        );
      }

      await callbackCard.scrollIntoViewIfNeeded();
      await expect(callbackCard).toContainText("On callback");
      await expect(callbackCard).not.toContainText("External callback");
      const [onChildBox, onSlackBox, callbackBox] = await Promise.all([
        onChildCard.boundingBox(),
        onSlackCard.boundingBox(),
        callbackCard.boundingBox(),
      ]);
      expect(onChildBox).not.toBeNull();
      expect(onSlackBox).not.toBeNull();
      expect(callbackBox).not.toBeNull();
      const regularCardGap =
        onSlackBox!.y - (onChildBox!.y + onChildBox!.height);
      const callbackCardGap =
        callbackBox!.y - (onSlackBox!.y + onSlackBox!.height);
      expect(Math.abs(callbackCardGap - regularCardGap)).toBeLessThanOrEqual(
        SLACK,
      );

      expect(valueBox!.x).toBeGreaterThanOrEqual(panelBox!.x - SLACK);
      expect(valueBox!.x + valueBox!.width).toBeLessThanOrEqual(
        panelBox!.x + panelBox!.width + SLACK,
      );
      expect(unitBox!.x).toBeGreaterThanOrEqual(panelBox!.x - SLACK);
      expect(unitBox!.x + unitBox!.width).toBeLessThanOrEqual(
        panelBox!.x + panelBox!.width + SLACK,
      );
    }

    await page.setViewportSize({ width: 1200, height: 900 });
    await assertGeometryAtCurrentSize();

    await page.setViewportSize({ width: 390, height: 844 });
    await assertGeometryAtCurrentSize();
  });
});
