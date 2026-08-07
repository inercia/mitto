/**
 * Unit tests for prompt menu utility functions
 */

import { mockFn } from "./testing/mockFn.js";
import {
  promptMenus,
  promptMenuExcludes,
  promptMenuIncludes,
  isSingletonPrompt,
  promptLoopMode,
  promptLoopIsToggleable,
  promptLoopDefaultOn,
  promptResolveAsLoop,
  promptParameters,
  KNOWN_PARAM_TYPES,
  MENU_PARAM_TYPES,
  menuSatisfies,
  collectPromptArguments,
  shouldOpenPromptDialog,
  promptDialogParameters,
  isAlwaysShownParam,
  isNeverShownParam,
  autofillConversationMenuArgs,
  isBooleanParam,
  isInteractivePickerParam,
  isOptionsPickerParam,
  isCacheableParam,
  fetchCachedParamNames,
  resolvePromptModelOverride,
  currentModelName,
  groupDialogParameters,
  unmetRequiredByGroup,
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
// isSingletonPrompt Tests
// =============================================================================

describe("isSingletonPrompt", () => {
  test("returns true when singleton is true", () => {
    expect(isSingletonPrompt({ singleton: true })).toBe(true);
  });

  test("returns false when singleton is false", () => {
    expect(isSingletonPrompt({ singleton: false })).toBe(false);
  });

  test("returns false when singleton is absent", () => {
    expect(isSingletonPrompt({})).toBe(false);
  });

  test("returns false for null prompt", () => {
    expect(isSingletonPrompt(null)).toBe(false);
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

  test("includes prompts", () => {
    expect(KNOWN_PARAM_TYPES).toContain("prompts");
  });

  test("includes filename", () => {
    expect(KNOWN_PARAM_TYPES).toContain("filename");
  });
});

// =============================================================================
// MENU_PARAM_TYPES Tests
// =============================================================================

describe("MENU_PARAM_TYPES", () => {
  test("prompts menu provides no types", () => {
    expect(MENU_PARAM_TYPES.prompts).toEqual([]);
  });

  test("promptsLoop menu provides no types", () => {
    expect(MENU_PARAM_TYPES.promptsLoop).toEqual([]);
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

  test("prompts param never gates — satisfied by any menu even when required", () => {
    const prompt = {
      parameters: [{ name: "P", type: "prompts", required: true }],
    };
    expect(menuSatisfies(prompt, "prompts")).toBe(true);
    expect(menuSatisfies(prompt, "conversation")).toBe(true);
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    expect(menuSatisfies(prompt, "unknownMenu")).toBe(true);
  });

  // mitto-cwz.1: a required type:text param with a declared options array
  // must not gate menu visibility (it is collected via the dialog's dropdown,
  // never auto-supplied by a menu) — previously it was misclassified as a
  // plain required text field and hid the prompt from every menu.
  test("required text+options param never gates — satisfied by any menu", () => {
    const prompt = {
      parameters: [
        { name: "Mode", type: "text", options: ["a", "b"], required: true },
      ],
    };
    expect(menuSatisfies(prompt, "prompts")).toBe(true);
    expect(menuSatisfies(prompt, "conversation")).toBe(true);
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    expect(menuSatisfies(prompt, "unknownMenu")).toBe(true);
  });

  test("text+options alongside a required gating param does not relax that gate", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: true },
        { name: "Mode", type: "text", options: ["a", "b"] },
      ],
    };
    expect(menuSatisfies(prompt, "beadsIssues")).toBe(true);
    expect(menuSatisfies(prompt, "conversation")).toBe(false);
  });
});

// =============================================================================
// isOptionsPickerParam Tests (mitto-cwz.1)
// =============================================================================

describe("isOptionsPickerParam", () => {
  test("returns true for type:text with a non-empty options array", () => {
    expect(isOptionsPickerParam({ type: "text", options: ["a", "b"] })).toBe(
      true,
    );
  });

  test("returns false for type:text with an empty options array", () => {
    expect(isOptionsPickerParam({ type: "text", options: [] })).toBe(false);
  });

  test("returns false for type:text with no options field", () => {
    expect(isOptionsPickerParam({ type: "text" })).toBe(false);
  });

  test("returns false for type:text when options is not an array", () => {
    expect(isOptionsPickerParam({ type: "text", options: "a,b" })).toBe(false);
  });

  test("returns false for a non-text type even with a non-empty options array", () => {
    expect(isOptionsPickerParam({ type: "beadsId", options: ["a", "b"] })).toBe(
      false,
    );
  });

  test("returns false for undefined/null/no type", () => {
    expect(isOptionsPickerParam(undefined)).toBe(false);
    expect(isOptionsPickerParam(null)).toBe(false);
    expect(isOptionsPickerParam({})).toBe(false);
  });
});

// =============================================================================
// isInteractivePickerParam Tests
// =============================================================================

describe("isInteractivePickerParam", () => {
  test("returns true for boolean", () => {
    expect(isInteractivePickerParam({ type: "boolean" })).toBe(true);
  });

  test("returns true for prompts", () => {
    expect(isInteractivePickerParam({ type: "prompts" })).toBe(true);
  });

  test("returns true for filename", () => {
    expect(isInteractivePickerParam({ type: "filename" })).toBe(true);
  });

  test("returns true for dirname", () => {
    expect(isInteractivePickerParam({ type: "dirname" })).toBe(true);
  });

  test("returns true for workspaceFolder", () => {
    expect(isInteractivePickerParam({ type: "workspaceFolder" })).toBe(true);
  });

  // mitto-cwz.1: type:text with a non-empty options array renders as a
  // dropdown picker and must be treated the same as the other picker types.
  test("returns true for text with a non-empty options array", () => {
    expect(
      isInteractivePickerParam({ type: "text", options: ["a", "b"] }),
    ).toBe(true);
  });

  // Regression pin (mitto-cwz.1): a text+options fix must not accidentally
  // widen plain free-text params into pickers.
  test("returns false for text with an empty options array", () => {
    expect(isInteractivePickerParam({ type: "text", options: [] })).toBe(false);
  });

  test("returns false for text", () => {
    expect(isInteractivePickerParam({ type: "text" })).toBe(false);
  });

  test("returns false for beadsId", () => {
    expect(isInteractivePickerParam({ type: "beadsId" })).toBe(false);
  });

  test("returns false for undefined/null/no type", () => {
    expect(isInteractivePickerParam(undefined)).toBe(false);
    expect(isInteractivePickerParam(null)).toBe(false);
    expect(isInteractivePickerParam({})).toBe(false);
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
      collectPromptArguments(prompt, { beadsTitle: "Fix the bug" }),
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
      }),
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
    expect(collectPromptArguments(prompt, { beadsId: undefined })).toEqual({});
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
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions),
    ).toEqual({ TARGET_CONVERSATION: "child-1" });
  });

  test("does not fill when host has multiple children", () => {
    const sessions = [
      { session_id: "child-1", parent_session_id: "host-1" },
      { session_id: "child-2", parent_session_id: "host-1" },
    ];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions),
    ).toEqual({});
  });

  test("does not fill when host has no children", () => {
    const sessions = [{ session_id: "child-1", parent_session_id: "host-2" }];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions),
    ).toEqual({});
  });

  test("ignores archived children when counting", () => {
    const sessions = [
      { session_id: "child-1", parent_session_id: "host-1" },
      { session_id: "child-2", parent_session_id: "host-1", archived: true },
    ];
    expect(
      autofillConversationMenuArgs(childParamPrompt, "host-1", sessions),
    ).toEqual({ TARGET_CONVERSATION: "child-1" });
  });

  test("does not fill non-childSessionId param types", () => {
    const prompt = {
      parameters: [{ name: "TARGET", type: "sessionId" }],
    };
    const sessions = [{ session_id: "child-1", parent_session_id: "host-1" }];
    expect(autofillConversationMenuArgs(prompt, "host-1", sessions)).toEqual(
      {},
    );
  });
});

// =============================================================================
// shouldOpenPromptDialog Tests
// =============================================================================

describe("shouldOpenPromptDialog", () => {
  test("prompt with no parameters does not open", () => {
    expect(shouldOpenPromptDialog({}, "beadsIssues")).toBe(false);
  });

  test("all parameters auto-filled by menu does not open", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId" },
        { name: "TITLE", type: "beadsTitle" },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(false);
  });

  test("a required text param not auto-filled by the menu opens", () => {
    const prompt = { parameters: [{ name: "MSG", type: "text" }] };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
  });

  test("unknown menu value treats every param as unsupplied → opens", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId" }],
    };
    expect(shouldOpenPromptDialog(prompt, "unknownMenu")).toBe(true);
  });

  test("missing menu argument treats every param as unsupplied → opens", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(shouldOpenPromptDialog(prompt, undefined)).toBe(true);
  });

  // --- Optional parameter (required: false) ---

  test("optional beadsId param in conversation menu does NOT open", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(false);
  });

  test("optional beadsId param in beadsIssues menu does NOT open (auto-filled)", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: false }],
    };
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(false);
  });

  test("required beadsId param in conversation menu opens", () => {
    const prompt = {
      parameters: [{ name: "ISSUE_ID", type: "beadsId", required: true }],
    };
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
  });

  test("unset required (absent required field) beadsId param in conversation menu opens", () => {
    const prompt = { parameters: [{ name: "ISSUE_ID", type: "beadsId" }] };
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
  });

  test("mixed: dialog opens due to the required unsupplied param even though the optional one alone would not open it", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: true },
        { name: "EXTRA", type: "text", required: false },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
  });

  // --- show: always / never ---

  test("optional text param with show:always opens the dialog", () => {
    const prompt = {
      parameters: [
        { name: "Instructions", type: "text", required: false, show: "always" },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
  });

  test("optional text param with show:auto or absent show does NOT open by itself", () => {
    for (const show of [undefined, "auto"]) {
      const prompt = {
        parameters: [
          { name: "Instructions", type: "text", required: false, show },
        ],
      };
      expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(false);
    }
  });

  test("show:never never opens the dialog even when required and unsupplied", () => {
    const prompt = {
      parameters: [
        { name: "Secret", type: "text", required: true, show: "never" },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(false);
  });

  test("show:always on a menu-supplied param still forces the dialog open", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: false, show: "always" },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(true);
  });

  test("show:always does not change menu gating", () => {
    const prompt = {
      parameters: [
        { name: "ISSUE_ID", type: "beadsId", required: false, show: "always" },
      ],
    };
    expect(menuSatisfies(prompt, "conversation")).toBe(true);
  });

  test("boolean param ALWAYS opens the dialog in every menu", () => {
    const prompt = { parameters: [{ name: "Commit", type: "boolean" }] };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(true);
  });

  test("boolean param opens even when marked required:false", () => {
    const prompt = {
      parameters: [{ name: "Commit", type: "boolean", required: false }],
    };
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
  });

  test("prompts param ALWAYS opens the dialog in every menu", () => {
    const prompt = {
      parameters: [{ name: "P", type: "prompts", required: true }],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(true);
  });

  test("text+options param ALWAYS opens the dialog in every menu", () => {
    const prompt = {
      parameters: [{ name: "Mode", type: "text", options: ["a", "b"] }],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "beadsIssues")).toBe(true);
  });

  test("text with an empty options array is NOT an options picker — still gated by required", () => {
    const prompt = {
      parameters: [{ name: "Note", type: "text", options: [], required: true }],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
  });

  test("cachedNames suppresses a param's contribution to the open decision", () => {
    const prompt = {
      parameters: [
        {
          name: "Note",
          type: "text",
          required: true,
          cache: { destination: "memory" },
        },
      ],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts")).toBe(true);
    expect(shouldOpenPromptDialog(prompt, "prompts", new Set(["Note"]))).toBe(
      false,
    );
  });

  test("cachedNames does not suppress a non-cacheable param", () => {
    const prompt = {
      parameters: [{ name: "Note", type: "text", required: true }],
    };
    expect(shouldOpenPromptDialog(prompt, "prompts", new Set(["Note"]))).toBe(
      true,
    );
  });

  test("knownNames suppresses a param's contribution to the open decision", () => {
    const prompt = {
      parameters: [{ name: "CHILD", type: "childSessionId", required: true }],
    };
    expect(shouldOpenPromptDialog(prompt, "conversation")).toBe(true);
    expect(
      shouldOpenPromptDialog(
        prompt,
        "conversation",
        undefined,
        new Set(["CHILD"]),
      ),
    ).toBe(false);
  });
});

// =============================================================================
// promptDialogParameters Tests
// =============================================================================

describe("promptDialogParameters", () => {
  test("prompt with no parameters returns []", () => {
    expect(promptDialogParameters({}, "beadsIssues")).toEqual([]);
  });

  test("preserves declared parameter order in the result", () => {
    const p1 = { name: "ALPHA", type: "text" };
    const p2 = { name: "BETA", type: "sessionId" };
    const p3 = { name: "GAMMA", type: "workspaceId" };
    const prompt = { parameters: [p1, p2, p3] };
    expect(promptDialogParameters(prompt, "prompts")).toEqual([p1, p2, p3]);
  });

  test("an optional free-text param renders even though it would not open the dialog by itself (mitto-9rff)", () => {
    const required = { name: "ISSUE_ID", type: "beadsId", required: true };
    const optional = { name: "AdditionalInstructions", type: "text" };
    const prompt = { parameters: [required, optional] };
    expect(promptDialogParameters(prompt, "prompts")).toEqual([
      required,
      optional,
    ]);
  });

  test("show:never excludes the parameter from rendering", () => {
    const shown = { name: "Kept", type: "text" };
    const hidden = { name: "Secret", type: "text", show: "never" };
    const prompt = { parameters: [shown, hidden] };
    expect(promptDialogParameters(prompt, "prompts")).toEqual([shown]);
  });

  test("a menu-supplied param is included but marked readOnly", () => {
    const param = { name: "ISSUE_ID", type: "beadsId" };
    const prompt = { parameters: [param] };
    expect(promptDialogParameters(prompt, "beadsIssues")).toEqual([
      { ...param, readOnly: true },
    ]);
    // Not auto-suppliable by "prompts" → editable (no readOnly annotation).
    expect(promptDialogParameters(prompt, "prompts")).toEqual([param]);
  });

  test("show:always on a menu-supplied param promotes it to editable (no readOnly)", () => {
    const param = { name: "ISSUE_ID", type: "beadsId", show: "always" };
    const prompt = { parameters: [param] };
    expect(promptDialogParameters(prompt, "beadsIssues")).toEqual([param]);
  });

  test("knownNames marks a param readOnly like a menu-supplied param", () => {
    const param = { name: "CHILD", type: "childSessionId" };
    const prompt = { parameters: [param] };
    expect(
      promptDialogParameters(prompt, "conversation", new Set(["CHILD"])),
    ).toEqual([{ ...param, readOnly: true }]);
  });

  test("boolean, prompts, and text+options params all render normally (no readOnly) when not menu-supplied", () => {
    const boolParam = { name: "Commit", type: "boolean" };
    const promptsParam = { name: "P", type: "prompts" };
    const optionsParam = { name: "Mode", type: "text", options: ["a", "b"] };
    const prompt = { parameters: [boolParam, promptsParam, optionsParam] };
    expect(promptDialogParameters(prompt, "prompts")).toEqual([
      boolParam,
      promptsParam,
      optionsParam,
    ]);
  });
});

// =============================================================================
// isAlwaysShownParam / isNeverShownParam Tests
// =============================================================================

describe("isAlwaysShownParam", () => {
  test("true for show: always", () => {
    expect(isAlwaysShownParam({ show: "always" })).toBe(true);
  });
  test("false for auto/never/absent", () => {
    expect(isAlwaysShownParam({ show: "auto" })).toBe(false);
    expect(isAlwaysShownParam({ show: "never" })).toBe(false);
    expect(isAlwaysShownParam({})).toBe(false);
    expect(isAlwaysShownParam(null)).toBe(false);
  });
});

describe("isNeverShownParam", () => {
  test("true for show: never", () => {
    expect(isNeverShownParam({ show: "never" })).toBe(true);
  });
  test("false for auto/always/absent", () => {
    expect(isNeverShownParam({ show: "auto" })).toBe(false);
    expect(isNeverShownParam({ show: "always" })).toBe(false);
    expect(isNeverShownParam({})).toBe(false);
    expect(isNeverShownParam(null)).toBe(false);
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
    expect(
      isCacheableParam({
        name: "X",
        cache: { destination: "memory", ttl: "1h" },
      }),
    ).toBe(true);
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
// fetchCachedParamNames Tests
// =============================================================================

describe("fetchCachedParamNames", () => {
  test("returns Set with cached names on ok response", async () => {
    const fetchImpl = mockFn().mockResolvedValue({
      ok: true,
      json: async () => ({ cached: ["A", "B"] }),
    });
    const result = await fetchCachedParamNames("sess-1", "my-prompt", {
      fetchImpl,
    });
    expect(result).toEqual(new Set(["A", "B"]));
  });

  test("passes URL containing /prompt-arg-cache and prompt= to fetchImpl", async () => {
    const fetchImpl = mockFn().mockResolvedValue({
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
    const fetchImpl = mockFn().mockResolvedValue({ ok: false });
    const result = await fetchCachedParamNames("sess-1", "my-prompt", {
      fetchImpl,
    });
    expect(result).toEqual(new Set());
  });

  test("returns empty Set and does not throw when fetchImpl throws", async () => {
    const fetchImpl = mockFn().mockRejectedValue(new Error("network error"));
    const result = await fetchCachedParamNames("sess-1", "my-prompt", {
      fetchImpl,
    });
    expect(result).toEqual(new Set());
  });

  test("returns empty Set and does NOT call fetchImpl when sessionId is missing", async () => {
    const fetchImpl = mockFn();
    const result = await fetchCachedParamNames("", "my-prompt", { fetchImpl });
    expect(result).toEqual(new Set());
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  test("returns empty Set and does NOT call fetchImpl when promptName is missing", async () => {
    const fetchImpl = mockFn();
    const result = await fetchCachedParamNames("sess-1", "", { fetchImpl });
    expect(result).toEqual(new Set());
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  test("returns empty Set when response json has no cached array", async () => {
    const fetchImpl = mockFn().mockResolvedValue({
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
  // Live ACP model list: Haiku → Sonnet → Opus (ordered).
  const modelOption = {
    current_value: "claude-opus-4-8",
    options: [
      { value: "claude-haiku-3-5", name: "Claude Haiku 3.5" },
      { value: "claude-sonnet-4-5", name: "Claude Sonnet 4.5" },
      { value: "claude-opus-4-8", name: "Claude Opus 4.8" },
    ],
  };

  // Global model profiles (config.models) — Settings → Models.
  const profiles = [
    {
      name: "Claude Opus",
      criteria: { matchMode: "contains", pattern: "Opus" },
      tags: ["Reasoning", "Smartest"],
    },
    {
      name: "Claude Sonnet",
      criteria: { matchMode: "contains", pattern: "Sonnet" },
      tags: ["Coding", "Smart"],
    },
    {
      name: "Claude Haiku",
      criteria: { matchMode: "contains", pattern: "Haiku" },
      tags: ["Cheap", "Fast"],
    },
  ];

  test("modelName resolves to the profile's matched model when it differs from current", () => {
    const result = resolvePromptModelOverride(
      [{ modelName: "Claude Sonnet" }],
      modelOption,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("modelName is case-insensitive against profile.name", () => {
    const result = resolvePromptModelOverride(
      [{ modelName: "claude sonnet" }],
      modelOption,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("modelTag resolves the first tagged profile that yields an available model", () => {
    // "Coding" is only on the Sonnet profile.
    const result = resolvePromptModelOverride(
      [{ modelTag: "Coding" }],
      modelOption,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("modelTag is deterministic by profile order when multiple profiles share the tag", () => {
    // Add a shared "Cheap" tag on Sonnet so both Sonnet (index 1) and Haiku
    // (index 2) match. Sonnet comes first in profile order → wins.
    const shared = [
      profiles[0],
      { ...profiles[1], tags: [...profiles[1].tags, "Cheap"] },
      profiles[2],
    ];
    const result = resolvePromptModelOverride(
      [{ modelTag: "Cheap" }],
      modelOption,
      shared,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("current-satisfies: modelName matching current model returns null (no chip)", () => {
    // Current is Opus, and the resolved target of "Claude Opus" is Opus → keep.
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Claude Opus" }],
        modelOption,
        profiles,
      ),
    ).toBeNull();
  });

  test("current-satisfies: modelTag whose tagged profile matches current model returns null", () => {
    // Current is Opus; "Reasoning" tags Opus → current already satisfies.
    expect(
      resolvePromptModelOverride(
        [{ modelTag: "Reasoning" }],
        modelOption,
        profiles,
      ),
    ).toBeNull();
  });

  test("ordered first-match-wins: earlier entry that resolves takes precedence", () => {
    const result = resolvePromptModelOverride(
      [{ modelName: "Claude Sonnet" }, { modelName: "Claude Haiku" }],
      modelOption,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("unknown entries are skipped so a later resolvable entry can win", () => {
    const result = resolvePromptModelOverride(
      [
        { modelName: "Nonexistent Profile" },
        { modelTag: "NoSuchTag" },
        { modelName: "Claude Sonnet" },
      ],
      modelOption,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
  });

  test("returns null when a modelName's profile resolves to no available model", () => {
    // No model whose name contains "Gemini" → skip; nothing else → null.
    const withOrphan = [
      ...profiles,
      {
        name: "Gemini",
        criteria: { matchMode: "contains", pattern: "Gemini" },
        tags: [],
      },
    ];
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Gemini" }],
        modelOption,
        withOrphan,
      ),
    ).toBeNull();
  });

  test("returns null for empty/absent preferredModels", () => {
    expect(resolvePromptModelOverride([], modelOption, profiles)).toBeNull();
    expect(
      resolvePromptModelOverride(undefined, modelOption, profiles),
    ).toBeNull();
  });

  test("returns null when modelOption is absent or has no options", () => {
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Claude Sonnet" }],
        null,
        profiles,
      ),
    ).toBeNull();
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Claude Sonnet" }],
        { current_value: "x", options: [] },
        profiles,
      ),
    ).toBeNull();
  });

  test("returns null when modelProfiles is absent or empty", () => {
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Claude Sonnet" }],
        modelOption,
        undefined,
      ),
    ).toBeNull();
    expect(
      resolvePromptModelOverride(
        [{ modelName: "Claude Sonnet" }],
        modelOption,
        [],
      ),
    ).toBeNull();
  });

  test("latest-version-wins: MatchConstraintOption returns the last matching option", () => {
    // Two Sonnet versions; the later one (4.5) should win.
    const modelOptionMulti = {
      current_value: "claude-opus-4-8",
      options: [
        { value: "claude-sonnet-3-5", name: "Claude Sonnet 3.5" },
        { value: "claude-sonnet-4-5", name: "Claude Sonnet 4.5" },
        { value: "claude-opus-4-8", name: "Claude Opus 4.8" },
      ],
    };
    const result = resolvePromptModelOverride(
      [{ modelName: "Claude Sonnet" }],
      modelOptionMulti,
      profiles,
    );
    expect(result).toEqual({
      value: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
    });
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

// =============================================================================
// promptMenus — exclusion (`!`-prefix) behaviour
// =============================================================================

describe("promptMenus — exclusion token stripping", () => {
  test("strips !-prefixed tokens from the positive list", () => {
    expect(promptMenus({ menus: "prompts, !promptsLoop" })).toEqual([
      "prompts",
    ]);
  });

  test("defaults to ['prompts'] when only exclusion tokens remain", () => {
    expect(promptMenus({ menus: "!promptsLoop" })).toEqual(["prompts"]);
  });

  test("strips multiple exclusion tokens", () => {
    expect(
      promptMenus({ menus: "prompts, !promptsLoop, !conversation" }),
    ).toEqual(["prompts"]);
  });

  test("preserves positive tokens alongside exclusions", () => {
    expect(
      promptMenus({ menus: "prompts, conversation, !promptsLoop" }),
    ).toEqual(["prompts", "conversation"]);
  });

  test("handles whitespace around ! tokens", () => {
    expect(promptMenus({ menus: "prompts , ! promptsLoop" })).toEqual([
      "prompts",
    ]);
  });
});

// =============================================================================
// promptMenuExcludes
// =============================================================================

describe("promptMenuExcludes", () => {
  test("returns empty Set for prompt with no menus field", () => {
    expect(promptMenuExcludes({})).toEqual(new Set());
  });

  test("returns empty Set when menus is empty string", () => {
    expect(promptMenuExcludes({ menus: "" })).toEqual(new Set());
  });

  test("returns empty Set when no !-prefixed tokens present", () => {
    expect(promptMenuExcludes({ menus: "prompts, conversation" })).toEqual(
      new Set(),
    );
  });

  test("returns Set of excluded menu names without leading !", () => {
    expect(promptMenuExcludes({ menus: "prompts, !promptsLoop" })).toEqual(
      new Set(["promptsLoop"]),
    );
  });

  test("returns multiple excluded names", () => {
    expect(
      promptMenuExcludes({ menus: "prompts, !promptsLoop, !conversation" }),
    ).toEqual(new Set(["promptsLoop", "conversation"]));
  });

  test("handles whitespace around ! token (defensive)", () => {
    expect(promptMenuExcludes({ menus: "prompts, ! promptsLoop" })).toEqual(
      new Set(["promptsLoop"]),
    );
  });

  test("handles null prompt gracefully", () => {
    expect(promptMenuExcludes(null)).toEqual(new Set());
  });

  test("handles undefined prompt gracefully", () => {
    expect(promptMenuExcludes(undefined)).toEqual(new Set());
  });
});

// =============================================================================
// promptMenuIncludes
// =============================================================================

describe("promptMenuIncludes", () => {
  test("returns true when menu is included and not excluded", () => {
    expect(promptMenuIncludes({ menus: "prompts" }, "prompts")).toBe(true);
  });

  test("returns false when menu is not in the positive list", () => {
    expect(promptMenuIncludes({ menus: "conversation" }, "prompts")).toBe(
      false,
    );
  });

  test("returns false when menu is explicitly excluded", () => {
    expect(
      promptMenuIncludes({ menus: "prompts, !promptsLoop" }, "promptsLoop"),
    ).toBe(false);
  });

  test("returns true for a menu that is included but a different menu is excluded", () => {
    expect(
      promptMenuIncludes({ menus: "prompts, !promptsLoop" }, "prompts"),
    ).toBe(true);
  });

  test("returns true using default when menus is absent", () => {
    expect(promptMenuIncludes({}, "prompts")).toBe(true);
  });

  test("returns false for promptsLoop when only !promptsLoop specified", () => {
    expect(promptMenuIncludes({ menus: "!promptsLoop" }, "promptsLoop")).toBe(
      false,
    );
  });
});

// =============================================================================
// Loop filter behaviour — union with !promptsLoop exclusion
// =============================================================================

describe("loop prompt filter logic (union + exclusion)", () => {
  // Replicates the loopPrompts predicate from useWorkspacePrompts.js
  function isLoopPrompt(p) {
    if (promptMenuExcludes(p).has("promptsLoop")) return false;
    const menus = promptMenus(p);
    return menus.includes("prompts") || menus.includes("promptsLoop");
  }

  test("prompts-only prompt IS in loop selector (union rule)", () => {
    expect(isLoopPrompt({ menus: "prompts" })).toBe(true);
  });

  test("promptsLoop-only prompt IS in loop selector", () => {
    expect(isLoopPrompt({ menus: "promptsLoop" })).toBe(true);
  });

  test("prompt with menus: prompts, !promptsLoop is NOT in loop selector", () => {
    expect(isLoopPrompt({ menus: "prompts, !promptsLoop" })).toBe(false);
  });

  test("prompt with menus: conversation is NOT in loop selector", () => {
    expect(isLoopPrompt({ menus: "conversation" })).toBe(false);
  });

  test("prompt with no menus field IS in loop selector (defaults to prompts)", () => {
    expect(isLoopPrompt({})).toBe(true);
  });
});

// =============================================================================
// promptLoopMode / promptLoopIsToggleable / promptLoopDefaultOn
// =============================================================================

describe("promptLoopMode / IsToggleable / DefaultOn", () => {
  test("no loop ({}) -> mode none, toggleable false, defaultOn false", () => {
    expect(promptLoopMode({})).toBe("none");
    expect(promptLoopIsToggleable({})).toBe(false);
    expect(promptLoopDefaultOn({})).toBe(false);
  });

  test("loop: null -> mode none, toggleable false, defaultOn false", () => {
    const p = { loop: null };
    expect(promptLoopMode(p)).toBe("none");
    expect(promptLoopIsToggleable(p)).toBe(false);
    expect(promptLoopDefaultOn(p)).toBe(false);
  });

  test("loop present, no mode -> mode always, toggleable false, defaultOn true", () => {
    const p = { loop: {} };
    expect(promptLoopMode(p)).toBe("always");
    expect(promptLoopIsToggleable(p)).toBe(false);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("mode: always -> mode always, toggleable false, defaultOn true", () => {
    const p = { loop: { mode: "always" } };
    expect(promptLoopMode(p)).toBe("always");
    expect(promptLoopIsToggleable(p)).toBe(false);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("mode: always with default:false -> default ignored, defaultOn true", () => {
    const p = { loop: { mode: "always", default: false } };
    expect(promptLoopMode(p)).toBe("always");
    expect(promptLoopIsToggleable(p)).toBe(false);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("mode: optional -> mode optional, toggleable true, defaultOn true", () => {
    const p = { loop: { mode: "optional" } };
    expect(promptLoopMode(p)).toBe("optional");
    expect(promptLoopIsToggleable(p)).toBe(true);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("mode: optional, default:true -> mode optional, toggleable true, defaultOn true", () => {
    const p = { loop: { mode: "optional", default: true } };
    expect(promptLoopMode(p)).toBe("optional");
    expect(promptLoopIsToggleable(p)).toBe(true);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("mode: optional, default:false -> mode optional, toggleable true, defaultOn false", () => {
    const p = { loop: { mode: "optional", default: false } };
    expect(promptLoopMode(p)).toBe("optional");
    expect(promptLoopIsToggleable(p)).toBe(true);
    expect(promptLoopDefaultOn(p)).toBe(false);
  });

  test("unknown mode is treated as always", () => {
    const p = { loop: { mode: "weird" } };
    expect(promptLoopMode(p)).toBe("always");
    expect(promptLoopIsToggleable(p)).toBe(false);
    expect(promptLoopDefaultOn(p)).toBe(true);
  });

  test("null-safe: undefined prompt -> mode none, toggleable false, defaultOn false", () => {
    expect(promptLoopMode(undefined)).toBe("none");
    expect(promptLoopIsToggleable(undefined)).toBe(false);
    expect(promptLoopDefaultOn(undefined)).toBe(false);
  });
});

// =============================================================================
// promptResolveAsLoop
// =============================================================================

describe("promptResolveAsLoop", () => {
  test("mode none -> false (override ignored)", () => {
    expect(promptResolveAsLoop({})).toBe(false);
    expect(promptResolveAsLoop({}, true)).toBe(false);
    expect(promptResolveAsLoop({ loop: null }, true)).toBe(false);
  });

  test("mode always -> true (override ignored)", () => {
    const p = { loop: { mode: "always" } };
    expect(promptResolveAsLoop(p)).toBe(true);
    expect(promptResolveAsLoop(p, false)).toBe(true);
  });

  test("mode optional, no override, default:false -> false", () => {
    const p = { loop: { mode: "optional", default: false } };
    expect(promptResolveAsLoop(p)).toBe(false);
  });

  test("mode optional, no override, default:true -> true", () => {
    const p = { loop: { mode: "optional", default: true } };
    expect(promptResolveAsLoop(p)).toBe(true);
  });

  test("mode optional, no override, default absent -> true", () => {
    const p = { loop: { mode: "optional" } };
    expect(promptResolveAsLoop(p)).toBe(true);
  });

  test("mode optional, override:true -> true even if default:false", () => {
    const p = { loop: { mode: "optional", default: false } };
    expect(promptResolveAsLoop(p, true)).toBe(true);
  });

  test("mode optional, override:false -> false even if default:true", () => {
    const p = { loop: { mode: "optional", default: true } };
    expect(promptResolveAsLoop(p, false)).toBe(false);
  });
});

// =============================================================================
// buildPromptGroupMenuItems (ContextMenu.js) — mitto-92x.5
//
// ContextMenu.js (and its Icons.js dependency) destructure window.preact at
// module load time, so window.preact must be stubbed BEFORE the module is
// evaluated. Static imports are hoisted ahead of any module-level statement,
// so the stub is installed first and the module is loaded via a dynamic
// import() inside beforeAll (which Jest's ESM mode supports).
// =============================================================================

describe("buildPromptGroupMenuItems", () => {
  let buildPromptGroupMenuItems;

  beforeAll(async () => {
    window.preact = {
      html: (strings, ...values) => ({ __htmlStub: true, strings, values }),
      useState: (initial) => [initial, () => {}],
    };
    ({ buildPromptGroupMenuItems } =
      await import("../components/ContextMenu.js"));
  });

  const prompts = [
    { name: "Always On", group: "G" }, // no loop block -> "none"
    {
      name: "Always Loop",
      group: "G",
      loop: { mode: "always" },
    },
    {
      name: "Maybe Loop",
      group: "G",
      loop: { mode: "optional", default: false },
    },
  ];

  function findSub(items, label) {
    for (const group of items) {
      const found = (group.submenu || []).find((s) => s.label === label);
      if (found) return found;
    }
    return undefined;
  }

  test("a 'none'-mode prompt yields loopMode 'none'", () => {
    const items = buildPromptGroupMenuItems(prompts, () => {}, null);
    const sub = findSub(items, "Always On");
    expect(sub).toBeDefined();
    expect(sub.loopMode).toBe("none");
  });

  test("an 'always'-mode prompt carries loopMode 'always' and loopDefaultOn true", () => {
    const items = buildPromptGroupMenuItems(prompts, () => {}, null);
    const sub = findSub(items, "Always Loop");
    expect(sub).toBeDefined();
    expect(sub.loopMode).toBe("always");
    expect(sub.loopDefaultOn).toBe(true);
  });

  test("an 'optional'-mode prompt carries loopMode 'optional' and loopDefaultOn matching its default", () => {
    const items = buildPromptGroupMenuItems(prompts, () => {}, null);
    const sub = findSub(items, "Maybe Loop");
    expect(sub).toBeDefined();
    expect(sub.loopMode).toBe("optional");
    expect(sub.loopDefaultOn).toBe(false);
  });

  test("calling item.onClick({ asLoop: true }) invokes onRun with (prompt, { asLoop: true })", () => {
    const onRun = mockFn();
    const items = buildPromptGroupMenuItems(prompts, onRun, null);
    const sub = findSub(items, "Maybe Loop");
    sub.onClick({ asLoop: true });
    expect(onRun).toHaveBeenCalledTimes(1);
    const [calledPrompt, calledOpts] = onRun.mock.calls[0];
    expect(calledPrompt.name).toBe("Maybe Loop");
    expect(calledOpts).toEqual({ asLoop: true });
  });

  test("calling item.onClick({ asLoop: false }) forwards false", () => {
    const onRun = mockFn();
    const items = buildPromptGroupMenuItems(prompts, onRun, null);
    const sub = findSub(items, "Maybe Loop");
    sub.onClick({ asLoop: false });
    expect(onRun).toHaveBeenCalledTimes(1);
    const [calledPrompt, calledOpts] = onRun.mock.calls[0];
    expect(calledPrompt.name).toBe("Maybe Loop");
    expect(calledOpts).toEqual({ asLoop: false });
  });
});

// =============================================================================
// Loop-prompt name resolution (allPrompts fallback for menu-scoped loop prompts)
//
// Regression test for mitto-uo8e. When a loop conversation runs a prompt whose
// `menus` front-matter targets a non-loop scope (e.g. `menus: beadsList` — the
// builtin "Loop processing tasks"), useWorkspacePrompts filters that prompt out
// of `loopPrompts`. LoopFrequencyPanel and ChatInput.handleEditLoopArguments
// must fall back to the full workspace prompt list (`allPrompts`) so the
// sliders/edit-args button stays enabled for the active loop body.
//
// The production lookup expression (duplicated in both components) is:
//   (allPrompts || []).find((p) => p.name === name) ||
//   (prompts    || []).find((p) => p.name === name)
// This test mirrors that logic — keep in sync with LoopFrequencyPanel.js
// (~L876) and ChatInput.js handleEditLoopArguments (~L1138).
// =============================================================================

function resolveLoopPromptByName(name, allPrompts, prompts) {
  return (
    (allPrompts || []).find((p) => p.name === name) ||
    (prompts || []).find((p) => p.name === name)
  );
}

describe("loop-prompt name resolution (allPrompts fallback)", () => {
  const beadsListPrompt = {
    name: "Loop processing tasks",
    menus: "beadsList",
    parameters: [{ name: "Commit" }, { name: "FixBugs" }],
  };
  const loopScopedPrompt = {
    name: "Some loop prompt",
    menus: "promptsLoop",
    parameters: [{ name: "Foo" }],
  };

  test("resolves a menu-scoped prompt from allPrompts when absent from loopPrompts", () => {
    const allPrompts = [beadsListPrompt, loopScopedPrompt];
    const loopPrompts = [loopScopedPrompt]; // beadsList filtered out
    const found = resolveLoopPromptByName(
      "Loop processing tasks",
      allPrompts,
      loopPrompts,
    );
    expect(found).toBe(beadsListPrompt);
    expect(promptParameters(found).length).toBe(2);
  });

  test("resolves a loop-scoped prompt from loopPrompts when allPrompts is empty", () => {
    const found = resolveLoopPromptByName(
      "Some loop prompt",
      [],
      [loopScopedPrompt],
    );
    expect(found).toBe(loopScopedPrompt);
  });

  test("prefers allPrompts when the same name exists in both", () => {
    const dupInAll = { name: "Dup", parameters: [{ name: "A" }] };
    const dupInLoop = { name: "Dup", parameters: [{ name: "B" }] };
    const found = resolveLoopPromptByName("Dup", [dupInAll], [dupInLoop]);
    expect(found).toBe(dupInAll);
  });

  test("returns undefined when name is in neither list", () => {
    expect(
      resolveLoopPromptByName("missing", [loopScopedPrompt], []),
    ).toBeUndefined();
  });

  test("handles null/undefined lists without throwing", () => {
    expect(resolveLoopPromptByName("x", null, null)).toBeUndefined();
    expect(resolveLoopPromptByName("x", undefined, undefined)).toBeUndefined();
  });

  test("baseline: promptParameters returns declared parameters for a beadsList prompt", () => {
    const p = {
      name: "Loop processing tasks",
      menus: "beadsList",
      parameters: [
        { name: "Commit" },
        { name: "FixBugs" },
        { name: "WorkOnFeatures" },
      ],
    };
    expect(promptParameters(p).length).toBe(3);
  });
});

// =============================================================================
// groupDialogParameters / unmetRequiredByGroup Tests (mitto-boio)
// =============================================================================

describe("groupDialogParameters", () => {
  test("no parameter declares a group -> tabbed=false, single unnamed group preserving order", () => {
    const params = [
      { name: "A", type: "text" },
      { name: "B", type: "text" },
      { name: "C", type: "text" },
    ];
    const result = groupDialogParameters(params);
    expect(result.tabbed).toBe(false);
    expect(result.groups).toEqual([{ name: "", params }]);
  });

  test("empty/whitespace-only group values do not trigger tabbing", () => {
    const params = [
      { name: "A", type: "text", group: "" },
      { name: "B", type: "text", group: "   " },
    ];
    const result = groupDialogParameters(params);
    expect(result.tabbed).toBe(false);
    expect(result.groups).toEqual([{ name: "", params }]);
  });

  test("all parameters share one explicit group -> tabbed=true with ONE named tab (not distinctGroups>1)", () => {
    const params = [
      { name: "A", type: "text", group: "Changes Submission" },
      { name: "B", type: "text", group: "Changes Submission" },
    ];
    const result = groupDialogParameters(params);
    expect(result.tabbed).toBe(true);
    expect(result.groups).toEqual([{ name: "Changes Submission", params }]);
  });

  test("mixed grouped + ungrouped -> General tab first, then one tab per group in first-appearance order", () => {
    const a = { name: "A", type: "text" }; // ungrouped
    const b = { name: "B", type: "text", group: "Advanced" };
    const c = { name: "C", type: "text" }; // ungrouped
    const d = { name: "D", type: "text", group: "Changes Submission" };
    const e = { name: "E", type: "text", group: "Advanced" };
    const result = groupDialogParameters([a, b, c, d, e]);
    expect(result.tabbed).toBe(true);
    expect(result.groups.map((g) => g.name)).toEqual([
      "General",
      "Advanced",
      "Changes Submission",
    ]);
    expect(result.groups[0].params).toEqual([a, c]);
    expect(result.groups[1].params).toEqual([b, e]);
    expect(result.groups[2].params).toEqual([d]);
  });

  test("explicit group: General merges with ungrouped params into the same tab", () => {
    const a = { name: "A", type: "text" }; // ungrouped
    const b = { name: "B", type: "text", group: "General" };
    const result = groupDialogParameters([a, b]);
    expect(result.tabbed).toBe(true);
    expect(result.groups).toEqual([{ name: "General", params: [a, b] }]);
  });

  test("handles non-array/undefined input without throwing", () => {
    expect(groupDialogParameters(undefined)).toEqual({
      tabbed: false,
      groups: [{ name: "", params: [] }],
    });
    expect(groupDialogParameters(null)).toEqual({
      tabbed: false,
      groups: [{ name: "", params: [] }],
    });
  });
});

describe("unmetRequiredByGroup", () => {
  test("flags a group with a required, unfilled, non-boolean, non-readOnly param", () => {
    const groups = [
      {
        name: "General",
        params: [{ name: "A", type: "text", required: true }],
      },
    ];
    expect(unmetRequiredByGroup(groups, {})).toEqual(new Set(["General"]));
  });

  test("does not flag a group whose required param is filled", () => {
    const groups = [
      {
        name: "General",
        params: [{ name: "A", type: "text", required: true }],
      },
    ];
    expect(unmetRequiredByGroup(groups, { A: "value" })).toEqual(new Set());
  });

  test("ignores boolean params even when required and unfilled", () => {
    const groups = [
      {
        name: "General",
        params: [{ name: "Commit", type: "boolean", required: true }],
      },
    ];
    expect(unmetRequiredByGroup(groups, {})).toEqual(new Set());
  });

  test("ignores readOnly params even when required and unfilled", () => {
    const groups = [
      {
        name: "General",
        params: [{ name: "A", type: "text", required: true, readOnly: true }],
      },
    ];
    expect(unmetRequiredByGroup(groups, {})).toEqual(new Set());
  });

  test("ignores non-required params", () => {
    const groups = [{ name: "General", params: [{ name: "A", type: "text" }] }];
    expect(unmetRequiredByGroup(groups, {})).toEqual(new Set());
  });

  test("handles multiple groups independently", () => {
    const groups = [
      {
        name: "General",
        params: [{ name: "A", type: "text", required: true }],
      },
      {
        name: "Advanced",
        params: [{ name: "B", type: "text", required: true }],
      },
    ];
    expect(unmetRequiredByGroup(groups, { A: "x" })).toEqual(
      new Set(["Advanced"]),
    );
  });

  test("handles empty/undefined groups and values without throwing", () => {
    expect(unmetRequiredByGroup([], undefined)).toEqual(new Set());
    expect(unmetRequiredByGroup(undefined, undefined)).toEqual(new Set());
  });
});
