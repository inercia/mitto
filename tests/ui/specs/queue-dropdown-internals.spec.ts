import { test, expect } from "../fixtures/test-fixtures";
import { selectors, timeouts, apiUrl } from "../utils/selectors";

/**
 * QueueDropdown internals tests.
 *
 * Regression net: catches a daisyUI restyle breaking the queue dropdown's
 * anchored positioning, item rows, or move-up/move-down/delete actions.
 *
 * Strategy:
 *   Intercept queue REST endpoints with `page.route()` backed by an in-memory
 *   array. This avoids timing races (slow-response agent-busy approach) and
 *   makes the test fully deterministic. The GET /queue handler returns 3 items
 *   on page load so queueLength=3 immediately, making the toggle appear without
 *   any prompt or agent interaction.
 *
 *   Route handlers (registered before reload so they catch the initial fetch):
 *     GET  …/queue           → {messages: queue, count: queue.length}
 *     DELETE …/queue/{id}    → remove item, 200 {success:true}; frontend re-fetches via GET
 *     POST  …/queue/{id}/move → reorder by direction, 200 {messages, count}
 *
 *   All other requests fall through to the real server.
 */

test.describe("QueueDropdown internals", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("networkidle");
  });

  test("should open dropdown and show item rows with action buttons", async ({
    page,
    request,
  }) => {
    // ── 1. Create session via real API ──────────────────────────────────────
    const resp = await request.post(apiUrl("/api/sessions"), { data: {} });
    expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
    const data = await resp.json();
    const sessionId: string = data.session_id;
    expect(sessionId).toBeTruthy();

    // ── 2. Set up in-memory queue and intercept queue endpoints ─────────────
    // Use a plain object wrapper so the closure captures the reference and
    // mutations are visible across handler calls.
    const state = {
      queue: [
        { id: "q1", message: "Queued msg 1" },
        { id: "q2", message: "Queued msg 2" },
        { id: "q3", message: "Queued msg 3" },
      ] as Array<{ id: string; message: string }>,
    };

    // Single combined handler to avoid glob-ordering pitfalls.
    // Matches any URL containing /queue (with or without a path suffix).
    await page.route(`**/${sessionId}/queue**`, async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      // GET …/queue — initial fetch + re-fetch after delete
      if (method === "GET" && /\/queue$/.test(url)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ messages: state.queue, count: state.queue.length }),
        });
        return;
      }

      // DELETE …/queue/{id}
      const deleteMatch = url.match(/\/queue\/([^/]+)$/);
      if (method === "DELETE" && deleteMatch) {
        const id = deleteMatch[1];
        state.queue = state.queue.filter((item) => item.id !== id);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true }),
        });
        return;
      }

      // POST …/queue/{id}/move
      const moveMatch = url.match(/\/queue\/([^/]+)\/move$/);
      if (method === "POST" && moveMatch) {
        const id = moveMatch[1];
        const body = JSON.parse(route.request().postData() || "{}");
        const direction: string = body.direction;
        const idx = state.queue.findIndex((item) => item.id === id);
        if (idx !== -1) {
          if (direction === "up" && idx > 0) {
            [state.queue[idx - 1], state.queue[idx]] = [state.queue[idx], state.queue[idx - 1]];
          } else if (direction === "down" && idx < state.queue.length - 1) {
            [state.queue[idx], state.queue[idx + 1]] = [state.queue[idx + 1], state.queue[idx]];
          }
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ messages: state.queue, count: state.queue.length }),
        });
        return;
      }

      // Everything else (POST /queue to add items, etc.) passes through.
      await route.fallback();
    });

    // ── 3. Activate session — route is already registered so initial GET is intercepted ──
    await page.evaluate((id) => {
      localStorage.setItem("mitto_last_session_id", id);
      localStorage.removeItem("mitto_conversation_filter_tab");
    }, sessionId);
    await page.reload();

    // Wait for ACP ready (placeholder changes from "Waiting…" to "Type your message…")
    await expect(page.locator(selectors.chatInput)).toHaveAttribute(
      "placeholder",
      /Type your message/,
      { timeout: timeouts.agentResponse },
    );

    // ── 4. Toggle button should be visible (queueLength=3 from intercepted GET) ──
    const toggleBtn = page.locator(selectors.queueToggleButton);
    await expect(toggleBtn).toBeVisible({ timeout: timeouts.shortAction });

    // ── 5. Open the dropdown ────────────────────────────────────────────────
    await toggleBtn.click();
    const dropdown = page.locator('[data-testid="queue-dropdown"]');
    await expect(dropdown).toHaveAttribute("data-is-open", "true");

    // ── 6. Three item rows visible ──────────────────────────────────────────
    const items = page.locator('[data-testid="queue-item"]');
    await expect(items).toHaveCount(3, { timeout: timeouts.shortAction });
    await expect(items.first().locator(".queue-item-text")).toContainText("Queued msg 1");

    // ── 7. Hover first row — action buttons ────────────────────────────────
    await items.first().hover();

    // Move-up on first item is disabled (index 0)
    const moveUp = items.first().locator(".queue-item-move-up");
    await expect(moveUp).toBeVisible();
    await expect(moveUp).toBeDisabled();

    // Move-down on first item is enabled
    const moveDown = items.first().locator(".queue-item-move-down");
    await expect(moveDown).toBeVisible();
    await expect(moveDown).toBeEnabled();

    // Delete button is always enabled
    const deleteBtn = items.first().locator(".queue-item-delete");
    await expect(deleteBtn).toBeVisible();
    await expect(deleteBtn).toBeEnabled();

    // ── 8. Exercise move-down on first row ─────────────────────────────────
    await moveDown.click();
    // After move, first row should now show "Queued msg 2"
    await expect(items.first().locator(".queue-item-text")).toContainText("Queued msg 2", {
      timeout: timeouts.shortAction,
    });

    // ── 9. Delete the last item — count drops to 2 ──────────────────────────
    const lastItem = items.last();
    await lastItem.hover();
    await lastItem.locator(".queue-item-delete").click();

    const remainingItems = page.locator('[data-testid="queue-item"]');
    await expect(remainingItems).toHaveCount(2, { timeout: timeouts.shortAction });

    // New last item: move-up enabled, move-down disabled
    const newLastItem = remainingItems.last();
    await newLastItem.hover();
    await expect(newLastItem.locator(".queue-item-move-up")).toBeEnabled();
    await expect(newLastItem.locator(".queue-item-move-down")).toBeDisabled();
  });
});
