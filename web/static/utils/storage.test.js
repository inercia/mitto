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

  test("returns 'folder' when localStorage has 'folder'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "folder");
    expect(getGroupingMode()).toBe("folder");
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

  test("saves 'folder' to localStorage", () => {
    setGroupingMode("folder");
    expect(mockStore["mitto_conversation_grouping_mode"]).toBe("folder");
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

  test("cycles from 'server' to 'folder'", () => {
    mockStore["mitto_conversation_grouping_mode"] = "server";
    const result = cycleGroupingMode();
    expect(result).toBe("folder");
  });

  test("cycles from 'folder' to 'none'", () => {
    mockStore["mitto_conversation_grouping_mode"] = "folder";
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
    const stored = JSON.parse(
      mockStore["mitto_conversation_expanded_groups"],
    );
    expect(stored["my-group"]).toBe(true);
  });

  test("saves collapsed state to localStorage", () => {
    setGroupExpanded("my-group", false);
    const stored = JSON.parse(
      mockStore["mitto_conversation_expanded_groups"],
    );
    expect(stored["my-group"]).toBe(false);
  });

  test("preserves existing groups when adding new one", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      existing: true,
    });
    setGroupExpanded("new-group", false);
    const stored = JSON.parse(
      mockStore["mitto_conversation_expanded_groups"],
    );
    expect(stored["existing"]).toBe(true);
    expect(stored["new-group"]).toBe(false);
  });

  test("updates existing group state", () => {
    mockStore["mitto_conversation_expanded_groups"] = JSON.stringify({
      "my-group": true,
    });
    setGroupExpanded("my-group", false);
    const stored = JSON.parse(
      mockStore["mitto_conversation_expanded_groups"],
    );
    expect(stored["my-group"]).toBe(false);
  });
});
