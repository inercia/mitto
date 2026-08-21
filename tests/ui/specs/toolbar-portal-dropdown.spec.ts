import { test, expect } from "../fixtures/test-fixtures";

test.describe("Toolbar portal dropdown", () => {
  test("escapes a narrow clipped pane and remains inside the viewport", async ({
    page,
    helpers,
  }) => {
    await helpers.navigateAndWait(page);
    await page.setViewportSize({ width: 800, height: 600 });

    await page.evaluate(async () => {
      const moduleUrl = new URL("./components/Toolbar.js", document.baseURI)
        .href;
      const { Toolbar } = await import(moduleUrl);
      const { h, render, useState } = window.preact;
      const host = document.createElement("div");
      host.dataset.testid = "portal-dropdown-test-host";
      Object.assign(host.style, {
        position: "fixed",
        right: "0",
        top: "100px",
        width: "220px",
        overflow: "hidden",
      });
      document.body.appendChild(host);

      function Harness() {
        const [open, setOpen] = useState(false);
        const menu = h(
          "ul",
          {
            class:
              "dropdown-content menu bg-mitto-surface-2 rounded-box shadow border border-mitto-border-1 w-64 p-1",
          },
          h(
            "li",
            null,
            h(
              "button",
              { type: "button", "data-testid": "portal-dropdown-row" },
              "Implementation child conversation",
            ),
          ),
        );
        return h(Toolbar, {
          variant: "block",
          items: [
            { kind: "spacer" },
            {
              kind: "dropdown",
              testId: "portal-dropdown-trigger",
              icon: h("span", null, "1"),
              open,
              onToggle: setOpen,
              closeOnOutsideClick: true,
              align: "end",
              portal: true,
              menu,
            },
          ],
        });
      }

      render(h(Harness), host);
    });

    await page.getByTestId("portal-dropdown-trigger").click();
    const surface = page.locator(".mitto-toolbar-portal-dropdown");
    const menu = surface.locator(":scope > .dropdown-content");
    await expect(page.getByTestId("portal-dropdown-row")).toBeVisible();

    const [surfaceBox, menuBox, hostBox] = await Promise.all([
      surface.boundingBox(),
      menu.boundingBox(),
      page.getByTestId("portal-dropdown-test-host").boundingBox(),
    ]);
    expect(surfaceBox).not.toBeNull();
    expect(menuBox).not.toBeNull();
    expect(hostBox).not.toBeNull();
    expect(menuBox!.x).toBeGreaterThanOrEqual(8);
    expect(menuBox!.x + menuBox!.width).toBeLessThanOrEqual(792);
    expect(menuBox!.y).toBeGreaterThanOrEqual(8);
    expect(menuBox!.y + menuBox!.height).toBeLessThanOrEqual(592);
    expect(menuBox!.x).toBeLessThan(hostBox!.x);

    // A click inside the portaled subtree must not look like an outside click.
    await page.getByTestId("portal-dropdown-row").click();
    await expect(surface).toBeVisible();
    await page.mouse.click(400, 500);
    await expect(surface).toBeHidden();
  });
});
