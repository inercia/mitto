import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { apiUrl } from "../utils/selectors";

/**
 * On-completion loop trigger UI tests (mitto-r6j: multi-trigger UI).
 *
 * Verifies that the LoopFrequencyPanel correctly handles the
 * "On completion" trigger checkbox: arming/disarming, delay-panel
 * visibility, delay clamping (>= minDelaySeconds), max time inputs,
 * and that the correct PATCH bodies (with a `triggers` array) are sent.
 *
 * Setup: creates a session and configures it as loop via the REST API
 * (more reliable than context-menu UI flows in beforeEach). The backend
 * sends a loop_updated WebSocket event that flips loopEnabled=true
 * in the frontend, causing the LoopFrequencyPanel to appear.
 */

test.describe("Loop on-completion trigger", () => {
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

    // Configure the session as loop directly via REST API.
    // This is more reliable in beforeEach than UI-driven context menus because
    // it avoids click-timing races; the backend still broadcasts loop_updated
    // over WebSocket so the frontend panel appears as expected.
    const putResp = await request.put(
      apiUrl(`/api/sessions/${sessionId}/loop`),
      {
        data: {
          prompt: "Test loop",
          frequency: { value: 1, unit: "hours" },
          enabled: true,
          max_iterations: 0,
          triggers: ["schedule"],
        },
      },
    );
    expect(
      putResp.ok(),
      `PUT loop failed: ${putResp.status()}`,
    ).toBeTruthy();

    // The loop_updated WS event flips loopEnabled=true in ChatInput,
    // which makes the LoopFrequencyPanel visible.
    await expect(
      page.locator('[data-testid="loop-frequency-panel"]'),
    ).toBeVisible({ timeout: timeouts.appReady });

    // Expand the settings body to show the trigger checkboxes and limit rows.
    await page.locator('[data-testid="loop-expand-toggle"]').click();

    // Both baseline trigger checkboxes should now be visible.
    await expect(
      page.locator('[data-testid="loop-trigger-check-schedule"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
    await expect(
      page.locator('[data-testid="loop-trigger-check-oncompletion"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("trigger checkboxes are visible after expanding the panel", async ({
    page,
    timeouts,
  }) => {
    // Checkboxes were asserted in beforeEach — confirm both are present
    await expect(
      page.locator('[data-testid="loop-trigger-check-schedule"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="loop-trigger-check-oncompletion"]'),
    ).toBeVisible();
  });

  test("max time value and unit inputs are visible in expanded panel", async ({
    page,
    timeouts,
  }) => {
    await expect(
      page.locator('[data-testid="loop-max-duration-value"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="loop-max-duration-unit"]'),
    ).toBeVisible();
  });

  test("arming 'On completion' (only) sends PATCH with triggers=['onCompletion']", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/loop`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Arm onCompletion (schedule is armed by default), then disarm schedule
    // so the resulting body carries only ["onCompletion"].
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();
    await page
      .locator('[data-testid="loop-trigger-check-schedule"]')
      .click();

    // Staged edits: changes are only persisted when the Save button is pressed.
    await page.locator('[data-testid="loop-save-button"]').click();

    await expect
      .poll(() => patchBodies.length, { timeout: timeouts.shortAction })
      .toBeGreaterThan(0);
    expect(patchBodies[0].triggers).toEqual(["onCompletion"]);
  });

  test("delay panel appears after arming 'On completion'", async ({
    page,
    timeouts,
  }) => {
    // Initially only schedule is armed — delay panel/input should not exist.
    await expect(
      page.locator('[data-testid="loop-delay-input"]'),
    ).toHaveCount(0);

    // Arm onCompletion (multi-select: schedule stays armed too).
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();

    // Delay input should now appear.
    await expect(
      page.locator('[data-testid="loop-delay-input"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });

  test("delay below floor is clamped to >= 5 after blur", async ({
    page,
    timeouts,
  }) => {
    // Arm onCompletion so the delay panel is visible.
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();
    await expect(
      page.locator('[data-testid="loop-delay-input"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });

    // Enter a value below the 5s floor
    const delayInput = page.locator('[data-testid="loop-delay-input"]');
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
      `**${apiUrl(`/api/sessions/${sessionId}/loop`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Set max time to 2 hours
    const maxDurInput = page.locator(
      '[data-testid="loop-max-duration-value"]',
    );
    await maxDurInput.fill("2");
    await maxDurInput.blur();

    // Staged edits: changes are only persisted when the Save button is pressed.
    await page.locator('[data-testid="loop-save-button"]').click();

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

  test("saving a new unbounded on-completion loop warns, then saves on confirm", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/loop`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Arm onCompletion (pre-fills safety limits for this new conversation)
    // and disarm schedule so the loop is on-completion-only.
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();
    await page
      .locator('[data-testid="loop-trigger-check-schedule"]')
      .click();

    // Clear both limits → unbounded config (dangerous for a brand-new loop).
    await page
      .locator('[data-testid="loop-panel-max-iterations"]')
      .fill("0");
    await page.locator('[data-testid="loop-max-duration-value"]').fill("0");

    // Saving an unbounded, dangerous, brand-new loop must prompt first.
    await page.locator('[data-testid="loop-save-button"]').click();
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
    expect(patchBodies[0].triggers).toEqual(["onCompletion"]);
    expect(patchBodies[0].max_iterations).toBe(0);
    expect(patchBodies[0].max_duration_seconds).toBe(0);
  });

  test("cancelling the danger warning does not save", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/loop`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();
    await page
      .locator('[data-testid="loop-trigger-check-schedule"]')
      .click();
    await page
      .locator('[data-testid="loop-panel-max-iterations"]')
      .fill("0");
    await page.locator('[data-testid="loop-max-duration-value"]').fill("0");

    await page.locator('[data-testid="loop-save-button"]').click();
    const dialog = page.locator('[data-testid="confirm-dialog"]');
    await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

    // Cancel → the dialog closes and nothing is persisted.
    await page.locator('[data-testid="confirm-dialog-cancel"]').click();
    await expect(dialog).not.toBeVisible({ timeout: timeouts.shortAction });
    await page.waitForTimeout(500);
    expect(patchBodies.length).toBe(0);
  });

  // The "On tasks" trigger checkbox (mitto-oja.4) is gated to beads-enabled
  // workspaces. This test's session has no `.beads` directory, so the checkbox
  // must stay hidden alongside the two always-visible checkboxes.
  test("on-tasks trigger checkbox is hidden for a non-beads workspace", async ({
    page,
  }) => {
    await expect(
      page.locator('[data-testid="loop-trigger-check-ontasks"]'),
    ).toHaveCount(0);
  });

  // mitto-r6j: multi-trigger loops let the user arm any non-empty subset of
  // {schedule, onCompletion, onTasks}. Verify that arming BOTH schedule and
  // onCompletion sends both entries in the PATCH `triggers` array and shows
  // both per-trigger sub-panels.
  test("arming schedule + onCompletion together sends triggers=['schedule','onCompletion']", async ({
    page,
    timeouts,
  }) => {
    const patchBodies: any[] = [];
    await page.route(
      `**${apiUrl(`/api/sessions/${sessionId}/loop`)}`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          patchBodies.push(route.request().postDataJSON());
        }
        await route.continue();
      },
    );

    // Schedule is armed by default; also arm onCompletion.
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();

    // Both sub-panels must be visible simultaneously.
    await expect(
      page.locator('[data-testid="loop-panel-schedule"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
    await expect(
      page.locator('[data-testid="loop-panel-oncompletion"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });

    await page.locator('[data-testid="loop-save-button"]').click();
    await expect
      .poll(() => patchBodies.length, { timeout: timeouts.shortAction })
      .toBeGreaterThan(0);
    expect(patchBodies[0].triggers).toEqual(["schedule", "onCompletion"]);
  });

  // mitto-r6j: the panel enforces "at least one trigger armed" — trying to
  // uncheck the last remaining trigger must be a no-op.
  test("cannot uncheck the last remaining trigger (invariant: >=1 armed)", async ({
    page,
  }) => {
    // Only schedule is armed by default.
    const scheduleCheckbox = page.locator(
      '[data-testid="loop-trigger-check-schedule"]',
    );
    await expect(scheduleCheckbox).toBeChecked();

    // Click it — the invariant must reject the uncheck.
    await scheduleCheckbox.click();
    await expect(scheduleCheckbox).toBeChecked();

    // The schedule sub-panel must therefore stay visible.
    await expect(
      page.locator('[data-testid="loop-panel-schedule"]'),
    ).toBeVisible();
  });

  // mitto-r6j.6: after arming a second trigger and saving, a page reload must
  // hydrate the panel with the full multi-trigger list from the server — both
  // checkboxes still checked and both sub-panels still visible. Regression
  // guard for a class of bugs where the frontend correctly PATCHes the whole
  // list but the initial GET/hydrate on reload only keeps the primary/first
  // trigger.
  test("multi-trigger config survives page reload (checkboxes + sub-panels)", async ({
    page,
    timeouts,
  }) => {
    // Arm onCompletion in addition to schedule and save.
    await page
      .locator('[data-testid="loop-trigger-check-oncompletion"]')
      .click();
    await expect(
      page.locator('[data-testid="loop-panel-oncompletion"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
    await page.locator('[data-testid="loop-save-button"]').click();

    // Reload the page. A loop session hides the textarea (which the standard
    // navigateAndWait helper waits on), so pin the session ID in localStorage
    // (mirrors the loop-edit-args pattern) and reload. The frontend can still
    // land on Dashboard rather than restoring the chat view for a loop
    // session, so open the sidebar and click the session tile explicitly if
    // the loop panel isn't already visible.
    await page.evaluate((sid) => {
      localStorage.setItem("mitto_last_session_id", sid);
      localStorage.removeItem("mitto_conversation_filter_tab");
    }, sessionId);
    await page.reload();

    const panel = page.locator('[data-testid="loop-frequency-panel"]');
    // If the frontend lands on Dashboard, the "Show conversations" toggle
    // opens the sidebar; then a data-session-id tile clicks into the loop
    // session. Both selectors are guarded so the test is resilient to a
    // future frontend change that restores the loop view directly.
    if (!(await panel.isVisible().catch(() => false))) {
      const openSidebar = page.getByText("Show conversations").first();
      if (await openSidebar.isVisible().catch(() => false)) {
        await openSidebar.click();
      }
      const sessionItem = page.locator(`[data-session-id="${sessionId}"]`);
      await expect(sessionItem).toBeVisible({ timeout: timeouts.appReady });
      await sessionItem.click();
    }
    await expect(panel).toBeVisible({ timeout: timeouts.appReady });

    // Re-expand the panel — the collapsed state is UI-local.
    await page.locator('[data-testid="loop-expand-toggle"]').click();

    // Both checkboxes must be checked after reload.
    await expect(
      page.locator('[data-testid="loop-trigger-check-schedule"]'),
    ).toBeChecked({ timeout: timeouts.appReady });
    await expect(
      page.locator('[data-testid="loop-trigger-check-oncompletion"]'),
    ).toBeChecked({ timeout: timeouts.shortAction });

    // Both sub-panels must be visible simultaneously.
    await expect(
      page.locator('[data-testid="loop-panel-schedule"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
    await expect(
      page.locator('[data-testid="loop-panel-oncompletion"]'),
    ).toBeVisible({ timeout: timeouts.shortAction });
  });
});
