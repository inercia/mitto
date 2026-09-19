/**
 * Render-domain isolation regression spec (mitto-b1k).
 *
 * Asserts the render-count acceptance criterion from
 * docs/devel/frontend-render-domains.md: a background session's message
 * chunk, a keystroke in the active composer, or an idle keepalive round trip
 * must not force the sidebar (`SessionList`) or the active conversation
 * (`MessageList`) — and, for the first two scenarios, the composer
 * (`ChatInput`) — to reconcile when nothing they read has actually changed.
 *
 * Reads `window.__mittoRenderCounts` (utils/renderCounters.js) and resets it
 * via `window.__mittoResetRenderCounts` (installRenderCountsReset(), also
 * mitto-b1k) between the scenario setup and the measured window, mirroring
 * the `window.__mittoPerfBuffer` drain pattern the other perf specs use.
 *
 * Smoke test of the instrumentation + the shipped memo()/slice migration,
 * not a hard performance gate — see docs/devel/frontend-render-domains.md
 * "Status after mitto-b1k" for the numeric before/after table this spec's
 * counts feed.
 */
import { Page } from "@playwright/test";
import { test, expect } from "../../fixtures/test-fixtures";
import { enablePerf, writePerfSample } from "../../utils/perf";
import { selectors, timeouts } from "../../utils/selectors";

type RenderCounts = Record<string, number>;

async function resetRenderCounts(page: Page): Promise<void> {
  await page.evaluate(() => {
    (
      window as unknown as { __mittoResetRenderCounts?: () => void }
    ).__mittoResetRenderCounts?.();
  });
}

async function getRenderCounts(page: Page): Promise<RenderCounts> {
  return page.evaluate(
    () =>
      (window as unknown as { __mittoRenderCounts?: RenderCounts })
        .__mittoRenderCounts || {},
  );
}

async function switchTo(page: Page, sessionId: string): Promise<void> {
  await page.locator(`[data-session-id="${sessionId}"]`).click();
  await expect
    .poll(async () =>
      page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
    )
    .toBe(sessionId);
}

test.describe("Perf: render-domain isolation (mitto-b1k)", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("a background session's chunk does not re-render SessionList/MessageList/ChatInput", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    // Foreground session stays active and mounted throughout.
    const foregroundId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(page, "perf render isolation foreground");

    // Background session: start the long deterministic stream, then switch
    // straight back to the foreground -- it keeps streaming in the
    // background per useWSConnection.js's isStreaming keep-connected rule.
    await helpers.createFreshSession(page);
    await helpers.sendMessage(page, "perf plain long");
    await helpers.waitForUserMessage(page, "perf plain long");
    // Let the background session's own one-time title assignment (its name
    // flips from "New conversation" to the first message's text, a
    // legitimate summary-field change the sidebar SHOULD reflect) settle
    // BEFORE switching away -- otherwise it races the reset below and gets
    // misattributed to the chunk-delivery measurement.
    await page.waitForTimeout(500);
    await switchTo(page, foregroundId);

    // Let the session-switch settle before the baseline reset.
    await page.waitForTimeout(300);
    await resetRenderCounts(page);

    // 200 chunks at a 5ms server cadence (~1s) plus the backend's ~200ms
    // soft-flush window -- 4s comfortably covers full delivery.
    await page.waitForTimeout(4000);

    const counts = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after background chunk: ${JSON.stringify(counts)}`,
    );

    expect(counts.MessageList || 0).toBe(0);
    expect(counts.ChatInput || 0).toBe(0);
    // SessionList (unlike MessageList/ChatInput) legitimately re-renders a
    // bounded, small number of times here: the background session's own
    // isStreaming flag flips true->false when its stream completes, and the
    // backend retries title generation once more right after completion --
    // both are real summary-field changes the sidebar SHOULD reflect (see
    // docs/devel/frontend-render-domains.md's domain table), not chunk-level
    // churn. Empirically 2-3 across repeated local runs; 3 is the tolerance.
    expect(counts.SessionList || 0).toBeLessThanOrEqual(3);

    writePerfSample(
      "render.background-chunk",
      "SessionList",
      counts.SessionList || 0,
    );
    writePerfSample(
      "render.background-chunk",
      "MessageList",
      counts.MessageList || 0,
    );
    writePerfSample(
      "render.background-chunk",
      "ChatInput",
      counts.ChatInput || 0,
    );
  });

  test("keystrokes in the active composer do not re-render SessionList/MessageList", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    await helpers.createFreshSession(page);
    await page.waitForTimeout(300);
    await resetRenderCounts(page);

    const textarea = page.locator(selectors.chatInput);
    await expect(textarea).toBeEnabled({ timeout: timeouts.shortAction });
    await textarea.pressSequentially("hello render isolation", {
      delay: 20,
    });

    const counts = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after composer keystrokes: ${JSON.stringify(counts)}`,
    );

    expect(counts.SessionList || 0).toBe(0);
    expect(counts.MessageList || 0).toBe(0);
    // Sanity check: the composer's own domain does re-render on its own
    // keystrokes (local draft state) -- a flat 0 here would mean the
    // instrumentation itself is not wired up, not that isolation improved.
    expect(counts.ChatInput || 0).toBeGreaterThan(0);

    writePerfSample("render.keystroke", "SessionList", counts.SessionList || 0);
    writePerfSample("render.keystroke", "MessageList", counts.MessageList || 0);
    writePerfSample("render.keystroke", "ChatInput", counts.ChatInput || 0);
  });

  test("an idle keepalive round trip does not re-render any render domain", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    const foregroundId = await helpers.createFreshSession(page);
    await helpers.sendMessageAndWait(
      page,
      "perf render isolation keepalive foreground",
    );
    await helpers.createFreshSession(page); // background session, left idle
    await switchTo(page, foregroundId);

    // Let one keepalive interval (10s) elapse unobserved -- absorbs any
    // one-time isRunning/acp_ready reconciliation from session creation --
    // then reset right before the measured window.
    await page.waitForTimeout(11_000);
    await resetRenderCounts(page);

    // A second full keepalive interval with nothing else happening.
    await page.waitForTimeout(11_000);

    const counts = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after idle keepalive interval: ${JSON.stringify(counts)}`,
    );

    expect(counts.SessionList || 0).toBe(0);
    expect(counts.MessageList || 0).toBe(0);
    expect(counts.ChatInput || 0).toBe(0);

    writePerfSample(
      "render.keepalive-idle",
      "SessionList",
      counts.SessionList || 0,
    );
    writePerfSample(
      "render.keepalive-idle",
      "MessageList",
      counts.MessageList || 0,
    );
    writePerfSample(
      "render.keepalive-idle",
      "ChatInput",
      counts.ChatInput || 0,
    );
  });

  test("a toast fired without a WS round trip does not re-render App/SessionList/MessageList/ChatInput (mitto-sus.11)", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    await page.waitForTimeout(300);
    await resetRenderCounts(page);

    // Fire the same window event useBackgroundNotifications.js's
    // `mitto:notification` listener reacts to on a WS `notification`
    // message -- exercises stores/notificationsStore.js's showToast() call
    // site without an actual WS round trip (mirrors the plan's "showToast
    // fired via module-level import" scenario).
    await page.evaluate(() => {
      window.dispatchEvent(
        new CustomEvent("mitto:notification", {
          detail: {
            title: "Render isolation toast",
            message: "fired without a WS round trip",
            style: "info",
          },
        }),
      );
    });

    // The toast renders (ToastContainer self-subscribes) -- App does not
    // need to re-render for this to happen.
    await expect(page.locator(".toast .alert")).toBeVisible({
      timeout: timeouts.shortAction,
    });

    const counts = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after showToast (no WS): ${JSON.stringify(counts)}`,
    );

    expect(counts.App || 0).toBe(0);
    expect(counts.SessionList || 0).toBe(0);
    expect(counts.MessageList || 0).toBe(0);
    expect(counts.ChatInput || 0).toBe(0);
    // Sanity check: ToastContainer is the ONLY domain expected to
    // re-render -- a flat 0 here would mean the toast never actually
    // rendered, not that isolation improved.
    expect(counts.ToastContainer || 0).toBeGreaterThan(0);

    writePerfSample("render.toast-no-ws", "App", counts.App || 0);
    writePerfSample(
      "render.toast-no-ws",
      "SessionList",
      counts.SessionList || 0,
    );
    writePerfSample(
      "render.toast-no-ws",
      "MessageList",
      counts.MessageList || 0,
    );
    writePerfSample("render.toast-no-ws", "ChatInput", counts.ChatInput || 0);
  });
});
