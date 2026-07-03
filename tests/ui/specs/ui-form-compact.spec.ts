import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * MCP UI form panel — compact sizing test.
 *
 * Regression test for the UX bug where an active `mitto_ui_form` panel always
 * stretched to the full persisted panel height (fixed `height`), leaving large
 * empty space below short forms. The panel now uses `max-height`, so it shrinks
 * to fit its content while still capping + scrolling for long forms.
 *
 * Strategy: pin the panel-height preference to the maximum (600px) before the
 * app mounts, inject a short 3-field form, and assert the rendered panel is far
 * smaller than that cap. With the old fixed-`height` code the panel would be a
 * solid 600px (test fails); with `max-height` it fits content (test passes).
 */

const PANEL_CAP = 600;

test.describe("MCP UI form panel — compact sizing", () => {
  test.beforeEach(async ({ page, helpers }) => {
    // Patch WebSocket BEFORE the page opens connections so we can dispatch
    // synthetic "message" events into the active session WS later. Also pin the
    // UI-prompt panel height preference to the maximum so the content-fitting
    // behavior is unambiguous (a fixed-height panel would equal the cap).
    await page.addInitScript((cap) => {
      try {
        localStorage.setItem("mitto_ui_prompt_panel_height", String(cap));
      } catch {
        /* ignore */
      }
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
    }, PANEL_CAP);

    await helpers.navigateAndEnsureSession(page);
  });

  test("short form panel fits content instead of stretching to the cap", async ({
    page,
  }) => {
    const sessionId = await page.evaluate(
      () => localStorage.getItem("mitto_last_session_id") || "",
    );
    expect(sessionId).not.toBe("");

    // Inject a synthetic ui_prompt (form) message into the active session WS.
    // Matches the payload shape produced by the backend in session_ws.go and
    // consumed by useWebSocket.js (prompt_type "form" + form_html).
    const dispatched = await page.evaluate((sid) => {
      const sockets = (window as any).__testWebSockets || [];
      const formHTML = [
        "<label for='name'>Name:</label>",
        "<input type='text' name='name' id='name' placeholder='Your name'>",
        "<label for='role'>Role:</label>",
        "<select name='role' id='role'>",
        "<option value='dev'>Developer</option>",
        "<option value='design'>Designer</option>",
        "</select>",
        "<div><label><input type='checkbox' name='sub' value='true'> Subscribe</label></div>",
      ].join("\n");
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          request_id: "test-ui-form-1",
          prompt_type: "form",
          title: "Compact form test",
          question: "Compact form test",
          form_html: formHTML,
          timeout_seconds: 60,
          blocking: true,
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

    // Form panel appears with our title and the injected fields.
    const panel = page.locator(".ui-prompt-panel");
    await expect(panel).toBeVisible({ timeout: 5000 });
    await expect(panel.filter({ hasText: "Compact form test" })).toBeVisible();
    await expect(panel.locator(".ui-form-content #name")).toBeVisible();

    // Key assertion: the panel fits its content rather than filling the 600px
    // cap. A short form is well under ~320px; assert comfortably below the cap.
    const box = await panel.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeLessThan(PANEL_CAP - 180); // < 420px
    expect(box!.height).toBeGreaterThan(80); // sanity: content actually rendered

    // The short form should not need to scroll (content fits its container).
    const overflow = await panel
      .locator(".ui-form-content")
      .evaluate((el) => el.scrollHeight - el.clientHeight);
    expect(overflow).toBeLessThanOrEqual(2);
  });

  test("radio-group question renders on its own line above stacked options", async ({
    page,
  }) => {
    const sessionId = await page.evaluate(
      () => localStorage.getItem("mitto_last_session_id") || "",
    );
    expect(sessionId).not.toBe("");

    // Inject a radio group using the imperfect markup agents commonly emit: the
    // question is a standalone (inline-by-default) <strong> heading inside a
    // <fieldset>, immediately followed by bare <input> options separated by
    // <br> (NOT each wrapped in a <label>). Without the form CSS safety net the
    // first option renders glued to the question line; the net forces such
    // headings to display:block so the question sits on its own row.
    const dispatched = await page.evaluate((sid) => {
      const sockets = (window as any).__testWebSockets || [];
      const formHTML = [
        "<fieldset>",
        "<strong id='q-head'>Scope of the drop:</strong>",
        "<input type='radio' name='scope' value='a' id='opt-a' checked> Drop only MCP requests<br>",
        "<input type='radio' name='scope' value='b' id='opt-b'> Drop by literal path match<br>",
        "<input type='radio' name='scope' value='c' id='opt-c'> Blanket: drop all spans",
        "</fieldset>",
      ].join("\n");
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          request_id: "test-ui-form-radio-1",
          prompt_type: "form",
          title: "Radio group test",
          question: "Radio group test",
          form_html: formHTML,
          timeout_seconds: 60,
          blocking: true,
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

    const panel = page.locator(".ui-prompt-panel");
    await expect(panel).toBeVisible({ timeout: 5000 });
    const head = panel.locator(".ui-form-content #q-head");
    const optA = panel.locator(".ui-form-content #opt-a");
    const optB = panel.locator(".ui-form-content #opt-b");
    const optC = panel.locator(".ui-form-content #opt-c");
    await expect(head).toBeVisible();
    await expect(optA).toBeVisible();

    // The safety net rule must make the standalone question heading block-level.
    const headDisplay = await head.evaluate(
      (el) => getComputedStyle(el).display,
    );
    expect(headDisplay).toBe("block");

    // Geometry: the question heading occupies its own row — the first radio
    // option starts at or below the heading's bottom (they do not share a line).
    const headBox = await head.boundingBox();
    const aBox = await optA.boundingBox();
    const bBox = await optB.boundingBox();
    const cBox = await optC.boundingBox();
    expect(headBox).not.toBeNull();
    expect(aBox).not.toBeNull();
    expect(bBox).not.toBeNull();
    expect(cBox).not.toBeNull();
    // First option's top is at/after the heading's bottom (small tolerance for
    // sub-pixel rounding) — i.e. the option is on a new line, not glued to the
    // question.
    expect(aBox!.y).toBeGreaterThanOrEqual(headBox!.y + headBox!.height - 4);
    // All three options are stacked on separate rows.
    expect(bBox!.y).toBeGreaterThan(aBox!.y + 4);
    expect(cBox!.y).toBeGreaterThan(bBox!.y + 4);
  });

  test("short textbox panel fits content instead of stretching to the cap", async ({
    page,
  }) => {
    const sessionId = await page.evaluate(
      () => localStorage.getItem("mitto_last_session_id") || "",
    );
    expect(sessionId).not.toBe("");

    // Inject a synthetic ui_prompt (textbox) with a short single line of text.
    const dispatched = await page.evaluate((sid) => {
      const sockets = (window as any).__testWebSockets || [];
      const payload = JSON.stringify({
        type: "ui_prompt",
        data: {
          session_id: sid,
          request_id: "test-ui-textbox-1",
          prompt_type: "textbox",
          title: "Compact textbox test",
          question: "Compact textbox test",
          text: "short line",
          result_mode: "text",
          allow_abort: true,
          timeout_seconds: 60,
          blocking: true,
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

    const panel = page.locator(".ui-prompt-panel");
    await expect(panel).toBeVisible({ timeout: 5000 });
    await expect(
      panel.filter({ hasText: "Compact textbox test" }),
    ).toBeVisible();
    const textarea = panel.locator(".ui-textbox-textarea");
    await expect(textarea).toBeVisible();

    // Panel fits content rather than filling the 600px cap (old: fixed height).
    const box = await panel.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeLessThan(PANEL_CAP - 180); // < 420px
    expect(box!.height).toBeGreaterThan(80); // sanity: content actually rendered

    // The textarea keeps a usable minimum editing height even for short text.
    const taHeight = await textarea.evaluate((el) => el.clientHeight);
    expect(taHeight).toBeGreaterThanOrEqual(110);
  });
});
