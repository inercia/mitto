import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * MCP UI options panel — chevron toggle test.
 *
 * Regression test for the UX bug where an active `mitto_ui_options` panel
 * auto-collapsed the chat input but provided no chevron to re-expand it
 * (unlike the textbox and form panels). Verifies that the extracted
 * PromptCollapseToggle is rendered inside the options panel and that
 * clicking it restores the chat input.
 */

test.describe("MCP UI options panel — chevron toggle", () => {
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

  test("chevron toggles chat input visibility while options panel is active", async ({
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
          question: "Chevron toggle test question?",
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
      panel.filter({ hasText: "Chevron toggle test question?" }),
    ).toBeVisible();

    // Chat input auto-collapses while an MCP UI prompt is active.
    await expect(page.locator(".chat-input-container")).toBeHidden();

    // The chevron (PromptCollapseToggle) is rendered inside the options panel.
    const chevronShow = page.locator(
      '.ui-prompt-panel button[data-tip="Show prompt area"]',
    );
    await expect(chevronShow).toBeVisible();

    // Clicking the chevron restores the chat input and flips the title.
    await chevronShow.click();
    await expect(page.locator(".chat-input-container")).toBeVisible();
    await expect(
      page.locator('.ui-prompt-panel button[data-tip="Hide prompt area"]'),
    ).toBeVisible();
  });
});
