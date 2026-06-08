/**
 * Unit tests for prompt menu utility functions
 */

import {
  promptMenus,
  promptRequires,
  menuSatisfiesRequires,
  MENU_CAPABILITIES,
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
// promptRequires Tests
// =============================================================================

describe("promptRequires", () => {
  test("returns [] when requires field is absent", () => {
    expect(promptRequires({})).toEqual([]);
  });

  test("returns [] when requires is empty string", () => {
    expect(promptRequires({ requires: "" })).toEqual([]);
  });

  test("returns single capability from non-empty requires", () => {
    expect(promptRequires({ requires: "parameters" })).toEqual(["parameters"]);
  });

  test("returns multiple capabilities when comma-separated", () => {
    expect(promptRequires({ requires: "parameters, context" })).toEqual([
      "parameters",
      "context",
    ]);
  });

  test("trims whitespace around each capability name", () => {
    expect(promptRequires({ requires: " parameters , context " })).toEqual([
      "parameters",
      "context",
    ]);
  });

  test("handles null prompt gracefully", () => {
    expect(promptRequires(null)).toEqual([]);
  });
});

// =============================================================================
// menuSatisfiesRequires Tests
// =============================================================================

describe("menuSatisfiesRequires", () => {
  test("prompt with no requires is satisfied by any menu", () => {
    expect(menuSatisfiesRequires({}, "prompts")).toBe(true);
    expect(menuSatisfiesRequires({}, "conversation")).toBe(true);
    expect(menuSatisfiesRequires({}, "beadsIssues")).toBe(true);
    expect(menuSatisfiesRequires({}, "beadsList")).toBe(true);
  });

  test("beadsIssues menu satisfies 'parameters' requirement", () => {
    expect(menuSatisfiesRequires({ requires: "parameters" }, "beadsIssues")).toBe(true);
  });

  test("prompts menu does NOT satisfy 'parameters' requirement", () => {
    expect(menuSatisfiesRequires({ requires: "parameters" }, "prompts")).toBe(false);
  });

  test("conversation menu does NOT satisfy 'parameters' requirement", () => {
    expect(menuSatisfiesRequires({ requires: "parameters" }, "conversation")).toBe(false);
  });

  test("unknown menu does NOT satisfy any capability requirement", () => {
    expect(menuSatisfiesRequires({ requires: "parameters" }, "unknownMenu")).toBe(false);
  });

  test("returns true for unknown menu when prompt has no requirements", () => {
    expect(menuSatisfiesRequires({ requires: "" }, "unknownMenu")).toBe(true);
  });
});

// =============================================================================
// MENU_CAPABILITIES Tests
// =============================================================================

describe("MENU_CAPABILITIES", () => {
  test("prompts menu has no capabilities", () => {
    expect(MENU_CAPABILITIES.prompts).toEqual([]);
  });

  test("conversation menu has no capabilities", () => {
    expect(MENU_CAPABILITIES.conversation).toEqual([]);
  });

  test("beadsIssues menu has 'parameters' capability", () => {
    expect(MENU_CAPABILITIES.beadsIssues).toContain("parameters");
  });

  test("beadsList menu has no capabilities", () => {
    expect(MENU_CAPABILITIES.beadsList).toEqual([]);
  });
});
