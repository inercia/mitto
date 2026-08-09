/**
 * Tests for useFolderPromptsConfig.js (mitto-7gta.17 slice S2 Test phase).
 *
 * Covers the load effect (fires GET /api/workspace-prompts when the Prompts
 * tab is active for a selected folder) and the three CRUD handlers
 * (save/delete/toggle-enabled), each of which now goes through
 * getSdkClient().prompts.* instead of authFetch/secureFetch. Mirrors the
 * window.preact stub harness established by useBeadsFolderConfig.test.js.
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
};

/** Mirrors storage.test.js/useBeadsFolderConfig.test.js's helper of the same
 * name: shaped for sdk/core/transport.js's decodeBody() (.text() + a
 * content-type header). */
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

// Await enough microtask ticks that the SDK's authorize() -> fetch() ->
// decodeBody() -> .then()/.finally() chain fully settles (mirrors
// useLinkedBeadPhase.test.js's flush()).
async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useFolderPromptsConfig.js");
  return {
    useFolderPromptsConfig: mod.useFolderPromptsConfig,
    setters: currentSetters,
    effects: currentEffects,
  };
}

const IDX = {
  setFolderPrompts: 0,
  setPromptsLoading: 1,
  setPromptSaving: 12,
};

const GROUPED = [
  { displayName: "myfolder", workspaces: [{ working_dir: "/tmp/myfolder" }] },
];

describe("useFolderPromptsConfig — load effect", () => {
  test("loads folder prompts when the folder is selected and the tab is Prompts", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ prompts: [{ name: "P1" }] })),
    );
    const { useFolderPromptsConfig, setters, effects } = await loadHook();
    useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError: jest.fn(),
    });

    expect(effects).toHaveLength(1);
    effects[0].cb();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, opts] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/workspace-prompts");
    expect((opts && opts.method) || "GET").toBe("GET");
    expect(setters[IDX.setPromptsLoading]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setFolderPrompts]).toHaveBeenCalledWith([
      { name: "P1" },
    ]);
    expect(setters[IDX.setPromptsLoading]).toHaveBeenLastCalledWith(false);
  });

  test("does nothing when the active tab is not prompts", async () => {
    global.fetch = jest.fn();
    const { useFolderPromptsConfig, effects } = await loadHook();
    useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError: jest.fn(),
    });

    effects[0].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("does nothing when the selected folder has no resolvable working_dir", async () => {
    global.fetch = jest.fn();
    const { useFolderPromptsConfig, effects } = await loadHook();
    useFolderPromptsConfig({
      selectedFolder: "ghost",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => null,
      setError: jest.fn(),
    });

    effects[0].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderPromptsConfig — saveWorkspacePrompt", () => {
  test("creates the prompt, reloads, and toggles promptSaving", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ url: String(url), method });
      if (method === "POST") return Promise.resolve(jsonResponse({}));
      return Promise.resolve(jsonResponse({ prompts: [{ name: "New" }] }));
    });
    global.document.cookie = "mitto_csrf=test-token";
    const { useFolderPromptsConfig, setters } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError: jest.fn(),
    });

    await promptsHandlers.saveWorkspacePrompt({ name: "New", prompt: "Do it" });

    expect(calls.map((c) => c.method)).toEqual(["POST", "GET"]);
    expect(setters[IDX.setPromptSaving]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setPromptSaving]).toHaveBeenLastCalledWith(false);
    expect(setters[IDX.setFolderPrompts]).toHaveBeenCalledWith([
      { name: "New" },
    ]);
  });

  test("sets the shared error banner on failure", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ error: { message: "name already exists" } }, 409),
      ),
    );
    global.document.cookie = "mitto_csrf=test-token";
    const setError = jest.fn();
    const { useFolderPromptsConfig, setters } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError,
    });

    await promptsHandlers.saveWorkspacePrompt({ name: "New" });

    expect(setError).toHaveBeenCalledWith(
      "Failed to save prompt: name already exists",
    );
    expect(setters[IDX.setPromptSaving]).toHaveBeenLastCalledWith(false);
  });

  test("is a no-op when there is no resolvable folder working_dir", async () => {
    global.fetch = jest.fn();
    const { useFolderPromptsConfig } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: null,
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => null,
      setError: jest.fn(),
    });

    await promptsHandlers.saveWorkspacePrompt({ name: "New" });
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderPromptsConfig — deleteWorkspacePrompt", () => {
  test("deletes the prompt via DELETE and reloads", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push(method);
      if (method === "DELETE") return Promise.resolve(jsonResponse({}));
      return Promise.resolve(jsonResponse({ prompts: [] }));
    });
    global.document.cookie = "mitto_csrf=test-token";
    const { useFolderPromptsConfig, setters } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError: jest.fn(),
    });

    await promptsHandlers.deleteWorkspacePrompt("Old");

    expect(calls).toEqual(["DELETE", "GET"]);
    expect(setters[IDX.setFolderPrompts]).toHaveBeenCalledWith([]);
  });

  test("sets the shared error banner on failure", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "not found" } }, 404)),
    );
    global.document.cookie = "mitto_csrf=test-token";
    const setError = jest.fn();
    const { useFolderPromptsConfig } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError,
    });

    await promptsHandlers.deleteWorkspacePrompt("Old");
    expect(setError).toHaveBeenCalledWith("Failed to delete prompt: not found");
  });
});

describe("useFolderPromptsConfig — togglePromptEnabled", () => {
  test("PATCHes the inverted enabled flag and reloads", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ method, body: opts && opts.body });
      if (method === "PATCH") return Promise.resolve(jsonResponse({}));
      return Promise.resolve(jsonResponse({ prompts: [] }));
    });
    global.document.cookie = "mitto_csrf=test-token";
    const { useFolderPromptsConfig } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError: jest.fn(),
    });

    await promptsHandlers.togglePromptEnabled({ name: "P1", enabled: true });

    const patchCall = calls.find((c) => c.method === "PATCH");
    expect(patchCall).toBeDefined();
    expect(JSON.parse(patchCall.body)).toEqual({ enabled: false });
  });

  test("sets the shared error banner on failure", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "boom" } }, 500)),
    );
    global.document.cookie = "mitto_csrf=test-token";
    const setError = jest.fn();
    const { useFolderPromptsConfig } = await loadHook();
    const { promptsHandlers } = useFolderPromptsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderDir: () => "/tmp/myfolder",
      setError,
    });

    await promptsHandlers.togglePromptEnabled({ name: "P1", enabled: true });
    expect(setError).toHaveBeenCalledWith("Failed to toggle prompt: boom");
  });
});
