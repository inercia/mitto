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
