import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";

let setters = [];
let effects = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    setters.push(setter);
    return [initial, setter];
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
  const { useWorkspaceModels } = await import("./useWorkspaceModels.js");
  return { useWorkspaceModels, setters, effects };
}

const IDX = { data: 0, loading: 1, error: 2 };

describe("useWorkspaceModels", () => {
  test("loads catalog and normalizes models to an array", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ acp_server: "agent-a", models: null })),
    );
    const { useWorkspaceModels, setters } = await loadHook();
    const { loadModels } = useWorkspaceModels({});
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
    const { loadModels } = useWorkspaceModels({});
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
    const loadEffect = effects.find((effect) => effect.deps?.length === 2);
    loadEffect.cb();
    await Promise.resolve();
    expect(global.fetch).toHaveBeenCalled();
  });
});
