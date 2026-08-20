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
            ? [{ id: "C1", name: "general", is_member: true }]
            : [{ id: "C2", name: "announcements", is_member: true }],
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
    test("edits multiple workspaces, manual channel IDs, and preserves unknown row fields", async () => {
      const client = makeClient();
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1", { futureFilter: "keep" }),
        subscription("inst-b", "C2"),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Beta"),
          container,
          "catalog loaded",
        );
        expect(container.textContent).toContain(
          "Workspace Alpha · TA · App Alpha",
        );
        expect(container.textContent).toContain(
          "Workspace Beta · TB · App Beta",
        );
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          container,
          "channels requested for both workspaces",
        );

        for (const testId of [
          "slack-installation-0",
          "slack-event-mode-0",
          "slack-thread-policy-0",
        ]) {
          const select = container.querySelector(`[data-testid="${testId}"]`);
          expect(select.className).toContain("pr-8");
          expect(select.style.appearance).toBe("none");
          expect(
            container.querySelector(`[data-testid="${testId}-chevron"]`),
          ).not.toBeNull();
        }

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

        search(
          container.querySelector('[data-testid="slack-channel-id-0"]'),
          "C2",
        );
        await waitFor(
          () => getSubscriptions()[0].channelId === "C2",
          container,
          "manual channel id",
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

    test("replaces the inline channel dropdown/search/load-more with a Channel ID input and search button", async () => {
      const client = makeClient();
      const { container } = await mount(client, [subscription("inst-a", "C1")]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "catalog loaded",
        );
        expect(
          container.querySelector('[data-testid="slack-channel-id-0"]'),
        ).not.toBeNull();
        expect(
          container.querySelector(
            '[data-testid="slack-channel-picker-open-0"]',
          ),
        ).not.toBeNull();
        expect(
          container.querySelector('[data-testid="slack-channel-search-0"]'),
        ).toBeNull();
        expect(
          container.querySelector('[data-testid="slack-channel-0"]'),
        ).toBeNull();
        expect(
          container.querySelector('[data-testid="slack-channel-load-more-0"]'),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("typing a Channel ID updates the subscription directly", async () => {
      const client = makeClient();
      const { container, getSubscriptions } = await mount(client, [
        subscription("", ""),
      ]);
      try {
        const input = container.querySelector(
          '[data-testid="slack-channel-id-0"]',
        );
        search(input, "C555");
        await waitFor(
          () => getSubscriptions()[0].channelId === "C555",
          container,
          "manual channel id",
        );
        expect(input.value).toBe("C555");
      } finally {
        unmount(container);
      }
    });

    test("search button is disabled until a valid installation with bot credential is selected", async () => {
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => ({
          installations:
            appId === "app-a"
              ? [
                  {
                    id: "inst-a",
                    name: "Workspace Alpha",
                    team_id: "TA",
                    token_configured: false,
                  },
                ]
              : installations[appId] || [],
        })),
      });
      const { container } = await mount(client, [subscription("", "")]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "catalog loaded",
        );
        const openBtn = container.querySelector(
          '[data-testid="slack-channel-picker-open-0"]',
        );
        expect(openBtn.disabled).toBe(true);

        choose(
          container.querySelector('[data-testid="slack-installation-0"]'),
          "inst-a",
        );
        await waitFor(
          () =>
            container.querySelector('[data-testid="slack-installation-0"]')
              .value === "inst-a",
          container,
          "workspace selected",
        );
        expect(openBtn.disabled).toBe(true);
      } finally {
        unmount(container);
      }
    });

    test("delegated-user mode keeps the shared picker and normalizes a stale mention draft without losing IDs", async () => {
      const delegated = {
        id: "inst-user",
        name: "Workspace User",
        team_id: "TU",
        credential_kind: "user",
        user_id: "U123",
        token_configured: true,
      };
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => ({
          installations: appId === "app-a" ? [delegated] : [],
        })),
        listChannels: jest.fn(async () => ({
          channels: [
            {
              id: "G1",
              name: "private-user",
              is_private: true,
              is_member: true,
            },
          ],
          next_cursor: "",
        })),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-user", "G1", {
          eventMode: "appMention",
          futureFilter: "keep",
        }),
      ]);
      try {
        await waitFor(
          () => getSubscriptions()[0].eventMode === "anyHumanMessage",
          container,
          "stale mention mode normalized",
        );
        expect(getSubscriptions()[0]).toEqual(
          expect.objectContaining({
            installationId: "inst-user",
            channelId: "G1",
            eventMode: "anyHumanMessage",
            futureFilter: "keep",
          }),
        );
        const mode = container.querySelector(
          '[data-testid="slack-credential-mode-0"]',
        );
        expect(mode.getAttribute("role")).toBe("status");
        expect(mode.textContent).toContain("Delegated user");
        expect(
          mode.querySelector(".badge").classList.contains("badge-info"),
        ).toBe(true);
        expect(mode.textContent).toContain("authorizing user's membership");
        expect(mode.textContent).toContain("stale mention-only drafts reset");
        expect(
          container.querySelector(
            '[data-testid="slack-event-mode-0"] option[value="appMention"]',
          ),
        ).toBeNull();

        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-G1"]',
            ),
          document.body,
          "delegated-user channel picker",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-G1"]')
            .textContent,
        ).toContain("PrivateMember");
      } finally {
        unmount(container);
      }
    });

    test("switching from bot to delegated user clears the channel and mention mode but preserves other draft fields", async () => {
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => ({
          installations:
            appId === "app-a"
              ? [
                  { ...installations["app-a"][0], credential_kind: "bot" },
                  {
                    id: "inst-user",
                    name: "Workspace User",
                    team_id: "TU",
                    credential_kind: "user",
                    token_configured: true,
                  },
                ]
              : [],
        })),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1", {
          eventMode: "appMention",
          threadPolicy: "rootOnly",
          futureFilter: "keep",
        }),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace User"),
          container,
          "delegated-user workspace",
        );
        choose(
          container.querySelector('[data-testid="slack-installation-0"]'),
          "inst-user",
        );
        await waitFor(
          () => getSubscriptions()[0].installationId === "inst-user",
          container,
          "workspace mode switch",
        );
        expect(getSubscriptions()[0]).toEqual(
          expect.objectContaining({
            installationId: "inst-user",
            channelId: "",
            eventMode: "anyHumanMessage",
            threadPolicy: "rootOnly",
            futureFilter: "keep",
          }),
        );
      } finally {
        unmount(container);
      }
    });

    test("clicking search opens the picker modal without mutating the draft", async () => {
      const client = makeClient();
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1"),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "catalog loaded",
        );
        const before = getSubscriptions();
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-modal"]',
            ),
          document.body,
          "picker modal",
        );
        const modal = document.querySelector(
          '[data-testid="slack-channel-picker-modal"]',
        );
        expect(document.body.contains(modal)).toBe(true);
        expect(container.contains(modal)).toBe(false);
        expect(
          container
            .querySelector('[data-testid="slack-subscription-editor"]')
            .contains(modal),
        ).toBe(false);
        expect(modal.textContent).toContain("Select a Slack channel");
        expect(modal.classList.contains("max-h-[80vh]")).toBe(true);
        expect(
          modal.querySelector(".flex-1.min-h-0.overflow-y-auto"),
        ).not.toBeNull();
        expect(getSubscriptions()).toEqual(before);
      } finally {
        unmount(container);
      }
    });

    test("modal search filters loaded channels by name or ID, case-insensitively, and shows the result count", async () => {
      const client = makeClient({
        listChannels: jest.fn(async () => ({
          channels: [
            { id: "C1", name: "general" },
            { id: "C2", name: "random" },
          ],
          next_cursor: "",
        })),
      });

      const { container } = await mount(client, [subscription("inst-a", "")]);
      try {
        await waitFor(
          () =>
            !container.querySelector(
              '[data-testid="slack-channel-picker-open-0"]',
            ).disabled,
          container,
          "catalog loaded",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          document.body,
          "channels loaded in modal",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-count"]')
            .textContent,
        ).toContain("Showing 2 of 2");

        search(
          document.querySelector('[data-testid="slack-channel-picker-search"]'),
          "GEN",
        );
        await waitFor(
          () =>
            document
              .querySelector('[data-testid="slack-channel-picker-count"]')
              .textContent.includes("Showing 1 of 2"),
          document.body,
          "name filter",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C1"]'),
        ).not.toBeNull();
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C2"]'),
        ).toBeNull();

        search(
          document.querySelector('[data-testid="slack-channel-picker-search"]'),
          "c2",
        );
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "id filter",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C1"]'),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("labels and searches private/member state and warns when the bot is not joined", async () => {
      const client = makeClient({
        listChannels: jest.fn(async () => ({
          channels: [
            { id: "C1", name: "general", is_member: true },
            {
              id: "G1",
              name: "private-ops",
              is_private: true,
              is_member: true,
            },
            { id: "C2", name: "public-unjoined", is_member: false },
          ],
          next_cursor: "",
        })),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "G1"),
      ]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 1,
          container,
          "private channels loaded",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-G1"]',
            ),
          document.body,
          "private row",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-G1"]')
            .textContent,
        ).toContain("PrivateJoined");
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C2"]')
            .textContent,
        ).toContain("PublicNot joined");

        search(
          document.querySelector('[data-testid="slack-channel-picker-search"]'),
          "private",
        );
        await waitFor(
          () =>
            !document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          document.body,
          "privacy filter",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-G1"]'),
        ).not.toBeNull();

        search(
          document.querySelector('[data-testid="slack-channel-picker-search"]'),
          "not joined",
        );
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "membership filter",
        );
        document
          .querySelector('[data-testid="slack-channel-picker-row-C2"]')
          .click();
        await waitFor(
          () => getSubscriptions()[0].channelId === "C2",
          container,
          "non-member selection",
        );
        expect(
          container.querySelector(
            '[data-testid="slack-channel-invite-guidance-0"]',
          ).textContent,
        ).toContain("Invite the bot");
      } finally {
        unmount(container);
      }
    });

    test("selecting a channel row sets the correct subscription's channelId and closes the modal", async () => {
      const client = makeClient({
        listChannels: jest.fn(async (installationId) => ({
          channels:
            installationId === "inst-a"
              ? [{ id: "C1", name: "general" }]
              : [{ id: "C2", name: "announcements" }],
          next_cursor: "",
        })),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", ""),
        subscription("inst-b", ""),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Beta"),
          container,
          "catalog loaded",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-1"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "row for second subscription",
        );
        document
          .querySelector('[data-testid="slack-channel-picker-row-C2"]')
          .click();
        await waitFor(
          () => getSubscriptions()[1].channelId === "C2",
          container,
          "channel selected",
        );
        expect(getSubscriptions()[0].channelId).toBe("");
        expect(
          document.querySelector('[data-testid="slack-channel-picker-modal"]'),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("automatically fetches every page in the background without a Load more button, and filtering applies across all loaded pages", async () => {
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, params) =>
          params.cursor
            ? { channels: [{ id: "C9", name: "ops-team" }], next_cursor: "" }
            : {
                channels: [{ id: "C1", name: "general" }],
                next_cursor: "page-2",
              },
        ),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      try {
        // Background pagination starts as soon as the installation is
        // referenced by a row — no need to open the picker first.
        await waitFor(
          () => client.slack.listChannels.mock.calls.length >= 2,
          container,
          "both pages fetched in the background",
        );
        expect(client.slack.listChannels.mock.calls[0][1]).toEqual(
          expect.objectContaining({ cursor: "" }),
        );
        expect(client.slack.listChannels.mock.calls[1][1]).toEqual(
          expect.objectContaining({ cursor: "page-2" }),
        );

        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C9"]',
            ),
          document.body,
          "both pages already merged when the modal opens",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C1"]'),
        ).not.toBeNull();
        expect(
          document.querySelector(
            '[data-testid="slack-channel-picker-load-more"]',
          ),
        ).toBeNull();

        search(
          document.querySelector('[data-testid="slack-channel-picker-search"]'),
          "ops",
        );
        await waitFor(
          () =>
            !document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          document.body,
          "filter narrows across all loaded pages",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C9"]'),
        ).not.toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("editing the draft channel ID does not abort or restart pagination for the same installation", async () => {
      let resolveSecondPage;
      let secondPageFinished = false;
      const secondPage = new Promise((resolve) => {
        resolveSecondPage = resolve;
      });
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, params) => {
          if (!params.cursor) {
            return {
              channels: [{ id: "C1", name: "general" }],
              next_cursor: "page-2",
            };
          }
          const page = await secondPage;
          secondPageFinished = true;
          return page;
        }),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          container,
          "second page request",
        );
        const secondPageSignal =
          client.slack.listChannels.mock.calls[1][2].signal;

        search(
          container.querySelector('[data-testid="slack-channel-id-0"]'),
          "C-manual",
        );
        await new Promise((resolve) => setTimeout(resolve, 10));

        expect(secondPageSignal.aborted).toBe(false);
        expect(client.slack.listChannels.mock.calls.length).toBe(2);
        resolveSecondPage({
          channels: [{ id: "C2", name: "operations" }],
          next_cursor: "",
        });
        await waitFor(
          () => secondPageFinished,
          container,
          "pagination completes after draft edit",
        );
        expect(client.slack.listChannels.mock.calls.length).toBe(2);
      } finally {
        unmount(container);
      }
    });

    test("channel cache survives a remount (no cursor restart) and is reused by a second mounted editor", async () => {
      const client = makeClient({
        listChannels: jest.fn(async () => ({
          channels: [{ id: "C1", name: "general" }],
          next_cursor: "",
        })),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      await waitFor(
        () => client.slack.listChannels.mock.calls.length === 1,
        container,
        "initial background fetch",
      );
      unmount(container);

      // Remounting (e.g. closing/reopening the Loop Properties panel) must
      // not refetch already-complete channel data.
      const { container: container2 } = await mount(client, [
        subscription("inst-a", ""),
      ]);
      try {
        await waitFor(
          () =>
            !container2.querySelector(
              '[data-testid="slack-channel-picker-open-0"]',
            ).disabled,
          container2,
          "catalog reloaded after remount",
        );
        container2
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          document.body,
          "cached channel visible immediately on remount",
        );
        expect(client.slack.listChannels.mock.calls.length).toBe(1);
      } finally {
        unmount(container2);
      }
    });

    test("requests the maximum page size (200) to minimize round-trips", async () => {
      const client = makeClient();
      const { container } = await mount(client, [subscription("inst-a", "")]);
      await waitFor(
        () => client.slack.listChannels.mock.calls.length === 1,
        container,
        "background fetch",
      );
      unmount(container);
      expect(client.slack.listChannels.mock.calls[0][1]).toEqual(
        expect.objectContaining({ limit: 200 }),
      );
    });

    test("two mounted editors sharing the same client dedup into a single fetch for the same installation", async () => {
      const client = makeClient({
        listChannels: jest.fn(async () => ({
          channels: [{ id: "C1", name: "general" }],
          next_cursor: "",
        })),
      });
      const { container: containerA } = await mount(client, [
        subscription("inst-a", ""),
      ]);
      const { container: containerB } = await mount(client, [
        subscription("inst-a", ""),
      ]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length >= 1,
          containerA,
          "at least one fetch fires",
        );
        // Give any (incorrect) second fetch a chance to fire before asserting.
        await new Promise((resolve) => setTimeout(resolve, 20));
        // One background fetch shared by both mounted instances (same
        // client -> same cache entry -> same in-flight AbortController), not
        // one per mounted component.
        expect(client.slack.listChannels.mock.calls.length).toBe(1);
      } finally {
        unmount(containerA);
        unmount(containerB);
      }
    });

    test("a complete cache entry is reused for 24 hours, then refreshed in the background", async () => {
      const nowSpy = jest.spyOn(Date, "now");
      try {
        let time = 1_000_000;
        nowSpy.mockImplementation(() => time);
        let channelVersion = 1;
        const client = makeClient({
          listChannels: jest.fn(async () => ({
            channels:
              channelVersion === 1
                ? [{ id: "C1", name: "general" }]
                : [{ id: "C2", name: "operations" }],
            next_cursor: "",
          })),
        });
        const { container } = await mount(client, [subscription("inst-a", "")]);
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 1,
          container,
          "initial background fetch",
        );
        unmount(container);

        // Still within the TTL: a remount must not refetch.
        time += 12 * 60 * 60 * 1000;
        const { container: freshContainer } = await mount(client, [
          subscription("inst-a", ""),
        ]);
        await new Promise((resolve) => setTimeout(resolve, 10));
        expect(client.slack.listChannels.mock.calls.length).toBe(1);
        unmount(freshContainer);

        // Past the TTL: a remount refreshes in the background.
        time += 13 * 60 * 60 * 1000; // total 25h > 24h TTL
        channelVersion = 2;
        const { container: staleContainer } = await mount(client, [
          subscription("inst-a", ""),
        ]);
        try {
          await waitFor(
            () => client.slack.listChannels.mock.calls.length === 2,
            staleContainer,
            "stale entry triggers a background refresh",
          );
          staleContainer
            .querySelector('[data-testid="slack-channel-picker-open-0"]')
            .click();
          await waitFor(
            () =>
              document.querySelector(
                '[data-testid="slack-channel-picker-row-C2"]',
              ),
            document.body,
            "refreshed channel visible",
          );
          expect(
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          ).toBeNull();
        } finally {
          unmount(staleContainer);
        }
      } finally {
        nowSpy.mockRestore();
      }
    });

    test("a no-match search revalidates a cache older than five minutes", async () => {
      const nowSpy = jest.spyOn(Date, "now");
      try {
        let time = 1_000_000;
        nowSpy.mockImplementation(() => time);
        let channelVersion = 1;
        const client = makeClient({
          listChannels: jest.fn(async () => ({
            channels:
              channelVersion === 1
                ? [{ id: "C1", name: "general" }]
                : [{ id: "C2", name: "new-operations" }],
            next_cursor: "",
          })),
        });
        const { container } = await mount(client, [subscription("inst-a", "")]);
        try {
          await waitFor(
            () => client.slack.listChannels.mock.calls.length === 1,
            container,
            "initial channel load",
          );
          container
            .querySelector('[data-testid="slack-channel-picker-open-0"]')
            .click();
          await waitFor(
            () =>
              document.querySelector(
                '[data-testid="slack-channel-picker-search"]',
              ),
            document.body,
            "picker open",
          );

          time += 6 * 60 * 1000;
          channelVersion = 2;
          search(
            document.querySelector(
              '[data-testid="slack-channel-picker-search"]',
            ),
            "new-operations",
          );

          await waitFor(
            () => client.slack.listChannels.mock.calls.length === 2,
            document.body,
            "search miss refresh",
          );
          await waitFor(
            () =>
              document.querySelector(
                '[data-testid="slack-channel-picker-row-C2"]',
              ),
            document.body,
            "new channel appears after revalidation",
          );
        } finally {
          unmount(container);
        }
      } finally {
        nowSpy.mockRestore();
      }
    });

    test("the picker Refresh action forces revalidation of a fresh cache", async () => {
      let channelVersion = 1;
      const client = makeClient({
        listChannels: jest.fn(async () => ({
          channels:
            channelVersion === 1
              ? [{ id: "C1", name: "general" }]
              : [{ id: "C2", name: "operations" }],
          next_cursor: "",
        })),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 1,
          container,
          "initial channel load",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-refresh"]',
            ),
          document.body,
          "picker refresh action",
        );

        channelVersion = 2;
        document
          .querySelector('[data-testid="slack-channel-picker-refresh"]')
          .click();
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          document.body,
          "forced refresh",
        );
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "refreshed channel visible",
        );
      } finally {
        unmount(container);
      }
    });

    test("closing the picker modal via ✕, backdrop, or Escape all leave the channelId unchanged", async () => {
      const client = makeClient();
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1"),
      ]);
      const openModal = async () => {
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-modal"]',
            ),
          document.body,
          "modal open",
        );
      };
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "catalog loaded",
        );

        await openModal();
        document
          .querySelector('[data-testid="slack-channel-picker-close"]')
          .click();
        await waitFor(
          () =>
            !document.querySelector(
              '[data-testid="slack-channel-picker-modal"]',
            ),
          document.body,
          "closed via close button",
        );
        expect(getSubscriptions()[0].channelId).toBe("C1");

        await openModal();
        document
          .querySelector('[data-testid="slack-channel-picker-backdrop"]')
          .click();
        await waitFor(
          () =>
            !document.querySelector(
              '[data-testid="slack-channel-picker-modal"]',
            ),
          document.body,
          "closed via backdrop",
        );
        expect(getSubscriptions()[0].channelId).toBe("C1");

        await openModal();
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
        await waitFor(
          () =>
            !document.querySelector(
              '[data-testid="slack-channel-picker-modal"]',
            ),
          document.body,
          "closed via Escape",
        );
        expect(getSubscriptions()[0].channelId).toBe("C1");
      } finally {
        unmount(container);
      }
    });

    test("manual channel IDs remain silent during discovery and after a complete miss", async () => {
      let resolveSecondPage;
      let secondPageFinished = false;
      const secondPage = new Promise((resolve) => {
        resolveSecondPage = resolve;
      });
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, params) => {
          if (params.cursor) {
            await secondPage;
            secondPageFinished = true;
            return {
              channels: [{ id: "C9", name: "operations" }],
              next_cursor: "",
            };
          }
          return {
            channels: [{ id: "C1", name: "general" }],
            next_cursor: "page-2",
          };
        }),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C404"),
      ]);
      try {
        // mitto-hcwd: catalog discovery enriches a manual ID; it does not
        // validate it or render absence-based status beside the field.
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          container,
          "second page pending",
        );
        expect(container.textContent).not.toContain(
          "Still checking loaded channels",
        );
        expect(container.textContent).not.toContain(
          "saved channel is not visible",
        );

        resolveSecondPage();
        await waitFor(
          () => secondPageFinished,
          container,
          "complete channel miss",
        );
        expect(container.textContent).not.toContain(
          "Still checking loaded channels",
        );
        expect(container.textContent).not.toContain(
          "saved channel is not visible",
        );
        expect(getSubscriptions()[0].channelId).toBe("C404");
      } finally {
        unmount(container);
      }
    });

    test("channel load errors surface inside the modal with a Retry action that keeps the draft intact", async () => {
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
        subscription("inst-a", "C404"),
      ]);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "catalog loaded",
        );
        expect(container.textContent).toContain(
          "Some Slack workspace installations could not be loaded",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.body.textContent.includes("channels:read and groups:read"),
          document.body,
          "channel error in modal",
        );
        expect(container.textContent).not.toContain(
          "Still checking loaded channels",
        );
        expect(container.textContent).not.toContain(
          "saved channel is not visible",
        );
        expect(getSubscriptions()[0]).toEqual(subscription("inst-a", "C404"));
        [...document.querySelectorAll("button")]
          .find((button) => button.textContent.trim() === "Retry")
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C1"]',
            ),
          document.body,
          "successful retry",
        );
        expect(document.body.textContent).not.toContain(
          "channels:read and groups:read",
        );
        expect(getSubscriptions()[0]).toEqual(subscription("inst-a", "C404"));
      } finally {
        unmount(container);
      }
    });

    test("rate-limit exhaustion keeps partial channels and resumes the failed cursor without scope guidance", async () => {
      let pageTwoAttempts = 0;
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, params) => {
          if (!params.cursor) {
            return {
              channels: [{ id: "C1", name: "general" }],
              next_cursor: "page-2",
            };
          }
          pageTwoAttempts += 1;
          if (pageTwoAttempts === 1) {
            throw Object.assign(new Error("rate limited"), {
              code: "rate_limited",
            });
          }
          return {
            channels: [
              { id: "C1", name: "general" },
              { id: "C2", name: "operations" },
            ],
            next_cursor: "",
          };
        }),
      });
      const { container, getSubscriptions } = await mount(client, [
        subscription("inst-a", "C1"),
      ]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          container,
          "rate-limited second page",
        );
        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () => document.body.textContent.includes("still rate limiting"),
          document.body,
          "rate-limit guidance",
        );
        expect(document.body.textContent).not.toContain(
          "channels:read and groups:read",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C1"]'),
        ).not.toBeNull();

        document
          .querySelector('[data-testid="slack-channel-picker-retry"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "resumed second page",
        );
        expect(
          client.slack.listChannels.mock.calls.map((call) => call[1].cursor),
        ).toEqual(["", "page-2", "page-2"]);
        expect(
          document.querySelectorAll(
            '[data-testid="slack-channel-picker-row-C1"]',
          ).length,
        ).toBe(1);
        expect(getSubscriptions()[0]).toEqual(subscription("inst-a", "C1"));
      } finally {
        unmount(container);
      }
    });

    test("credential replacement refresh preserves IDs and safely normalizes a stale mention draft", async () => {
      // mitto-xh79: the "Manage Slack integrations" action moved out of this
      // component into the Loop Properties TriggerSection card header, so
      // this editor no longer owns/dispatches mitto:open_settings itself —
      // see LoopSettingsTab.test.js for that behavior.
      let teamName = "Workspace Alpha";
      let credentialKind = "bot";
      const client = makeClient({
        listInstallations: jest.fn(async (appId) => ({
          installations: (installations[appId] || []).map((item) => ({
            ...item,
            name: appId === "app-a" ? teamName : item.name,
            credential_kind: appId === "app-a" ? credentialKind : "bot",
          })),
        })),
      });
      const initial = [
        subscription("inst-a", "C1", {
          eventMode: "appMention",
          future: "keep",
        }),
      ];
      const { container, getSubscriptions } = await mount(client, initial);
      try {
        await waitFor(
          () => container.textContent.includes("Workspace Alpha"),
          container,
          "initial catalog",
        );
        expect(
          container.querySelector('[data-testid="slack-manage-integrations"]'),
        ).toBeNull();

        teamName = "Workspace Renamed";
        credentialKind = "user";
        window.dispatchEvent(
          new CustomEvent("mitto:slack_integrations_updated"),
        );
        await waitFor(
          () => container.textContent.includes("Workspace Renamed"),
          container,
          "catalog refresh",
        );
        expect(client.slack.listApps.mock.calls.length).toBeGreaterThan(1);
        await waitFor(
          () => getSubscriptions()[0].eventMode === "anyHumanMessage",
          container,
          "credential mode normalization",
        );
        expect(getSubscriptions()).toEqual([
          subscription("inst-a", "C1", {
            eventMode: "anyHumanMessage",
            future: "keep",
          }),
        ]);
      } finally {
        unmount(container);
      }
    });

    test("mitto:slack_integrations_updated invalidates the channel cache and re-fetches from scratch", async () => {
      let resolveStaleRequest;
      let calls = 0;
      const client = makeClient({
        listChannels: jest.fn(async () => {
          calls += 1;
          if (calls === 1) {
            return new Promise((resolve) => {
              resolveStaleRequest = () =>
                resolve({
                  channels: [{ id: "C1", name: "stale" }],
                  next_cursor: "",
                });
            });
          }
          return {
            channels: [{ id: "C2", name: "fresh" }],
            next_cursor: "",
          };
        }),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      try {
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 1,
          container,
          "initial background fetch",
        );

        window.dispatchEvent(
          new CustomEvent("mitto:slack_integrations_updated"),
        );
        await waitFor(
          () => client.slack.listChannels.mock.calls.length === 2,
          container,
          "channel cache invalidated and re-fetched",
        );
        // A fresh fetch starts from an empty cursor, not the (discarded)
        // completed page's cursor.
        expect(client.slack.listChannels.mock.calls[1][1]).toEqual(
          expect.objectContaining({ cursor: "" }),
        );
        resolveStaleRequest();
        await new Promise((resolve) => setTimeout(resolve, 10));

        container
          .querySelector('[data-testid="slack-channel-picker-open-0"]')
          .click();
        await waitFor(
          () =>
            document.querySelector(
              '[data-testid="slack-channel-picker-row-C2"]',
            ),
          document.body,
          "fresh channel retained",
        );
        expect(
          document.querySelector('[data-testid="slack-channel-picker-row-C1"]'),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("aborts the in-flight background fetch when the only interested row/component goes away, without throwing", async () => {
      let releaseFirstPage;
      const firstPage = new Promise((resolve) => {
        releaseFirstPage = resolve;
      });
      const client = makeClient({
        listChannels: jest.fn(async (_installationId, _params, opts) => {
          await firstPage;
          if (opts?.signal?.aborted) {
            const error = new Error("aborted");
            error.name = "AbortError";
            throw error;
          }
          return { channels: [{ id: "C1", name: "general" }], next_cursor: "" };
        }),
      });
      const { container } = await mount(client, [subscription("inst-a", "")]);
      await waitFor(
        () => client.slack.listChannels.mock.calls.length === 1,
        container,
        "background fetch started",
      );
      // Unmounting is the only interested consumer going away; the pending
      // fetch's AbortSignal must fire and the rejection must not escape as
      // an unhandled error.
      unmount(container);
      releaseFirstPage();
      await new Promise((resolve) => setTimeout(resolve, 10));
      const [, , opts] = client.slack.listChannels.mock.calls[0];
      expect(opts?.signal?.aborted).toBe(true);
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
