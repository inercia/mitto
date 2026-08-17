import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, jest, test } from "../utils/testing/testGlobals.js";
import * as preact from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";

const previousPreact = window.preact;
window.preact = { ...preact, ...hooks, html: htm.bind(preact.h) };
const { SlackSubscriptionEditor } =
  await import("./SlackSubscriptionEditor.js?mitto-37nx-7-tests");
window.preact = previousPreact;

const childRun = process.env.MITTO_SLACK_SUBSCRIPTION_TEST_CHILD === "1";
const apps = [
  { id: "app-a", name: "App Alpha", token_configured: true },
  { id: "app-b", name: "App Beta", token_configured: true },
];
const installations = {
  "app-a": [
    {
      id: "inst-a",
      name: "Workspace Alpha",
      team_id: "TA",
      token_configured: true,
    },
  ],
  "app-b": [
    {
      id: "inst-b",
      name: "Workspace Beta",
      team_id: "TB",
      token_configured: true,
    },
  ],
};

function subscription(installationId, channelId, extra = {}) {
  return {
    installationId,
    channelId,
    eventMode: "anyHumanMessage",
    threadPolicy: "any",
    ...extra,
  };
}

function makeClient(overrides = {}) {
  return {
    slack: {
      listApps: jest.fn(async () => ({ apps })),
      listInstallations: jest.fn(async (appId) => ({
        installations: installations[appId] || [],
      })),
      listChannels: jest.fn(async (installationId) => ({
        channels:
          installationId === "inst-a"
            ? [{ id: "C1", name: "general" }]
            : [{ id: "C2", name: "announcements" }],
        next_cursor: "",
      })),
      ...overrides,
    },
  };
}

async function waitFor(predicate, container, message = "condition") {
  for (let attempt = 0; attempt < 150; attempt++) {
    if (predicate()) return;
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
  throw new Error(`Timed out waiting for ${message}: ${container.innerHTML}`);
}

async function mount(client, initialSubscriptions, fieldErrors = {}) {
  let latest = initialSubscriptions;
  const container = document.createElement("div");
  document.body.appendChild(container);
  function Harness() {
    const [subscriptions, setSubscriptions] =
      hooks.useState(initialSubscriptions);
    latest = subscriptions;
    return preact.h(SlackSubscriptionEditor, {
      subscriptions,
      onChange: setSubscriptions,
      fieldErrors,
      client,
    });
  }
  preact.render(preact.h(Harness), container);
  await waitFor(
    () => container.querySelector('[data-testid="slack-subscription-editor"]'),
    container,
    "Slack subscription editor",
  );
  return { container, getSubscriptions: () => latest };
}

function unmount(container) {
  preact.render(null, container);
  container.remove();
}

function choose(element, value) {
  element.value = value;
  element.dispatchEvent(new Event("change", { bubbles: true }));
}

function search(element, value) {
  element.value = value;
  element.dispatchEvent(new Event("input", { bubbles: true }));
}

if (childRun) {
  describe("SlackSubscriptionEditor mounted behavior", () => {
    test("edits multiple workspaces and preserves unknown row fields", async () => {
      const client = makeClient();
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1", { futureFilter: "keep" }),
        subscription("inst-b", "C2"),
      ]);
      try {
        await waitFor(
          () =>
            container.textContent.includes("#general · C1") &&
            container.textContent.includes("#announcements · C2"),
          container,
          "channels from both workspaces",
        );
        expect(container.textContent).toContain(
          "Workspace Alpha · TA · App Alpha",
        );
        expect(container.textContent).toContain(
          "Workspace Beta · TB · App Beta",
        );
        expect(client.slack.listChannels).toHaveBeenCalledTimes(2);

        choose(
          container.querySelector('[data-testid="slack-installation-0"]'),
          "inst-b",
        );
        await waitFor(
          () => getSubscriptions()[0].installationId === "inst-b",
          container,
          "workspace switch",
        );
        expect(getSubscriptions()[0]).toEqual(
          expect.objectContaining({
            channelId: "",
            futureFilter: "keep",
          }),
        );
        choose(
          container.querySelector('[data-testid="slack-channel-0"]'),
          "C2",
        );
        await waitFor(
          () => getSubscriptions()[0].channelId === "C2",
          container,
          "channel selection",
        );
        choose(
          container.querySelector('[data-testid="slack-event-mode-0"]'),
          "appMention",
        );
        await waitFor(
          () => getSubscriptions()[0].eventMode === "appMention",
          container,
          "event mode",
        );
        choose(
          container.querySelector('[data-testid="slack-thread-policy-0"]'),
          "repliesOnly",
        );
        await waitFor(
          () => getSubscriptions()[0].threadPolicy === "repliesOnly",
          container,
          "thread policy",
        );

        container
          .querySelector('[data-testid="slack-subscription-add"]')
          .click();
        await waitFor(
          () => getSubscriptions().length === 3,
          container,
          "third subscription",
        );
        expect(getSubscriptions()[0]).toEqual(
          expect.objectContaining({
            installationId: "inst-b",
            channelId: "C2",
            eventMode: "appMention",
            threadPolicy: "repliesOnly",
            futureFilter: "keep",
          }),
        );
        expect(getSubscriptions()[2]).toEqual({
          installationId: "",
          channelId: "",
          eventMode: "anyHumanMessage",
          threadPolicy: "any",
        });

        container
          .querySelector('[data-testid="slack-subscription-remove-1"]')
          .click();
        await waitFor(
          () => getSubscriptions().length === 2,
          container,
          "row removal",
        );
      } finally {
        unmount(container);
      }
    });

    test("searches accumulated pages and retains a missing saved channel", async () => {
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, params) =>
          params.cursor
            ? {
                channels: [{ id: "C9", name: "operations" }],
                next_cursor: "",
              }
            : {
                channels: [{ id: "C1", name: "general" }],
                next_cursor: "page-2",
              },
        ),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C404"),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Load more channels"),
          container,
          "first channel page",
        );
        expect(container.textContent).toContain("not in the loaded pages yet");
        container
          .querySelector('[data-testid="slack-channel-load-more-0"]')
          .click();
        await waitFor(
          () =>
            container.textContent.includes("saved channel could not be found"),
          container,
          "missing channel state",
        );
        const channel = container.querySelector(
          '[data-testid="slack-channel-0"]',
        );
        expect(channel.querySelector('option[value="C404"]')).not.toBeNull();
        expect(channel.textContent).toContain("#operations · C9");

        search(
          container.querySelector('[data-testid="slack-channel-search-0"]'),
          "C9",
        );
        await waitFor(
          () => !channel.textContent.includes("#general · C1"),
          container,
          "filtered channel list",
        );
        expect(channel.textContent).toContain("#operations · C9");
        expect(channel.querySelector('option[value="C404"]')).not.toBeNull();
        expect(getSubscriptions()[0].channelId).toBe("C404");
      } finally {
        unmount(container);
      }
    });

    test("keeps the draft through picker errors and retries successfully", async () => {
      let attempts = 0;
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => {
          if (appId === "app-b") throw new Error("catalog unavailable");
          return {
            installations: installations[appId].map((item) => ({ ...item })),
          };
        }),
        listChannels: jest.fn(async () => {
          attempts += 1;
          if (attempts === 1) throw new Error("missing scope");
          return { channels: [{ id: "C1", name: "general" }], next_cursor: "" };
        }),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1"),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Check channels:read and retry"),
          container,
          "channel error",
        );
        expect(container.textContent).toContain(
          "Some Slack workspace installations could not be loaded",
        );
        expect(getSubscriptions()[0]).toEqual(subscription("inst-a", "C1"));
        [...container.querySelectorAll("button")]
          .find((button) => button.textContent.trim() === "Retry")
          .click();
        await waitFor(
          () => container.textContent.includes("#general · C1"),
          container,
          "successful retry",
        );
        expect(container.textContent).not.toContain(
          "Check channels:read and retry",
        );
        expect(getSubscriptions()[0]).toEqual(subscription("inst-a", "C1"));
      } finally {
        unmount(container);
      }
    });

    test("opens value-free Slack settings and refreshes names without draft loss", async () => {
      let teamName = "Workspace Alpha";
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => ({
          installations: (installations[appId] || []).map((item) => ({
            ...item,
            name: appId === "app-a" ? teamName : item.name,
          })),
        })),
      });
      const initial = [subscription("inst-a", "C1", { future: "keep" })];
      const { container, getSubscriptions } = await mount(client, initial);
      let detail;
      const onOpen = (event) => {
        detail = event.detail;
      };
      window.addEventListener("mitto:open_settings", onOpen);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "initial catalog",
        );
        container
          .querySelector('[data-testid="slack-manage-integrations"]')
          .click();
        expect(detail).toEqual({ tab: "slack" });
        expect(JSON.stringify(detail)).not.toMatch(/token|credential|channel/i);

        teamName = "Workspace Renamed";
        window.dispatchEvent(
          new CustomEvent("mitto:slack_integrations_updated"),
        );
        await waitFor(
          () => container.textContent.includes("Workspace Renamed"),
          container,
          "catalog refresh",
        );
        expect(client.slack.listApps.mock.calls.length).toBeGreaterThan(1);
        expect(getSubscriptions()).toEqual(initial);
      } finally {
        window.removeEventListener("mitto:open_settings", onOpen);
        unmount(container);
      }
    });
  });
} else {
  describe("SlackSubscriptionEditor", () => {
    test("passes mounted behavior tests in an isolated happy-dom process", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_SLACK_SUBSCRIPTION_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Slack subscription tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
