import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * MCP UI options panel — stop button test.
 *
 * Verifies that when a mitto_ui_options panel is active, a Stop button is
 * rendered inside the panel (replacing the former show/hide chevron toggle),
 * the chat-input composition area is hidden, and clicking Stop aborts the
 * prompt and restores the chat input.
 */

test.describe("MCP UI options panel — stop button", () => {
  test.beforeEach(async ({ page, helpers }) => {
    // Monkey-patch WebSocket BEFORE the page opens any connections so we can
    // dispatch synthetic "message" events into the active session WS later.
    await page.addInitScript(() => {
      const originalWS = window.WebSocket;
      (window as any).__testWebSockets = [];
      const PatchedWS: any = function (
        url: string,
        protocols?: string | string[],
      ) {
        const ws = new originalWS(url, protocols);
        (window as any).__testWebSockets.push(ws);
        return ws;
      };
      PatchedWS.prototype = originalWS.prototype;
      PatchedWS.CONNECTING = originalWS.CONNECTING;
      PatchedWS.OPEN = originalWS.OPEN;
      PatchedWS.CLOSING = originalWS.CLOSING;
      PatchedWS.CLOSED = originalWS.CLOSED;
      (window as any).WebSocket = PatchedWS;
    });

    await helpers.navigateAndEnsureSession(page);
  });

  test("stop button aborts the prompt and restores chat input", async ({
    page,
  }) => {
    const sessionId = await page.evaluate(
      () => localStorage.getItem("mitto_last_session_id") || "",
    );
    expect(sessionId).not.toBe("");

    // Chat input is visible before any UI prompt arrives.
    await expect(page.locator(".chat-input-container")).toBeVisible();

    // Inject a synthetic ui_prompt (options_buttons) message into the active
    // session WebSocket. Matches the payload shape produced by the backend
    // in internal/web/session_ws.go and consumed by useWebSocket.js.
    const dispatched = await page.evaluate((sid) => {
      const sockets = (window as any).__testWebSockets || [];
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          request_id: "test-ui-options-1",
          prompt_type: "options_buttons",
          question: "Stop button test question?",
          options: [
            { id: "a", label: "Option A", description: "First option" },
            { id: "b", label: "Option B", description: "Second option" },
          ],
          timeout_seconds: 60,
          blocking: true,
          allow_free_text: false,
        },
      });
      let count = 0;
      for (const ws of sockets) {
        if (
          ws.readyState === WebSocket.OPEN &&
          typeof ws.url === "string" &&
          ws.url.includes(`/sessions/${sid}/ws`)
        ) {
          ws.dispatchEvent(new MessageEvent("message", { data: payload }));
          count++;
        }
      }
      return count;
    }, sessionId);

    expect(dispatched).toBeGreaterThan(0);

    // Options panel appears with our question.
    const panel = page.locator(".ui-prompt-panel");
    await expect(panel).toBeVisible({ timeout: 5000 });
    await expect(
      panel.filter({ hasText: "Stop button test question?" }),
    ).toBeVisible();

    // Chat input auto-collapses while an MCP UI prompt is active.
    await expect(page.locator(".chat-input-container")).toBeHidden();

    // The old toggle is GONE — no "Show prompt area" button.
    await expect(
      page.locator('.ui-prompt-panel button[data-tip="Show prompt area"]'),
    ).toHaveCount(0);

    // A Stop button IS present inside the options panel.
    const stopBtn = page.locator(
      '.ui-prompt-panel button[data-tip="Stop the agent"]',
    );
    await expect(stopBtn).toBeVisible();

    // Clicking Stop dismisses the panel and restores the chat input.
    await stopBtn.click();
    await expect(page.locator(".ui-prompt-panel")).toBeHidden();
    await expect(page.locator(".chat-input-container")).toBeVisible();
  });

  test("periodic frequency panel hides during a UI prompt and reappears after Stop", async ({
    page,
    request,
    apiUrl,
    helpers,
    timeouts,
  }) => {
    // Create a fresh, isolated session and convert it to periodic via the API.
    // (The right-click "Make periodic" context menu flow is covered elsewhere;
    // here we go straight through the REST endpoint so this test does not depend
    // on the session-list context menu.)
    const sessionId = await helpers.createFreshSession(page);
    expect(sessionId).toBeTruthy();

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

    // The periodic_updated broadcast flips periodicConfigured=true, so the
    // PeriodicFrequencyPanel opens (isOpen → opacity-100; collapsed → h-0).
    const periodicPanel = page.locator(
      '[data-testid="periodic-frequency-panel"]',
    );
    await expect(periodicPanel).toBeVisible({ timeout: timeouts.appReady });

    // Inject a synthetic ui_prompt (options) into the active session WebSocket.
    const dispatched = await page.evaluate((sid) => {
      const sockets = (window as any).__testWebSockets || [];
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          request_id: "test-ui-periodic-1",
          prompt_type: "options_buttons",
          question: "Periodic hide test question?",
          options: [
            { id: "a", label: "Option A", description: "First option" },
            { id: "b", label: "Option B", description: "Second option" },
          ],
          timeout_seconds: 60,
          blocking: true,
          allow_free_text: false,
        },
      });
      let count = 0;
      for (const ws of sockets) {
        if (
          ws.readyState === WebSocket.OPEN &&
          typeof ws.url === "string" &&
          ws.url.includes(`/sessions/${sid}/ws`)
        ) {
          ws.dispatchEvent(new MessageEvent("message", { data: payload }));
          count++;
        }
      }
      return count;
    }, sessionId);

    expect(dispatched).toBeGreaterThan(0);

    // The UI prompt panel appears...
    const panel = page.locator(".ui-prompt-panel");
    await expect(panel).toBeVisible({ timeout: 5000 });

    // ...and the periodic frequency panel collapses (hidden) while the UI prompt
    // is active — the two are mutually exclusive.
    await expect(periodicPanel).toBeHidden({ timeout: 5000 });

    // Stopping the prompt aborts it; the periodic frequency panel reappears.
    const stopBtn = page.locator(
      '.ui-prompt-panel button[data-tip="Stop the agent"]',
    );
    await expect(stopBtn).toBeVisible();
    await stopBtn.click();

    await expect(panel).toBeHidden();
    await expect(periodicPanel).toBeVisible({ timeout: timeouts.appReady });
  });
});
