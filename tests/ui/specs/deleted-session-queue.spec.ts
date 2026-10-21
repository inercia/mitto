import { testWithCleanup, expect } from "../fixtures/test-fixtures";

async function createSession(request, apiUrl, name: string) {
  const response = await request.post(apiUrl("/api/sessions"), {
    data: { name },
  });
  expect(response.ok()).toBeTruthy();
  const session = await response.json();
  return session.session_id || session.id;
}

testWithCleanup.describe("Deleted session queue reads", () => {
  testWithCleanup(
    "a metadata-only 404 clears the selection without a queue fetch error",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      const sessionId = await createSession(
        request,
        apiUrl,
        `Metadata 404 ${Date.now()}`,
      );
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));
      page.on("console", (message) => {
        if (
          message.type() === "error" &&
          message.text().includes("Failed to fetch queue messages")
        ) {
          pageErrors.push(message.text());
        }
      });
      try {
        await page.route(`**/api/sessions/${sessionId}`, (route) =>
          route.fulfill({
            status: 404,
            contentType: "application/json",
            body: "{}",
          }),
        );
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionId,
        );
        await helpers.navigateAndWait(page);

        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBeNull();
        await expect(
          page.locator("span.font-semibold", { hasText: "Dashboard" }).first(),
        ).toBeVisible({ timeout: timeouts.appReady });
        await expect.poll(() => pageErrors.length).toBe(0);
      } finally {
        await request.delete(apiUrl(`/api/sessions/${sessionId}`));
      }
    },
  );

  testWithCleanup(
    "metadata and queue 404s show only one unavailable notice",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      const sessionId = await createSession(
        request,
        apiUrl,
        `Duplicate missing ${Date.now()}`,
      );
      try {
        await page.route(`**/api/sessions/${sessionId}`, (route) =>
          route.fulfill({
            status: 404,
            contentType: "application/json",
            body: "{}",
          }),
        );
        await page.route(`**/api/sessions/${sessionId}/queue`, (route) =>
          route.fulfill({
            status: 404,
            contentType: "application/json",
            body: "{}",
          }),
        );
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionId,
        );
        await helpers.navigateAndWait(page);
        await expect(
          page.locator("span.font-semibold", { hasText: "Dashboard" }).first(),
        ).toBeVisible({ timeout: timeouts.appReady });
        await expect(
          page
            .getByRole("alert")
            .filter({ hasText: "Conversation unavailable" }),
        ).toHaveCount(1);
      } finally {
        await request.delete(apiUrl(`/api/sessions/${sessionId}`));
      }
    },
  );

  testWithCleanup(
    "a selected session queue 404 clears selection and lands safely",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      const sessionId = await createSession(
        request,
        apiUrl,
        `Queue 404 ${Date.now()}`,
      );
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));
      page.on("console", (message) => {
        if (
          message.type() === "error" &&
          message.text().includes("Failed to fetch queue messages")
        ) {
          pageErrors.push(message.text());
        }
      });
      try {
        await page.route(`**/api/sessions/${sessionId}/queue`, (route) =>
          route.fulfill({
            status: 404,
            contentType: "application/json",
            body: "{}",
          }),
        );
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionId,
        );
        await helpers.navigateAndWait(page);

        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBeNull();
        await expect(
          page.locator("span.font-semibold", { hasText: "Dashboard" }).first(),
        ).toBeVisible({
          timeout: timeouts.appReady,
        });
        await expect.poll(() => pageErrors.length).toBe(0);
      } finally {
        await request.delete(apiUrl(`/api/sessions/${sessionId}`));
      }
    },
  );

  testWithCleanup(
    "deletion during a pending queue read settles once without a later refetch",
    async ({ page, request, apiUrl, helpers, timeouts }) => {
      const sessionId = await createSession(
        request,
        apiUrl,
        `Pending queue 404 ${Date.now()}`,
      );
      let resolveQueueRequested: (() => void) | undefined;
      let releaseQueueResponse: (() => void) | undefined;
      const queueRequested = new Promise<void>((resolve) => {
        resolveQueueRequested = resolve;
      });
      const heldResponse = new Promise<void>((resolve) => {
        releaseQueueResponse = resolve;
      });
      const pageErrors: string[] = [];
      let queueRequests = 0;
      page.on("pageerror", (error) => pageErrors.push(error.message));
      page.on("console", (message) => {
        if (
          message.type() === "error" &&
          message.text().includes("Failed to fetch queue messages")
        ) {
          pageErrors.push(message.text());
        }
      });
      try {
        await page.route(
          `**/api/sessions/${sessionId}/queue`,
          async (route) => {
            queueRequests += 1;
            resolveQueueRequested?.();
            await heldResponse;
            await route.fulfill({
              status: 404,
              contentType: "application/json",
              body: "{}",
            });
          },
        );
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionId,
        );
        await helpers.navigateAndWait(page);
        await queueRequested;
        const deleted = await request.delete(
          apiUrl(`/api/sessions/${sessionId}`),
        );
        expect(deleted.ok()).toBeTruthy();
        releaseQueueResponse?.();

        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBeNull();
        await expect(
          page.locator("span.font-semibold", { hasText: "Dashboard" }).first(),
        ).toBeVisible({ timeout: timeouts.appReady });
        await page.waitForTimeout(250);
        expect(queueRequests).toBe(1);
        await page.evaluate(() =>
          window.dispatchEvent(new CustomEvent("mitto:queue_updated")),
        );
        await page.waitForTimeout(100);
        expect(queueRequests).toBe(1);
        expect(pageErrors).toEqual([]);
      } finally {
        releaseQueueResponse?.();
        await request.delete(apiUrl(`/api/sessions/${sessionId}`));
      }
    },
  );

  testWithCleanup(
    "a delayed 404 for A cannot evict the newer B selection",
    async ({ page, request, apiUrl, helpers, selectors, timeouts }) => {
      const sessionA = await createSession(
        request,
        apiUrl,
        `Queue A ${Date.now()}`,
      );
      const sessionB = await createSession(
        request,
        apiUrl,
        `Queue B ${Date.now()}`,
      );
      let releaseQueueResponse: (() => void) | undefined;
      let resolveQueueRequested: (() => void) | undefined;
      const heldResponse = new Promise<void>((resolve) => {
        releaseQueueResponse = resolve;
      });
      const queueRequested = new Promise<void>((resolve) => {
        resolveQueueRequested = resolve;
      });
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));
      try {
        await page.route(`**/api/sessions/${sessionA}/queue`, async (route) => {
          resolveQueueRequested?.();
          await heldResponse;
          await route.fulfill({
            status: 404,
            contentType: "application/json",
            body: "{}",
          });
        });
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionA,
        );
        await helpers.navigateAndWait(page);
        await queueRequested;
        await page.locator(`[data-session-id="${sessionB}"]`).click();
        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBe(sessionB);
        await expect(page.locator(selectors.chatInput)).toBeVisible({
          timeout: timeouts.appReady,
        });

        releaseQueueResponse?.();
        await page.waitForTimeout(250);
        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBe(sessionB);
        expect(pageErrors).toEqual([]);
      } finally {
        releaseQueueResponse?.();
        await request.delete(apiUrl(`/api/sessions/${sessionA}`));
        await request.delete(apiUrl(`/api/sessions/${sessionB}`));
      }
    },
  );

  testWithCleanup(
    "a queue 500 does not evict the selected session",
    async ({ page, request, apiUrl, helpers, selectors, timeouts }) => {
      const sessionId = await createSession(
        request,
        apiUrl,
        `Queue 500 ${Date.now()}`,
      );
      try {
        await page.route(`**/api/sessions/${sessionId}/queue`, (route) =>
          route.fulfill({
            status: 500,
            contentType: "application/json",
            body: "{}",
          }),
        );
        await page.addInitScript(
          (id) => localStorage.setItem("mitto_last_session_id", id),
          sessionId,
        );
        await helpers.navigateAndWait(page);

        await expect
          .poll(() =>
            page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
          )
          .toBe(sessionId);
        await expect(page.locator(selectors.chatInput)).toBeVisible({
          timeout: timeouts.appReady,
        });
      } finally {
        await request.delete(apiUrl(`/api/sessions/${sessionId}`));
      }
    },
  );
});
