import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * On-completion periodic trigger UI tests.
 *
 * Verifies that the PeriodicFrequencyPanel correctly handles the
 * "On completion" trigger tab: tab switching, delay input visibility,
 * delay clamping (>= minDelaySeconds), max time inputs, and that the
 * correct PATCH bodies are sent.
 *
 * Setup: creates a session and configures it as periodic via the REST API
 * (more reliable than context-menu UI flows in beforeEach). The backend
 * sends a periodic_updated WebSocket event that flips periodicEnabled=true
 * in the frontend, causing the PeriodicFrequencyPanel to appear.
 */

test.describe("Periodic on-completion trigger", () => {
  let sessionId: string;

  test.beforeEach(async ({ page, request, helpers, timeouts }) => {
    // Create a fresh regular session
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `On-Completion Test ${Date.now()}` },
    });
    expect(
      createResp.ok(),
      `POST /api/sessions failed: ${createResp.status()}`,
    ).toBeTruthy();
    const created = await createResp.json();
    sessionId = created.session_id || created.id;
    expect(sessionId).toBeTruthy();

    await helpers.navigateAndWait(page);
    await helpers.navigateToSession(page, sessionId);

    // Configure the session as periodic directly via REST API.
    // This is more reliable in beforeEach than UI-driven context menus because
    // it avoids click-timing races; the backend still broadcasts periodic_updated
    // over WebSocket so the frontend panel appears as expected.
    const putResp = await request.put(
      apiUrl(`/api/sessions/${sessionId}/periodic`),
      {
        data: {
          prompt: "Test periodic",
          frequency: { value: 1, unit: "hours" },
          enabled: true,
          max_iterations: 0,
        },
      },
    );
    expect(
      putResp.ok(),
      `PUT periodic failed: ${putResp.status()}`,
    ).toBeTruthy();

    // The periodic_updated WS event flips periodicEnabled=true in ChatInput,
    // which makes the PeriodicFrequencyPanel visible.
    await expect(
      page.locator('[data-testid="periodic-frequency-panel"]'),
    ).toBeVisible({ timeout: timeouts.appReady });

    // Expand the settings body to show the trigger tabs and limit rows.
    await page.locator('[data-testid="periodic-expand-toggle"]').click();

    // Both trigger tabs should now be visible.
    await expect(
      page.locator('[data-testid="periodic-trigger-tab-schedule"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
    await expect(
      page.locator('[data-testid="periodic-trigger-tab-oncompletion"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("trigger tabs are visible after expanding the panel", async ({
    page,
    timeouts,
  }) => {
    // Tabs were asserted in beforeEach — confirm both are present
    await expect(
      page.locator('[data-testid="periodic-trigger-tab-schedule"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="periodic-trigger-tab-oncompletion"]'),
    ).toBeVisible();
  });

  test("max time value and unit inputs are visible in expanded panel", async ({
    page,
    timeouts,
  }) => {
    await expect(
      page.locator('[data-testid="periodic-max-duration-value"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="periodic-max-duration-unit"]'),
    ).toBeVisible();
  });

  test("clicking 'On completion' tab sends PATCH with trigger=onCompletion", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/periodic`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    await page
      .locator('[data-testid="periodic-trigger-tab-oncompletion"]')
      .click();

    // Staged edits: changes are only persisted when the Save button is pressed.
    await page.locator('[data-testid="periodic-save-button"]').click();

    await expect
      .poll(() => patchBodies.length, { timeout: timeouts.shortAction })
      .toBeGreaterThan(0);
    expect(patchBodies[0].trigger).toBe("onCompletion");
  });

  test("delay input appears after switching to 'On completion'", async ({
    page,
    timeouts,
  }) => {
    // Initially in schedule mode — delay input should not be visible
    await expect(
      page.locator('[data-testid="periodic-delay-input"]'),
    ).not.toBeVisible();

    // Switch to onCompletion
    await page
      .locator('[data-testid="periodic-trigger-tab-oncompletion"]')
      .click();

    // Delay input should now appear
    await expect(
      page.locator('[data-testid="periodic-delay-input"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("delay below floor is clamped to >= 5 after blur", async ({
    page,
    timeouts,
  }) => {
    // Switch to onCompletion
    await page
      .locator('[data-testid="periodic-trigger-tab-oncompletion"]')
      .click();
    await expect(
      page.locator('[data-testid="periodic-delay-input"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });

    // Enter a value below the 5s floor
    const delayInput = page.locator('[data-testid="periodic-delay-input"]');
    await delayInput.fill("2");
    await delayInput.blur();

    // After blur, the displayed value must be >= 5 (clamped client-side before PATCH)
    await expect
      .poll(async () => parseInt(await delayInput.inputValue(), 10), {
        timeout: timeouts.shortAction,
      })
      .toBeGreaterThanOrEqual(5);
  });

  test("setting max time value sends PATCH with max_duration_seconds > 0", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/periodic`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Set max time to 2 hours
    const maxDurInput = page.locator(
      '[data-testid="periodic-max-duration-value"]',
    );
    await maxDurInput.fill("2");
    await maxDurInput.blur();

    // Staged edits: changes are only persisted when the Save button is pressed.
    await page.locator('[data-testid="periodic-save-button"]').click();

    await expect
      .poll(
        () => patchBodies.find((b) => b.max_duration_seconds !== undefined),
        { timeout: timeouts.shortAction },
      )
      .toBeTruthy();

    const maxDurPatch = patchBodies.find(
      (b) => b.max_duration_seconds !== undefined,
    );
    // 2 hours = 7200 seconds (default unit is hours)
    expect(maxDurPatch.max_duration_seconds).toBeGreaterThan(0);
  });

  test("saving a new unbounded on-completion periodic warns, then saves on confirm", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/periodic`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Switch to onCompletion (pre-fills safety limits for this new conversation).
    await page
      .locator('[data-testid="periodic-trigger-tab-oncompletion"]')
      .click();

    // Clear both limits → unbounded config (dangerous for a brand-new periodic).
    await page
      .locator('[data-testid="periodic-panel-max-iterations"]')
      .fill("0");
    await page.locator('[data-testid="periodic-max-duration-value"]').fill("0");

    // Saving an unbounded, dangerous, brand-new periodic must prompt first.
    await page.locator('[data-testid="periodic-save-button"]').click();
    const dialog = page.locator('[data-testid="confirm-dialog"]');
    await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });
    await expect(dialog).toContainText("could keep running indefinitely");

    // No PATCH yet — the save is held pending confirmation.
    expect(patchBodies.length).toBe(0);

    // Confirm → the staged PATCH is sent with the unbounded on-completion config.
    await page.locator('[data-testid="confirm-dialog-confirm"]').click();
    await expect
      .poll(() => patchBodies.length, { timeout: timeouts.shortAction })
      .toBeGreaterThan(0);
    expect(patchBodies[0].trigger).toBe("onCompletion");
    expect(patchBodies[0].max_iterations).toBe(0);
    expect(patchBodies[0].max_duration_seconds).toBe(0);
  });

  test("cancelling the danger warning does not save", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/periodic`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    await page
      .locator('[data-testid="periodic-trigger-tab-oncompletion"]')
      .click();
    await page
      .locator('[data-testid="periodic-panel-max-iterations"]')
      .fill("0");
    await page.locator('[data-testid="periodic-max-duration-value"]').fill("0");

    await page.locator('[data-testid="periodic-save-button"]').click();
    const dialog = page.locator('[data-testid="confirm-dialog"]');
    await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

    // Cancel → the dialog closes and nothing is persisted.
    await page.locator('[data-testid="confirm-dialog-cancel"]').click();
    await expect(dialog).not.toBeVisible({ timeout: timeouts.shortAction });
    await page.waitForTimeout(500);
    expect(patchBodies.length).toBe(0);
  });

  // The "On tasks" trigger tab (mitto-oja.4) is gated to beads-enabled
  // workspaces. This test's session has no `.beads` directory, so the tab
  // must stay hidden alongside the two always-visible tabs.
  test("on-tasks trigger tab is hidden for a non-beads workspace", async ({
    page,
  }) => {
    await expect(
      page.locator('[data-testid="periodic-trigger-tab-ontasks"]'),
    ).toHaveCount(0);
  });
});
