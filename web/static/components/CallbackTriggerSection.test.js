/**
 * Focused behavior tests for CallbackTriggerSection.
 * Uses the vendored Preact runtime and the real SDK resource seam; only network
 * responses and clipboard access are mocked.
 */

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  jest,
  test,
} from "../utils/testing/testGlobals.js";
import {
  Component,
  Fragment,
  h,
  render as preactRender,
} from "../vendor/preact.js";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";

const html = htm.bind(h);
const previousPreact = window.preact;
window.preact = {
  Component,
  Fragment,
  h,
  html,
  render: preactRender,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
};
const { CallbackTriggerSection } =
  await import("./CallbackTriggerSection.js?behavior-test");
const { _resetSdkClientForTests } = await import("../utils/sdkClient.js");
window.preact = previousPreact;

const __dirname = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(
  resolve(__dirname, "CallbackTriggerSection.js"),
  "utf8",
);

let container;
let clipboard;

function response(body, status = 200) {
  return new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function methodOf(init) {
  return (init?.method || "GET").toUpperCase();
}

function csrfResponse(url) {
  return String(url).endsWith("/api/csrf-token")
    ? response({ token: "test-token" })
    : null;
}

function mount(props) {
  preactRender(h(CallbackTriggerSection, props), container);
}

async function waitFor(predicate, message = "condition") {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return;
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 2));
  }
  throw new Error(`Timed out waiting for ${message}: ${container.innerHTML}`);
}

function byTestId(id) {
  return container.querySelector(`[data-testid="${id}"]`);
}

beforeEach(() => {
  _resetSdkClientForTests();
  window.mittoApiPrefix = "";
  document.cookie = "mitto_csrf=test-token; path=/";
  container = document.createElement("div");
  document.body.appendChild(container);
  clipboard = { writeText: jest.fn(() => Promise.resolve()) };
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: clipboard,
  });
});

afterEach(() => {
  preactRender(null, container);
  container.remove();
  jest.restoreAllMocks();
  _resetSdkClientForTests();
});

describe("CallbackTriggerSection", () => {
  test("loads not-configured state, then generates and copies a credential", async () => {
    const secret = "https://example.test/mitto/api/callback/cb_secret";
    global.fetch = jest.fn((url, init) => {
      const csrf = csrfResponse(url);
      if (csrf) return Promise.resolve(csrf);
      const method = methodOf(init);
      if (method === "GET")
        return Promise.resolve(response({ error: "missing" }, 404));
      if (method === "POST")
        return Promise.resolve(response({ callback_url: secret }));
      throw new Error(`unexpected ${method} ${url}`);
    });

    mount({ sessionId: "session-1", loopEnabled: true });
    await waitFor(() => byTestId("callback-enable"), "enable button");
    expect(byTestId("callback-status").textContent).toBe("Not configured");

    byTestId("callback-enable").click();
    await waitFor(
      () =>
        global.fetch.mock.calls.some(([, init]) => methodOf(init) === "POST"),
      "callback POST",
    );
    await waitFor(
      () => byTestId("callback-status")?.textContent === "Active",
      "active callback",
    );

    expect(clipboard.writeText).toHaveBeenCalledWith(secret);
    expect(container.innerHTML).not.toContain(secret);
    expect(
      global.fetch.mock.calls.some(([, init]) => methodOf(init) === "POST"),
    ).toBe(true);
  });

  test("shows configured callback as inactive while paused and keeps it copyable", async () => {
    const secret = "https://example.test/callback/paused-secret";
    global.fetch = jest.fn((url) =>
      Promise.resolve(csrfResponse(url) || response({ callback_url: secret })),
    );

    mount({ sessionId: "session-2", loopEnabled: false });
    await waitFor(
      () => byTestId("callback-status")?.textContent === "Inactive",
      "inactive callback",
    );

    expect(container.textContent).toContain(
      "Configured but inactive while the loop is paused.",
    );
    expect(byTestId("callback-rotate")).toBeNull();
    expect(byTestId("callback-revoke")).not.toBeNull();
    expect(container.innerHTML).not.toContain(secret);

    byTestId("callback-copy").click();
    await waitFor(
      () => clipboard.writeText.mock.calls.length === 1,
      "clipboard copy",
    );
    expect(clipboard.writeText).toHaveBeenCalledWith(secret);
  });

  test("rotates only after confirmation and copies the replacement URL", async () => {
    const oldSecret = "https://example.test/callback/old-secret";
    const newSecret = "https://example.test/callback/new-secret";
    global.fetch = jest.fn((url, init) => {
      const csrf = csrfResponse(url);
      if (csrf) return Promise.resolve(csrf);
      const method = methodOf(init);
      if (method === "GET")
        return Promise.resolve(response({ callback_url: oldSecret }));
      if (method === "POST")
        return Promise.resolve(response({ callback_url: newSecret }));
      throw new Error(`unexpected method ${method}`);
    });

    mount({ sessionId: "session-3", loopEnabled: true });
    await waitFor(() => byTestId("callback-rotate"), "rotate button");
    byTestId("callback-rotate").click();
    await waitFor(
      () => byTestId("confirm-dialog-confirm"),
      "rotate confirmation",
    );
    expect(
      global.fetch.mock.calls.filter(([, init]) => methodOf(init) === "POST"),
    ).toHaveLength(0);

    byTestId("confirm-dialog-confirm").click();
    await waitFor(
      () => clipboard.writeText.mock.calls.length === 1,
      "rotated URL copy",
    );
    expect(clipboard.writeText).toHaveBeenCalledWith(newSecret);
    expect(container.innerHTML).not.toContain(oldSecret);
    expect(container.innerHTML).not.toContain(newSecret);
  });

  test("revokes only after confirmation and returns to not-configured state", async () => {
    global.fetch = jest.fn((url, init) => {
      const csrf = csrfResponse(url);
      if (csrf) return Promise.resolve(csrf);
      const method = methodOf(init);
      if (method === "GET") {
        return Promise.resolve(
          response({ callback_url: "https://example.test/secret" }),
        );
      }
      if (method === "DELETE") return Promise.resolve(response(null, 204));
      throw new Error(`unexpected method ${method}`);
    });

    mount({ sessionId: "session-4", loopEnabled: true });
    await waitFor(() => byTestId("callback-revoke"), "revoke button");
    byTestId("callback-revoke").click();
    await waitFor(
      () => byTestId("confirm-dialog-confirm"),
      "revoke confirmation",
    );
    expect(
      global.fetch.mock.calls.filter(([, init]) => methodOf(init) === "DELETE"),
    ).toHaveLength(0);

    byTestId("confirm-dialog-confirm").click();
    await waitFor(
      () => byTestId("callback-status")?.textContent === "Not configured",
      "revoked state",
    );
    expect(
      global.fetch.mock.calls.filter(([, init]) => methodOf(init) === "DELETE"),
    ).toHaveLength(1);
  });

  test("session switch resets state and ignores a stale callback response", async () => {
    let resolveFirst;
    const firstSecret = "https://example.test/callback/first";
    const secondSecret = "https://example.test/callback/second";
    global.fetch = jest.fn((url) => {
      const csrf = csrfResponse(url);
      if (csrf) return Promise.resolve(csrf);
      if (String(url).includes("session-first")) {
        return new Promise((resolvePromise) => {
          resolveFirst = () =>
            resolvePromise(response({ callback_url: firstSecret }));
        });
      }
      return Promise.resolve(response({ callback_url: secondSecret }));
    });

    mount({ sessionId: "session-first", loopEnabled: true });
    await waitFor(() => typeof resolveFirst === "function", "first request");
    mount({ sessionId: "session-second", loopEnabled: true });
    await waitFor(
      () => byTestId("callback-status")?.textContent === "Active",
      "second session callback",
    );

    resolveFirst();
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 5));
    byTestId("callback-copy").click();
    await waitFor(
      () => clipboard.writeText.mock.calls.length === 1,
      "second URL copy",
    );
    expect(clipboard.writeText).toHaveBeenCalledWith(secondSecret);
    expect(clipboard.writeText).not.toHaveBeenCalledWith(firstSecret);
  });

  test("has no loop trigger-list or loop PATCH dependency", () => {
    expect(source).not.toMatch(
      /buildLoopPatch|toggleTrigger|\.sessions\.loop\.update/,
    );
    expect(source).not.toMatch(/\btriggers\b/);
    expect(source).toMatch(/\.sessions\.getCallback\(sessionId\)/);
    expect(source).toMatch(/\.sessions\.createCallback\(targetSessionId\)/);
    expect(source).toMatch(/\.sessions\.revokeCallback\(targetSessionId\)/);
  });
});
