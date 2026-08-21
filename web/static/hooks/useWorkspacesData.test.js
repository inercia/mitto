/**
 * Tests for useWorkspacesData.js (mitto-7gta.17 slice S6 Test phase).
 *
 * Covers the 1 bare-fetch->getSdkClient() call site migrated in the
 * Implementation phase: loadData's supported-runners load
 * (serverConfig.supportedRunners), including the errorStatus(err) ===
 * undefined silent-skip-on-non-2xx / fallback-list-on-network-failure split
 * that replaces the old `if (runnersRes.ok)` guard. Mirrors the
 * window.preact stub harness established by useFolderPromptsConfig.test.js
 * (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentSetters = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
  },
  useCallback: (fn) => fn,
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

async function loadHook() {
  currentSetters = [];
  const configMod = await import("../utils/configCache.js");
  configMod.invalidateConfigCache();
  const mod = await import("./useWorkspacesData.js");
  return {
    useWorkspacesData: mod.useWorkspacesData,
    setters: currentSetters,
  };
}

const IDX = {
  setLoading: 0,
  setError: 1,
  setWorkspaces: 2,
  setAcpServers: 3,
  setModelProfiles: 4,
  setSupportedRunners: 5,
};

const DEFAULT_FALLBACK_RUNNERS = [
  { type: "exec", label: "exec (no restrictions)", supported: true },
  { type: "sandbox-exec", label: "sandbox-exec (macOS)", supported: false },
  { type: "firejail", label: "firejail (Linux)", supported: false },
  { type: "docker", label: "docker (all platforms)", supported: true },
];

const CONFIG_BODY = {
  acp_servers: [{ name: "auggie" }],
  models: [],
  workspaces: [{ working_dir: "/tmp/ws1", acp_server: "auggie" }],
};

describe("useWorkspacesData — loadData supported-runners", () => {
  test("success: GETs supported-runners and stores the returned list", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/supported-runners")) {
        return Promise.resolve(
          jsonResponse([{ type: "docker", supported: true }]),
        );
      }
      return Promise.resolve(jsonResponse(CONFIG_BODY));
    });
    const { useWorkspacesData, setters } = await loadHook();
    const { loadData } = useWorkspacesData({
      prevSelectedWorkspaceKeyRef: { current: null },
      selectedWorkspaceKey: null,
      setSelectedWorkspaceKey: jest.fn(),
      setSelectedFolder: jest.fn(),
      getWorkspaceKey: (ws) => ws.working_dir,
    });
    await loadData();
    expect(setters[IDX.setSupportedRunners]).toHaveBeenCalledWith([
      { type: "docker", supported: true },
    ]);
  });

  test("a non-2xx status is silently skipped in favor of the fallback list (mirrors the old res.ok guard)", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/supported-runners")) {
        return Promise.resolve(jsonResponse({ error: "forbidden" }, 403));
      }
      return Promise.resolve(jsonResponse(CONFIG_BODY));
    });
    const { useWorkspacesData, setters } = await loadHook();
    const { loadData } = useWorkspacesData({
      prevSelectedWorkspaceKeyRef: { current: null },
      selectedWorkspaceKey: null,
      setSelectedWorkspaceKey: jest.fn(),
      setSelectedFolder: jest.fn(),
      getWorkspaceKey: (ws) => ws.working_dir,
    });
    await loadData();
    expect(setters[IDX.setSupportedRunners]).toHaveBeenCalledWith(
      DEFAULT_FALLBACK_RUNNERS,
    );
    // A silently-skipped non-2xx must NOT set the top-level error banner —
    // only a network-level failure (Promise.all's outer catch) does that.
    expect(setters[IDX.setError]).not.toHaveBeenCalled();
  });

  test("a network-level failure propagates to the outer catch and sets the error banner", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/supported-runners")) {
        return Promise.reject(new Error("offline"));
      }
      return Promise.resolve(jsonResponse(CONFIG_BODY));
    });
    const { useWorkspacesData, setters } = await loadHook();
    const { loadData } = useWorkspacesData({
      prevSelectedWorkspaceKeyRef: { current: null },
      selectedWorkspaceKey: null,
      setSelectedWorkspaceKey: jest.fn(),
      setSelectedFolder: jest.fn(),
      getWorkspaceKey: (ws) => ws.working_dir,
    });
    await loadData();
    // transport.js wraps a rejected fetch as
    // `Request to <path> failed: <original message>` before the SDK's
    // MittoNetworkError reaches this hook's outer catch.
    expect(setters[IDX.setError]).toHaveBeenCalledWith(
      "Failed to load configuration: Request to /api/supported-runners failed: offline",
    );
    // The whole Promise.all rejected, so config-derived state is never set.
    expect(setters[IDX.setWorkspaces]).not.toHaveBeenCalled();
  });
});
