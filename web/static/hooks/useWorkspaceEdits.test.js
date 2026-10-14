import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

let setters = [];
let effects = [];
let initialValues = [];
global.window = global.window || {};
window.preact = {
  useState: (initial) => {
    const index = setters.length;
    const setter = jest.fn();
    setters.push(setter);
    return [
      initialValues[index] === undefined ? initial : initialValues[index],
      setter,
    ];
  },
  useEffect: (cb, deps) => effects.push({ cb, deps }),
  useCallback: (fn) => fn,
};

async function loadHook(values = []) {
  setters = [];
  effects = [];
  initialValues = values;
  const { useWorkspaceEdits } = await import("./useWorkspaceEdits.js");
  return { useWorkspaceEdits, setters, effects };
}

describe("useWorkspaceEdits model overrides", () => {
  test("saves complete overrides and preserves other-agent settings", async () => {
    const overrides = [
      { acp_server: "agent-b", tag: "Fast", model_id: "b1" },
      { acp_server: "agent-a", tag: "Coding", model_id: "a1" },
      { acp_server: "agent-a", tag: "", model_id: "" },
    ];
    const { useWorkspaceEdits } = await loadHook([
      "agent-a", // editAcpServer
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      overrides,
    ]);
    const { buildWorkspaceEditsFor } = useWorkspaceEdits({
      selectedWorkspace: { uuid: "w1", acp_server: "agent-a" },
      selectedWorkspaceKey: "w1",
      setWorkspaces: jest.fn(),
      getWorkspaceKey: (workspace) => workspace.uuid,
      prevSelectedWorkspaceKeyRef: { current: "w1" },
    });

    const edited = buildWorkspaceEditsFor(
      { uuid: "w1", acp_server: "agent-a" },
      "w1",
    );
    expect(edited.model_overrides).toEqual(overrides.slice(0, 2));
    expect(edited.model_overrides[0]).toEqual(overrides[0]);
  });

  test("loads saved overrides when a workspace is selected", async () => {
    const { useWorkspaceEdits, setters, effects } = await loadHook();
    const saved = [{ acp_server: "agent-a", tag: "Fast", model_id: "a1" }];
    useWorkspaceEdits({
      selectedWorkspace: { uuid: "w1", model_overrides: saved },
      selectedWorkspaceKey: "w1",
      setWorkspaces: jest.fn(),
      getWorkspaceKey: (workspace) => workspace.uuid,
      prevSelectedWorkspaceKeyRef: { current: "w1" },
    });
    effects[0].cb();
    expect(setters[6]).toHaveBeenCalledWith(saved);
  });
});
