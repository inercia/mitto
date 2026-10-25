import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";

let setters = [];
let effects = [];
let refs = [];
let refCall = 0;
let stateCall = 0;
window.preact = {
  useState: (initial) => {
    if (!setters[stateCall]) setters[stateCall] = jest.fn();
    return [initial, setters[stateCall++]];
  },
  useRef: (initial) => {
    if (!refs[refCall]) refs[refCall] = { current: initial };
    return refs[refCall++];
  },
  useEffect: (cb, deps) => effects.push({ cb, deps }),
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
  setters = [];
  effects = [];
  refs = [];
  refCall = 0;
  stateCall = 0;
  const { useWorkspaceModels } = await import("./useWorkspaceModels.js");
  const render = (props) => {
    refCall = 0;
    stateCall = 0;
    effects = [];
    return useWorkspaceModels(props);
  };
  return { useWorkspaceModels: render, setters, effects: () => effects };
}

const IDX = { data: 0, loading: 1, error: 2 };

describe("useWorkspaceModels", () => {
  test("loads catalog and normalizes models to an array", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ acp_server: "agent-a", models: null })),
    );
    const { useWorkspaceModels, setters } = await loadHook();
    const { loadModels } = useWorkspaceModels({
      selectedWorkspace: { uuid: "workspace-1" },
    });
    await loadModels("workspace-1");
    expect(String(global.fetch.mock.calls[0][0])).toContain(
      "/api/workspaces/workspace-1/models",
    );
    expect(setters[IDX.data]).toHaveBeenCalledWith({
      acp_server: "agent-a",
      models: [],
    });
    expect(setters[IDX.loading]).toHaveBeenLastCalledWith(false);
  });

  test("missing uuid clears data without making a request", async () => {
    global.fetch = jest.fn();
    const { useWorkspaceModels, setters } = await loadHook();
    const { loadModels } = useWorkspaceModels({});
    await loadModels(null);
    expect(global.fetch).not.toHaveBeenCalled();
    expect(setters[IDX.data]).toHaveBeenCalledWith(null);
  });

  test("request failure reports the error and degrades to an empty catalog", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { useWorkspaceModels, setters } = await loadHook();
    const { loadModels } = useWorkspaceModels({
      selectedWorkspace: { uuid: "workspace-1" },
    });
    await loadModels("workspace-1");
    expect(setters[IDX.data]).toHaveBeenCalledWith({ models: [] });
    expect(setters[IDX.error].mock.calls.at(-1)[0]).toContain("offline");
  });

  test("loads automatically for the active Models tab only", async () => {
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({ models: [] })));
    const { useWorkspaceModels, effects } = await loadHook();
    useWorkspaceModels({
      activeTab: "models",
      selectedWorkspace: { uuid: "workspace-1" },
      selectedWorkspaceKey: "key-1",
    });
    const loadEffect = effects()[2];
    loadEffect.cb();
    await Promise.resolve();
    expect(global.fetch).toHaveBeenCalled();
  });

  const ws = { uuid: "workspace-1", acp_server: "a", working_dir: "/p" };
  async function pending() {
    let resolve;
    global.fetch = jest.fn(
      () =>
        new Promise(
          (r) => (resolve = () => r(jsonResponse({ models: [{ id: "m" }] }))),
        ),
    );
    return async () => {
      for (let i = 0; i < 50 && !resolve; i++)
        await new Promise((r) => setTimeout(r, 1));
      resolve();
    };
  }

  for (const [name, next] of [
    ["workspace UUID", { selectedWorkspace: { ...ws, uuid: "workspace-2" } }],
    ["server", { selectedWorkspace: ws, editAcpServer: "b" }],
    ["folder", { selectedWorkspace: { ...ws, working_dir: "/q" } }],
  ]) {
    test(`drops a response that lands after the ${name} changed`, async () => {
      const release = await pending();
      const { useWorkspaceModels, setters } = await loadHook();
      const { loadModels } = useWorkspaceModels({
        selectedWorkspace: ws,
        editAcpServer: "a",
      });
      const p = loadModels("workspace-1");
      useWorkspaceModels({ editAcpServer: "a", ...next });
      await release();
      await p;
      expect(setters[IDX.data]).not.toHaveBeenCalledWith(
        expect.objectContaining({ models: expect.any(Array) }),
      );
      expect(setters[IDX.loading]).not.toHaveBeenLastCalledWith(false);
    });
  }

  test("drops a response that lands after unmount", async () => {
    const release = await pending();
    const { useWorkspaceModels, setters, effects } = await loadHook();
    const { loadModels } = useWorkspaceModels({ selectedWorkspace: ws });
    const cleanup = effects()[0].cb();
    const p = loadModels("workspace-1");
    cleanup();
    await release();
    await p;
    expect(setters[IDX.data]).not.toHaveBeenCalledWith(
      expect.objectContaining({ models: expect.any(Array) }),
    );
  });

  test("applies a response when the scope is unchanged", async () => {
    const release = await pending();
    const { useWorkspaceModels, setters } = await loadHook();
    const { loadModels } = useWorkspaceModels({ selectedWorkspace: ws });
    const p = loadModels("workspace-1");
    await release();
    await p;
    expect(setters[IDX.data]).toHaveBeenLastCalledWith({
      models: [{ id: "m" }],
    });
  });
});
