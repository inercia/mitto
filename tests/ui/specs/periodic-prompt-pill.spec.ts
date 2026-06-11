import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import { selectors } from "../utils/selectors";

/**
 * Periodic Prompt Pill tests.
 *
 * These tests verify the end-to-end flow of periodic prompt triggering and
 * the NamedPromptPill appearing in the UI.
 *
 * Bug context:
 *   Before the fix, `checkAndFillGap` was called AFTER `updateLastKnownSeq`
 *   in the WebSocket message handlers. This meant that when a periodic prompt
 *   was triggered and the `user_prompt` event was missed by the WebSocket
 *   observer, gap detection in subsequent agent messages would fail because
 *   `updateLastKnownSeq` had already advanced the watermark past the missing
 *   event.
 *
 *   The fix swaps the order so `checkAndFillGap` runs BEFORE `updateLastKnownSeq`.
 *
 * Regression test:
 *   The pill should appear QUICKLY (within ~10s) after the periodic prompt is
 *   triggered via run-now. If the old bug were present the pill would either
 *   never appear (gap never filled) or only appear after a reconnect cycle
 *   (much longer than 10s).
 *
 * Fixture used:
 *   - Prompt: tests/fixtures/workspaces/project-alpha/.mitto/prompts/greeting.md
 *     → name: "Hello Greeting", text: "Hello! How are you doing today?"
 *   - Mock ACP response: tests/fixtures/responses/simple-greeting.json
 *     → trigger pattern: (?i)(^|\n)(hello|hi|hey)([!., \n]|$)  ← matches the prompt text
 */

test.describe("Periodic Prompt Pill", () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to the app. We do NOT use navigateAndEnsureSession here because
    // after test 1 creates a periodic session the app may auto-select it, and
    // periodic sessions hide the textarea that waitForAppReady expects.
    // Each test creates its own fresh session via createFreshSession() instead.
    await page.goto("/");
    // Wait for the page to be minimally loaded (session list visible).
    await page.waitForLoadState("networkidle");
  });

  test("should show NamedPromptPill when periodic prompt is triggered via run-now", async ({
    page,
    request,
    helpers,
    timeouts,
    apiUrl,
  }) => {
    // 1. Create a fresh session so we start with a clean slate.
    const sessionId = await helpers.createFreshSession(page);
    expect(sessionId).toBeTruthy();

    // 2. Configure periodic prompt using the "Hello Greeting" prompt that exists in
    //    the project-alpha workspace fixture. The mock ACP matches the resolved
    //    prompt text ("Hello! How are you doing today?") via the simple-greeting
    //    fixture (pattern: (?i)(hello|hi|hey)…).
    const periodicResponse = await request.put(
      apiUrl(`/api/sessions/${sessionId}/periodic`),
      {
        data: {
          prompt_name: "Hello Greeting",
          frequency: { value: 1, unit: "hours" },
          enabled: true,
        },
      },
    );
    expect(
      periodicResponse.ok(),
      `PUT periodic failed: ${periodicResponse.status()} ${await periodicResponse.text()}`,
    ).toBe(true);

    // 3. Record T0 so we can assert the pill appears promptly (not after a long delay).
    const t0 = Date.now();

    // 4. Trigger the periodic prompt immediately via run-now (bypass the schedule).
    //    The server may need a moment to un-archive / resume the session before it
    //    can accept a new prompt, so allow a brief retry on 409/503.
    let runResponse = await request.post(
      apiUrl(`/api/sessions/${sessionId}/periodic/run-now`),
      { data: { reset_timer: false } },
    );
    // If the session is briefly busy (409), wait a moment and retry once.
    if (runResponse.status() === 409) {
      await page.waitForTimeout(2000);
      runResponse = await request.post(
        apiUrl(`/api/sessions/${sessionId}/periodic/run-now`),
        { data: { reset_timer: false } },
      );
    }
    expect(
      runResponse.ok(),
      `POST run-now failed: ${runResponse.status()} ${await runResponse.text()}`,
    ).toBe(true);

    // 5. Assert the NamedPromptPill appears QUICKLY (within ~10s).
    //    A generous timeout is used to avoid flakiness, but we also record the
    //    elapsed time to prove the fix is working (the pill should not need a
    //    full reconnect cycle to appear).
    const pillLocator = page
      .locator('[data-testid="named-prompt-pill"]')
      .filter({ hasText: "Hello Greeting" });

    await expect(pillLocator).toBeVisible({ timeout: 10_000 });

    const elapsed = Date.now() - t0;
    console.log(
      `[periodic-prompt-pill] NamedPromptPill appeared after ${elapsed}ms ` +
        `(should be well under 10s if gap detection is working)`,
    );

    // 6. Assert the agent response also appears (proves the full round-trip worked).
    await expect(page.locator(selectors.agentMessage).first()).toBeVisible({
      timeout: timeouts.agentResponse,
    });
  });


});
