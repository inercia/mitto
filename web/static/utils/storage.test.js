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
  getBeadsFilters,
  setBeadsFilters,
  getBeadsGrouping,
  setBeadsGrouping,
  getBeadsSort,
  setBeadsSort,
  getCategoryFilter,
  setCategoryFilter,
  DEFAULT_CATEGORY_FILTER,
  migrateLegacyTabStorage,
  getDensity,
  setDensity,
} from "./storage.js";

const DENSITY_KEY = "mitto_conversation_density";

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

// Simple sessionStorage mock
let sessionMockStore = {};
const sessionStorageMock = {
  getItem: (key) => sessionMockStore[key] || null,
  setItem: (key, value) => {
    sessionMockStore[key] = value;
  },
  removeItem: (key) => {
    delete sessionMockStore[key];
  },
  clear: () => {
    sessionMockStore = {};
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
  sessionMockStore = {};
  Object.defineProperty(window, "localStorage", { value: localStorageMock });
  Object.defineProperty(window, "sessionStorage", { value: sessionStorageMock, writable: true });
});

// =============================================================================
// getGroupingMode Tests
// =============================================================================

describe("getGroupingMode", () => {
  test("returns 'folder' when localStorage is empty (default)", () => {
    expect(getGroupingMode()).toBe("folder");
  });

  test("returns 'server' when localStorage has 'server'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "server");
    expect(getGroupingMode()).toBe("server");
  });

  test("returns 'workspace' when localStorage has 'workspace'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "workspace");
    expect(getGroupingMode()).toBe("workspace");
  });

  test("returns 'folder' when localStorage has 'folder'", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "folder");
    expect(getGroupingMode()).toBe("folder");
  });

  test("returns 'folder' for invalid values (default)", () => {
    localStorageMock.setItem("mitto_conversation_grouping_mode", "invalid");
    expect(getGroupingMode()).toBe("folder");
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
  test("cycles from default 'folder' to 'workspace'", () => {
    // Default is now 'folder', so cycling should go to 'workspace'
    const result = cycleGroupingMode();
    expect(result).toBe("workspace");
  });

  test("cycles from 'server' to 'folder'", () => {
    mockStore["mitto_conversation_grouping_mode"] = "server";
    const result = cycleGroupingMode();
    expect(result).toBe("folder");
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
// getBeadsFilters / setBeadsFilters Tests
// =============================================================================

const BEADS_FILTERS_KEY = "mitto_beads_filters";

describe("getBeadsFilters", () => {
  test("returns defaults when localStorage is empty", () => {
    expect(getBeadsFilters()).toEqual({ type: "all", search: "" });
  });

  test("returns stored filters when present", () => {
    mockStore[BEADS_FILTERS_KEY] = JSON.stringify({
      type: "bug",
      search: "timeout",
    });
    expect(getBeadsFilters()).toEqual({ type: "bug", search: "timeout" });
  });

  test("fills missing fields with defaults", () => {
    mockStore[BEADS_FILTERS_KEY] = JSON.stringify({ type: "bug" });
    expect(getBeadsFilters()).toEqual({ type: "bug", search: "" });
  });

  test("ignores non-string field values and uses defaults", () => {
    mockStore[BEADS_FILTERS_KEY] = JSON.stringify({ type: null, search: {} });
    expect(getBeadsFilters()).toEqual({ type: "all", search: "" });
  });

  test("returns defaults for corrupt JSON", () => {
    mockStore[BEADS_FILTERS_KEY] = "not-json{";
    expect(getBeadsFilters()).toEqual({ type: "all", search: "" });
  });
});

describe("setBeadsFilters", () => {
  test("persists filters to localStorage", () => {
    setBeadsFilters({ type: "feature", search: "copy" });
    expect(JSON.parse(mockStore[BEADS_FILTERS_KEY])).toEqual({
      type: "feature",
      search: "copy",
    });
  });

  test("fills missing fields with defaults when saving", () => {
    setBeadsFilters({ search: "abc" });
    expect(JSON.parse(mockStore[BEADS_FILTERS_KEY])).toEqual({
      type: "all",
      search: "abc",
    });
  });

  test("uses all defaults when given no argument", () => {
    setBeadsFilters();
    expect(JSON.parse(mockStore[BEADS_FILTERS_KEY])).toEqual({
      type: "all",
      search: "",
    });
  });

  test("round-trips through getBeadsFilters", () => {
    const filters = { type: "task", search: "port" };
    setBeadsFilters(filters);
    expect(getBeadsFilters()).toEqual(filters);
  });
});

// =============================================================================
// getBeadsGrouping / setBeadsGrouping Tests
// =============================================================================

const BEADS_GROUPING_KEY = "mitto_beads_grouping";

describe("getBeadsGrouping", () => {
  test("returns defaults when localStorage is empty", () => {
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("returns stored grouping when present", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({ enabled: true, collapsedEpics: ["mitto-abc", "mitto-xyz"] });
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: ["mitto-abc", "mitto-xyz"] });
  });

  test("fills missing fields with defaults", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({ enabled: true });
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("ignores non-boolean enabled and uses default", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({ enabled: "yes", collapsedEpics: [] });
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("filters non-string entries from collapsedEpics", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({ enabled: false, collapsedEpics: ["ok", 42, null, "also-ok"] });
    expect(getBeadsGrouping()).toEqual({ enabled: false, collapsedEpics: ["ok", "also-ok"] });
  });

  test("returns defaults for corrupt JSON", () => {
    mockStore[BEADS_GROUPING_KEY] = "not-json{";
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });
});

describe("setBeadsGrouping", () => {
  test("persists grouping state to localStorage", () => {
    setBeadsGrouping({ enabled: true, collapsedEpics: ["mitto-1"] });
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({ enabled: true, collapsedEpics: ["mitto-1"] });
  });

  test("fills missing fields with defaults when saving", () => {
    setBeadsGrouping({ enabled: true });
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("uses all defaults when given no argument", () => {
    setBeadsGrouping();
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("round-trips through getBeadsGrouping", () => {
    const state = { enabled: true, collapsedEpics: ["mitto-abc", "mitto-def"] };
    setBeadsGrouping(state);
    expect(getBeadsGrouping()).toEqual(state);
  });
});

// =============================================================================
// getBeadsSort / setBeadsSort Tests
// =============================================================================

const BEADS_SORT_KEY = "mitto_beads_sort";

describe("getBeadsSort", () => {
  test("returns newest-first default when localStorage empty", () => {
    expect(getBeadsSort()).toEqual({ field: "created", direction: "desc" });
  });

  test("returns stored field and direction", () => {
    mockStore[BEADS_SORT_KEY] = JSON.stringify({ field: "priority", direction: "asc" });
    expect(getBeadsSort()).toEqual({ field: "priority", direction: "asc" });
  });

  test("falls back to defaults for invalid field/direction", () => {
    mockStore[BEADS_SORT_KEY] = JSON.stringify({ field: "bogus", direction: "sideways" });
    expect(getBeadsSort()).toEqual({ field: "created", direction: "desc" });
  });

  test("invalid JSON → returns default", () => {
    mockStore[BEADS_SORT_KEY] = "not-json{";
    expect(getBeadsSort()).toEqual({ field: "created", direction: "desc" });
  });
});

describe("setBeadsSort", () => {
  test("persists sort state to localStorage", () => {
    setBeadsSort({ field: "updated", direction: "asc" });
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({ field: "updated", direction: "asc" });
  });

  test("normalizes invalid values to defaults when saving", () => {
    setBeadsSort({ field: "nope", direction: "nope" });
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({ field: "created", direction: "desc" });
  });

  test("uses all defaults when given no argument", () => {
    setBeadsSort();
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({ field: "created", direction: "desc" });
  });

  test("round-trips through getBeadsSort", () => {
    const state = { field: "priority", direction: "desc" };
    setBeadsSort(state);
    expect(getBeadsSort()).toEqual(state);
  });
});

// =============================================================================
// getCategoryFilter / setCategoryFilter Tests
// =============================================================================

describe("getCategoryFilter / setCategoryFilter", () => {
  test("returns all-true default when sessionStorage empty", () => {
    const result = getCategoryFilter();
    expect(result).toEqual(DEFAULT_CATEGORY_FILTER);
    expect(result.regular).toBe(true);
    expect(result.periodic).toBe(true);
    expect(result.archived).toBe(true);
    expect(result.tasks).toBe(true);
  });

  test("round-trips: setCategoryFilter then getCategoryFilter", () => {
    setCategoryFilter({ regular: false, periodic: true, archived: true, tasks: false });
    const result = getCategoryFilter();
    expect(result.regular).toBe(false);
    expect(result.periodic).toBe(true);
    expect(result.archived).toBe(true);
    expect(result.tasks).toBe(false);
  });

  test("invalid JSON in sessionStorage → returns all-true default", () => {
    sessionMockStore["mitto_category_filter"] = "not-valid-json{{{";
    const result = getCategoryFilter();
    expect(result).toEqual(DEFAULT_CATEGORY_FILTER);
  });

  test("partial object persisted → missing keys normalized to true", () => {
    sessionMockStore["mitto_category_filter"] = JSON.stringify({ regular: false });
    const result = getCategoryFilter();
    expect(result.regular).toBe(false);
    expect(result.periodic).toBe(true);
    expect(result.archived).toBe(true);
    expect(result.tasks).toBe(true);
  });
});

// =============================================================================
// migrateLegacyTabStorage Tests
// =============================================================================

describe("migrateLegacyTabStorage", () => {
  const EXPANDED_KEY = "mitto_conversation_expanded_groups";
  const DONE_KEY = "mitto_detab_migration_done";

  test("removes orphaned tab keys and strips \\u0001-scoped expanded-group entries", () => {
    // Seed orphaned top-level keys
    mockStore["mitto_conversation_filter_tab"] = "conversations";
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({ conversations: "folder" });
    mockStore["mitto_last_session_id_conversations"] = "s1";
    mockStore["mitto_last_session_id_periodic"] = "s2";
    mockStore["mitto_last_session_id_archived"] = "s3";

    // Seed expanded-groups with a mix of old tab-scoped (\u0001) and new unscoped keys
    mockStore[EXPANDED_KEY] = JSON.stringify({
      "conversations\u0001/home/user/project": true,  // OLD — must be removed
      "/home/user/project": false,                     // NEW bare folder — must survive
      "archived:/home/user/project": true,             // NEW — must survive
      "parent:abc123": true,                           // NEW — must survive
    });

    migrateLegacyTabStorage();

    // Orphaned top-level keys gone
    expect(mockStore["mitto_conversation_filter_tab"]).toBeUndefined();
    expect(mockStore["mitto_filter_tab_grouping"]).toBeUndefined();
    expect(mockStore["mitto_last_session_id_conversations"]).toBeUndefined();
    expect(mockStore["mitto_last_session_id_periodic"]).toBeUndefined();
    expect(mockStore["mitto_last_session_id_archived"]).toBeUndefined();

    // Tab-scoped entry stripped; unscoped entries survive
    const groups = JSON.parse(mockStore[EXPANDED_KEY]);
    expect(groups["conversations\u0001/home/user/project"]).toBeUndefined();
    expect(groups["/home/user/project"]).toBe(false);
    expect(groups["archived:/home/user/project"]).toBe(true);
    expect(groups["parent:abc123"]).toBe(true);

    // Done flag set
    expect(mockStore[DONE_KEY]).toBe("1");
  });

  test("idempotency: second call is a no-op when guard is already set", () => {
    // Run migration once to set the done flag
    mockStore["mitto_conversation_filter_tab"] = "conversations";
    migrateLegacyTabStorage();
    expect(mockStore[DONE_KEY]).toBe("1");

    // Re-seed the orphaned key (simulating stale state)
    mockStore["mitto_conversation_filter_tab"] = "periodic";

    // Second call should not touch anything
    migrateLegacyTabStorage();
    expect(mockStore["mitto_conversation_filter_tab"]).toBe("periodic");
  });
});

// =============================================================================
// getDensity Tests
// =============================================================================

describe("getDensity", () => {
  test("returns 'condensed' when localStorage is empty (default)", () => {
    expect(getDensity()).toBe("condensed");
  });

  test("returns 'condensed' when localStorage has 'condensed'", () => {
    localStorageMock.setItem(DENSITY_KEY, "condensed");
    expect(getDensity()).toBe("condensed");
  });

  test("returns 'comfortable' when localStorage has 'comfortable'", () => {
    localStorageMock.setItem(DENSITY_KEY, "comfortable");
    expect(getDensity()).toBe("comfortable");
  });

  test("returns 'condensed' for invalid values (default)", () => {
    localStorageMock.setItem(DENSITY_KEY, "invalid");
    expect(getDensity()).toBe("condensed");
  });
});

// =============================================================================
// setDensity Tests
// =============================================================================

describe("setDensity", () => {
  test("persists 'comfortable' to localStorage", () => {
    setDensity("comfortable");
    expect(mockStore[DENSITY_KEY]).toBe("comfortable");
  });

  test("persists 'condensed' to localStorage", () => {
    setDensity("condensed");
    expect(mockStore[DENSITY_KEY]).toBe("condensed");
  });

  test("removes the stored value for invalid input", () => {
    localStorageMock.setItem(DENSITY_KEY, "comfortable");
    setDensity("invalid");
    expect(mockStore[DENSITY_KEY]).toBeUndefined();
  });

  test("round-trips through getDensity (persists across reads)", () => {
    setDensity("comfortable");
    expect(getDensity()).toBe("comfortable");
    setDensity("condensed");
    expect(getDensity()).toBe("condensed");
  });
});
