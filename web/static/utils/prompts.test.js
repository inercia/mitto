/**
 * Unit tests for prompt menu utility functions
 */

import { jest } from "@jest/globals";
import {
  promptMenus,
  promptParameters,
  KNOWN_PARAM_TYPES,
  MENU_PARAM_TYPES,
  menuSatisfies,
  collectPromptArguments,
  getMissingPromptParameters,
  autofillConversationMenuArgs,
  isCacheableParam,
  fetchCachedParamNames,
  effectiveMissingParams,
  resolvePromptModelOverride,
  currentModelName,
} from "./prompts.js";

// =============================================================================
// promptMenus Tests
// =============================================================================

describe("promptMenus", () => {
  test("returns ['prompts'] when menus field is absent", () => {
    expect(promptMenus({})).toEqual(["prompts"]);
  });

  test("returns ['prompts'] when menus is empty string", () => {
    expect(promptMenus({ menus: "" })).toEqual(["prompts"]);
  });

  test("returns ['prompts'] when menus is whitespace only", () => {
    expect(promptMenus({ menus: "   " })).toEqual(["prompts"]);
  });

  test("returns single menu from non-empty menus field", () => {
    expect(promptMenus({ menus: "conversation" })).toEqual(["conversation"]);
  });

  test("returns multiple menus when comma-separated", () => {
    expect(promptMenus({ menus: "prompts, conversation" })).toEqual([
      "prompts",
      "conversation",
    ]);
  });

  test("trims whitespace around each menu name", () => {
    expect(promptMenus({ menus: " prompts , beadsIssues " })).toEqual([
      "prompts",
      "beadsIssues",
    ]);
  });

  test("filters out empty entries from comma list", () => {
    expect(promptMenus({ menus: "prompts,,conversation" })).toEqual([
      "prompts",
      "conversation",
    ]);
  });

  test("handles null prompt gracefully", () => {
    expect(promptMenus(null)).toEqual(["prompts"]);
  });

  test("handles undefined prompt gracefully", () => {
    expect(promptMenus(undefined)).toEqual(["prompts"]);
  });
});

// =============================================================================
// promptParameters Tests
// =============================================================================

describe("promptParameters", () => {
  test("returns [] when parameters field is absent", () => {
    expect(promptParameters({})).toEqual([]);
  });

  test("returns [] when parameters is an empty array", () => {
    expect(promptParameters({ parameters: [] })).toEqual([]);
  });

  test("returns the parameters array when non-empty", () => {
    const params = [{ name: "ISSUE_ID", type: "beadsId" }];
    expect(promptParameters({ parameters: params })).toEqual(params);
  });

  test("returns [] for null prompt", () => {
    expect(promptParameters(null)).toEqual([]);
  });

  test("returns [] for undefined prompt", () => {
    expect(promptParameters(undefined)).toEqual([]);
  });

  test("returns [] when parameters is not an array", () => {
    expect(promptParameters({ parameters: "beadsId" })).toEqual([]);
  });
});

// =============================================================================
// KNOWN_PARAM_TYPES Tests
// =============================================================================

describe("KNOWN_PARAM_TYPES", () => {
  test("includes beadsId", () => {
    expect(KNOWN_PARAM_TYPES).toContain("beadsId");
  });

  test("includes beadsTitle", () => {
    expect(KNOWN_PARAM_TYPES).toContain("beadsTitle");
  });

  test("includes sessionId", () => {
    expect(KNOWN_PARAM_TYPES).toContain("sessionId");
  });

  test("includes workspaceId", () => {
    expect(KNOWN_PARAM_TYPES).toContain("workspaceId");
  });

  test("includes workspaceFolder", () => {
    expect(KNOWN_PARAM_TYPES).toContain("workspaceFolder");
  });

  test("includes text", () => {
    expect(KNOWN_PARAM_TYPES).toContain("text");
  });

  test("includes boolean", () => {
    expect(KNOWN_PARAM_TYPES).toContain("boolean");
  });
});

// =============================================================================
// MENU_PARAM_TYPES Tests
// =============================================================================

describe("MENU_PARAM_TYPES", () => {
  test("prompts menu provides no types", () => {
    expect(MENU_PARAM_TYPES.prompts).toEqual([]);
  });

  test("promptsPeriodic menu provides no types", () => {
    expect(MENU_PARAM_TYPES.promptsPeriodic).toEqual([]);
  });

  test("conversation menu provides no types", () => {
    expect(MENU_PARAM_TYPES.conversation).toEqual([]);
  });

  test("beadsIssues menu provides beadsId and beadsTitle", () => {
    expect(MENU_PARAM_TYPES.beadsIssues).toContain("beadsId");
    expect(MENU_PARAM_TYPES.beadsIssues).toContain("beadsTitle");
  });

  test("beadsList menu provides no types", () => {
    expect(MENU_PARAM_TYPES.beadsList).toEqual([]);
  });
});

// =============================================================================
// menuSatisfies Tests
// =============================================================================

describe("menuSatisfies", () => {
  test("prompt with no parameters is satisfied by any known menu", () => {
    expect(menuSatisfies({}, "prompts")).toBe(true);
    expect(menuSatisfies({}, "conversation")).toBe(true);
    expect(menuSatisfies({}, "beadsIssues")).toBe(true);
    expect(menuSatisfies({}, "beadsList")).toBe(true);
  });

  test("prompt with no parameters is satisfied by an unknown menu", () => {
    expect(menuSatisfies({}, "unknownMenu")).toBe(true);
  });

  test("beadsId prompt is satisfied by beadsIssues menu", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
  });

  test("beadsId prompt is NOT satisfied by prompts menu", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(menuSatisfies(prompt, "prompts")).toBe(false);
  });

  test("beadsId prompt is NOT satisfied by conversation menu", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
  });

  test("beadsId prompt is NOT satisfied by an unknown menu", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(menuSatisfies(prompt, "unknownMenu")).toBe(false);
  });

  test("prompt requiring beadsId and beadsTitle is satisfied by beadsIssues", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "TITLE", type: "beadsTitle" },
      ],
    };
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
  });

  test("prompt requiring beadsId and beadsTitle is NOT satisfied by prompts", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "TITLE", type: "beadsTitle" },
      ],
    };
    expect(menuSatisfies(prompt, "prompts")).toBe(false);
  });

  // --- Optional parameter (required: false) gating tests ---

  test("optional beadsId param (required: false) is satisfied by beadsIssues menu", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
  });

  test("optional beadsId param (required: false) is ALSO satisfied by conversation menu", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(menuSatisfies(prompt, "conversation")).toBe(true);
  });

  test("optional beadsId param (required: false) is ALSO satisfied by prompts menu", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(menuSatisfies(prompt, "prompts")).toBe(true);
  });

  test("required beadsId param (required: true) still gates — NOT satisfied by conversation", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: true }],
    };
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
  });

  test("unset required (no required field) beadsId still gates — NOT satisfied by conversation", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId" }],
    };
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
  });

  test("mixed: required param gates, optional param does not — only the required type determines satisfaction", () => {
    // required beadsId gates; optional text does not affect gating
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: true },
        { name: "EXTRA", type: "text", required: false },
      ],
    };
    // beadsIssues supplies beadsId → satisfies the required gate, optional text ignored
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    // conversation cannot supply beadsId → fails on the required param
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
  });

  test("boolean param never gates — satisfied by any menu even when required", () => {
    const prompt = {
      parameters: [{ name: "Commit", type: "boolean", required: true }],
    };
    expect(menuSatisfies(prompt, "prompts")).toBe(true);
    expect(menuSatisfies(prompt, "conversation")).toBe(true);
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    expect(menuSatisfies(prompt, "unknownMenu")).toBe(true);
  });

  test("boolean alongside a required gating param does not relax that gate", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: true },
        { name: "Commit", type: "boolean" },
      ],
    };
    // boolean is satisfied everywhere, but beadsId still gates conversation
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
  });
});

// =============================================================================
// collectPromptArguments Tests
// =============================================================================

describe("collectPromptArguments", () => {
  test("returns empty object for prompt with no parameters", () => {
    expect(collectPromptArguments({}, { beadsId: "mitto-42" })).toEqual({});
  });

  test("maps beadsId type to the correct param name", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(collectPromptArguments(prompt, { beadsId: "mitto-42" })).toEqual({
      ISSUE_ID: "mitto-42",
    });
  });

  test("maps beadsTitle type to the correct param name", () => {
    const prompt = { parameters: [{ name: "TITLE", type: "beadsTitle" }] };
    expect(
      collectPromptArguments(prompt, { beadsTitle: "Fix the bug" })
    ).toEqual({ TITLE: "Fix the bug" });
  });

  test("maps both beadsId and beadsTitle when both are supplied", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "ISSUE_TITLE", type: "beadsTitle" },
      ],
    };
    expect(
      collectPromptArguments(prompt, {
        beadsId: "mitto-42",
        beadsTitle: "Fix the bug",
      })
    ).toEqual({ ISSUE_ID: "mitto-42", ISSUE_TITLE: "Fix the bug" });
  });

  test("ignores parameter types not present in typeValues", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "TITLE", type: "beadsTitle" },
      ],
    };
    // Only beadsId is supplied; beadsTitle is absent
    expect(collectPromptArguments(prompt, { beadsId: "mitto-42" })).toEqual({
      ISSUE_ID: "mitto-42",
    });
  });

  test("ignores parameter types whose value is null", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(collectPromptArguments(prompt, { beadsId: null })).toEqual({});
  });

  test("ignores parameter types whose value is undefined", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(
      collectPromptArguments(prompt, { beadsId: undefined })
    ).toEqual({});
  });

  test("returns empty object when typeValues is empty", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(collectPromptArguments(prompt, {})).toEqual({});
  });

  test("optional beadsId param (required: false) still auto-fills when value is provided", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(collectPromptArguments(prompt, { beadsId: "mitto-42" })).toEqual({
      ISSUE_ID: "mitto-42",
    });
  });

  test("optional beadsId param produces empty result when value is not provided", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(collectPromptArguments(prompt, {})).toEqual({});
  });
});

// =============================================================================
// autofillConversationMenuArgs Tests
// =============================================================================

describe("autofillConversationMenuArgs", () => {
  const childParamPrompt = {
    parameters: [{ name: "TARGET_CONVERSATION", type: "childSessionId" }],
  };

  test("returns {} when hostSessionId is missing", () => {
    expect(autofillConversationMenuArgs(childParamPrompt, "", [])).toEqual({});
  });

  test("returns {} when prompt has no parameters", () => {
    expect(autofillConversationMenuArgs({}, "host-1", [])).toEqual({});
  });

  test("fills a childSessionId param when host has exactly one child", () => {
    const sessions = [
      { session_id: "child-1", parent_session_id: "host-1" },
      { session_id: "other", parent_session_id: "host-2" },
    ];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions)
    ).toEqual({ TARGET_CONVERSATION: "child-1" });
  });

  test("does not fill when host has multiple children", () => {
    const sessions = [
      { session_id: "child-1", parent_session_id: "host-1" },
      { session_id: "child-2", parent_session_id: "host-1" },
    ];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions)
    ).toEqual({});
  });

  test("does not fill when host has no children", () => {
    const sessions = [{ session_id: "child-1", parent_session_id: "host-2" }];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions)
    ).toEqual({});
  });

  test("ignores archived children when counting", () => {
    const sessions = [
      { session_id: "child-1", parent_session_id: "host-1" },
      { session_id: "child-2", parent_session_id: "host-1", archived: true },
    ];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions)
    ).toEqual({ TARGET_CONVERSATION: "child-1" });
  });

  test("does not fill non-childSessionId param types", () => {
    const prompt = {
      parameters: [{ name: "TARGET", type: "sessionId" }],
    };
    const sessions = [{ session_id: "child-1", parent_session_id: "host-1" }];
    expect(autofillConversationMenuArgs(prompt, "host-1", sessions)).toEqual({});
  });
});

// =============================================================================
// getMissingPromptParameters Tests
// =============================================================================

describe("getMissingPromptParameters", () => {
  test("prompt with no parameters returns []", () => {
    expect(getMissingPromptParameters({}, "beadsIssues")).toEqual([]);
  });

  test("all parameters auto-filled by menu returns []", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "TITLE", type: "beadsTitle" },
      ],
    };
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([]);
  });

  test("none auto-filled (text param in prompts menu) returns all params", () => {
    const params = [{ name: "MSG", type: "text" }];
    const prompt = { parameters: params };
    expect(getMissingPromptParameters(prompt, "prompts")).toEqual(params);
  });

  test("none auto-filled in unknown menu returns all params in declared order", () => {
    const params = [
      { name: "ISSUE_ID", type: "beadsId" },
      { name: "MSG", type: "text" },
    ];
    const prompt = { parameters: params };
    expect(getMissingPromptParameters(prompt, "prompts")).toEqual(params);
  });

  test("mix of auto-filled and free params returns only free ones in order", () => {
    const beadsIdParam = { name: "ISSUE_ID", type: "beadsId" };
    const textParam = { name: "MSG", type: "text" };
    const prompt = { parameters: [beadsIdParam, textParam] };
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([
      textParam,
    ]);
  });

  test("unknown parameter type is treated as missing", () => {
    const param = { name: "FOO", type: "unknownType" };
    const prompt = { parameters: [param] };
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([param]);
  });

  test("unknown menu value causes all params to be treated as missing", () => {
    const params = [
      { name: "ISSUE_ID", type: "beadsId" },
      { name: "TITLE", type: "beadsTitle" },
    ];
    const prompt = { parameters: params };
    expect(getMissingPromptParameters(prompt, "unknownMenu")).toEqual(params);
  });

  test("missing menu argument causes all params to be treated as missing", () => {
    const params = [{ name: "ISSUE_ID", type: "beadsId" }];
    const prompt = { parameters: params };
    expect(getMissingPromptParameters(prompt, undefined)).toEqual(params);
  });

  test("returned objects preserve the required field (required + optional)", () => {
    const requiredParam = { name: "QUERY", type: "text", required: true };
    const optionalParam = { name: "NOTES", type: "text" };
    const prompt = { parameters: [requiredParam, optionalParam] };
    const result = getMissingPromptParameters(prompt, "prompts");
    expect(result).toHaveLength(2);
    expect(result[0]).toBe(requiredParam);
    expect(result[0].required).toBe(true);
    expect(result[1]).toBe(optionalParam);
    expect(result[1].required).toBeUndefined();
  });

  test("preserves declared parameter order in the result", () => {
    const p1 = { name: "ALPHA", type: "text" };
    const p2 = { name: "BETA", type: "sessionId" };
    const p3 = { name: "GAMMA", type: "workspaceId" };
    const prompt = { parameters: [p1, p2, p3] };
    expect(getMissingPromptParameters(prompt, "prompts")).toEqual([p1, p2, p3]);
  });

  // --- Optional parameter (required: false) missing-param tests ---

  test("optional beadsId param in conversation menu is NOT missing (no form shown)", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    // conversation cannot supply beadsId, but it's optional → not missing
    expect(getMissingPromptParameters(prompt, "conversation")).toEqual([]);
  });

  test("optional beadsId param in beadsIssues menu is NOT missing (auto-filled)", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    // beadsIssues supplies beadsId and it's optional → also not in missing list
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([]);
  });

  test("required beadsId param in conversation menu IS missing (form shown)", () => {
    const param = { name: "ISSUE_ID", type: "beadsId", required: true };
    const prompt = { parameters: [param] };
    expect(getMissingPromptParameters(prompt, "conversation")).toEqual([param]);
  });

  test("unset required beadsId param in conversation menu IS missing (form shown)", () => {
    const param = { name: "ISSUE_ID", type: "beadsId" };
    const prompt = { parameters: [param] };
    expect(getMissingPromptParameters(prompt, "conversation")).toEqual([param]);
  });

  test("mixed: only required unsupplied params appear in missing list", () => {
    const requiredParam = { name: "ISSUE_ID", type: "beadsId", required: true };
    const optionalParam = { name: "EXTRA", type: "text", required: false };
    const prompt = { parameters: [requiredParam, optionalParam] };
    // prompts menu supplies nothing; required beadsId is missing, optional text is not
    expect(getMissingPromptParameters(prompt, "prompts")).toEqual([requiredParam]);
  });

  test("boolean param is ALWAYS missing (collected via checkbox) in every menu", () => {
    const param = { name: "Commit", type: "boolean" };
    const prompt = { parameters: [param] };
    expect(getMissingPromptParameters(prompt, "prompts")).toEqual([param]);
    expect(getMissingPromptParameters(prompt, "conversation")).toEqual([param]);
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([param]);
  });

  test("boolean param is collected even when marked required:false", () => {
    const param = { name: "Commit", type: "boolean", required: false };
    const prompt = { parameters: [param] };
    // required:false would normally suppress it, but boolean overrides that
    expect(getMissingPromptParameters(prompt, "conversation")).toEqual([param]);
  });

  test("mixed boolean + auto-supplied param: boolean still collected, supplied one excluded", () => {
    const boolParam = { name: "Commit", type: "boolean" };
    const issueParam = { name: "ISSUE_ID", type: "beadsId", required: true };
    const prompt = { parameters: [issueParam, boolParam] };
    // beadsIssues supplies beadsId → only the boolean remains to be collected
    expect(getMissingPromptParameters(prompt, "beadsIssues")).toEqual([boolParam]);
  });
});

// =============================================================================
// isCacheableParam Tests
// =============================================================================

describe("isCacheableParam", () => {
  test("returns true when param has a cache block", () => {
    expect(isCacheableParam({ name: "X", cache: {} })).toBe(true);
  });

  test("returns true when cache block has destination+ttl", () => {
    expect(isCacheableParam({ name: "X", cache: { destination: "memory", ttl: "1h" } })).toBe(true);
  });

  test("returns false when param has no cache field", () => {
    expect(isCacheableParam({ name: "X", type: "string" })).toBe(false);
  });

  test("returns false when cache is null", () => {
    expect(isCacheableParam({ name: "X", cache: null })).toBe(false);
  });

  test("returns false for null param", () => {
    expect(isCacheableParam(null)).toBe(false);
  });

  test("returns false for undefined param", () => {
    expect(isCacheableParam(undefined)).toBe(false);
  });
});

// =============================================================================
// effectiveMissingParams Tests
// =============================================================================

describe("effectiveMissingParams", () => {
  const cacheableA = { name: "A", type: "string", cache: { destination: "memory" } };
  const cacheableB = { name: "B", type: "string", cache: { destination: "memory" } };
  const nonCacheable = { name: "C", type: "string" };

  test("removes a cacheable param whose name is in the cached Set", () => {
    const result = effectiveMissingParams([cacheableA, nonCacheable], new Set(["A"]));
    expect(result).toEqual([nonCacheable]);
  });

  test("keeps a cacheable param whose name is NOT in the cached set", () => {
    const result = effectiveMissingParams([cacheableA], new Set(["Z"]));
    expect(result).toEqual([cacheableA]);
  });

  test("keeps a non-cacheable param even if its name is in the cached set", () => {
    const result = effectiveMissingParams([nonCacheable], new Set(["C"]));
    expect(result).toEqual([nonCacheable]);
  });

  test("accepts an array for cachedNames in addition to a Set", () => {
    const result = effectiveMissingParams([cacheableA, cacheableB], ["A"]);
    expect(result).toEqual([cacheableB]);
  });

  test("accepts empty array for cachedNames — nothing removed", () => {
    const result = effectiveMissingParams([cacheableA], []);
    expect(result).toEqual([cacheableA]);
  });

  test("accepts null cachedNames — treated as empty, nothing removed", () => {
    const result = effectiveMissingParams([cacheableA], null);
    expect(result).toEqual([cacheableA]);
  });

  test("returns empty array when missing is empty", () => {
    expect(effectiveMissingParams([], new Set(["A"]))).toEqual([]);
  });

  test("returns empty array when missing is null", () => {
    expect(effectiveMissingParams(null, new Set(["A"]))).toEqual([]);
  });

  test("removes all cacheable params when all are cached", () => {
    const result = effectiveMissingParams([cacheableA, cacheableB, nonCacheable], new Set(["A", "B"]));
    expect(result).toEqual([nonCacheable]);
  });
});

// =============================================================================
// fetchCachedParamNames Tests
// =============================================================================

describe("fetchCachedParamNames", () => {
  test("returns Set with cached names on ok response", async () => {
    const fetchImpl = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ cached: ["A", "B"] }),
    });
    const result = await fetchCachedParamNames("sess-1", "my-prompt", { fetchImpl });
    expect(result).toEqual(new Set(["A", "B"]));
  });

  test("passes URL containing /prompt-arg-cache and prompt= to fetchImpl", async () => {
    const fetchImpl = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ cached: [] }),
    });
    await fetchCachedParamNames("sess-1", "my-prompt", { fetchImpl });
    const calledUrl = fetchImpl.mock.calls[0][0];
    expect(calledUrl).toContain("/prompt-arg-cache");
    expect(calledUrl).toContain("prompt=");
    expect(calledUrl).toContain("my-prompt");
  });

  test("returns empty Set on non-ok response", async () => {
    const fetchImpl = jest.fn().mockResolvedValue({ ok: false });
    const result = await fetchCachedParamNames("sess-1", "my-prompt", { fetchImpl });
    expect(result).toEqual(new Set());
  });

  test("returns empty Set and does not throw when fetchImpl throws", async () => {
    const fetchImpl = jest.fn().mockRejectedValue(new Error("network error"));
    const result = await fetchCachedParamNames("sess-1", "my-prompt", { fetchImpl });
    expect(result).toEqual(new Set());
  });

  test("returns empty Set and does NOT call fetchImpl when sessionId is missing", async () => {
    const fetchImpl = jest.fn();
    const result = await fetchCachedParamNames("", "my-prompt", { fetchImpl });
    expect(result).toEqual(new Set());
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  test("returns empty Set and does NOT call fetchImpl when promptName is missing", async () => {
    const fetchImpl = jest.fn();
    const result = await fetchCachedParamNames("sess-1", "", { fetchImpl });
    expect(result).toEqual(new Set());
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  test("returns empty Set when response json has no cached array", async () => {
    const fetchImpl = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ prompt: "x" }),
    });
    const result = await fetchCachedParamNames("sess-1", "x", { fetchImpl });
    expect(result).toEqual(new Set());
  });
});

// =============================================================================
// resolvePromptModelOverride / currentModelName Tests
// =============================================================================

describe("resolvePromptModelOverride", () => {
  const modelOption = {
    current_value: "claude-opus-4-8",
    options: [
      { value: "claude-opus-4-8", name: "Opus 4.8" },
      { value: "claude-sonnet-4-5", name: "Sonnet 4.5" },
      { value: "gpt-4o", name: "GPT-4o" },
    ],
  };

  test("returns the override model when a pattern resolves to a different model", () => {
    const result = resolvePromptModelOverride(["*sonnet*"], modelOption);
    expect(result).toEqual({ value: "claude-sonnet-4-5", name: "Sonnet 4.5" });
  });

  test("matches against the display name as well as the id", () => {
    const result = resolvePromptModelOverride(["*gpt-4o*"], modelOption);
    expect(result).toEqual({ value: "gpt-4o", name: "GPT-4o" });
  });

  test("returns null when the current model already satisfies a pattern (no switch)", () => {
    expect(resolvePromptModelOverride(["*opus*"], modelOption)).toBeNull();
  });

  test("current-model-first: a later pattern matching current does not stop an earlier match", () => {
    // First pattern matches sonnet (not current), so it wins before opus is considered.
    const result = resolvePromptModelOverride(["*sonnet*", "*opus*"], modelOption);
    expect(result).toEqual({ value: "claude-sonnet-4-5", name: "Sonnet 4.5" });
  });

  test("current model wins when it matches the first pattern", () => {
    // Current (opus) matches the first pattern → no override even though sonnet exists.
    expect(
      resolvePromptModelOverride(["*opus*", "*sonnet*"], modelOption),
    ).toBeNull();
  });

  test("walks patterns in order and skips patterns with no available match", () => {
    const result = resolvePromptModelOverride(
      ["*flash*", "*sonnet*"],
      modelOption,
    );
    expect(result).toEqual({ value: "claude-sonnet-4-5", name: "Sonnet 4.5" });
  });

  test("is case-insensitive", () => {
    const result = resolvePromptModelOverride(["*SONNET*"], modelOption);
    expect(result).toEqual({ value: "claude-sonnet-4-5", name: "Sonnet 4.5" });
  });

  test("returns null when nothing matches", () => {
    expect(resolvePromptModelOverride(["*nope*"], modelOption)).toBeNull();
  });

  test("returns null for empty/absent preferredModels", () => {
    expect(resolvePromptModelOverride([], modelOption)).toBeNull();
    expect(resolvePromptModelOverride(undefined, modelOption)).toBeNull();
  });

  test("returns null when modelOption is absent or has no options", () => {
    expect(resolvePromptModelOverride(["*sonnet*"], null)).toBeNull();
    expect(
      resolvePromptModelOverride(["*sonnet*"], { current_value: "x", options: [] }),
    ).toBeNull();
  });
});

describe("currentModelName", () => {
  const modelOption = {
    current_value: "claude-opus-4-8",
    options: [
      { value: "claude-opus-4-8", name: "Opus 4.8" },
      { value: "claude-sonnet-4-5", name: "Sonnet 4.5" },
    ],
  };

  test("returns the display name of the current model", () => {
    expect(currentModelName(modelOption)).toBe("Opus 4.8");
  });

  test("falls back to the value when the name is missing", () => {
    expect(
      currentModelName({ current_value: "x", options: [{ value: "x" }] }),
    ).toBe("x");
  });

  test("returns empty string when unavailable", () => {
    expect(currentModelName(null)).toBe("");
    expect(currentModelName({ current_value: "x", options: [] })).toBe("");
  });
});
