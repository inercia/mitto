/**
 * Tests for useFolderTaskLabelColorsConfig.js (mitto-m5f.3 Test phase).
 *
 * Covers the tab-open load effect (GET /api/folders/task-label-colors), the
 * folder-switch reset effect, the four row-mutation handlers (reused from
 * utils/taskLabelColors.js), and persistTaskLabelColors (validation, PUT,
 * and the mitto:folder_task_label_colors_updated dispatch). Mirrors the
 * window.preact stub harness established by useFolderShortcutsConfig.test.js.
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentSetters = [];
let currentEffects = [];
// Optional per-call-index overrides for the current test, keyed by the
// useState declaration order (see IDX below). The hook's useState calls are
// stubbed (setters never actually re-render the hook), so a test that needs
// to exercise behavior gated on a state value reaching some non-initial
// value (e.g. `loaded` becoming true after the load effect resolves) sets
// the override here before calling the hook.
let stateOverrides = {};
window.preact = {
  useState: (initial) => {
    const idx = currentSetters.length;
    const setter = jest.fn();
    currentSetters.push(setter);
    const value = Object.prototype.hasOwnProperty.call(stateOverrides, idx)
      ? stateOverrides[idx]
      : initial;
    return [value, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

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

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

async function loadHook(overrides = {}) {
  currentSetters = [];
  currentEffects = [];
  stateOverrides = overrides;
  const mod = await import("./useFolderTaskLabelColorsConfig.js");
  return {
    useFolderTaskLabelColorsConfig: mod.useFolderTaskLabelColorsConfig,
    setters: currentSetters,
    effects: currentEffects,
  };
}

// Positional setter indices matching the useState declaration order in the
// source: entries, loading, loaded, error.
const IDX = {
  setEntries: 0,
  setLoading: 1,
  setLoaded: 2,
  setError: 3,
};

describe("useFolderTaskLabelColorsConfig — load effect", () => {
  test("loads folder entries and marks loaded when the Tasks tab is active", async () => {
    global.fetch = jest.fn((url) => {
      expect(String(url)).toContain("/api/folders/task-label-colors");
      expect(String(url)).toContain("working_dir=");
      return Promise.resolve(
        jsonResponse({
          entries: [{ label: "blocked", color: "#111111" }],
        }),
      );
    });
    const { useFolderTaskLabelColorsConfig, setters, effects } =
      await loadHook();
    useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });

    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("beads"),
    );
    expect(loadEffect).toBeDefined();
    loadEffect.cb();
    await flush();

    expect(setters[IDX.setEntries]).toHaveBeenCalledWith([
      { label: "blocked", color: "#111111" },
    ]);
    expect(setters[IDX.setLoaded]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setLoading]).toHaveBeenLastCalledWith(false);
  });

  test("sets an error and never marks loaded when the fetch fails", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: "boom" }, 500)),
    );
    const { useFolderTaskLabelColorsConfig, setters, effects } =
      await loadHook();
    useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });

    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("beads"),
    );
    loadEffect.cb();
    await flush();

    expect(setters[IDX.setError]).toHaveBeenCalledWith(
      expect.stringContaining("Failed to load task label colors:"),
    );
    expect(setters[IDX.setLoaded]).not.toHaveBeenCalledWith(true);
    expect(setters[IDX.setLoading]).toHaveBeenLastCalledWith(false);
  });

  test("is a no-op when the active tab is not beads", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig, effects } = await loadHook();
    useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("shortcuts"),
    );
    loadEffect.cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("is a no-op when there is no selected folder or resolvable working_dir", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig, effects } = await loadHook();
    useFolderTaskLabelColorsConfig({
      selectedFolder: null,
      activeTab: "beads",
      getSelectedFolderDir: () => null,
    });
    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("beads"),
    );
    loadEffect.cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderTaskLabelColorsConfig — folder-switch reset effect", () => {
  test("resets entries, error, and loaded on folder switch", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig, setters, effects } =
      await loadHook();
    useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    const resetEffect = effects.find(
      (e) => e.deps && e.deps.length === 1 && e.deps[0] === "myfolder",
    );
    expect(resetEffect).toBeDefined();
    resetEffect.cb();
    expect(setters[IDX.setEntries]).toHaveBeenCalledWith([]);
    expect(setters[IDX.setError]).toHaveBeenCalledWith("");
    expect(setters[IDX.setLoaded]).toHaveBeenCalledWith(false);
  });
});

describe("useFolderTaskLabelColorsConfig — row handlers", () => {
  // All four handlers call setEntries with a functional updater (prev =>
  // next); the useState stub records the updater without applying it, so
  // each test invokes the captured updater against a synthetic `prev`.
  async function mountHandlers() {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig, setters } = await loadHook();
    const { taskLabelColorsHandlers } = useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    return { taskLabelColorsHandlers, setEntries: setters[IDX.setEntries] };
  }

  test("onAdd appends a blank row with the default color", async () => {
    const { taskLabelColorsHandlers, setEntries } = await mountHandlers();
    taskLabelColorsHandlers.onAdd();
    const updater = setEntries.mock.calls[0][0];
    expect(updater([{ label: "a", color: "#111111" }])).toEqual([
      { label: "a", color: "#111111" },
      { label: "", color: "#ef4444" },
    ]);
  });

  test("onUpdate immutably merges a patch into the target row", async () => {
    const { taskLabelColorsHandlers, setEntries } = await mountHandlers();
    taskLabelColorsHandlers.onUpdate(0, { color: "#123456" });
    const updater = setEntries.mock.calls[0][0];
    const prev = [{ label: "a", color: "#111111" }];
    expect(updater(prev)).toEqual([{ label: "a", color: "#123456" }]);
    expect(prev[0].color).toBe("#111111"); // unmutated
  });

  test("onRemove splices the row out", async () => {
    const { taskLabelColorsHandlers, setEntries } = await mountHandlers();
    taskLabelColorsHandlers.onRemove(0);
    const updater = setEntries.mock.calls[0][0];
    expect(
      updater([
        { label: "a", color: "#111111" },
        { label: "b", color: "#222222" },
      ]),
    ).toEqual([{ label: "b", color: "#222222" }]);
  });

  test("onMove swaps adjacent rows and no-ops out of bounds", async () => {
    const { taskLabelColorsHandlers, setEntries } = await mountHandlers();
    taskLabelColorsHandlers.onMove(0, 1);
    const updater = setEntries.mock.calls[0][0];
    const prev = [
      { label: "a", color: "#111111" },
      { label: "b", color: "#222222" },
    ];
    expect(updater(prev)).toEqual([prev[1], prev[0]]);

    taskLabelColorsHandlers.onMove(0, -1);
    const updater2 = setEntries.mock.calls[setEntries.mock.calls.length - 1][0];
    expect(updater2(prev)).toBe(prev); // out-of-range move is a no-op
  });
});

describe("useFolderTaskLabelColorsConfig — persistTaskLabelColors", () => {
  test("PUTs the trimmed entries and dispatches mitto:folder_task_label_colors_updated", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
        return Promise.resolve(
          jsonResponse({ entries: [{ label: "blocked", color: "#111111" }] }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    });
    const events = [];
    window.addEventListener("mitto:folder_task_label_colors_updated", (e) =>
      events.push(e.detail),
    );

    // `loaded` (IDX.setLoaded's paired state) is true and `entries` (IDX.setEntries)
    // already holds a valid row, simulating the post-load-effect state that
    // gates persistTaskLabelColors open.
    const { useFolderTaskLabelColorsConfig } = await loadHook({
      [IDX.setEntries]: [{ label: "  blocked  ", color: " #111111 " }],
      [IDX.setLoaded]: true,
    });
    const { persistTaskLabelColors } = useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });

    await persistTaskLabelColors();

    expect(putCalls).toHaveLength(1);
    expect(putCalls[0].url).toContain("/api/folders/task-label-colors");
    expect(putCalls[0].url).toContain("working_dir=");
    const body = JSON.parse(putCalls[0].body);
    // Entries are trimmed (label and color) before being sent.
    expect(body).toEqual({ entries: [{ label: "blocked", color: "#111111" }] });
    expect(events).toEqual([{ working_dir: "/tmp/myfolder" }]);
  });

  test("rejects an empty label before calling the SDK", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig } = await loadHook({
      [IDX.setEntries]: [{ label: "  ", color: "#111111" }],
      [IDX.setLoaded]: true,
    });
    const { persistTaskLabelColors } = useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    await expect(persistTaskLabelColors()).rejects.toThrow(
      "Task labels must not be empty",
    );
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("rejects a non-six-digit-hex color before calling the SDK", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig } = await loadHook({
      [IDX.setEntries]: [{ label: "blocked", color: "red" }],
      [IDX.setLoaded]: true,
    });
    const { persistTaskLabelColors } = useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    await expect(persistTaskLabelColors()).rejects.toThrow(
      "Task label colors must be six-digit hex values",
    );
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("is a no-op when not yet loaded (persist runs before the load effect resolves)", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig } = await loadHook({
      [IDX.setLoaded]: false,
    });
    const { persistTaskLabelColors } = useFolderTaskLabelColorsConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    await persistTaskLabelColors();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("is a no-op when there is no resolvable folder working_dir", async () => {
    global.fetch = jest.fn();
    const { useFolderTaskLabelColorsConfig } = await loadHook({
      [IDX.setLoaded]: true,
    });
    const { persistTaskLabelColors } = useFolderTaskLabelColorsConfig({
      selectedFolder: null,
      activeTab: "beads",
      getSelectedFolderDir: () => null,
    });
    await persistTaskLabelColors();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});
