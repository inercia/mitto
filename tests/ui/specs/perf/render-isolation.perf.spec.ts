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
import { selectors, timeouts, apiUrl } from "../../utils/selectors";

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

  test("adding and deleting a queued message via the REST API does not re-render MessageList (mitto-sus.11)", async ({
    page,
    helpers,
    request,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    const sessionId = await helpers.createFreshSession(page);

    // Keep the agent busy (isStreaming=true) for the whole scenario so the
    // queued message is NOT auto-processed by TryProcessQueuedMessage() --
    // that path only fires when the agent is idle, and would turn this into
    // a real new agent turn, legitimately re-rendering MessageList and
    // defeating the isolation assertion below. Unlike "perf plain long"
    // (continuous 5ms-cadence chunks that would themselves keep re-rendering
    // MessageList/App throughout the window), the dedicated
    // "perfqueuequiet" fixture (tests/fixtures/responses/perf-queue-quiet
    // .json) sends NO session/update notification at all for a fixed 3s
    // response-level delay -- a wide, deterministic quiet window to run the
    // queue add/delete round trip in without any unrelated chunk delivery
    // contaminating the render counts.
    await helpers.sendMessage(page, "perfqueuequiet");
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.agentResponse,
    });

    // Let the isStreaming=true transition settle before the baseline reset.
    await page.waitForTimeout(300);
    await resetRenderCounts(page);

    // POST /queue exercises the exact writer path this increment migrated:
    // useWebSocket.js's "queue_updated" handler now writes to queueStore.js
    // keyed by session id instead of an App-level useState (mirrors the
    // plan's "queue add / delete on the active session" test item).
    const queuedMessage = helpers.uniqueMessage("Queue-perf");
    const addResponse = await request.post(
      apiUrl(`/api/sessions/${sessionId}/queue`),
      { data: { message: queuedMessage } },
    );
    expect(addResponse.ok()).toBeTruthy();
    const added = await addResponse.json();

    // The queue toggle button only renders once queueLength > 0 -- reading
    // ChatInput's own self-subscribed useQueueLength(sessionId).
    await expect(page.locator(selectors.queueToggleButton)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    const countsAfterAdd = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after queue add: ${JSON.stringify(countsAfterAdd)}`,
    );
    // MessageList has no reason to read queue state at all -- this is the
    // core cross-domain isolation invariant this scenario exists to pin.
    expect(countsAfterAdd.MessageList || 0).toBe(0);

    const deleteResponse = await request.delete(
      apiUrl(`/api/sessions/${sessionId}/queue/${added.id}`),
    );
    expect(deleteResponse.ok()).toBeTruthy();

    // The toggle button disappears again once the queue drains back to 0.
    await expect(page.locator(selectors.queueToggleButton)).toBeHidden({
      timeout: timeouts.shortAction,
    });

    const counts = await getRenderCounts(page);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] render counts after queue add+delete: ${JSON.stringify(counts)}`,
    );

    expect(counts.MessageList || 0).toBe(0);
    // App and SessionList each keep exactly ONE legitimate, documented direct
    // subscription to the ACTIVE session's queue length (mitto-sus.11's
    // Implementation comment): App's own useQueueLength(activeSessionId)
    // drives headerHasQueued (the archive-button gate), and SessionList's
    // self-subscription drives its sidebar "queued messages" badge. Neither
    // is prop-drilled from the other, so each fires independently, at most
    // once per add and once per delete (bounded 2) -- unlike the flat-0
    // scenarios above, a nonzero count here is the intended reactive
    // behavior, not a re-render-isolation regression.
    expect(counts.App || 0).toBeLessThanOrEqual(2);
    expect(counts.SessionList || 0).toBeLessThanOrEqual(2);
    // Sanity check: ChatInput/QueueDropdown/App/SessionList ARE the domains
    // expected to re-render across the add+delete round trip -- a flat 0
    // here would mean the store wiring is broken, not that isolation
    // improved.
    expect(counts.ChatInput || 0).toBeGreaterThan(0);
    expect(counts.QueueDropdown || 0).toBeGreaterThan(0);
    expect(counts.App || 0).toBeGreaterThan(0);
    expect(counts.SessionList || 0).toBeGreaterThan(0);

    writePerfSample("render.queue-add-delete", "App", counts.App || 0);
    writePerfSample(
      "render.queue-add-delete",
      "SessionList",
      counts.SessionList || 0,
    );
    writePerfSample(
      "render.queue-add-delete",
      "MessageList",
      counts.MessageList || 0,
    );
    writePerfSample(
      "render.queue-add-delete",
      "ChatInput",
      counts.ChatInput || 0,
    );
  });
});
