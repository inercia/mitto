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
  slackAppManifestYAML,
  formatSlackValidation,
  formatSlackRelativeTime,
  slackAppSettingsURL,
  slackHealth,
  deriveSlackDeliveryWarning,
  SLACK_DELIVERY_WARNING_GRACE_MS,
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

  test("formats a compact relative time for Last checked, given a fixed now", () => {
    const now = new Date("2026-08-17T12:10:30Z");
    expect(formatSlackRelativeTime("2026-08-17T12:10:28Z", now)).toBe("2s ago");
    expect(formatSlackRelativeTime("2026-08-17T12:08:30Z", now)).toBe("2m ago");
    expect(formatSlackRelativeTime("2026-08-17T10:10:30Z", now)).toBe("2h ago");
    expect(formatSlackRelativeTime("2026-08-15T12:10:30Z", now)).toBe("2d ago");
  });

  test("relative time handles unset/zero timestamps and future skew safely", () => {
    const now = new Date("2026-08-17T12:10:30Z");
    expect(formatSlackRelativeTime(null, now)).toBe("Never");
    expect(formatSlackRelativeTime("", now)).toBe("Never");
    expect(formatSlackRelativeTime("0001-01-01T00:00:00Z", now)).toBe("Never");
    // Clock skew / not-yet-advanced `now`: treat as "just now" instead of
    // a nonsensical negative duration.
    expect(formatSlackRelativeTime("2026-08-17T12:10:30Z", now)).toBe(
      "just now",
    );
    expect(formatSlackRelativeTime("2026-08-17T12:20:00Z", now)).toBe(
      "just now",
    );
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

  // mitto-yn5: pins the three deriveSlackDeliveryWarning() acceptance-criteria
  // paths (warning / healthy-idle / fresh-connection debounce), plus the
  // supporting not-connected and already-delivering short-circuits.
  test("surfaces a delivery-health warning only for a connected, subscribed, silent app past the grace window", () => {
    const now = new Date("2026-08-21T12:00:00Z");
    const longConnected = new Date(
      now.getTime() - SLACK_DELIVERY_WARNING_GRACE_MS - 1000,
    ).toISOString();

    // Warning path: connected, has subscribers, zero envelopes ever, past grace.
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "connected",
          subscription_count: 1,
          events_api_received: 0,
          last_envelope_at: "0001-01-01T00:00:00Z",
          connected_at: longConnected,
        },
        { now },
      ),
    ).toEqual({ message: "Connected, but 0 events received." });

    // Healthy-idle: no onSlack subscriptions reference this app, so zero
    // events is expected, not broken — no warning.
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "connected",
          subscription_count: 0,
          events_api_received: 0,
          connected_at: longConnected,
        },
        { now },
      ),
    ).toBeNull();

    // Fresh-connection debounce: still inside the grace window.
    const justConnected = new Date(now.getTime() - 1000).toISOString();
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "connected",
          subscription_count: 1,
          events_api_received: 0,
          connected_at: justConnected,
        },
        { now },
      ),
    ).toBeNull();

    // Not connected (e.g. still backing off): no warning regardless of counts.
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "backoff",
          subscription_count: 1,
          events_api_received: 0,
          connected_at: longConnected,
        },
        { now },
      ),
    ).toBeNull();

    // Already delivering: events_api_received > 0 short-circuits.
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "connected",
          subscription_count: 1,
          events_api_received: 3,
          connected_at: longConnected,
        },
        { now },
      ),
    ).toBeNull();

    // last_envelope_at set to a real (non-sentinel) time also short-circuits,
    // even if events_api_received were somehow unset.
    expect(
      deriveSlackDeliveryWarning(
        {
          state: "connected",
          subscription_count: 1,
          events_api_received: 0,
          last_envelope_at: "2026-08-21T11:00:00Z",
          connected_at: longConnected,
        },
        { now },
      ),
    ).toBeNull();

    // No status at all (e.g. app not yet reported): no warning.
    expect(deriveSlackDeliveryWarning(null, { now })).toBeNull();
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
        inputValue(tokenInput, candidate);
        container
          .querySelector('[aria-label="Rename Slack app profile"]')
          .click();
        await flushUI();
        const nameInput = container.querySelector(
          '[data-testid="slack-app-name-input"]',
        );
        inputValue(nameInput, "Renamed App");
        await flushUI();
        container.querySelector('[aria-label="Save app name"]').click();
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

    test("the selected app name has an inline pencil rename control, not a separate Friendly name editor", async () => {
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [] });
        if (url === "/api/slack/apps/app-a" && init.method === "PATCH")
          return json({ ...appA, name: "Renamed App" });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        const header = container.querySelector(
          '[data-testid="slack-app-header"]',
        );
        expect(header.textContent).toContain("App Alpha");
        expect(container.textContent).not.toContain("Friendly name");
        expect(
          header.querySelector('[data-testid="slack-app-name-input"]'),
        ).toBeNull();

        header.querySelector('[aria-label="Rename Slack app profile"]').click();
        await flushUI();
        let input = header.querySelector(
          '[data-testid="slack-app-name-input"]',
        );
        expect(input).not.toBeNull();
        input.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
        await flushUI();
        expect(
          header.querySelector('[data-testid="slack-app-name-input"]'),
        ).toBeNull();
        expect(header.textContent).toContain("App Alpha");

        header.querySelector('[aria-label="Rename Slack app profile"]').click();
        await flushUI();
        input = header.querySelector('[data-testid="slack-app-name-input"]');
        inputValue(input, "Renamed App");
        await flushUI();
        input.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
        );
        await flushUI();
        const patchCall = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/apps/app-a" && init.method === "PATCH",
        );
        expect(JSON.parse(patchCall[1].body)).toEqual({ name: "Renamed App" });
        expect(header.textContent).toContain("Renamed App");
        expect(
          header.querySelector('[data-testid="slack-app-name-input"]'),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("a single workspace is named once and renamed inline", async () => {
      const installation = {
        id: "inst-a",
        app_id: "app-a",
        name: "Alpha Team",
        team_id: "T111",
        token_configured: true,
      };
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [installation] });
        if (
          url === "/api/slack/installations/inst-a" &&
          init.method === "PATCH"
        )
          return json({ ...installation, name: "Renamed Team" });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
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
        const detail = container.querySelector(
          '[data-testid="slack-installation-detail"]',
        );
        expect(
          container.querySelector('[data-testid="slack-installation-list"]'),
        ).toBeNull();
        expect(detail.textContent.match(/Alpha Team/g)).toHaveLength(1);
        expect(detail.textContent).not.toContain("Friendly name");

        detail.querySelector('[aria-label="Rename Slack workspace"]').click();
        await flushUI();
        let input = detail.querySelector(
          '[data-testid="slack-installation-name-input"]',
        );
        input.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
        await flushUI();
        expect(
          detail.querySelector('[data-testid="slack-installation-name-input"]'),
        ).toBeNull();

        detail.querySelector('[aria-label="Rename Slack workspace"]').click();
        await flushUI();
        input = detail.querySelector(
          '[data-testid="slack-installation-name-input"]',
        );
        inputValue(input, "Renamed Team");
        await flushUI();
        input.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
        );
        await flushUI();
        const patchCall = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/installations/inst-a" &&
            init.method === "PATCH",
        );
        expect(JSON.parse(patchCall[1].body)).toEqual({ name: "Renamed Team" });
        expect(detail.textContent).toContain("Renamed Team");
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

    test("dependency preview shows names and can remove onSlack blockers", async () => {
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/prepare-delete")
          return json({
            references: [{ session_id: "session-1", name: "Slack watcher" }],
            installation_ids: [],
          });
        if (
          url === "/api/slack/apps/app-a/references" &&
          init.method === "DELETE"
        )
          return json({
            removed: [{ session_id: "session-1", name: "Slack watcher" }],
            preview: { references: [], installation_ids: [] },
          });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        container
          .querySelector('[aria-label="Delete Slack app profile"]')
          .click();
        await flushUI();
        expect(container.textContent).toContain("Slack watcher");
        expect(container.textContent).not.toContain("session-1");
        expect(
          container.querySelector('[data-testid="confirm-dialog-confirm"]')
            .disabled,
        ).toBe(true);
        expect(
          fetchMock.mock.calls.some(([, init]) => init.method === "DELETE"),
        ).toBe(false);
        container
          .querySelector('[data-testid="slack-remove-delete-references"]')
          .click();
        await flushUI();
        expect(container.textContent).not.toContain("Slack watcher");
        expect(
          container.querySelector('[data-testid="confirm-dialog-confirm"]')
            .disabled,
        ).toBe(false);
        expect(fetchMock).toHaveBeenCalledWith(
          "/api/slack/apps/app-a/references",
          expect.objectContaining({ method: "DELETE" }),
        );
      } finally {
        unmount(container);
      }
    });

    test("remove-on-slack failure refreshes the preview to reflect partial removal", async () => {
      let prepareCalls = 0;
      const { container } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/apps/app-a/prepare-delete") {
          prepareCalls += 1;
          // Second call (post-failure refresh) reflects that session-2 was
          // actually removed on disk even though the overall request failed.
          return prepareCalls === 1
            ? json({
                references: [
                  { session_id: "session-1", name: "Slack watcher" },
                  { session_id: "session-2", name: "Slack auditor" },
                ],
                installation_ids: [],
              })
            : json({
                references: [
                  { session_id: "session-1", name: "Slack watcher" },
                ],
                installation_ids: [],
              });
        }
        if (
          url === "/api/slack/apps/app-a/references" &&
          init.method === "DELETE"
        )
          return json(
            {
              error: {
                code: "unavailable",
                message: "Slack integration is temporarily unavailable",
              },
            },
            503,
          );
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        container
          .querySelector('[aria-label="Delete Slack app profile"]')
          .click();
        await flushUI();
        expect(container.textContent).toContain("Slack watcher");
        expect(container.textContent).toContain("Slack auditor");

        container
          .querySelector('[data-testid="slack-remove-delete-references"]')
          .click();
        await flushUI();

        expect(container.textContent).toContain(
          "could not be removed from every affected conversation",
        );
        expect(prepareCalls).toBe(2);
        expect(container.textContent).toContain("Slack watcher");
        expect(container.textContent).not.toContain("Slack auditor");
        expect(
          container.querySelector('[data-testid="confirm-dialog-confirm"]')
            .disabled,
        ).toBe(true);
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
        const guide = container.querySelector(
          '[data-testid="slack-create-app-guide"]',
        );
        const guideCopy = guide.textContent.replace(/\s+/g, " ").trim();
        expect(guideCopy).toContain("1. Bot: Use a bot token");
        expect(guideCopy).toContain(
          "2. User delegation: Configure the OAuth client on the app profile",
        );
        expect(guideCopy).toContain("app-level token: connections:write");
        expect(guideCopy).toContain("private channel");
        expect(guideCopy).toContain(
          "Existing app? Apply the current manifest and reauthorize it.",
        );
        // mitto-3od5.1: manual delegated-user entry must not be advertised
        // as supported — Slack cannot prove a pasted token's app identity.
        expect(guideCopy).not.toContain(
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

    test("keeps app name + badge with right-aligned actions on the header line, App ID + Last checked below", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const header = container.querySelector(
          '[data-testid="slack-app-header"]',
        );
        expect(header.textContent).toContain("App Alpha");
        expect(header.textContent).toContain("Connected");
        // App ID moved off the first line onto the smaller metadata line.
        expect(header.textContent).not.toContain("App ID:");
        for (const testId of [
          "slack-open-app-settings",
          "slack-validate-app",
        ]) {
          expect(
            header.querySelector(`[data-testid="${testId}"]`),
          ).not.toBeNull();
        }
        expect(
          header.querySelector('[aria-label="Delete Slack app profile"]'),
        ).not.toBeNull();
        // Actions live in a trailing ml-auto group, right-aligned from the
        // name/badge on the same flex line.
        const actions = header
          .querySelector('[data-testid="slack-open-app-settings"]')
          .closest(".ml-auto");
        expect(actions).not.toBeNull();

        const lastCheck = container.querySelector(
          '[data-testid="slack-app-last-check"]',
        );
        expect(lastCheck.previousElementSibling).toBe(header);
        expect(lastCheck.textContent).toContain("App ID: A111");
        expect(lastCheck.textContent).toContain("Last checked:");
      } finally {
        unmount(container);
      }
    });

    test("renders Last checked as compact relative time with an exact-timestamp tooltip", async () => {
      const recent = { ...appA, validated_at: new Date().toISOString() };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [recent] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const lastCheck = container.querySelector(
          '[data-testid="slack-app-last-check"]',
        );
        expect(lastCheck.textContent).toMatch(
          /Last checked:\s*(just now|\ds ago)/,
        );
        expect(lastCheck.textContent).not.toContain(
          new Date(recent.validated_at).toLocaleString(),
        );
        const timestampSpan = [...lastCheck.querySelectorAll("span")].find(
          (span) => span.textContent.includes("Last checked:"),
        );
        expect(timestampSpan.getAttribute("title")).toBe(
          new Date(recent.validated_at).toLocaleString(),
        );
      } finally {
        unmount(container);
      }
    });

    test("Never-validated app shows 'Never' without a stray App ID separator", async () => {
      const neverValidated = { ...appA, validated_at: null };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [neverValidated] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        const lastCheck = container.querySelector(
          '[data-testid="slack-app-last-check"]',
        );
        expect(lastCheck.textContent).toContain("App ID: A111");
        expect(lastCheck.textContent).toContain("Last checked:");
        expect(lastCheck.textContent).toContain("Never");
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

    test("mitto-yn5: renders a delivery-health warning badge and remediation hint fed by the initial connections fetch", async () => {
      const longConnectedAt = new Date(
        Date.now() - SLACK_DELIVERY_WARNING_GRACE_MS - 60_000,
      ).toISOString();
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/connections")
          return json({
            connections: [
              {
                app_id: "app-a",
                state: "connected",
                subscription_count: 1,
                events_api_received: 0,
                connected_at: longConnectedAt,
              },
            ],
          });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () =>
            container.querySelector(
              '[data-testid="slack-delivery-warning-badge"]',
            ),
          container,
          "delivery-health warning badge",
        );
        expect(
          container.querySelector(
            '[data-testid="slack-delivery-warning-badge"]',
          ).textContent,
        ).toBe("Connected, but 0 events received.");
        const hint = container.querySelector(
          '[data-testid="slack-delivery-warning-hint"]',
        );
        expect(hint.textContent).toContain("message.channels");
        const link = container.querySelector(
          '[data-testid="slack-delivery-troubleshooting-link"]',
        );
        expect(link.tagName).toBe("A");
        link.click();
        expect(window.open).toHaveBeenCalledWith(
          expect.stringContaining(
            "#troubleshooting-connected-but-0-events-received",
          ),
          "_blank",
          "noopener,noreferrer",
        );
      } finally {
        unmount(container);
      }
    });

    test("mitto-yn5: a healthy idle app (no onSlack subscriptions) shows no delivery-health warning", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/connections")
          return json({
            connections: [
              {
                app_id: "app-a",
                state: "connected",
                subscription_count: 0,
                events_api_received: 0,
              },
            ],
          });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes("Connected"),
          container,
          "health badge",
        );
        expect(
          container.querySelector(
            '[data-testid="slack-delivery-warning-badge"]',
          ),
        ).toBeNull();
        expect(
          container.querySelector(
            '[data-testid="slack-delivery-warning-hint"]',
          ),
        ).toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("mitto-yn5: live slack_connection_status window events update the warning without a refetch", async () => {
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations")) return json({ installations: [] });
        if (url === "/api/slack/connections") return json({ connections: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes("Connected"),
          container,
          "initial health badge",
        );
        expect(
          container.querySelector(
            '[data-testid="slack-delivery-warning-badge"]',
          ),
        ).toBeNull();

        const longConnectedAt = new Date(
          Date.now() - SLACK_DELIVERY_WARNING_GRACE_MS - 60_000,
        ).toISOString();
        window.dispatchEvent(
          new CustomEvent("mitto:slack_connection_status", {
            detail: {
              app_id: "app-a",
              state: "connected",
              subscription_count: 1,
              events_api_received: 0,
              connected_at: longConnectedAt,
            },
          }),
        );
        await waitFor(
          () =>
            container.querySelector(
              '[data-testid="slack-delivery-warning-badge"]',
            ),
          container,
          "live-updated delivery-health warning badge",
        );
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

    test("mitto-3od5.1: surfaces the safe OAuth-required message on a rejected delegated-user create instead of a generic error", async () => {
      const canary = "write-only-user-token-missing-app-id";
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url.endsWith("/installations") && init.method === "GET")
          return json({ installations: [] });
        if (
          url === "/api/slack/apps/app-a/installations" &&
          init.method === "POST"
        ) {
          return json(
            {
              error: {
                code: "conflict",
                message:
                  "Slack did not return the app identity needed to safely bind this delegated-user credential. Manual delegated-user setup is unavailable until Slack OAuth provenance is supported; use a bot token instead.",
              },
            },
            409,
          );
        }
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        container
          .querySelector('[data-testid="slack-add-installation"]')
          .click();
        await flushUI();
        const form = container.querySelector(
          '[data-testid="slack-new-installation-form"]',
        );
        inputValue(form.querySelector("input[required]"), "Team");
        inputValue(
          form.querySelector('input[placeholder="Bot token"]'),
          canary,
        );
        await flushUI();
        form.querySelector("button").click();
        await waitFor(
          () => container.querySelector('[data-testid="slack-action-error"]'),
          container,
          "action error",
        );
        const message = container.querySelector(
          '[data-testid="slack-action-error"]',
        ).textContent;
        expect(message).toContain("OAuth");
        expect(message).toContain("delegated-user");
        expect(message).not.toBe(
          "Slack workspace installation could not be created.",
        );
        expect(container.textContent).not.toContain(canary);
        expect(
          fetchMock.mock.calls.some(([url]) =>
            String(url).endsWith("/installations"),
          ),
        ).toBe(true);
      } finally {
        unmount(container);
      }
    });

    test("configures a write-only OAuth secret and copies the exact redirect-aware manifest", async () => {
      const secret = "write-only-oauth-client-secret";
      const redirect = "https://mitto.example/mitto/api/slack/oauth/callback";
      const { container, fetchMock, clipboard } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [appA] });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [] });
        if (url === "/api/slack/oauth/config")
          return json({ available: true, redirect_uri: redirect });
        if (
          url === "/api/slack/apps/app-a/oauth-client" &&
          init.method === "PUT"
        )
          return json({
            ...appA,
            oauth_client_id: "123.456",
            oauth_client_secret_configured: true,
          });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes(redirect),
          container,
          "OAuth redirect guidance",
        );
        const panel = container.querySelector(
          '[data-testid="slack-oauth-client-config"]',
        );
        expect(panel.textContent.replace(/\s+/g, " ")).toContain(
          "Only required for delegated-user authorization—not bot-token installations.",
        );
        const [clientId, clientSecret] = panel.querySelectorAll("input");
        inputValue(clientId, "123.456");
        inputValue(clientSecret, secret);
        await flushUI();
        buttonByText(panel, "Save OAuth client").click();
        await waitFor(
          () =>
            panel.querySelector('input[type="password"]').value === "" &&
            panel
              .querySelector('input[type="password"]')
              .placeholder.includes("Configured"),
          container,
          "OAuth secret clearing",
        );
        const request = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/apps/app-a/oauth-client" &&
            init.method === "PUT",
        );
        expect(JSON.parse(request[1].body)).toEqual({
          client_id: "123.456",
          client_secret: secret,
        });
        expect(container.textContent).not.toContain(secret);

        container.querySelector('[data-testid="slack-copy-manifest"]').click();
        await waitFor(
          () => clipboard.writeText.mock.calls.length === 1,
          container,
          "redirect-aware manifest copy",
        );
        expect(clipboard.writeText).toHaveBeenCalledWith(
          slackAppManifestYAML(redirect),
        );
      } finally {
        unmount(container);
      }
    });

    test("completes delegated-user OAuth creation and refreshes value-free identity metadata", async () => {
      const oauthApp = {
        ...appA,
        oauth_client_id: "123.456",
        oauth_client_secret_configured: true,
      };
      const installation = {
        id: "inst-oauth",
        app_id: "app-a",
        name: "OAuth Team",
        credential_kind: "user",
        team_id: "T123",
        team_name: "Example",
        user_id: "U123",
        oauth_authorized: true,
        token_configured: true,
      };
      let installationLists = 0;
      const { container, fetchMock, showToast } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
        if (url === "/api/slack/oauth/config")
          return json({
            available: true,
            redirect_uri:
              "https://mitto.example/mitto/api/slack/oauth/callback",
          });
        if (
          url === "/api/slack/apps/app-a/installations" &&
          init.method === "GET"
        ) {
          installationLists += 1;
          return json({
            installations: installationLists > 1 ? [installation] : [],
          });
        }
        if (
          url === "/api/slack/apps/app-a/oauth/start" &&
          init.method === "POST"
        )
          return json({
            flow_id: "flow-create",
            authorization_url: "https://slack.example/authorize",
            expires_at: "2026-08-19T20:10:00Z",
          });
        if (url === "/api/slack/oauth/flows/flow-create")
          return json({
            flow_id: "flow-create",
            status: "succeeded",
            installation_id: "inst-oauth",
            expires_at: "2026-08-19T20:10:00Z",
          });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
      });
      try {
        container
          .querySelector('[data-testid="slack-add-installation"]')
          .click();
        await flushUI();
        const form = container.querySelector(
          '[data-testid="slack-new-installation-form"]',
        );
        inputValue(form.querySelector("input[required]"), "OAuth Team");
        inputValue(form.querySelector('input[placeholder="T…"]'), "T123");
        const authorize = buttonByText(form, "Authorize delegated user");
        await waitFor(
          () => !authorize.disabled,
          container,
          "OAuth create ready",
        );
        authorize.click();
        await waitFor(
          () => container.textContent.includes("User: U123"),
          container,
          "OAuth identity refresh",
        );
        const start = fetchMock.mock.calls.find(
          ([url, init]) =>
            url === "/api/slack/apps/app-a/oauth/start" &&
            init.method === "POST",
        );
        expect(JSON.parse(start[1].body)).toEqual({
          name: "OAuth Team",
          team_id: "T123",
        });
        expect(window.open).toHaveBeenCalledWith(
          "https://slack.example/authorize",
          "_blank",
          "noopener,noreferrer",
        );
        expect(container.textContent).toContain("Slack team: Example");
        expect(container.textContent).not.toContain("access_token");
        expect(showToast).toHaveBeenCalledWith(
          expect.objectContaining({
            message: "Slack delegated-user authorization completed.",
          }),
        );
      } finally {
        unmount(container);
      }
    });

    test("replacement cancellation is actionable and uses the replacement OAuth endpoint", async () => {
      const oauthApp = {
        ...appA,
        oauth_client_id: "123.456",
        oauth_client_secret_configured: true,
      };
      const installation = {
        id: "inst-a",
        app_id: "app-a",
        name: "Alpha Team",
        credential_kind: "bot",
        team_id: "T111",
        token_configured: true,
      };
      const { container, fetchMock } = await mount((url, init) => {
        if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
        if (url === "/api/slack/oauth/config")
          return json({
            available: true,
            redirect_uri: "https://mitto.example/cb",
          });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [installation] });
        if (
          url === "/api/slack/installations/inst-a/oauth/start" &&
          init.method === "POST"
        )
          return json({
            flow_id: "flow-replace",
            authorization_url: "https://slack.example/replace",
            expires_at: "2026-08-19T20:10:00Z",
          });
        if (url === "/api/slack/oauth/flows/flow-replace")
          return json({
            flow_id: "flow-replace",
            status: "failed",
            error: "authorization_cancelled",
            message: "Slack authorization was cancelled or denied.",
            expires_at: "2026-08-19T20:10:00Z",
          });
        throw new Error(`Unexpected request: ${init.method} ${url}`);
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
        const authorize = buttonByText(container, "Authorize delegated user");
        await waitFor(
          () => !authorize.disabled,
          container,
          "OAuth replacement ready",
        );
        authorize.click();
        await waitFor(
          () => container.textContent.includes("cancelled or denied"),
          container,
          "OAuth cancellation",
        );
        const start = fetchMock.mock.calls.find(
          ([url]) => url === "/api/slack/installations/inst-a/oauth/start",
        );
        expect(start[1].body).toBe("{}");
        expect(container.textContent).toContain("Bot");
      } finally {
        unmount(container);
      }
    });

    test("surfaces mismatch, expiry, and replay OAuth failures without rendering secrets", async () => {
      const cases = [
        ["identity_mismatch", "did not match the selected app or workspace"],
        ["expired", "authorization expired; start again"],
        ["authorization_rejected", "invalid or already used"],
      ];
      for (const [error, message] of cases) {
        const oauthApp = {
          ...appA,
          oauth_client_secret_configured: true,
        };
        const { container } = await mount((url, init) => {
          if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
          if (url === "/api/slack/oauth/config")
            return json({
              available: true,
              redirect_uri: "https://mitto.example/cb",
            });
          if (
            url === "/api/slack/apps/app-a/installations" &&
            init.method === "GET"
          )
            return json({ installations: [] });
          if (url === "/api/slack/apps/app-a/oauth/start")
            return json({
              flow_id: `flow-${error}`,
              authorization_url: "https://slack.example/authorize",
              expires_at: "2026-08-19T20:10:00Z",
            });
          if (url === `/api/slack/oauth/flows/flow-${error}`)
            return json({
              flow_id: `flow-${error}`,
              status: "failed",
              error,
              message,
              expires_at: "2026-08-19T20:10:00Z",
            });
          throw new Error(`Unexpected request: ${init.method} ${url}`);
        });
        try {
          container
            .querySelector('[data-testid="slack-add-installation"]')
            .click();
          await flushUI();
          const form = container.querySelector(
            '[data-testid="slack-new-installation-form"]',
          );
          inputValue(form.querySelector("input[required]"), "Workspace");
          const authorize = buttonByText(form, "Authorize delegated user");
          await waitFor(
            () => !authorize.disabled,
            container,
            `OAuth ${error} ready`,
          );
          authorize.click();
          await waitFor(
            () => container.textContent.includes(message),
            container,
            `OAuth ${error} error`,
          );
          expect(container.textContent).not.toContain("access_token");
          expect(container.textContent).not.toContain("client_secret");
        } finally {
          unmount(container);
        }
      }
    });

    test("disables OAuth start and shows external-address setup guidance when unavailable", async () => {
      const oauthApp = { ...appA, oauth_client_secret_configured: true };
      const guidance =
        "Configure an HTTPS web.hooks.external_address before starting Slack OAuth.";
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
        if (url === "/api/slack/oauth/config")
          return json({ available: false, message: guidance });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [] });
        throw new Error(`Unexpected request: ${url}`);
      });
      try {
        await waitFor(
          () => container.textContent.includes(guidance),
          container,
          "external OAuth guidance",
        );
        container
          .querySelector('[data-testid="slack-add-installation"]')
          .click();
        await flushUI();
        const form = container.querySelector(
          '[data-testid="slack-new-installation-form"]',
        );
        inputValue(form.querySelector("input[required]"), "Workspace");
        expect(buttonByText(form, "Authorize delegated user").disabled).toBe(
          true,
        );
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

    test("mitto-a13: renders the re-authorization badge and emphasizes the CTA when needs_reauthorization is set", async () => {
      const oauthApp = {
        ...appA,
        oauth_client_id: "123.456",
        oauth_client_secret_configured: true,
      };
      const installation = {
        id: "inst-a",
        app_id: "app-a",
        name: "Alpha Team",
        credential_kind: "user",
        team_id: "T111",
        team_name: "Example",
        user_id: "U123",
        oauth_authorized: true,
        token_configured: true,
        needs_reauthorization: true,
      };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
        if (url === "/api/slack/oauth/config")
          return json({
            available: true,
            redirect_uri: "https://mitto.example/cb",
          });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [installation] });
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
        const badge = container.querySelector(
          '[data-testid="slack-needs-reauthorization"]',
        );
        expect(badge).not.toBeNull();
        expect(badge.textContent).toContain("Re-authorization required");
        const authorize = buttonByText(container, "Authorize delegated user");
        expect(authorize).not.toBeNull();
        expect(authorize.className).toContain("btn-warning");
      } finally {
        unmount(container);
      }
    });

    test("mitto-a13: hides the re-authorization badge and CTA emphasis when needs_reauthorization is false", async () => {
      const oauthApp = {
        ...appA,
        oauth_client_id: "123.456",
        oauth_client_secret_configured: true,
      };
      const installation = {
        id: "inst-a",
        app_id: "app-a",
        name: "Alpha Team",
        credential_kind: "user",
        team_id: "T111",
        team_name: "Example",
        user_id: "U123",
        oauth_authorized: true,
        token_configured: true,
        needs_reauthorization: false,
      };
      const { container } = await mount((url) => {
        if (url === "/api/slack/apps") return json({ apps: [oauthApp] });
        if (url === "/api/slack/oauth/config")
          return json({
            available: true,
            redirect_uri: "https://mitto.example/cb",
          });
        if (url === "/api/slack/apps/app-a/installations")
          return json({ installations: [installation] });
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
        expect(
          container.querySelector(
            '[data-testid="slack-needs-reauthorization"]',
          ),
        ).toBeNull();
        const authorize = buttonByText(container, "Authorize delegated user");
        expect(authorize).not.toBeNull();
        expect(authorize.className).not.toContain("btn-warning");
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
