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
  SLACK_SETUP_URL,
  SLACK_APP_MANIFEST_YAML,
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

function installClipboard(writeText = jest.fn(() => Promise.resolve())) {
  const clipboard = { writeText };
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: clipboard,
  });
  return clipboard;
}

async function mount(handler, showToast = jest.fn()) {
  window.open = jest.fn();
  window.mittoOpenExternalURL = undefined;
  const clipboard = installClipboard();
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
  return { container, fetchMock, showToast, clipboard };
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

  test("manifest pins bot and delegated-user scopes and events", () => {
    expect(SLACK_APP_MANIFEST_YAML).toContain(`    bot:
      - channels:read
      - channels:history
      - groups:read
      - groups:history
      - app_mentions:read
      - users:read`);
    expect(SLACK_APP_MANIFEST_YAML).toContain(`    user:
      - channels:read
      - channels:history
      - groups:read
      - groups:history`);
    expect(SLACK_APP_MANIFEST_YAML).toContain(`    bot_events:
      - message.channels
      - message.groups
      - app_mention`);
    expect(SLACK_APP_MANIFEST_YAML).toContain(`    user_events:
      - message.channels
      - message.groups`);
    expect(SLACK_APP_MANIFEST_YAML).toContain("socket_mode_enabled: true");
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
          return json({
            app_id: "app-a",
            installation_id: "inst-a",
            subscription_created: true,
          });
        }
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        await waitFor(
          () =>
            container.querySelector('[data-testid="slack-import-environment"]'),
          container,
          "environment import card",
        );
        expect(container.textContent).toContain("Team T111");
        expect(container.textContent).toContain("Channel C111");
        expect(container.textContent).not.toContain("xapp-");
        container
          .querySelector('[data-testid="slack-import-environment"]')
          .click();
        await flushUI();
        container
          .querySelector('[data-testid="confirm-dialog-confirm"]')
          .click();
        await waitFor(() => imported, container, "environment import request");
        await flushUI();
        const post = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/environment-import" && init.method === "POST",
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
        expect(copy).toContain("No app profiles");
        expect(copy).toContain("mode 0600");
        expect(copy).toContain("not a Mitto project workspace");
        const setupLink = container.querySelector(
          '[data-testid="slack-setup-guide"]',
        );
        expect(setupLink.tagName).toBe("A");
        expect(setupLink.getAttribute("href")).toBe(SLACK_SETUP_URL);
        setupLink.click();
        expect(window.open).toHaveBeenCalledWith(
          SLACK_SETUP_URL,
          "_blank",
          "noopener,noreferrer",
        );
        container
          .querySelector('[data-testid="slack-create-app-external"]')
          .click();
        const createAppLink = container.querySelector(
          '[data-testid="slack-create-app-external"]',
        );
        expect(createAppLink.tagName).toBe("A");
        expect(createAppLink.getAttribute("href")).toBe(SLACK_CREATE_APP_URL);
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
        container.querySelector('[data-testid="slack-validate-app"]').click();
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
        container
          .querySelector('[aria-label="Rename Slack app profile"]')
          .click();
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

    test("create-app guide opens the official app creator and copies the exact manifest", async () => {
      const { container, clipboard, showToast } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        expect(
          container.querySelector('[data-testid="slack-app-manifest"]'),
        ).toBeNull();
        expect(container.textContent).not.toContain("display_information:");
        expect(container.textContent).toContain("groups:read");
        expect(container.textContent).toContain("groups:history");
        expect(container.textContent).toContain("reauthorized");
        expect(container.textContent).toContain("private channel");
        expect(container.textContent).toContain("app-level token");
        expect(container.textContent).toContain("connections:write");
        expect(container.textContent).toContain("bot token");
        expect(container.textContent).toContain("user token");
        expect(container.textContent.replace(/\s+/g, " ")).toContain(
          "delegated-user backend support is enabled",
        );

        container
          .querySelector('[data-testid="slack-create-app-external"]')
          .click();
        expect(window.open).toHaveBeenCalledWith(
          SLACK_CREATE_APP_URL,
          "_blank",
          "noopener,noreferrer",
        );

        container.querySelector('[data-testid="slack-copy-manifest"]').click();
        await waitFor(
          () => clipboard.writeText.mock.calls.length === 1,
          container,
          "manifest clipboard copy",
        );
        expect(clipboard.writeText).toHaveBeenCalledWith(
          SLACK_APP_MANIFEST_YAML,
        );
        await waitFor(
          () => container.textContent.includes("Copied!"),
          container,
          "copied confirmation",
        );
        await waitFor(
          () => showToast.mock.calls.length === 1,
          container,
          "copy toast",
        );
        expect(showToast).toHaveBeenCalledWith(
          expect.objectContaining({
            style: "success",
            title: "Slack app manifest copied to clipboard.",
          }),
        );
      } finally {
        unmount(container);
      }
    });

    test("manifest copy failure falls back to an execCommand attempt and reports an error toast", async () => {
      // happy-dom does not implement document.execCommand, so it must be
      // stubbed directly (jest.spyOn requires a pre-existing function).
      const previousExecCommand = document.execCommand;
      const execCommand = jest.fn(() => false);
      document.execCommand = execCommand;
      const { container, clipboard, showToast } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      clipboard.writeText.mockImplementation(() =>
        Promise.reject(new Error("clipboard denied")),
      );
      try {
        container.querySelector('[data-testid="slack-copy-manifest"]').click();
        await waitFor(
          () => showToast.mock.calls.length === 1,
          container,
          "copy failure toast",
        );
        expect(execCommand).toHaveBeenCalledWith("copy");
        expect(showToast).toHaveBeenCalledWith(
          expect.objectContaining({
            style: "error",
            title: "Slack app manifest could not be copied.",
          }),
        );
        expect(container.textContent).not.toContain("Copied!");
      } finally {
        document.execCommand = previousExecCommand;
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

    test("mitto-1afm: uses a 1/3 master + 2/3 detail grid at md+", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const grid = container
          .querySelector('[data-testid="slack-app-list"]')
          .closest("section").parentElement;
        expect(grid.className).toContain("grid-cols-1");
        expect(grid.className).toContain("md:grid-cols-3");
        const detail = container
          .querySelector('[data-testid="slack-open-app-settings"]')
          .closest("section");
        expect(detail.className).toContain("md:col-span-2");
      } finally {
        unmount(container);
      }
    });

    test("uses concise labels inside the Slack settings context", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes("no workspace installations"),
          container,
          "workspace section",
        );
        const headings = Array.from(
          container.querySelectorAll("h5"),
          (element) => element.textContent.trim(),
        );
        expect(headings).toEqual(
          expect.arrayContaining(["App manifest", "Apps", "Workspaces"]),
        );
        expect(headings).not.toEqual(
          expect.arrayContaining([
            "Slack app manifest",
            "Slack apps",
            "Slack workspaces",
          ]),
        );
        expect(container.textContent).toContain("App ID:");
        expect(container.textContent).not.toContain("Slack App ID:");
      } finally {
        unmount(container);
      }
    });

    test("uses a concise team identity label for workspace installations", async () => {
      const installation = {
        id: "inst-a",
        app_id: "app-a",
        name: "Alpha Team",
        team_id: "T111",
        token_configured: true,
      };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations"))
          return json({ installations: [installation] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes("Team ID: T111"),
          container,
          "workspace identity",
        );
        expect(container.textContent).not.toContain("Slack Team ID:");
      } finally {
        unmount(container);
      }
    });

    test("mitto-1afm: places the credential-storage note below the catalog", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const layout = container.querySelector(
          '[data-testid="slack-catalog-layout"]',
        );
        const note = container.querySelector(
          '[data-testid="slack-credential-storage-note"]',
        );
        expect(layout.nextElementSibling).toBe(note);
      } finally {
        unmount(container);
      }
    });

    test("mitto-1afm: compact icon actions carry accessible labels and drop verbose labels", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations"))
          return json({
            installations: [
              {
                id: "inst-a",
                app_id: "app-a",
                name: "Alpha Team",
                team_id: "T111",
                token_configured: true,
              },
            ],
          });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () =>
            container.querySelector(
              '[data-testid="slack-installation-detail"]',
            ),
          container,
          "installation detail",
        );

        // Old verbose labels are gone.
        const copy = container.textContent;
        expect(copy).not.toContain("Open app settings");
        expect(copy).not.toContain("Test connection");
        expect(copy).not.toContain("Save Slack workspace");

        // Icon-only actions keep accessible labels + a tooltip/title.
        for (const label of [
          "Open app settings",
          "Test app connection",
          "Rename Slack app profile",
          "Delete Slack app profile",
          "Test workspace connection",
          "Rename Slack workspace",
          "Delete Slack workspace installation",
        ]) {
          const el = container.querySelector(`[aria-label="${label}"]`);
          expect(el).not.toBeNull();
          expect(el.getAttribute("title")).toBe(label);
        }

        // Explicit-context actions may keep single-word labels.
        expect(buttonByText(container, "Replace")).not.toBeUndefined();

        // Add-profile/add-workspace forms compact "Save profile"/"Save
        // Slack workspace" down to a single-word "Save" once opened.
        container
          .querySelector('[data-testid="slack-add-installation"]')
          .click();
        await flushUI();
        expect(buttonByText(container, "Save")).not.toBeUndefined();
        expect(container.textContent).not.toContain("Save Slack workspace");

        // Test IDs/handlers for the compacted actions are preserved.
        expect(
          container.querySelector('[data-testid="slack-open-app-settings"]'),
        ).not.toBeNull();
        expect(
          container.querySelector('[data-testid="slack-validate-app"]'),
        ).not.toBeNull();
        expect(
          container.querySelector(
            '[data-testid="slack-validate-installation"]',
          ),
        ).not.toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("mitto-1afm: Test connection buttons swap to a spinner while busy", async () => {
      const validation = deferred();
      const { container } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/validate" && init.method === "POST")
          return validation.promise;
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        const button = container.querySelector(
          '[data-testid="slack-validate-app"]',
        );
        expect(button.querySelector("svg")).not.toBeNull();
        button.click();
        await flushUI();
        expect(button.disabled).toBe(true);
      } finally {
        validation.resolve(json(appA));
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
