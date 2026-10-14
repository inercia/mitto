import { describe, test, expect } from "./testing/testGlobals.js";
import {
  modelCatalogOptions,
  modelOverridesForAgent,
  replaceAgentModelOverrides,
} from "./workspaceModelOverrides.js";

describe("workspace model override helpers", () => {
  test("filters rows by exact ACP server", () => {
    const rows = [
      { acp_server: "agent-a", tag: "Fast", model_id: "a1" },
      { acp_server: "agent-b", tag: "Fast", model_id: "b1" },
    ];
    expect(modelOverridesForAgent(rows, "agent-b")).toEqual([rows[1]]);
  });

  test("replaces one agent rows while preserving other agents and their order", () => {
    const otherBefore = { acp_server: "agent-b", tag: "Cheap", model_id: "b1" };
    const otherAfter = { acp_server: "agent-c", tag: "Fast", model_id: "c1" };
    const rows = [
      otherBefore,
      { acp_server: "agent-a", tag: "Fast", model_id: "a1" },
      otherAfter,
      { acp_server: "agent-a", tag: "Cheap", model_id: "a2" },
    ];
    expect(
      replaceAgentModelOverrides(rows, "agent-a", [
        { tag: "Coding", model_id: "a3" },
      ]),
    ).toEqual([
      otherBefore,
      { acp_server: "agent-a", tag: "Coding", model_id: "a3" },
      otherAfter,
    ]);
  });

  test("retains unavailable saved model IDs as explicit options", () => {
    expect(
      modelCatalogOptions([{ id: "known", name: "Known" }], "retired"),
    ).toEqual([
      { id: "known", name: "Known" },
      { id: "retired", name: "retired (unavailable)", unavailable: true },
    ]);
  });

  test("empty catalog has no options unless preserving a saved selection", () => {
    expect(modelCatalogOptions([])).toEqual([]);
    expect(modelCatalogOptions([], "saved-id")).toEqual([
      { id: "saved-id", name: "saved-id (unavailable)", unavailable: true },
    ]);
  });
});
