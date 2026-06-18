/**
 * Unit tests for prompt menu utility functions
 */

import {
  promptMenus,
  promptParameters,
  KNOWN_PARAM_TYPES,
  MENU_PARAM_TYPES,
  menuSatisfies,
  collectPromptArguments,
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
});
