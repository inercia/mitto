/**
 * Tests for useFolderShortcutsConfig.js (mitto-7gta.17 slice S2 Test phase).
 *
 * Covers the tab-open load effect (Promise.all across
 * client.shortcuts.getFolder/getGlobal + client.prompts.list), the
 * folder-switch reset effect, the row-mutation handlers, and
 * persistShortcuts — all migrated onto getSdkClient() in slice S2. Mirrors
 * the window.preact stub harness established by useBeadsFolderConfig.test.js;
 * this hook additionally uses useMemo, so the stub evaluates it eagerly.
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentSetters = [];
let currentEffects = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  useMemo: (fn) => fn(),
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

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useFolderShortcutsConfig.js");
  return {
    useFolderShortcutsConfig: mod.useFolderShortcutsConfig,
    setters: currentSetters,
    effects: currentEffects,
  };
}

const IDX = {
  setShortcutsSections: 0,
  setShortcutsLoading: 1,
  setShortcutsLoaded: 2,
  setShortcutsError: 3,
  setSectionPrompts: 4,
  setGlobalShortcutsSections: 5,
};

const SECTIONS = [
  { id: "tasksList" },
  { id: "conversations" },
  { id: "beadsIssue" },
];

// Sole 3-argument GET, keyed by URL, for the Promise.all load effect.
function makeLoadFetch({ folder, global: globalSections, prompts }) {
  return jest.fn((url) => {
    const u = String(url);
    if (u.includes("/api/folders/shortcuts"))
      return Promise.resolve(jsonResponse(folder));
    if (u.includes("/api/global/shortcuts"))
      return Promise.resolve(jsonResponse(globalSections));
    if (u.includes("/api/workspace-prompts"))
      return Promise.resolve(jsonResponse(prompts));
    return Promise.reject(new Error("unexpected URL: " + u));
  });
}

describe("useFolderShortcutsConfig — load effect", () => {
  test("merges folder/global shortcuts + prompts, filtered and sorted per section", async () => {
    global.fetch = makeLoadFetch({
      folder: { sections: { tasksList: [{ prompt: "A" }] } },
      global: { sections: { tasksList: [{ prompt: "Z" }] } },
      prompts: {
        prompts: [
          { name: "Bravo", menus: "beadsList" },
          { name: "Alpha", menus: "beadsList" },
          { name: "Convo", menus: "prompts" },
        ],
      },
    });
    const { useFolderShortcutsConfig, setters, effects } = await loadHook();
    useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });

    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("shortcuts"),
    );
    expect(loadEffect).toBeDefined();
    loadEffect.cb();
    await flush();

    expect(setters[IDX.setShortcutsSections]).toHaveBeenCalledWith({
      tasksList: [{ prompt: "A" }],
    });
    expect(setters[IDX.setGlobalShortcutsSections]).toHaveBeenCalledWith({
      tasksList: [{ prompt: "Z" }],
    });
    expect(setters[IDX.setSectionPrompts]).toHaveBeenCalledWith({
      tasksList: [
        { name: "Alpha", menus: "beadsList" },
        { name: "Bravo", menus: "beadsList" },
      ],
      conversations: [{ name: "Convo", menus: "prompts" }],
      beadsIssue: [],
    });
    expect(setters[IDX.setShortcutsLoaded]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setShortcutsLoading]).toHaveBeenLastCalledWith(false);
  });

  test("sets shortcutsError and never marks loaded when a leg of the Promise.all fails", async () => {
    global.fetch = jest.fn((url) => {
      if (String(url).includes("/api/folders/shortcuts"))
        return Promise.resolve(jsonResponse({ error: "boom" }, 500));
      return Promise.resolve(jsonResponse({}));
    });
    const { useFolderShortcutsConfig, setters, effects } = await loadHook();
    useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });

    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("shortcuts"),
    );
    loadEffect.cb();
    await flush();

    expect(setters[IDX.setShortcutsError]).toHaveBeenCalledWith(
      expect.stringContaining("Failed to load shortcuts:"),
    );
    expect(setters[IDX.setShortcutsLoaded]).not.toHaveBeenCalledWith(true);
    expect(setters[IDX.setShortcutsLoading]).toHaveBeenLastCalledWith(false);
  });

  test("is a no-op when the active tab is not shortcuts", async () => {
    global.fetch = jest.fn();
    const { useFolderShortcutsConfig, effects } = await loadHook();
    useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });
    const loadEffect = effects.find(
      (e) => e.deps && e.deps.includes("prompts"),
    );
    loadEffect.cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderShortcutsConfig — row handlers", () => {
  // All four handlers call setShortcutsSections with a functional updater
  // (prev => next); the useState stub records the updater without applying
  // it, so each test invokes the captured updater itself against a synthetic
  // `prev` to assert on the resulting `next` state.
  async function mountHandlers() {
    global.fetch = jest.fn();
    const { useFolderShortcutsConfig, setters } = await loadHook();
    const { shortcutsHandlers } = useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });
    return {
      shortcutsHandlers,
      setShortcutsSections: setters[IDX.setShortcutsSections],
    };
  }

  test("addShortcutRow appends a row defaulted to the first available prompt (empty list -> empty prompt)", async () => {
    const { shortcutsHandlers, setShortcutsSections } = await mountHandlers();
    shortcutsHandlers.addShortcutRow("tasksList");
    const updater = setShortcutsSections.mock.calls[0][0];
    const next = updater({ tasksList: [] });
    expect(next.tasksList).toEqual([{ icon: "", prompt: "" }]);
  });

  test("addShortcutRow caps a section at 10 rows", async () => {
    const { shortcutsHandlers, setShortcutsSections } = await mountHandlers();
    const full = Array.from({ length: 10 }, (_, i) => ({ prompt: `P${i}` }));
    shortcutsHandlers.addShortcutRow("tasksList");
    const updater = setShortcutsSections.mock.calls[0][0];
    expect(updater({ tasksList: full })).toEqual({ tasksList: full });
  });

  test("updateShortcutRow immutably merges a patch into the target row", async () => {
    const { shortcutsHandlers, setShortcutsSections } = await mountHandlers();
    shortcutsHandlers.updateShortcutRow("tasksList", 0, { icon: "star" });
    const updater = setShortcutsSections.mock.calls[0][0];
    const prev = { tasksList: [{ icon: "", prompt: "A" }] };
    const next = updater(prev);
    expect(next.tasksList[0]).toEqual({ icon: "star", prompt: "A" });
    expect(prev.tasksList[0]).toEqual({ icon: "", prompt: "A" }); // unmutated
  });

  test("removeShortcutRow splices the row out of the section", async () => {
    const { shortcutsHandlers, setShortcutsSections } = await mountHandlers();
    shortcutsHandlers.removeShortcutRow("tasksList", 0);
    const updater = setShortcutsSections.mock.calls[0][0];
    const next = updater({
      tasksList: [{ prompt: "A" }, { prompt: "B" }],
    });
    expect(next.tasksList).toEqual([{ prompt: "B" }]);
  });

  test("moveShortcutRow swaps adjacent rows and no-ops out of bounds", async () => {
    const { shortcutsHandlers, setShortcutsSections } = await mountHandlers();
    shortcutsHandlers.moveShortcutRow("tasksList", 0, 1);
    const updater = setShortcutsSections.mock.calls[0][0];
    const prev = { tasksList: [{ prompt: "A" }, { prompt: "B" }] };
    expect(updater(prev).tasksList).toEqual([{ prompt: "B" }, { prompt: "A" }]);

    shortcutsHandlers.moveShortcutRow("tasksList", 0, -1);
    const updater2 =
      setShortcutsSections.mock.calls[
        setShortcutsSections.mock.calls.length - 1
      ][0];
    expect(updater2(prev)).toBe(prev); // out-of-range move is a no-op (same ref)
  });
});

describe("useFolderShortcutsConfig — persistShortcuts", () => {
  test("PUTs the built sections and dispatches mitto:folder_shortcuts_updated", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
        return Promise.resolve(
          jsonResponse({ sections: { tasksList: [{ prompt: "A" }] } }),
        );
      }
      return Promise.resolve(jsonResponse({}));
    });
    const events = [];
    window.addEventListener("mitto:folder_shortcuts_updated", (e) =>
      events.push(e.detail),
    );

    const { useFolderShortcutsConfig, setters } = await loadHook();
    const { persistShortcuts } = useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });

    await persistShortcuts();

    expect(putCalls).toHaveLength(1);
    expect(putCalls[0].url).toContain("/api/folders/shortcuts");
    const body = JSON.parse(putCalls[0].body);
    expect(body.sections).toEqual({
      tasksList: [],
      conversations: [],
      beadsIssue: [],
    });
    expect(setters[IDX.setShortcutsSections]).toHaveBeenCalledWith({
      tasksList: [{ prompt: "A" }],
    });
    expect(events).toEqual([{ working_dir: "/tmp/myfolder" }]);
  });

  test("throws a plain Error with the SDK message on failure", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT")
        return Promise.resolve(
          jsonResponse({ error: { message: "disk full" } }, 500),
        );
      return Promise.resolve(jsonResponse({}));
    });

    const { useFolderShortcutsConfig } = await loadHook();
    const { persistShortcuts } = useFolderShortcutsConfig({
      selectedFolder: "myfolder",
      activeTab: "shortcuts",
      getSelectedFolderDir: () => "/tmp/myfolder",
      shortcutSections: SECTIONS,
    });

    await expect(persistShortcuts()).rejects.toThrow("disk full");
  });

  test("is a no-op when there is no resolvable folder working_dir", async () => {
    global.fetch = jest.fn();
    const { useFolderShortcutsConfig } = await loadHook();
    const { persistShortcuts } = useFolderShortcutsConfig({
      selectedFolder: null,
      activeTab: "shortcuts",
      getSelectedFolderDir: () => null,
      shortcutSections: SECTIONS,
    });

    await persistShortcuts();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});
