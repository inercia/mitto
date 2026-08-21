import { test, expect } from "../fixtures/test-fixtures";
import { selectors, apiUrl, timeouts } from "../utils/selectors";
import type { Page, APIRequestContext } from "@playwright/test";

/**
 * Zero-byte image upload guard (mitto-e1bv).
 *
 * Verifies the three defense layers:
 *   A. Frontend guard in ChatInput.uploadImage() blocks the request and shows
 *      a toast — no POST to /api/sessions/{id}/images is emitted.
 *   B. A valid tiny PNG still uploads successfully (regression guard).
 *   C. Backend handleUploadImage() returns 400 code=image_empty when the
 *      frontend guard is bypassed (direct fetch of a header-only multipart
 *      part).
 *
 * Uses a local apiCreateSession (copied from loop-edit-args.spec.ts) instead
 * of helpers.navigateAndEnsureSession because the sidebar is mid-refactor and
 * the shared helper's new-conversation-btn selector is stale.
 */

// Well-known minimal 1x1 transparent PNG.
const TINY_PNG_B64 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";

async function apiCreateSession(
  page: Page,
  request: APIRequestContext,
): Promise<string> {
  // Create the session server-side FIRST, before the page ever loads. The
  // upload-zero-byte spec avoids `helpers.navigateAndEnsureSession` because
  // the sidebar is mid-refactor and its `new-conversation-btn` selector is
  // stale; instead we prime `mitto_last_session_id` via addInitScript so the
  // app boots straight into the conversation view (skipping the Dashboard
  // fallback that fires when no valid last-session id is present).
  const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  const id: string = (await resp.json()).session_id;
  await page.addInitScript((sid) => {
    localStorage.setItem("mitto_last_session_id", sid);
    localStorage.removeItem("mitto_conversation_filter_tab");
  }, id);
  await page.goto("/");
  // In the current sidebar refactor, cold-start may land on the global
  // Dashboard even when a valid mitto_last_session_id is present. Force the
  // conversation view by clicking the sidebar entry for the session we just
  // created (SessionItem exposes `data-session-id=<id>` on its clickable div).
  const chatInput = page.locator(selectors.chatInput);
  try {
    await expect(chatInput).toBeVisible({ timeout: 3000 });
  } catch {
    await page.locator(`[data-session-id="${id}"]`).click({ timeout: 5000 });
  }
  await expect(chatInput).toHaveAttribute(
    "placeholder",
    /Type your message/,
    { timeout: timeouts.agentResponse },
  );
  return id;
}

test.describe("Zero-byte image upload (mitto-e1bv)", () => {

  test("Scenario A: frontend guard blocks zero-byte image and shows toast", async ({
    page,
    request,
  }) => {
    await apiCreateSession(page, request);

    // Collect image-upload POSTs BEFORE injecting the file.
    const imagePosts: string[] = [];
    page.on("request", (req) => {
      if (req.method() === "POST" && /\/api\/sessions\/.+\/images$/.test(req.url())) {
        imagePosts.push(req.url());
      }
    });

    const imageInput = page
      .locator('input[type="file"][accept*="image"]')
      .first();
    await imageInput.setInputFiles({
      name: "empty.png",
      mimeType: "image/png",
      buffer: Buffer.alloc(0),
    });

    const errorBanner = page.locator("span", {
      hasText: /Cannot upload zero-byte image/,
    });
    await expect(errorBanner).toBeVisible({ timeout: 5000 });

    // Give the network a beat to prove no request was queued.
    await page.waitForTimeout(500);
    expect(imagePosts, "no POST to /images should occur").toEqual([]);
  });

  test("Scenario B: valid tiny PNG uploads successfully", async ({
    page,
    request,
  }) => {
    await apiCreateSession(page, request);

    const imageInput = page
      .locator('input[type="file"][accept*="image"]')
      .first();

    const uploadPromise = page.waitForResponse(
      (resp) =>
        resp.request().method() === "POST" &&
        /\/api\/sessions\/.+\/images$/.test(resp.url()),
      { timeout: 10000 },
    );

    await imageInput.setInputFiles({
      name: "tiny.png",
      mimeType: "image/png",
      buffer: Buffer.from(TINY_PNG_B64, "base64"),
    });

    const resp = await uploadPromise;
    expect(resp.status()).toBe(201);
    const body = await resp.json();
    // ImageUploadResponse has {id, url, name, mimeType, size}. `name` is the
    // filename analogue for this handler.
    expect(body.name || body.filename).toBeTruthy();
    expect(body.id).toBeTruthy();

    const errorBanner = page.locator("span", {
      hasText: /Cannot upload zero-byte image/,
    });
    await expect(errorBanner).toBeHidden();
  });

  test("Scenario C: backend rejects direct zero-byte POST with image_empty", async ({
    page,
    request,
  }) => {
    const sessionId = await apiCreateSession(page, request);

    // Post directly via fetch, bypassing the ChatInput frontend guard.
    const result = await page.evaluate(async (sid) => {
      const fd = new FormData();
      const emptyFile = new File([new Uint8Array(0)], "empty.png", {
        type: "image/png",
      });
      fd.append("image", emptyFile);
      const r = await fetch(`/mitto/api/sessions/${sid}/images`, {
        method: "POST",
        body: fd,
        credentials: "include",
      });
      let body: unknown = null;
      try {
        body = await r.json();
      } catch {
        body = null;
      }
      return { status: r.status, body };
    }, sessionId);

    expect(result.status).toBe(400);
    const body = result.body as
      | { code?: string; message?: string; error?: { code?: string; message?: string } }
      | null;
    expect(body).not.toBeNull();
    // The error envelope may be flat {code, message} or nested under `error`.
    const code = body?.code ?? body?.error?.code;
    const message = body?.message ?? body?.error?.message;
    expect(code).toBe("image_empty");
    expect(message).toMatch(/empty/i);
  });
});
