import { describe, test, expect } from "./testing/testGlobals.js";
import {
  groupModelOverrides,
  expandModelOverrideGroups,
  modelOverrideTagConflicts,
  modelCatalogOptions,
  modelOverridesForAgent,
  replaceAgentModelOverrides,
} from "./workspaceModelOverrides.js";

describe("workspace model override helpers", () => {
  test("groups five mappings into two exact-model groups and round trips source order", () => {
    const rows = [
      { tag: "Cheap", model_id: "luna" },
      { tag: "Smart", model_id: "sonnet" },
      { tag: "Coding", model_id: "luna" },
      { tag: "Fast", model_id: "luna" },
      { tag: "Smartest", model_id: "sonnet" },
    ];
    const groups = groupModelOverrides(rows);
    expect(groups).toHaveLength(2);
    expect(groups[0].tagCounts.map(({ tag }) => tag)).toEqual([
      "Cheap",
      "Coding",
      "Fast",
    ]);
    expect(expandModelOverrideGroups(groups)).toEqual(rows);
    expect(modelOverrideTagConflicts(groups)).toEqual([]);
  });

  test("preserves exact tag/model identity, duplicate evidence and conflicting precedence", () => {
    const rows = [
      { tag: "Fast", model_id: "Model" },
      { tag: "fast", model_id: "model" },
      { tag: "Fast", model_id: "model" },
      { tag: "Fast", model_id: "Model" },
    ];
    const groups = groupModelOverrides(rows);
    expect(groups).toHaveLength(2);
    expect(expandModelOverrideGroups(groups)).toEqual(rows);
    expect(modelOverrideTagConflicts(groups)).toEqual([
      { tag: "Fast", modelIDs: ["Model", "model"] },
    ]);
  });

  test("keeps incomplete drafts independent and preserves empty input", () => {
    const rows = [
      { tag: "", model_id: "saved" },
      { tag: "", model_id: "saved" },
      { tag: "Legacy", model_id: "" },
      { tag: "", model_id: "" },
    ];
    expect(groupModelOverrides(rows)).toHaveLength(4);
    expect(expandModelOverrideGroups(groupModelOverrides(rows))).toEqual(rows);
    expect(groupModelOverrides(null)).toEqual([]);
    expect(expandModelOverrideGroups(null)).toEqual([]);
    expect(modelOverrideTagConflicts(null)).toEqual([]);
  });

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
