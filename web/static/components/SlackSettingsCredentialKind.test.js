import { describe, expect, jest, test } from "../utils/testing/testGlobals.js";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import * as preact from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";
import { createClient } from "../sdk/index.js";

const isIsolatedRun = process.env.MITTO_SLACK_CREDENTIAL_TEST_CHILD === "1";
let html;
let SlackSettingsTab;
let slackCredentialKind;
if (isIsolatedRun) {
  html = htm.bind(preact.h);
  const previousPreact = window.preact;
  window.preact = { ...preact, ...hooks, html };
  window.mittoApiPrefix = "";
  ({ SlackSettingsTab, slackCredentialKind } =
    await import("./SlackSettingsTab.js?mitto-3od5-1-tests"));
  window.preact = previousPreact;
}

function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

async function waitFor(predicate, container, message) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
  throw new Error(`Timed out waiting for ${message}: ${container.innerHTML}`);
}

function inputValue(input, value) {
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

if (isIsolatedRun) {
  describe("Slack installation credential modes", () => {
    test("maps delegated-user and legacy installation metadata to safe labels", () => {
      expect(slackCredentialKind({ credential_kind: "user" })).toEqual({
        label: "Delegated user",
        className: "badge-info",
      });
      expect(slackCredentialKind({ credential_kind: "bot" })).toEqual({
        label: "Bot",
        className: "badge-primary",
      });
      expect(slackCredentialKind({})).toEqual({
        label: "Bot",
        className: "badge-primary",
      });
    });

    test("renders delegated-user identity and submits only the generic token field", async () => {
      const app = {
        id: "app-a",
        name: "App",
        slack_app_id: "A111",
        token_configured: true,
      };
      const installation = {
        id: "inst-user",
        app_id: "app-a",
        name: "User Team",
        credential_kind: "user",
        team_id: "T111",
        team_name: "Example",
        user_id: "U222",
        token_configured: true,
      };
      let createBody;
      const fetchMock = jest.fn(async (url, init = {}) => {
        if (url === "/api/slack/apps") return json({ apps: [app] });
        if (url === "/api/slack/environment-import")
          return json({ present: false });
        if (
          url === "/api/slack/apps/app-a/installations" &&
          (init.method || "GET") === "GET"
        )
          return json({ installations: [installation] });
        if (
          url === "/api/slack/apps/app-a/installations" &&
          init.method === "POST"
        ) {
          createBody = JSON.parse(init.body);
          return json(
            {
              ...installation,
              id: "inst-bot",
              name: createBody.name,
              credential_kind: "bot",
              bot_id: "B333",
              bot_user_id: "U333",
              user_id: undefined,
            },
            201,
          );
        }
        throw new Error(`Unexpected request: ${init.method || "GET"} ${url}`);
      });
      const client = createClient({ fetch: fetchMock });
      const container = document.createElement("div");
      document.body.appendChild(container);
      preact.render(
        html`<${SlackSettingsTab} client=${client} showToast=${jest.fn()} />`,
        container,
      );
      try {
        await waitFor(
          () =>
            container.querySelector('[data-testid="slack-credential-kind"]'),
          container,
          "credential mode",
        );
        expect(
          container.querySelector('[data-testid="slack-credential-kind"]')
            .textContent,
        ).toBe("Delegated user");
        expect(
          container
            .querySelector('[data-testid="slack-credential-kind"]')
            .classList.contains("badge-info"),
        ).toBe(true);
        expect(container.textContent).toContain("User: U222");

        container
          .querySelector('[data-testid="slack-add-installation"]')
          .click();
        await waitFor(
          () =>
            container.querySelector(
              '[data-testid="slack-new-installation-form"]',
            ),
          container,
          "new installation form",
        );
        const inputs = container.querySelectorAll(
          '[data-testid="slack-new-installation-form"] input',
        );
        inputValue(inputs[0], "Bot Team");
        inputValue(inputs[1], "T111");
        inputValue(inputs[2], "write-only-canary");
        await new Promise((resolve) => setTimeout(resolve, 0));
        container
          .querySelector('[data-testid="slack-new-installation-form"]')
          .dispatchEvent(
            new Event("submit", { bubbles: true, cancelable: true }),
          );
        await waitFor(
          () => createBody,
          container,
          "installation create request",
        );
        expect(createBody).toEqual({
          name: "Bot Team",
          team_id: "T111",
          token: "write-only-canary",
        });
        expect(JSON.stringify(createBody)).not.toContain("bot_token");
      } finally {
        preact.render(null, container);
        container.remove();
      }
    });
  });
} else {
  describe("Slack installation credential modes", () => {
    test("passes credential-mode component tests in an isolated process", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_SLACK_CREDENTIAL_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Slack credential tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
