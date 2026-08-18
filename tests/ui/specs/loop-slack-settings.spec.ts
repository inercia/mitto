import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { apiUrl, selectors } from "../utils/selectors";

const appA = {
  id: "app-a",
  name: "App Alpha",
  token_configured: true,
};
const appB = {
  id: "app-b",
  name: "App Beta",
  token_configured: true,
};

async function mockSlackCatalog(page: Page, failFirstChannel = false) {
  let instAChannelCalls = 0;
  await page.route("**/api/slack/apps", (route) =>
    route.fulfill({ json: { apps: [appA, appB] } }),
  );
  await page.route("**/api/slack/apps/app-a/installations", (route) =>
    route.fulfill({
      json: {
        installations: [
          {
            id: "inst-a",
            name: "Workspace Alpha",
            team_id: "TA",
            token_configured: true,
          },
        ],
      },
    }),
  );
  await page.route("**/api/slack/apps/app-b/installations", (route) =>
    route.fulfill({
      json: {
        installations: [
          {
            id: "inst-b",
            name: "Workspace Beta",
            team_id: "TB",
            token_configured: true,
          },
        ],
      },
    }),
  );
  await page.route(
    "**/api/slack/installations/inst-a/channels*",
    async (route) => {
      instAChannelCalls += 1;
      if (failFirstChannel && instAChannelCalls === 1) {
        await route.fulfill({
          status: 503,
          json: { error: { message: "missing channels:read" } },
        });
        return;
      }
      const cursor = new URL(route.request().url()).searchParams.get("cursor");
      await route.fulfill({
        json: cursor
          ? {
              channels: [
                {
                  id: "C2",
                  name: "private-ops",
                  is_private: true,
                  is_member: true,
                },
              ],
              next_cursor: "",
            }
          : {
              channels: [
                { id: "C1", name: "general", is_member: true },
                ...(failFirstChannel
                  ? [{ id: "C404", name: "saved-channel", is_member: true }]
                  : []),
              ],
              next_cursor: failFirstChannel ? "" : "page-2",
            },
      });
    },
  );
  await page.route("**/api/slack/installations/inst-b/channels*", (route) =>
    route.fulfill({
      json: {
        channels: [{ id: "C3", name: "announcements", is_member: true }],
        next_cursor: "",
      },
    }),
  );
}

async function mountLoopEditor(page: Page, sessionId: string) {
  await page.goto("/");
  await page.waitForSelector(selectors.loadingSpinner, {
    state: "detached",
    timeout: 15_000,
  });
  await page.waitForFunction(() =>
    Boolean((window as any).preact?.render && (window as any).preact?.h),
  );
  await page.evaluate(async (id) => {
    const preact = (window as any).preact;
    const app = document.getElementById("app")!;
    preact.render(null, app);
    app.replaceChildren();
    const host = document.createElement("div");
    host.id = "slack-loop-test-root";
    host.style.cssText =
      "position:fixed;inset:0;z-index:1;overflow:auto;padding:16px;background:white";
    app.appendChild(host);
    const { LoopSettingsTab } =
      await import("/components/LoopSettingsTab.js?e2e=mitto-37nx-7");
    (window as any).__slackLoopToasts = [];
    preact.render(
      preact.h(LoopSettingsTab, {
        sessionId: id,
        loopPrompts: [],
        allPrompts: [],
        hasBeadsWorkspace: false,
        showToast: (toast: unknown) =>
          (window as any).__slackLoopToasts.push(toast),
      }),
      host,
    );
  }, sessionId);
  await expect(page.getByTestId("loop-settings-tab")).toBeVisible();
}

async function createLoop(
  request: APIRequestContext,
  sessionId: string,
  body: Record<string, unknown>,
) {
  const response = await request.put(
    apiUrl(`/api/sessions/${sessionId}/loop`),
    {
      data: {
        loop_prompt: "Summarize Slack activity",
        triggers: ["schedule"],
        frequency: { value: 1, unit: "hours" },
        enabled: false,
        max_iterations: 5,
        ...body,
      },
    },
  );
  const failure = response.ok()
    ? ""
    : `${response.status()} ${await response.text()}`;
  expect(response.ok(), failure).toBe(true);
}

test.describe("onSlack loop settings", () => {
  let sessionId = "";

  test.beforeEach(async ({ page, request }) => {
    await mockSlackCatalog(page);
    const response = await request.post(apiUrl("/api/sessions"), {
      data: { title: "Slack loop picker test" },
    });
    expect(response.ok()).toBe(true);
    sessionId = (await response.json()).session_id;
    await createLoop(request, sessionId, {});
  });

  test.afterEach(async ({ request }) => {
    if (sessionId) {
      await request.delete(apiUrl(`/api/sessions/${sessionId}`));
    }
  });

  test("edits multiple workspaces and persists stable IDs across reload", async ({
    page,
  }) => {
    await mountLoopEditor(page, sessionId);
    await page.getByTestId("loop-settings-trigger-schedule").uncheck();
    await page.getByTestId("loop-settings-trigger-onSlack").check();
    await page.getByTestId("slack-subscription-add").click();
    await page.getByTestId("slack-subscription-add").click();

    await page.getByTestId("slack-installation-0").selectOption("inst-a");
    await page.getByTestId("slack-channel-picker-open-0").click();
    const privateRow = page.getByTestId("slack-channel-picker-row-C2");
    await expect(privateRow).toContainText("Private");
    await expect(privateRow).toContainText("Joined");
    await privateRow.click();
    await page.getByTestId("slack-event-mode-0").selectOption("appMention");
    await page.getByTestId("slack-thread-policy-0").selectOption("rootOnly");

    await page.getByTestId("slack-installation-1").selectOption("inst-b");
    await page.getByTestId("slack-channel-picker-open-1").click();
    await page.getByTestId("slack-channel-picker-row-C3").click();

    const patchRequest = page.waitForRequest(
      (request) =>
        request.method() === "PATCH" &&
        new URL(request.url()).pathname.endsWith(`/sessions/${sessionId}/loop`),
    );
    await page.getByTestId("loop-save-button").click();
    const patch = (await patchRequest).postDataJSON();
    expect(patch.triggers).toEqual(["onSlack"]);
    expect(patch.slack_subscriptions).toEqual([
      {
        installation_id: "inst-a",
        channel_id: "C2",
        event_mode: "appMention",
        thread_policy: "rootOnly",
      },
      {
        installation_id: "inst-b",
        channel_id: "C3",
        event_mode: "anyHumanMessage",
        thread_policy: "any",
      },
    ]);
    await expect
      .poll(() =>
        page.evaluate(() => (window as any).__slackLoopToasts.at(-1)?.message),
      )
      .toBe("Loop settings saved");

    await mountLoopEditor(page, sessionId);
    await expect(
      page.getByTestId("loop-settings-trigger-schedule"),
    ).not.toBeChecked();
    await expect(
      page.getByTestId("loop-settings-trigger-onSlack"),
    ).toBeChecked();
    await expect(page.getByTestId("slack-subscription-row-0")).toBeVisible();
    await expect(page.getByTestId("slack-subscription-row-1")).toBeVisible();
    await expect(page.getByTestId("slack-installation-0")).toHaveValue(
      "inst-a",
    );
    await expect(page.getByTestId("slack-channel-id-0")).toHaveValue("C2");
    await expect(page.getByTestId("slack-event-mode-0")).toHaveValue(
      "appMention",
    );
    await expect(page.getByTestId("slack-thread-policy-0")).toHaveValue(
      "rootOnly",
    );
    await expect(page.getByTestId("slack-installation-1")).toHaveValue(
      "inst-b",
    );
    await expect(page.getByTestId("slack-channel-id-1")).toHaveValue("C3");
  });

  test("retains a saved ID through a channel error and retry", async ({
    page,
    request,
  }) => {
    await page.unrouteAll();
    await mockSlackCatalog(page, true);
    await createLoop(request, sessionId, {
      triggers: ["onSlack"],
      slack_subscriptions: [
        {
          installation_id: "inst-a",
          channel_id: "C404",
          event_mode: "anyHumanMessage",
          thread_policy: "any",
        },
      ],
    });
    await mountLoopEditor(page, sessionId);

    await expect(page.getByTestId("slack-channel-id-0")).toHaveValue("C404");
    await page.getByTestId("slack-channel-picker-open-0").click();
    await expect(page.getByText(/channels:read and groups:read/)).toBeVisible();
    await page.getByRole("button", { name: "Retry" }).click();
    await expect(page.getByText(/channels:read and groups:read/)).toBeHidden();
    await expect(page.getByTestId("slack-channel-id-0")).toHaveValue("C404");
    await expect(
      page.getByTestId("slack-channel-picker-row-C404"),
    ).toContainText("#saved-channel");
  });
});
