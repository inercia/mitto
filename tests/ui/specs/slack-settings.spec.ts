import { expect, test, type Page } from "@playwright/test";
import { selectors } from "../utils/selectors";

const APPS_URL = "https://api.slack.com/apps";
const CREATE_URL = `${APPS_URL}?new_app=1`;
const SETTINGS_URL = `${APPS_URL}/A%201%2F2`;

async function mockSlackSettings(page: Page) {
  await page.route("**/api/slack/apps", (route) =>
    route.fulfill({
      json: {
        apps: [
          {
            id: "app-a",
            name: "App Alpha",
            slack_app_id: "A 1/2",
            token_configured: true,
            validated_at: "2026-08-17T12:00:00Z",
          },
        ],
      },
    }),
  );
  await page.route("**/api/slack/apps/app-a/installations", (route) =>
    route.fulfill({
      json: {
        installations: [
          {
            id: "inst-a",
            name: "Workspace Alpha",
            team_id: "T111",
            token_configured: true,
          },
        ],
      },
    }),
  );
  await page.route("**/api/slack/environment-import", (route) =>
    route.fulfill({ json: { present: false, complete: false } }),
  );
}

async function mountSlackSettings(page: Page) {
  await page.goto("/");
  await page.waitForSelector(selectors.loadingSpinner, {
    state: "detached",
    timeout: 15_000,
  });
  await page.waitForFunction(() =>
    Boolean((window as any).preact?.render && (window as any).preact?.h),
  );
  await page.evaluate(async () => {
    const preact = (window as any).preact;
    const app = document.getElementById("app")!;
    preact.render(null, app);
    app.replaceChildren();
    const host = document.createElement("div");
    host.style.cssText =
      "position:fixed;inset:0;z-index:9999;overflow:auto;padding:16px;background:white";
    app.appendChild(host);
    const { SlackSettingsTab } =
      await import("/components/SlackSettingsTab.js?e2e=mitto-37nx-10");
    preact.render(preact.h(SlackSettingsTab, { showToast: () => {} }), host);
  });
  await expect(page.getByTestId("slack-settings-tab")).toBeVisible();
  await expect(page.getByTestId("slack-app-app-a")).toBeVisible();
  await expect(page.getByTestId("slack-installation-inst-a")).toBeVisible();
}

test.describe("Slack Settings external links", () => {
  test.beforeEach(async ({ page }) => {
    await mockSlackSettings(page);
  });

  test("uses secret-free official URLs through the browser fallback", async ({
    page,
  }) => {
    await mountSlackSettings(page);
    await page.evaluate(() => {
      (window as any).__openedSlackURLs = [];
      window.open = ((...args: unknown[]) => {
        (window as any).__openedSlackURLs.push(args);
        return null;
      }) as typeof window.open;
      (window as any).mittoOpenExternalURL = undefined;
    });

    await page.getByTestId("slack-create-app-external").click();
    await page.getByTestId("slack-open-app-settings").click();
    const opened = await page.evaluate(() => (window as any).__openedSlackURLs);
    expect(opened).toEqual([
      [CREATE_URL, "_blank", "noopener,noreferrer"],
      [SETTINGS_URL, "_blank", "noopener,noreferrer"],
    ]);
    expect(JSON.stringify(opened)).not.toContain("xapp-");
    expect(JSON.stringify(opened)).not.toContain("xoxb-");
  });

  test("prefers the native bridge and falls back to Slack Apps on failure", async ({
    page,
  }) => {
    await mountSlackSettings(page);
    await page.evaluate(() => {
      (window as any).__nativeSlackURLs = [];
      (window as any).__browserSlackURLs = [];
      window.open = ((...args: unknown[]) => {
        (window as any).__browserSlackURLs.push(args);
        return null;
      }) as typeof window.open;
      (window as any).mittoOpenExternalURL = (url: string) => {
        (window as any).__nativeSlackURLs.push(url);
      };
    });

    await page.getByTestId("slack-create-app-external").click();
    await page.getByTestId("slack-open-app-settings").click();
    expect(
      await page.evaluate(() => (window as any).__nativeSlackURLs),
    ).toEqual([CREATE_URL, SETTINGS_URL]);
    expect(
      await page.evaluate(() => (window as any).__browserSlackURLs),
    ).toEqual([]);

    await page.evaluate(() => {
      (window as any).__nativeSlackURLs = [];
      (window as any).mittoOpenExternalURL = (url: string) => {
        (window as any).__nativeSlackURLs.push(url);
        if (url !== "https://api.slack.com/apps")
          throw new Error("native failed");
      };
    });
    await page.getByTestId("slack-open-app-settings").click();
    expect(
      await page.evaluate(() => (window as any).__nativeSlackURLs),
    ).toEqual([SETTINGS_URL, APPS_URL]);
  });
});
