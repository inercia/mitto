/**
 * Unit tests for Mitto Web Interface storage utilities
 */

import {
  getGroupingMode,
  setGroupingMode,
  cycleGroupingMode,
  getExpandedGroups,
  isGroupExpanded,
  setGroupExpanded,
  FILTER_TAB,
  getFilterTabGrouping,
  setFilterTabGrouping,
  getAllFilterTabGroupings,
  cycleFilterTabGrouping,
} from "./storage.js";

// Simple localStorage mock
let mockStore = {};
const localStorageMock = {
  getItem: (key) => mockStore[key] || null,
  setItem: (key, value) => {
    mockStore[key] = value;
  },
  removeItem: (key) => {
    delete mockStore[key];
  },
  clear: () => {
    mockStore = {};
  },
};

// Mock fetch for server-side storage (to prevent actual network calls)
global.fetch = () =>
  Promise.resolve({
    ok: true,
    json: () => Promise.resolve({}),
  });

beforeEach(() => {
  mockStore = {};
  Object.defineProperty(window, "localStorage", { value: localStorageMock });
});

// =============================================================================
// getGroupingMode Tests
// =============================================================================

describe("getGroupingMode", () => {
  test("returns 'none' when localStorage is empty", () => {
    expect(getGroupingMode()).toBe("none");
  });

  test("returns 'server' when localStorage has 'server'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "server");
    expect(getGroupingMode()).toBe("server");
  });

  test("returns 'workspace' when localStorage has 'workspace'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "workspace");
    expect(getGroupingMode()).toBe("workspace");
  });

  test("migrates 'folder' to 'workspace' for legacy support", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "folder");
    expect(getGroupingMode()).toBe("workspace");
    // Verify the value was migrated in localStorage
    expect(mockStore["mitto_conversation_grouping_mode"]).toBe("workspace");
  });

  test("returns 'none' for invalid values", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "invalid");
    expect(getGroupingMode()).toBe("none");
  });
});

// =============================================================================
// setGroupingMode Tests
// =============================================================================

describe("setGroupingMode", () => {
  test("saves 'server' to localStorage", () => {
    setGroupingMode("server");
    expect(mockStore["mitto_conversation_grouping_mode"]).toBe("server");
  });

  test("saves 'workspace' to localStorage", () => {
    setGroupingMode("workspace");
    expect(mockStore["mitto_conversation_grouping_mode"]).toBe("workspace");
  });

  test("removes key for 'none'", () => {
    // First set a value
    mockStore["mitto_conversation_grouping_mode"] = "server";
    setGroupingMode("none");
    expect(mockStore["mitto_conversation_grouping_mode"]).toBeUndefined();
  });
});

// =============================================================================
// cycleGroupingMode Tests
// =============================================================================

describe("cycleGroupingMode", () => {
  test("cycles from 'none' to 'server'", () => {
    const result = cycleGroupingMode();
    expect(result).toBe("server");
  });

  test("cycles from 'server' to 'workspace'", () => {
    mockStore["mitto_conversation_grouping_mode"] = "server";
    const result = cycleGroupingMode();
    expect(result).toBe("workspace");
  });

  test("cycles from 'workspace' to 'none'", () => {
    mockStore["mitto_conversation_grouping_mode"] = "workspace";
    const result = cycleGroupingMode();
    expect(result).toBe("none");
  });
});

// =============================================================================
// getExpandedGroups Tests
// =============================================================================

describe("getExpandedGroups", () => {
  test("returns empty object when localStorage is empty", () => {
    expect(getExpandedGroups()).toEqual({});
  });

  test("returns parsed object from localStorage", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      group1: true,
      group2: false,
    });
    expect(getExpandedGroups()).toEqual({ group1: true, group2: false });
  });

  test("returns empty object for invalid JSON", () => {
    mockStore["mitto_conversation_expanded_groups"] = "invalid json";
    expect(getExpandedGroups()).toEqual({});
  });
});

// =============================================================================
// isGroupExpanded Tests
// =============================================================================

describe("isGroupExpanded", () => {
  test("returns true for unknown groups (default expanded)", () => {
    expect(isGroupExpanded("unknown-group")).toBe(true);
  });

  test("returns true for explicitly expanded groups", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      "my-group": true,
    });
    expect(isGroupExpanded("my-group")).toBe(true);
  });

  test("returns false for explicitly collapsed groups", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      "my-group": false,
    });
    expect(isGroupExpanded("my-group")).toBe(false);
  });
});

// =============================================================================
// setGroupExpanded Tests
// =============================================================================

describe("setGroupExpanded", () => {
  test("saves expanded state to localStorage", () => {
    setGroupExpanded("my-group", true);
    const stored = JSON.parse(mockStore["mitto_conversation_expanded_groups"]);
    expect(stored["my-group"]).toBe(true);
  });

  test("saves collapsed state to localStorage", () => {
    setGroupExpanded("my-group", false);
    const stored = JSON.parse(mockStore["mitto_conversation_expanded_groups"]);
    expect(stored["my-group"]).toBe(false);
  });

  test("preserves existing groups when adding new one", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      existing: true,
    });
    setGroupExpanded("new-group", false);
    const stored = JSON.parse(mockStore["mitto_conversation_expanded_groups"]);
    expect(stored["existing"]).toBe(true);
    expect(stored["new-group"]).toBe(false);
  });

  test("updates existing group state", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      "my-group": true,
    });
    setGroupExpanded("my-group", false);
    const stored = JSON.parse(mockStore["mitto_conversation_expanded_groups"]);
    expect(stored["my-group"]).toBe(false);
  });
});

// =============================================================================
// getFilterTabGrouping Tests
// =============================================================================

describe("getFilterTabGrouping", () => {
  test("returns default 'workspace' for conversations tab when localStorage is empty", () => {
    expect(getFilterTabGrouping(FILTER_TAB.CONVERSATIONS)).toBe("workspace");
  });

  test("returns default 'none' for periodic tab when localStorage is empty", () => {
    expect(getFilterTabGrouping(FILTER_TAB.PERIODIC)).toBe("none");
  });

  test("returns default 'workspace' for archived tab when localStorage is empty", () => {
    expect(getFilterTabGrouping(FILTER_TAB.ARCHIVED)).toBe("workspace");
  });

  test("returns saved grouping mode for a specific tab", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });
    expect(getFilterTabGrouping(FILTER_TAB.CONVERSATIONS)).toBe("server");
  });

  test("returns default for tab not in localStorage", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });
    // Periodic tab not saved, should return default 'none'
    expect(getFilterTabGrouping(FILTER_TAB.PERIODIC)).toBe("none");
  });

  test("returns default for invalid JSON", () => {
    mockStore["mitto_filter_tab_grouping"] = "invalid json";
    expect(getFilterTabGrouping(FILTER_TAB.CONVERSATIONS)).toBe("workspace");
  });

  test("returns default for invalid mode value", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "invalid_mode",
    });
    expect(getFilterTabGrouping(FILTER_TAB.CONVERSATIONS)).toBe("workspace");
  });
});

// =============================================================================
// setFilterTabGrouping Tests
// =============================================================================

describe("setFilterTabGrouping", () => {
  test("saves grouping mode for a specific tab", () => {
    setFilterTabGrouping(FILTER_TAB.CONVERSATIONS, "server");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.CONVERSATIONS]).toBe("server");
  });

  test("saves 'none' mode correctly", () => {
    setFilterTabGrouping(FILTER_TAB.PERIODIC, "none");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.PERIODIC]).toBe("none");
  });

  test("saves 'workspace' mode correctly", () => {
    setFilterTabGrouping(FILTER_TAB.ARCHIVED, "workspace");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.ARCHIVED]).toBe("workspace");
  });

  test("preserves existing tab groupings when adding new one", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });
    setFilterTabGrouping(FILTER_TAB.PERIODIC, "workspace");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.CONVERSATIONS]).toBe("server");
    expect(stored[FILTER_TAB.PERIODIC]).toBe("workspace");
  });

  test("updates existing tab grouping", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });
    setFilterTabGrouping(FILTER_TAB.CONVERSATIONS, "workspace");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.CONVERSATIONS]).toBe("workspace");
  });

  test("uses default for invalid mode", () => {
    setFilterTabGrouping(FILTER_TAB.CONVERSATIONS, "invalid_mode");
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    // Should use default for conversations tab which is 'workspace'
    expect(stored[FILTER_TAB.CONVERSATIONS]).toBe("workspace");
  });
});

// =============================================================================
// getAllFilterTabGroupings Tests
// =============================================================================

describe("getAllFilterTabGroupings", () => {
  test("returns empty object when localStorage is empty", () => {
    expect(getAllFilterTabGroupings()).toEqual({});
  });

  test("returns all saved tab groupings", () => {
    const savedGroupings = {
      [FILTER_TAB.CONVERSATIONS]: "server",
      [FILTER_TAB.PERIODIC]: "workspace",
      [FILTER_TAB.ARCHIVED]: "none",
    };
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify(savedGroupings);
    expect(getAllFilterTabGroupings()).toEqual(savedGroupings);
  });

  test("returns empty object for invalid JSON", () => {
    mockStore["mitto_filter_tab_grouping"] = "invalid json";
    expect(getAllFilterTabGroupings()).toEqual({});
  });
});

// =============================================================================
// cycleFilterTabGrouping Tests
// =============================================================================

describe("cycleFilterTabGrouping", () => {
  test("cycles from default 'workspace' to 'none' for conversations tab", () => {
    // Conversations tab defaults to 'workspace'
    const result = cycleFilterTabGrouping(FILTER_TAB.CONVERSATIONS);
    expect(result).toBe("none");
  });

  test("cycles from 'none' to 'server'", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "none",
    });
    const result = cycleFilterTabGrouping(FILTER_TAB.CONVERSATIONS);
    expect(result).toBe("server");
  });

  test("cycles from 'server' to 'workspace'", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });
    const result = cycleFilterTabGrouping(FILTER_TAB.CONVERSATIONS);
    expect(result).toBe("workspace");
  });

  test("cycles from 'workspace' to 'none'", () => {
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "workspace",
    });
    const result = cycleFilterTabGrouping(FILTER_TAB.CONVERSATIONS);
    expect(result).toBe("none");
  });

  test("cycles independently for different tabs", () => {
    // Set conversations to server
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      [FILTER_TAB.CONVERSATIONS]: "server",
    });

    // Cycle periodic tab (defaults to 'none', should go to 'server')
    const periodicResult = cycleFilterTabGrouping(FILTER_TAB.PERIODIC);
    expect(periodicResult).toBe("server");

    // Verify conversations tab is unchanged
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    expect(stored[FILTER_TAB.CONVERSATIONS]).toBe("server");
    expect(stored[FILTER_TAB.PERIODIC]).toBe("server");
  });

  test("saves cycled value to localStorage", () => {
    cycleFilterTabGrouping(FILTER_TAB.ARCHIVED);
    const stored = JSON.parse(mockStore["mitto_filter_tab_grouping"]);
    // Archived defaults to 'workspace', cycling goes to 'none'
    expect(stored[FILTER_TAB.ARCHIVED]).toBe("none");
  });
});
