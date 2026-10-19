import { expect, test } from "../fixtures/test-fixtures";

test.describe("historical image failures", () => {
  test("suppresses repeated failed URL requests until explicit retry", async ({
    page,
    helpers,
  }) => {
    await helpers.navigateAndWait(page);

    const imageURL = "**/images/historical-missing.png";
    let requests = 0;
    await page.route(imageURL, async (route) => {
      await route.fulfill({
        status: 404,
        headers: { "cache-control": "no-store" },
        body: "not found",
      });
    });
    page.on("request", (request) => {
      if (request.url().includes("/images/historical-missing.png")) requests++;
    });

    await page.evaluate(async () => {
      const { AttachmentImage } = await import(
        new URL("components/AttachmentImage.js", location.href).href
      );
      const image = {
        id: "historical-missing.png",
        name: "Old attachment",
        url: new URL("api/sessions/test-session/images/historical-missing.png", location.href).href,
      };
      const host = document.createElement("div");
      host.id = "historical-image-test-host";
      document.body.appendChild(host);
      (window as any).__historicalImageTest = { AttachmentImage, image, host };
      window.preact.render(window.preact.h(AttachmentImage, { image }), host);
    });

    await expect(page.getByText("Image unavailable")).toBeVisible();
    await expect.poll(() => requests).toBe(1);

    await page.evaluate(() => {
      const { AttachmentImage, image, host } = (window as any).__historicalImageTest;
      window.preact.render(window.preact.h(AttachmentImage, { image }), host);
      window.preact.render(null, host);
      window.preact.render(window.preact.h(AttachmentImage, { image }), host);
    });
    await expect.poll(() => requests).toBe(1);

    await page.getByRole("button", { name: "Retry image" }).click();
    await expect.poll(() => requests).toBe(2); // exactly one explicit retry
    await expect(page.getByText("Image unavailable")).toBeVisible();
  });
});
