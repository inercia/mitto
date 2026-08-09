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
  getDashboardHiddenCharts,
  setDashboardHiddenCharts,
  initUIPreferences,
  onUIPreferencesLoaded,
  _resetUIPreferencesStateForTests,
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
  Object.defineProperty(window, "sessionStorage", {
    value: sessionStorageMock,
    writable: true,
  });
});

// =============================================================================
// UI Preferences Server Sync Tests (mitto-7gta.17 slice S1: SDK client)
// =============================================================================
//
// initUIPreferences()/saveUIPreferencesToServer() were migrated from
// authFetch/secureFetch onto getSdkClient().misc.uiPreferences.get()/.save()
// in slice S1 but had no dedicated coverage — the module-level `global.fetch`
// stub above only prevents test crashes, it never asserted on the request or
// on how a response (or a failure) is applied. These tests fill that gap.

/** A successful JSON response. `.text()` is what sdk/core/transport.js's
 * decodeBody() reads; `.json()` is what sdk/auth/browser-cookie.js's CSRF
 * token pre-flight (fired for the PUT save) reads — both are provided so
 * either code path can decode the same mock response. */
function jsonResponse(data, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name) =>
        name.toLowerCase() === "content-type" ? "application/json" : null,
    },
    text: () => Promise.resolve(JSON.stringify(data)),
    json: () => Promise.resolve(data),
  };
}

/** A non-2xx response, for exercising the SDK's MittoApiError throw path. */
function errorResponse(status, body) {
  return {
    ok: false,
    status,
    headers: { get: () => null },
    text: () => Promise.resolve(JSON.stringify(body)),
  };
}

describe("initUIPreferences", () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    _resetUIPreferencesStateForTests();
  });

  test("fetches GET /api/ui-preferences via the SDK client and syncs every field to localStorage", async () => {
    const prefs = {
      grouping_mode: "workspace",
      expanded_groups: { foo: true },
      prompt_sort_mode: "name",
      font_size: "large",
      theme: "dark",
      theme_light: "mitto-light",
      theme_dark: "mitto-dark",
      follow_system_theme: false,
      follow_system_reduced_motion: true,
      reduce_animations: true,
      dashboard_hidden_charts: ["tokens"],
    };
    let capturedUrl;
    let capturedMethod;
    global.fetch = (url, init) => {
      capturedUrl = url;
      capturedMethod = init?.method;
      return Promise.resolve(jsonResponse(prefs));
    };

    await initUIPreferences();

    expect(capturedMethod).toBe("GET");
    expect(capturedUrl).toContain("/api/ui-preferences");
    expect(mockStore["mitto_conversation_grouping_mode"]).toBe("workspace");
    expect(JSON.parse(mockStore["mitto_conversation_expanded_groups"])).toEqual({
      foo: true,
    });
    expect(mockStore["mitto_prompt_sort_mode"]).toBe("name");
    expect(mockStore["mitto-font-size"]).toBe("large");
    expect(mockStore["mitto-theme"]).toBe("dark");
    expect(mockStore["mitto-theme-light"]).toBe("mitto-light");
    expect(mockStore["mitto-theme-dark"]).toBe("mitto-dark");
    expect(mockStore["mitto-follow-system-theme"]).toBe("false");
    expect(mockStore["mitto-follow-system-reduced-motion"]).toBe("true");
    expect(mockStore["mitto-reduce-animations"]).toBe("true");
    expect(JSON.parse(mockStore["mitto-dashboard-hidden-charts"])).toEqual([
      "tokens",
    ]);
  });

  test("notifies onUIPreferencesLoaded listeners with the fetched preferences", async () => {
    const prefs = { grouping_mode: "server" };
    global.fetch = () => Promise.resolve(jsonResponse(prefs));

    const received = [];
    const unsubscribe = onUIPreferencesLoaded((p) => received.push(p));

    await initUIPreferences();

    expect(received).toEqual([prefs]);
    unsubscribe();
  });

  test("a listener subscribed after load has already completed fires immediately with the cached prefs", async () => {
    const prefs = { grouping_mode: "server" };
    global.fetch = () => Promise.resolve(jsonResponse(prefs));
    await initUIPreferences();

    const received = [];
    onUIPreferencesLoaded((p) => received.push(p));

    expect(received).toEqual([prefs]);
  });

  test("caches the sync promise: a second call does not issue a second request", async () => {
    let fetchCount = 0;
    global.fetch = () => {
      fetchCount++;
      return Promise.resolve(jsonResponse({ grouping_mode: "server" }));
    };

    await initUIPreferences();
    await initUIPreferences();

    expect(fetchCount).toBe(1);
  });

  test("a network failure is caught and does not throw or populate localStorage", async () => {
    global.fetch = () => Promise.reject(new Error("network down"));

    await expect(initUIPreferences()).resolves.toBeUndefined();
    expect(mockStore["mitto_conversation_grouping_mode"]).toBeUndefined();
  });

  test("a non-2xx response (SDK throws MittoApiError) is caught and does not throw", async () => {
    global.fetch = () => Promise.resolve(errorResponse(500, { error: "boom" }));

    await expect(initUIPreferences()).resolves.toBeUndefined();
  });
});

describe("saveUIPreferencesToServer (via setGroupingMode)", () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    _resetUIPreferencesStateForTests();
  });

  test("PUTs the current preferences to /api/ui-preferences after the debounce window", async () => {
    let capturedUrl;
    let capturedMethod;
    let capturedBody;
    global.fetch = (url, init) => {
      capturedUrl = url;
      capturedMethod = init?.method;
      capturedBody = init?.body ? JSON.parse(init.body) : null;
      return Promise.resolve(jsonResponse({}));
    };

    setGroupingMode("workspace");
    // Debounce is 500ms; wait past it for the async save to fire.
    await new Promise((resolve) => setTimeout(resolve, 550));

    expect(capturedMethod).toBe("PUT");
    expect(capturedUrl).toContain("/api/ui-preferences");
    expect(capturedBody.grouping_mode).toBe("workspace");
  });

  test("debounces rapid successive changes into a single PUT", async () => {
    // Count only the actual save PUTs, not the CSRF-token GET pre-flight
    // browserCookieAuth issues once per state-changing request.
    let putCount = 0;
    global.fetch = (url, init) => {
      if ((init?.method || "GET").toUpperCase() === "PUT") putCount++;
      return Promise.resolve(jsonResponse({}));
    };

    setGroupingMode("server");
    setGroupingMode("workspace");
    setGroupingMode("folder");
    await new Promise((resolve) => setTimeout(resolve, 550));

    expect(putCount).toBe(1);
  });

  test("a failed PUT is caught and does not throw or propagate", async () => {
    global.fetch = () => Promise.resolve(errorResponse(500, { error: "boom" }));

    expect(() => setGroupingMode("workspace")).not.toThrow();
    // Let the debounced, now-rejecting save settle without an unhandled
    // rejection surfacing (it must be caught internally).
    await new Promise((resolve) => setTimeout(resolve, 550));
  });
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
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({
      enabled: true,
      collapsedEpics: ["mitto-abc", "mitto-xyz"],
    });
    expect(getBeadsGrouping()).toEqual({
      enabled: true,
      collapsedEpics: ["mitto-abc", "mitto-xyz"],
    });
  });

  test("fills missing fields with defaults", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({ enabled: true });
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("ignores non-boolean enabled and uses default", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({
      enabled: "yes",
      collapsedEpics: [],
    });
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });

  test("filters non-string entries from collapsedEpics", () => {
    mockStore[BEADS_GROUPING_KEY] = JSON.stringify({
      enabled: false,
      collapsedEpics: ["ok", 42, null, "also-ok"],
    });
    expect(getBeadsGrouping()).toEqual({
      enabled: false,
      collapsedEpics: ["ok", "also-ok"],
    });
  });

  test("returns defaults for corrupt JSON", () => {
    mockStore[BEADS_GROUPING_KEY] = "not-json{";
    expect(getBeadsGrouping()).toEqual({ enabled: true, collapsedEpics: [] });
  });
});

describe("setBeadsGrouping", () => {
  test("persists grouping state to localStorage", () => {
    setBeadsGrouping({ enabled: true, collapsedEpics: ["mitto-1"] });
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({
      enabled: true,
      collapsedEpics: ["mitto-1"],
    });
  });

  test("fills missing fields with defaults when saving", () => {
    setBeadsGrouping({ enabled: true });
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({
      enabled: true,
      collapsedEpics: [],
    });
  });

  test("uses all defaults when given no argument", () => {
    setBeadsGrouping();
    expect(JSON.parse(mockStore[BEADS_GROUPING_KEY])).toEqual({
      enabled: true,
      collapsedEpics: [],
    });
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
    mockStore[BEADS_SORT_KEY] = JSON.stringify({
      field: "priority",
      direction: "asc",
    });
    expect(getBeadsSort()).toEqual({ field: "priority", direction: "asc" });
  });

  test("falls back to defaults for invalid field/direction", () => {
    mockStore[BEADS_SORT_KEY] = JSON.stringify({
      field: "bogus",
      direction: "sideways",
    });
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
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({
      field: "updated",
      direction: "asc",
    });
  });

  test("normalizes invalid values to defaults when saving", () => {
    setBeadsSort({ field: "nope", direction: "nope" });
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({
      field: "created",
      direction: "desc",
    });
  });

  test("uses all defaults when given no argument", () => {
    setBeadsSort();
    expect(JSON.parse(mockStore[BEADS_SORT_KEY])).toEqual({
      field: "created",
      direction: "desc",
    });
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
    expect(result.loop).toBe(true);
    expect(result.archived).toBe(true);
    expect(result.tasks).toBe(true);
  });

  test("round-trips: setCategoryFilter then getCategoryFilter", () => {
    setCategoryFilter({
      regular: false,
      loop: true,
      archived: true,
      tasks: false,
    });
    const result = getCategoryFilter();
    expect(result.regular).toBe(false);
    expect(result.loop).toBe(true);
    expect(result.archived).toBe(true);
    expect(result.tasks).toBe(false);
  });

  test("invalid JSON in sessionStorage → returns all-true default", () => {
    sessionMockStore["mitto_category_filter"] = "not-valid-json{{{";
    const result = getCategoryFilter();
    expect(result).toEqual(DEFAULT_CATEGORY_FILTER);
  });

  test("partial object persisted → missing keys normalized to true", () => {
    sessionMockStore["mitto_category_filter"] = JSON.stringify({
      regular: false,
    });
    const result = getCategoryFilter();
    expect(result.regular).toBe(false);
    expect(result.loop).toBe(true);
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
    mockStore["mitto_filter_tab_grouping"] = JSON.stringify({
      conversations: "folder",
    });
    mockStore["mitto_last_session_id_conversations"] = "s1";
    // Historical 3-tab sidebar key (tab was named "periodic" pre-rename)
    mockStore["mitto_last_session_id_periodic"] = "s2";
    mockStore["mitto_last_session_id_archived"] = "s3";

    // Seed expanded-groups with a mix of old tab-scoped (\u0001) and new unscoped keys
    mockStore[EXPANDED_KEY] = JSON.stringify({
      "conversations\u0001/home/user/project": true, // OLD — must be removed
      "/home/user/project": false, // NEW bare folder — must survive
      "archived:/home/user/project": true, // NEW — must survive
      "parent:abc123": true, // NEW — must survive
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
    mockStore["mitto_conversation_filter_tab"] = "loop";

    // Second call should not touch anything
    migrateLegacyTabStorage();
    expect(mockStore["mitto_conversation_filter_tab"]).toBe("loop");
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

// =============================================================================
// getDashboardHiddenCharts / setDashboardHiddenCharts (mitto-4t8 / mitto-3i2)
// =============================================================================
//
// Pins the storage-level contract used by StatsCharts.js's
// useDashboardHiddenCharts hook: (a) parsing rejects unknown ids and non-array
// payloads defensively, (b) writes filter to strings and dispatch the
// live-update CustomEvent BEFORE the debounced server sync, so any open
// Dashboard reacts instantly.

const DASHBOARD_HIDDEN_CHARTS_KEY = "mitto-dashboard-hidden-charts";
const DASHBOARD_EVENT_NAME = "mitto-dashboard-hidden-charts-changed";

describe("getDashboardHiddenCharts", () => {
  test("returns [] when nothing persisted", () => {
    expect(getDashboardHiddenCharts()).toEqual([]);
  });

  test("returns the persisted array when it is a subset of the known ids", () => {
    localStorageMock.setItem(
      DASHBOARD_HIDDEN_CHARTS_KEY,
      JSON.stringify(["tokens", "model_usage"]),
    );
    expect(getDashboardHiddenCharts()).toEqual(["tokens", "model_usage"]);
  });

  test("drops unknown ids and non-string entries (opt-out defence)", () => {
    localStorageMock.setItem(
      DASHBOARD_HIDDEN_CHARTS_KEY,
      JSON.stringify(["tokens", "bogus", 42, null, "tool_calls"]),
    );
    // Only canonical ids survive; ordering follows the persisted array.
    expect(getDashboardHiddenCharts()).toEqual(["tokens", "tool_calls"]);
  });

  test("returns [] when the stored value is not an array", () => {
    localStorageMock.setItem(
      DASHBOARD_HIDDEN_CHARTS_KEY,
      JSON.stringify({ tokens: true }),
    );
    expect(getDashboardHiddenCharts()).toEqual([]);
  });

  test("returns [] when the stored value is malformed JSON", () => {
    localStorageMock.setItem(DASHBOARD_HIDDEN_CHARTS_KEY, "not-json");
    // The parse error is swallowed with a console.warn; the caller sees the
    // safe fallback (empty array = everything visible).
    expect(getDashboardHiddenCharts()).toEqual([]);
  });

  test("accepts the beads_activity / beads_cycle_time ids (mitto-5rm6)", () => {
    // Pins that KNOWN_DASHBOARD_CHART_IDS was actually extended for the
    // beads throughput charts, not just claimed in a commit message — a
    // dropped mirror entry would otherwise silently un-hide-able these two
    // charts (getDashboardHiddenCharts filters unknown ids out on read).
    localStorageMock.setItem(
      DASHBOARD_HIDDEN_CHARTS_KEY,
      JSON.stringify(["beads_activity", "beads_cycle_time"]),
    );
    expect(getDashboardHiddenCharts()).toEqual([
      "beads_activity",
      "beads_cycle_time",
    ]);
  });
});

describe("setDashboardHiddenCharts", () => {
  test("writes a JSON-encoded array to localStorage", () => {
    setDashboardHiddenCharts(["tokens", "tool_calls"]);
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(
      JSON.stringify(["tokens", "tool_calls"]),
    );
  });

  test("coerces a non-array input to [] (never throws)", () => {
    setDashboardHiddenCharts(null);
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(JSON.stringify([]));
    setDashboardHiddenCharts(undefined);
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(JSON.stringify([]));
    setDashboardHiddenCharts("tokens");
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(JSON.stringify([]));
  });

  test("filters out non-string entries before persisting", () => {
    setDashboardHiddenCharts(["tokens", 42, null, "tool_calls", undefined]);
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(
      JSON.stringify(["tokens", "tool_calls"]),
    );
  });

  test("dispatches the mitto-dashboard-hidden-charts-changed CustomEvent with the sanitised ids", () => {
    const events = [];
    const listener = (e) => events.push(e);
    window.addEventListener(DASHBOARD_EVENT_NAME, listener);
    try {
      setDashboardHiddenCharts(["tokens", 7, "model_usage"]);
    } finally {
      window.removeEventListener(DASHBOARD_EVENT_NAME, listener);
    }
    expect(events).toHaveLength(1);
    // Every open Dashboard reads `detail.ids` and re-renders on this signal.
    expect(events[0].detail).toEqual({ ids: ["tokens", "model_usage"] });
    // Payload matches what was persisted (no drift between the localStorage
    // write and the broadcast — the hook can read either as source of truth).
    expect(mockStore[DASHBOARD_HIDDEN_CHARTS_KEY]).toBe(
      JSON.stringify(["tokens", "model_usage"]),
    );
  });

  test("dispatches the CustomEvent even for an empty selection (show-everything path)", () => {
    // Regression: the "show everything again" path (user unhides the last
    // chart in Settings ▸ Dashboard) must still broadcast so the Dashboard
    // stops rendering the empty-state fallback.
    const events = [];
    const listener = (e) => events.push(e);
    window.addEventListener(DASHBOARD_EVENT_NAME, listener);
    try {
      setDashboardHiddenCharts([]);
    } finally {
      window.removeEventListener(DASHBOARD_EVENT_NAME, listener);
    }
    expect(events).toHaveLength(1);
    expect(events[0].detail).toEqual({ ids: [] });
  });

  test("round-trips through getDashboardHiddenCharts", () => {
    setDashboardHiddenCharts(["tokens", "prompts_vs_turns"]);
    expect(getDashboardHiddenCharts()).toEqual(["tokens", "prompts_vs_turns"]);
    setDashboardHiddenCharts([]);
    expect(getDashboardHiddenCharts()).toEqual([]);
  });
});
