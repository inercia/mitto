/**
 * Tests for useWorkspaceMcpTools.js (mitto-7gta.17 slice S6 Test phase).
 *
 * Covers the 2 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: loadMcpTools (workspaces.listMcpTools) and
 * checkLiveAcpForWorkspace (workspaces.getAcpStatus), plus the reset and
 * MCP-tab load effects that drive them. Mirrors the window.preact stub
 * harness established by useFolderPromptsConfig.test.js (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentSetters = [];
let currentEffects = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
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

function textResponse(text, status = 500) {
  return {
    ok: false,
    status,
    headers: { get: () => "text/plain" },
    text: () => Promise.resolve(text),
    json: () => Promise.reject(new Error("not json")),
  };
}

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useWorkspaceMcpTools.js");
  return {
    useWorkspaceMcpTools: mod.useWorkspaceMcpTools,
    setters: currentSetters,
    effects: currentEffects,
  };
}

const IDX = {
  setMcpTools: 0,
  setMcpToolsLoading: 1,
  setMcpToolsError: 2,
  setHasLiveAcp: 3,
};

describe("useWorkspaceMcpTools — loadMcpTools", () => {
  test("no uuid: sets an error and an empty servers list without fetching", async () => {
    global.fetch = jest.fn();
    const { useWorkspaceMcpTools, setters } = await loadHook();
    const { loadMcpTools } = useWorkspaceMcpTools({});
    await loadMcpTools("auggie", null);
    expect(global.fetch).not.toHaveBeenCalled();
    expect(setters[IDX.setMcpToolsError]).toHaveBeenCalledWith(
      "No workspace selected",
    );
    expect(setters[IDX.setMcpTools]).toHaveBeenCalledWith({
      servers: [],
      agent_name: "",
    });
  });

  test("success: GETs mcp-tools with the acp_server query param", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ servers: [{ name: "s1" }], agent_name: "auggie" }),
      ),
    );
    const { useWorkspaceMcpTools, setters } = await loadHook();
    const { loadMcpTools } = useWorkspaceMcpTools({});
    await loadMcpTools("auggie", "uuid-1");

    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/workspaces/uuid-1/mcp-tools");
    expect(String(url)).toContain("acp_server=auggie");
    expect(setters[IDX.setMcpTools]).toHaveBeenCalledWith({
      servers: [{ name: "s1" }],
      agent_name: "auggie",
    });
  });

  test("a data.error field is surfaced without throwing", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: "agent not installed" })),
    );
    const { useWorkspaceMcpTools, setters } = await loadHook();
    const { loadMcpTools } = useWorkspaceMcpTools({});
    await loadMcpTools("auggie", "uuid-1");
    expect(setters[IDX.setMcpToolsError]).toHaveBeenCalledWith(
      "agent not installed",
    );
  });

  test("failure prefers the raw-text error body over the generic SDK message", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(textResponse("boom: proxy 500")),
    );
    const { useWorkspaceMcpTools, setters } = await loadHook();
    const { loadMcpTools } = useWorkspaceMcpTools({});
    await loadMcpTools("auggie", "uuid-1");
    expect(setters[IDX.setMcpToolsError]).toHaveBeenCalledWith(
      "Failed to load MCP tools: boom: proxy 500",
    );
    expect(setters[IDX.setMcpTools]).toHaveBeenLastCalledWith({
      servers: [],
      agent_name: "",
    });
  });
});

describe("useWorkspaceMcpTools — checkLiveAcpForWorkspace", () => {
  test("no uuid returns false without fetching", async () => {
    global.fetch = jest.fn();
    const { useWorkspaceMcpTools } = await loadHook();
    const { checkLiveAcpForWorkspace } = useWorkspaceMcpTools({});
    expect(await checkLiveAcpForWorkspace(null)).toBe(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("returns true when the server reports alive:true", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ alive: true })),
    );
    const { useWorkspaceMcpTools } = await loadHook();
    const { checkLiveAcpForWorkspace } = useWorkspaceMcpTools({});
    expect(await checkLiveAcpForWorkspace("uuid-1")).toBe(true);
    expect(String(global.fetch.mock.calls[0][0])).toContain(
      "/api/workspaces/uuid-1/acp-status",
    );
  });

  test("a network failure degrades to false, not a thrown rejection", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { useWorkspaceMcpTools } = await loadHook();
    const { checkLiveAcpForWorkspace } = useWorkspaceMcpTools({});
    await expect(checkLiveAcpForWorkspace("uuid-1")).resolves.toBe(false);
  });
});

describe("useWorkspaceMcpTools — effects", () => {
  test("reset effect clears tools/error on selectedWorkspaceKey change", async () => {
    const { useWorkspaceMcpTools, setters, effects } = await loadHook();
    useWorkspaceMcpTools({ selectedWorkspaceKey: "k1" });
    const resetEffect = effects.find((e) => e.deps?.length === 1);
    resetEffect.cb();
    expect(setters[IDX.setMcpTools]).toHaveBeenCalledWith(null);
    expect(setters[IDX.setMcpToolsError]).toHaveBeenCalledWith("");
  });

  test("MCP-tab load effect fires loadMcpTools + checkLiveAcpForWorkspace when active", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ alive: true })),
    );
    const { useWorkspaceMcpTools, setters, effects } = await loadHook();
    useWorkspaceMcpTools({
      activeTab: "mcp",
      selectedWorkspace: { uuid: "uuid-1", acp_server: "auggie" },
      selectedWorkspaceKey: "k1",
      selectedFolder: null,
    });
    const loadEffect = effects.find((e) => e.deps?.length === 3);
    loadEffect.cb();
    await flush();
    expect(global.fetch).toHaveBeenCalled();
    expect(setters[IDX.setHasLiveAcp]).toHaveBeenCalledWith(true);
  });

  test("MCP-tab load effect resets hasLiveAcp to false when a folder is selected", async () => {
    global.fetch = jest.fn();
    const { useWorkspaceMcpTools, setters, effects } = await loadHook();
    useWorkspaceMcpTools({
      activeTab: "mcp",
      selectedWorkspace: { uuid: "uuid-1" },
      selectedFolder: "myfolder",
    });
    const loadEffect = effects.find((e) => e.deps?.length === 3);
    loadEffect.cb();
    expect(setters[IDX.setHasLiveAcp]).toHaveBeenCalledWith(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });
});
