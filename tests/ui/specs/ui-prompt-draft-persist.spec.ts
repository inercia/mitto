import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";

/**
 * MCP UI prompt inputs survive conversation switches (mitto-osmb).
 *
 * ChatInput is a single component instance shared across sessions, so the
 * free-text / textbox / form inputs of a pending UI prompt used to be wiped
 * when the user switched to another conversation and back. Drafts now live in
 * utils/uiPromptDraftStore.js keyed by session + requestId.
 *
 * Strategy: inject a synthetic ui_prompt into session A's WS, type into it,
 * switch to session B, switch back to A, and assert the typed value is still
 * there.
 */

async function injectUIPrompt(page: Page, sid: string, data: object) {
  return page.evaluate(
    ({ sid, data }) => {
      const sockets = (window as any).__testWebSockets || [];
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          timeout_seconds: 120,
          blocking: true,
          ...data,
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
    },
    { sid, data },
  );
}

// Switch conversations without helpers.navigateToSession: that helper waits for
// the chat textarea, which is hidden while a UI prompt is active.
async function switchTo(page: Page, sessionId: string) {
  await page.locator(`[data-session-id="${sessionId}"]`).first().click();
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
    )
    .toBe(sessionId);
}

test.describe("MCP UI prompt drafts survive conversation switches", () => {
  let sessionA = "";
  let sessionB = "";

  test.beforeEach(async ({ page, helpers }) => {
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
    sessionA = await helpers.createFreshSession(page);
    sessionB = await helpers.createFreshSession(page);
    expect(sessionA).not.toBe(sessionB);
    await helpers.navigateToSession(page, sessionA);
  });

  test("options free text is restored after switching away and back", async ({
    page,
  }) => {
    const requestId = `draft-options-${Date.now()}`;
    expect(
      await injectUIPrompt(page, sessionA, {
        request_id: requestId,
        prompt_type: "options_buttons",
        question: "Free text draft test",
        options: [{ id: "a", label: "Option A" }],
        allow_free_text: true,
      }),
    ).toBeGreaterThan(0);

    const input = page.locator('.ui-prompt-panel input[type="text"]');
    await expect(input).toBeVisible({ timeout: 5000 });
    await input.fill("half-typed answer");

    await switchTo(page, sessionB);
    await expect(page.locator(".ui-prompt-panel")).toHaveCount(0);

    await switchTo(page, sessionA);
    await expect(input).toBeVisible({ timeout: 5000 });
    await expect(input).toHaveValue("half-typed answer");

    // A genuinely new prompt starts empty.
    await injectUIPrompt(page, sessionA, {
      request_id: `${requestId}-next`,
      prompt_type: "options_buttons",
      question: "Next free text prompt",
      options: [{ id: "a", label: "Option A" }],
      allow_free_text: true,
    });
    await expect(
      page.locator(".ui-prompt-panel", { hasText: "Next free text prompt" }),
    ).toBeVisible();
    await expect(input).toHaveValue("");
  });

  test("textbox edits are restored after switching away and back", async ({
    page,
  }) => {
    const requestId = `draft-textbox-${Date.now()}`;
    await injectUIPrompt(page, sessionA, {
      request_id: requestId,
      prompt_type: "textbox",
      title: "Textbox draft test",
      question: "Textbox draft test",
      text: "server text",
      result_mode: "text",
    });

    const textarea = page.locator(".ui-textbox-textarea");
    await expect(textarea).toBeVisible({ timeout: 5000 });
    await expect(textarea).toHaveValue("server text");
    await textarea.fill("server text, edited by me");

    await switchTo(page, sessionB);
    await expect(page.locator(".ui-prompt-panel")).toHaveCount(0);

    await switchTo(page, sessionA);
    await expect(textarea).toBeVisible({ timeout: 5000 });
    await expect(textarea).toHaveValue("server text, edited by me");
  });

  test("form field values are restored after switching away and back", async ({
    page,
  }) => {
    const requestId = `draft-form-${Date.now()}`;
    await injectUIPrompt(page, sessionA, {
      request_id: requestId,
      prompt_type: "form",
      title: "Form draft test",
      question: "Form draft test",
      form_html: [
        "<input type='text' name='name' id='name'>",
        "<select name='role'><option value='dev'>Dev</option><option value='design'>Design</option></select>",
        "<label><input type='checkbox' name='sub'> Subscribe</label>",
      ].join("\n"),
    });

    const content = page.locator(".ui-form-content");
    await expect(content.locator("[name=name]")).toBeVisible({ timeout: 5000 });
    await content.locator("[name=name]").fill("Ada");
    await content.locator("[name=role]").selectOption("design");
    await content.locator("[name=sub]").check();

    await switchTo(page, sessionB);
    await expect(page.locator(".ui-prompt-panel")).toHaveCount(0);

    await switchTo(page, sessionA);
    await expect(content.locator("[name=name]")).toBeVisible({ timeout: 5000 });
    await expect(content.locator("[name=name]")).toHaveValue("Ada");
    await expect(content.locator("[name=role]")).toHaveValue("design");
    await expect(content.locator("[name=sub]")).toBeChecked();
  });
});
