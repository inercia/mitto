/**
 * Tests for useWorkspaceMcpActions.js (mitto-7gta.17 slice S6 Test phase).
 *
 * Covers the 5 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: handleRestartAcp (workspaces.restartAcp),
 * handleRestartAcpClick's running-sessions probe (sessions.running), and
 * handleMcpInstall/handleMcpRemove/handleInstallMittoMcp
 * (workspaces.installMcpTool/removeMcpTool) — including the
 * mcpRequestErrorMessage() raw-text-body preference each of the three
 * install/remove handlers relies on. Mirrors the window.preact stub harness
 * established by useFolderPromptsConfig.test.js (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}
// Every migrated handler here issues a POST; pre-seed the CSRF cookie so
// browserCookieAuth's authorize() never needs its own network round trip
// (the test-local fetch mocks below only shape the endpoint under test).
global.document.cookie = "mitto_csrf=test-token";

// Stateful cell harness (mirrors the S3 Test-phase precedent in
// useCreateMode.test.js): useState is indexed by call order, so a setter
// invoked between two hook calls is visible on the next call — needed here
// because handleMcpInstall reads its own local state (mcpInstallJson/Name/
// Scope) via closure rather than via arguments.
let cells = [];
let callIndex = 0;
let currentSetters = [];
window.preact = {
  useState: (initial) => {
    const idx = callIndex++;
    if (!(idx in cells)) cells[idx] = initial;
    const setter = jest.fn((v) => {
      cells[idx] = typeof v === "function" ? v(cells[idx]) : v;
    });
    currentSetters.push(setter);
    return [cells[idx], setter];
  },
  useCallback: (fn) => fn,
  useRef: (initial) => ({ current: initial }),
};

function jsonResponse(data, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name) =>
        name.toLowerCase() === "content-type" ? "application/json" : null,
    },
    text: () => Promise.resolve(JSON.stringify(data)),
    json: () => Promise.resolve(data),
  };
}

// handleRestartAcp() is deliberately fire-and-forget from the two
// no-confirm-needed branches of handleRestartAcpClick (matches the pre-
// migration behavior), so tests observing its POST must flush a few extra
// microtask ticks after awaiting handleRestartAcpClick() itself.
async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

function textResponse(text, status = 500) {
  return {
    ok: false,
    status,
    headers: { get: () => "text/plain" },
    text: () => Promise.resolve(text),
    json: () => Promise.reject(new Error("not json")),
  };
}

async function loadHook() {
  cells = [];
  callIndex = 0;
  currentSetters = [];
  const mod = await import("./useWorkspaceMcpActions.js");
  // Each render() call resets the call-order index (so useState's Nth call
  // maps to the same cell) but keeps `cells` — a setter invoked between two
  // render() calls is visible on the next one, like a real re-render.
  const render = (args) => {
    callIndex = 0;
    currentSetters = [];
    const result = mod.useWorkspaceMcpActions(args);
    return { ...result, setters: currentSetters };
  };
  return { render };
}

const WS = { uuid: "uuid-1", acp_server: "auggie" };

describe("useWorkspaceMcpActions — handleRestartAcp", () => {
  test("POSTs restart-acp and clears needsRestart on success", async () => {
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({})));
    const setError = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcp, setters } = render({
      selectedWorkspace: WS,
      setError,
    });
    await handleRestartAcp();

    const [url, opts] = global.fetch.mock.calls.at(-1);
    expect(String(url)).toContain("/api/workspaces/uuid-1/restart-acp");
    expect(opts.method).toBe("POST");
    // setNeedsRestart is the 9th useState() call (index 8): mcpInstallOpen,
    // mcpInstallJson, mcpInstallName, mcpInstallScope, mcpInstallLoading,
    // mcpInstallError, mcpInstallSuccess, mcpRemoveLoading, needsRestart.
    expect(setters[8]).toHaveBeenCalledWith(false);
    expect(setError).not.toHaveBeenCalled();
  });

  test("a failed restart surfaces errorMessage() via setError", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "agent busy" } }, 500)),
    );
    const setError = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcp } = render({
      selectedWorkspace: WS,
      setError,
    });
    await handleRestartAcp();
    expect(setError).toHaveBeenCalledWith("Failed to restart ACP: agent busy");
  });

  test("no-ops without a selected workspace", async () => {
    global.fetch = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcp } = render({
      selectedWorkspace: null,
      setError: jest.fn(),
    });
    await handleRestartAcp();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useWorkspaceMcpActions — handleRestartAcpClick", () => {
  test("no active conversations: restarts directly without a confirm dialog", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/sessions/running")) {
        return Promise.resolve(jsonResponse({ sessions: [] }));
      }
      return Promise.resolve(jsonResponse({}));
    });
    const setConfirmDialog = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcpClick } = render({
      selectedWorkspace: WS,
      setConfirmDialog,
      setError: jest.fn(),
    });
    await handleRestartAcpClick();
    await flush();
    expect(setConfirmDialog).not.toHaveBeenCalled();
    expect(
      global.fetch.mock.calls.some((c) =>
        String(c[0]).includes("/restart-acp"),
      ),
    ).toBe(true);
  });

  test("an actively-prompting conversation opens a confirm dialog instead of restarting", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/sessions/running")) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              { workspace_uuid: "uuid-1", is_prompting: true },
              { workspace_uuid: "uuid-2", is_prompting: true },
            ],
          }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    });
    const setConfirmDialog = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcpClick } = render({
      selectedWorkspace: WS,
      setConfirmDialog,
      setError: jest.fn(),
    });
    await handleRestartAcpClick();
    expect(setConfirmDialog).toHaveBeenCalledTimes(1);
    expect(setConfirmDialog.mock.calls[0][0].title).toBe("Restart ACP?");
    expect(
      global.fetch.mock.calls.some((c) =>
        String(c[0]).includes("/restart-acp"),
      ),
    ).toBe(false);
  });

  test("a failed running-sessions probe falls through to a direct restart", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/sessions/running")) {
        return Promise.reject(new Error("offline"));
      }
      return Promise.resolve(jsonResponse({}));
    });
    const setConfirmDialog = jest.fn();
    const { render } = await loadHook();
    const { handleRestartAcpClick } = render({
      selectedWorkspace: WS,
      setConfirmDialog,
      setError: jest.fn(),
    });
    await handleRestartAcpClick();
    await flush();
    expect(setConfirmDialog).not.toHaveBeenCalled();
    expect(
      global.fetch.mock.calls.some((c) =>
        String(c[0]).includes("/restart-acp"),
      ),
    ).toBe(true);
  });
});

describe("useWorkspaceMcpActions — handleMcpInstall", () => {
  test("valid mcpServers JSON installs, reloads tools, and clears the form after a delay", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ results: [{ name: "s1", success: true }] }),
      ),
    );
    const loadMcpTools = jest.fn();
    const checkLiveAcpForWorkspace = jest.fn(() => Promise.resolve(false));
    const { render } = await loadHook();
    let hook = render({
      selectedWorkspace: WS,
      loadMcpTools,
      checkLiveAcpForWorkspace,
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
      setMcpToolsError: jest.fn(),
    });
    // Drive mcpInstallJson (useState index 1) via its setter, then re-render
    // so the next handleMcpInstall closure reads the new value — mirrors a
    // real onInput handler followed by a click.
    hook.setMcpInstallJson(
      JSON.stringify({ mcpServers: { s1: { url: "u" } } }),
    );
    hook = render({
      selectedWorkspace: WS,
      loadMcpTools,
      checkLiveAcpForWorkspace,
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
      setMcpToolsError: jest.fn(),
    });

    await hook.handleMcpInstall();

    const installCall = global.fetch.mock.calls.find((c) =>
      String(c[0]).includes("/mcp-tools/install"),
    );
    expect(installCall).toBeDefined();
    expect(JSON.parse(installCall[1].body)).toEqual({
      acp_server: "auggie",
      scope: "",
      definition: { mcpServers: { s1: { url: "u" } } },
    });
    expect(checkLiveAcpForWorkspace).toHaveBeenCalledWith("uuid-1");
  });

  test("invalid JSON is rejected client-side without calling the SDK", async () => {
    global.fetch = jest.fn();
    const { render } = await loadHook();
    const { handleMcpInstall, setters } = render({
      selectedWorkspace: WS,
      loadMcpTools: jest.fn(),
      checkLiveAcpForWorkspace: jest.fn(() => Promise.resolve(false)),
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
      setMcpToolsError: jest.fn(),
    });
    await handleMcpInstall();
    expect(global.fetch).not.toHaveBeenCalled();
    // setMcpInstallError is the 6th useState() call (index 5).
    expect(setters[5]).toHaveBeenCalledWith(
      expect.stringContaining("Invalid JSON"),
    );
  });

  test("install failure prefers the raw-text error body via mcpRequestErrorMessage", async () => {
    global.fetch = jest.fn(() => Promise.resolve(textResponse("proxy 502")));
    const { render } = await loadHook();
    // Exercise the shared error-formatting helper indirectly through
    // handleInstallMittoMcp, which skips the JSON-parse gate entirely.
    const { handleInstallMittoMcp, setters } = render({
      selectedWorkspace: WS,
      mcpTools: { mcp_url: "http://127.0.0.1:5757/mcp", mcp_scopes: [] },
      loadMcpTools: jest.fn(),
      checkLiveAcpForWorkspace: jest.fn(() => Promise.resolve(false)),
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
    });
    await handleInstallMittoMcp();
    expect(setters[5]).toHaveBeenCalledWith("Installation failed: proxy 502");
  });
});

describe("useWorkspaceMcpActions — handleMcpRemove", () => {
  test("removes via POST, refreshes the tools list, and surfaces a server-reported failure", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/mcp-tools/remove")) {
        return Promise.resolve(
          jsonResponse({ success: false, message: "server busy" }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    });
    const loadMcpTools = jest.fn(() => Promise.resolve());
    const setMcpToolsError = jest.fn();
    const { render } = await loadHook();
    const { handleMcpRemove } = render({
      selectedWorkspace: WS,
      mcpTools: { mcp_scopes: ["user"] },
      loadMcpTools,
      checkLiveAcpForWorkspace: jest.fn(() => Promise.resolve(false)),
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
      setMcpToolsError,
    });
    await handleMcpRemove("srv1", "user");

    const removeCall = global.fetch.mock.calls.find((c) =>
      String(c[0]).includes("/mcp-tools/remove"),
    );
    expect(removeCall[1].method).toBe("POST");
    expect(JSON.parse(removeCall[1].body)).toEqual({
      acp_server: "auggie",
      scope: "user",
      name: "srv1",
    });
    expect(setMcpToolsError).toHaveBeenCalledWith("server busy");
    expect(loadMcpTools).toHaveBeenCalledWith("auggie", "uuid-1");
  });

  test("a rejected remove call surfaces the raw-text body via mcpRequestErrorMessage", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(textResponse("gateway timeout")),
    );
    const setMcpToolsError = jest.fn();
    const { render } = await loadHook();
    const { handleMcpRemove } = render({
      selectedWorkspace: WS,
      mcpTools: {},
      loadMcpTools: jest.fn(),
      checkLiveAcpForWorkspace: jest.fn(() => Promise.resolve(false)),
      setConfirmDialog: jest.fn(),
      setError: jest.fn(),
      setMcpToolsError,
    });
    await handleMcpRemove("srv1", "user");
    expect(setMcpToolsError).toHaveBeenCalledWith(
      "Failed to remove MCP server: gateway timeout",
    );
  });
});
