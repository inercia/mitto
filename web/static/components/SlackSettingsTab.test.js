import { describe, expect, jest, test } from "../utils/testing/testGlobals.js";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import * as preact from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";
import { createClient } from "../sdk/index.js";

const html = htm.bind(preact.h);
const previousPreact = window.preact;
window.preact = { ...preact, ...hooks, html };
window.mittoApiPrefix = "";

const slackModule = await import("./SlackSettingsTab.js?mitto-37nx-6-tests");
const {
  SlackSettingsTab,
  SLACK_APPS_URL,
  SLACK_CREATE_APP_URL,
  formatSlackValidation,
  slackAppSettingsURL,
  slackHealth,
} = slackModule;
window.preact = previousPreact;

const appA = {
  id: "app-a",
  name: "App Alpha",
  slack_app_id: "A111",
  token_configured: true,
  validated_at: "2026-08-17T12:00:00Z",
};
const appB = {
  id: "app-b",
  name: "App Beta",
  slack_app_id: "A222",
  token_configured: true,
  validated_at: "2026-08-17T12:00:00Z",
};

const isIsolatedComponentRun =
  process.env.MITTO_SLACK_COMPONENT_TEST_CHILD === "1";

function json(body, status = 200) {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: status === 204 ? {} : { "Content-Type": "application/json" },
  });
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flushUI() {
  for (let i = 0; i < 10; i++) {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
}

async function waitFor(predicate, container, message = "condition") {
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

function buttonByText(root, text) {
  return [...root.querySelectorAll("button")].find(
    (button) => button.textContent.trim() === text,
  );
}

function installFetch(handler) {
  return jest.fn((url, init = {}) =>
    handler(String(url), { method: "GET", ...init }),
  );
}

async function mount(handler, showToast = jest.fn()) {
  window.open = jest.fn();
  window.mittoOpenExternalURL = undefined;
  const fetchMock = installFetch(handler);
  const client = createClient({ fetch: fetchMock });
  const container = document.createElement("div");
  document.body.appendChild(container);
  preact.render(
    html`<${SlackSettingsTab} showToast=${showToast} client=${client} />`,
    container,
  );
  await waitFor(
    () => container.querySelector('[data-testid="slack-settings-tab"]'),
    container,
    "Slack settings content",
  );
  return { container, fetchMock, showToast };
}

function unmount(container) {
  preact.render(null, container);
  container.remove();
}

describe("SlackSettingsTab helpers", () => {
  test("builds encoded app deep links and falls back to Apps home", () => {
    expect(slackAppSettingsURL("A 1/2")).toBe(`${SLACK_APPS_URL}/A%201%2F2`);
    expect(slackAppSettingsURL("")).toBe(SLACK_APPS_URL);
  });

  test("reports value-free health and treats zero validation time as never", () => {
    expect(formatSlackValidation("0001-01-01T00:00:00Z")).toBe("Never");
    expect(slackHealth({ token_configured: false })).toEqual({
      label: "Not configured",
      className: "badge-warning",
    });
    expect(slackHealth(appA)).toEqual({
      label: "Connected",
      className: "badge-success",
    });
    expect(slackHealth(appA, "failed").label).toBe("Validation failed");
  });
});

if (isIsolatedComponentRun) {
  describe("SlackSettingsTab component", () => {
		test("imports value-free environment metadata into the selected managed records", async () => {
			let imported = false;
			const installation = {
				id: "inst-a",
				app_id: "app-a",
				name: "Alpha Team",
				team_id: "T111",
				token_configured: true,
			};
			const { container, fetchMock, showToast } = await mount((url, init) => {
				if (url === "/api/slack/apps") return json({ apps: [appA] });
				if (url === "/api/slack/apps/app-a/installations")
					return json({ installations: [installation] });
				if (url === "/api/slack/environment-import" && init.method === "GET")
					return json({
						present: true,
						complete: true,
						team_id: "T111",
						channel_id: "C111",
						target_session_id: "session-1",
						active: !imported,
						shadowed: imported,
					});
				if (url === "/api/slack/environment-import" && init.method === "POST") {
					imported = true;
					return json({ app_id: "app-a", installation_id: "inst-a", subscription_created: true });
				}
				throw new Error(`Unexpected request: ${init.method} ${url}`);
			});
			try {
				await waitFor(
					() => container.querySelector('[data-testid="slack-import-environment"]'),
					container,
					"environment import card",
				);
				expect(container.textContent).toContain("Team T111");
				expect(container.textContent).toContain("Channel C111");
				expect(container.textContent).not.toContain("xapp-");
				container.querySelector('[data-testid="slack-import-environment"]').click();
				await flushUI();
				container.querySelector('[data-testid="confirm-dialog-confirm"]').click();
				await waitFor(() => imported, container, "environment import request");
				await flushUI();
				const post = fetchMock.mock.calls.find(
					([url, init]) => url === "/api/slack/environment-import" && init.method === "POST",
				);
				expect(JSON.parse(post[1].body)).toEqual({
					app_id: "app-a",
					app_name: "",
					installation_id: "inst-a",
					installation_name: "",
				});
				expect(post[1].body).not.toContain("token");
				expect(JSON.stringify(showToast.mock.calls)).not.toContain("xapp-");
				expect(container.textContent).toContain("Already managed");
			} finally {
				unmount(container);
			}
		});

    test("empty state explains security and opens the official app creator", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const copy = container.textContent.replace(/\s+/g, " ");
        expect(copy).toContain("No Slack app profiles");
        expect(copy).toContain("mode 0600");
        expect(copy).toContain("not a Mitto project workspace");
        container
          .querySelector('[data-testid="slack-create-app-external"]')
          .click();
        expect(window.open).toHaveBeenCalledWith(
          SLACK_CREATE_APP_URL,
          "_blank",
          "noopener,noreferrer",
        );
      } finally {
        unmount(container);
      }
    });

    test("profile switching aborts and ignores a stale installation response", async () => {
      const alpha = deferred();
      let alphaSignal;
      const { container } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA, appB] });
        if (url === "/api/slack/apps/app-a/installations") {
          alphaSignal = init.signal;
          return alpha.promise;
        }
        if (url === "/api/slack/apps/app-b/installations")
          return json({
            installations: [
              {
                id: "inst-b",
                app_id: "app-b",
                name: "Current Beta Team",
                team_id: "T222",
                token_configured: true,
              },
            ],
          });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        container.querySelector('[data-testid="slack-app-app-b"]').click();
        await flushUI();
        expect(alphaSignal.aborted).toBe(true);
        alpha.resolve(
          json({
            installations: [
              {
                id: "inst-a",
                app_id: "app-a",
                name: "Stale Alpha Team",
              },
            ],
          }),
        );
        await flushUI();
        expect(container.textContent).toContain("Current Beta Team");
        expect(container.textContent).not.toContain("Stale Alpha Team");
        expect(container.textContent).toContain("App Beta");
      } finally {
        alpha.resolve(json({ installations: [] }));
        unmount(container);
      }
    });

    test("profile switching ignores a stale validation failure", async () => {
      const validation = deferred();
      const configuredApp = { ...appB, validated_at: null };
      const { container } = await mount((url, init) => {
        if (url === "/api/slack/apps")
          return json({ apps: [appA, configuredApp] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/validate" && init.method === "POST")
          return validation.promise;
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        buttonByText(container, "Test connection").click();
        await flushUI();
        container.querySelector('[data-testid="slack-app-app-b"]').click();
        await flushUI();
        validation.reject(new Error("stale validation failure"));
        await flushUI();

        const detail = container
          .querySelector('[data-testid="slack-open-app-settings"]')
          .closest(".rounded-lg");
        expect(detail.textContent).toContain("App Beta");
        expect(detail.textContent).toContain("Configured");
        expect(detail.textContent).not.toContain("Validation failed");
        expect(container.textContent).not.toContain(
          "Slack app connection validation failed.",
        );
      } finally {
        validation.resolve(json(appA));
        unmount(container);
      }
    });

    test("switching app profiles clears the write-only token draft", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA, appB] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const tokenInput = container.querySelector(
          'input[placeholder="New xapp-… token"]',
        );
        inputValue(tokenInput, "sensitive-candidate");
        await flushUI();
        expect(tokenInput.value).toBe("sensitive-candidate");
        container.querySelector('[data-testid="slack-app-app-b"]').click();
        await flushUI();
        expect(
          container.querySelector('input[placeholder="New xapp-… token"]')
            .value,
        ).toBe("");
        expect(container.textContent).not.toContain("sensitive-candidate");
      } finally {
        unmount(container);
      }
    });

    test("rename preserves the configured credential and successful replacement clears the draft", async () => {
      const showToast = jest.fn();
      const candidate = "sensitive-replacement";
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps" && init.method === "GET")
          return json({ apps: [appA] });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [] });
        if (url === "/api/slack/apps/app-a" && init.method === "PATCH")
          return json({ ...appA, name: "Renamed App" });
        if (url === "/api/slack/apps/app-a/token" && init.method === "PUT")
          return json({ ...appA, validated_at: "2026-08-17T13:00:00Z" });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      }, showToast);
      try {
        const tokenInput = container.querySelector(
          'input[placeholder="New xapp-… token"]',
        );
        const nameInput = [
          ...container.querySelectorAll('input:not([type="password"])'),
        ].find((input) => input.value === "App Alpha");
        inputValue(tokenInput, candidate);
        inputValue(nameInput, "Renamed App");
        await flushUI();
        buttonByText(container, "Rename").click();
        await flushUI();
        const patchCall = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/apps/app-a" && init.method === "PATCH",
        );
        expect(JSON.parse(patchCall[1].body)).toEqual({ name: "Renamed App" });
        expect(patchCall[1].body).not.toContain(candidate);
        expect(tokenInput.value).toBe(candidate);

        buttonByText(container, "Replace").click();
        await flushUI();
        expect(tokenInput.value).toBe("");
        const toastText = JSON.stringify(showToast.mock.calls);
        expect(toastText).not.toContain(candidate);
        expect(container.textContent).toContain("Connected");
      } finally {
        unmount(container);
      }
    });

    test("failed token replacement keeps prior health and emits a value-free error", async () => {
      const candidate = "sensitive-rejected-token";
      const { container } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/token" && init.method === "PUT")
          return json(
            { error: { code: "invalid", message: "candidate rejected" } },
            400,
          );
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        const tokenInput = container.querySelector(
          'input[placeholder="New xapp-… token"]',
        );
        inputValue(tokenInput, candidate);
        await flushUI();
        buttonByText(container, "Replace").click();
        await flushUI();
        expect(container.textContent).toContain(
          "configured credential was not changed",
        );
        expect(container.textContent).toContain("Validation failed");
        expect(container.textContent).not.toContain(candidate);
        expect(tokenInput.value).toBe(candidate);
      } finally {
        unmount(container);
      }
    });

    test("dependency preview disables confirmation and never issues DELETE", async () => {
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/prepare-delete")
          return json({
            references: [{ session_id: "session-1", name: "Slack watcher" }],
            installation_ids: [],
          });
        if (init.method === "DELETE")
          throw new Error("DELETE must stay blocked");
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        container
          .querySelector('[aria-label="Delete Slack app profile"]')
          .click();
        await flushUI();
        expect(container.textContent).toContain("Slack watcher");
        expect(container.textContent).toContain("session-1");
        expect(
          container.querySelector('[data-testid="confirm-dialog-confirm"]')
            .disabled,
        ).toBe(true);
        expect(
          fetchMock.mock.calls.some(([, init]) => init.method === "DELETE"),
        ).toBe(false);
      } finally {
        unmount(container);
      }
    });

    test("configured app opens its encoded Slack settings deep link", async () => {
      const encoded = { ...appA, slack_app_id: "A 1/2" };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [encoded] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        container
          .querySelector('[data-testid="slack-open-app-settings"]')
          .click();
        expect(window.open).toHaveBeenCalledWith(
          `${SLACK_APPS_URL}/A%201%2F2`,
          "_blank",
          "noopener,noreferrer",
        );
      } finally {
        unmount(container);
      }
    });
  });
} else {
  describe("SlackSettingsTab component", () => {
    test("passes mounted behavior tests in an isolated happy-dom process", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_SLACK_COMPONENT_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Slack component tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
