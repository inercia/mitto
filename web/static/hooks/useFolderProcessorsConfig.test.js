/**
 * Tests for useFolderProcessorsConfig.js (mitto-7gta.17 slice S2 Test phase).
 *
 * Covers the tab-open load effect and the two CRUD handlers
 * (toggle-enabled, save-arguments), both migrated onto getSdkClient() in
 * slice S2. Mirrors the window.preact stub harness established by
 * useBeadsFolderConfig.test.js / useFolderPromptsConfig.test.js.
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
  const mod = await import("./useFolderProcessorsConfig.js");
  return {
    useFolderProcessorsConfig: mod.useFolderProcessorsConfig,
    setters: currentSetters,
    effects: currentEffects,
  };
}

const IDX = {
  setFolderProcessors: 0,
  setProcessorsLoading: 1,
  setProcessorArgEdits: 3,
};

const GROUPED = [{ displayName: "myfolder", workspaces: [{ uuid: "ws-1" }] }];

describe("useFolderProcessorsConfig — load effect", () => {
  test("loads processors for the selected folder's first workspace uuid", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ processors: [{ name: "P1" }] })),
    );
    const { useFolderProcessorsConfig, setters, effects } = await loadHook();
    useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError: jest.fn(),
    });

    expect(effects).toHaveLength(1);
    effects[0].cb();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/workspaces/ws-1/processors");
    expect(setters[IDX.setProcessorsLoading]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setFolderProcessors]).toHaveBeenCalledWith([
      { name: "P1" },
    ]);
    expect(setters[IDX.setProcessorsLoading]).toHaveBeenLastCalledWith(false);
  });

  test("does nothing when the active tab is not processors", async () => {
    global.fetch = jest.fn();
    const { useFolderProcessorsConfig, effects } = await loadHook();
    useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "prompts",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError: jest.fn(),
    });
    effects[0].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("does nothing when the selected folder has no resolvable workspace uuid", async () => {
    global.fetch = jest.fn();
    const { useFolderProcessorsConfig, effects } = await loadHook();
    useFolderProcessorsConfig({
      selectedFolder: "ghost",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => null,
      setError: jest.fn(),
    });
    effects[0].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderProcessorsConfig — toggleProcessorEnabled", () => {
  test("PATCHes the inverted enabled flag and reloads", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ method, body: opts && opts.body });
      if (method === "PATCH") return Promise.resolve(jsonResponse({}));
      return Promise.resolve(jsonResponse({ processors: [] }));
    });
    global.document.cookie = "mitto_csrf=test-token";
    const { useFolderProcessorsConfig, setters } = await loadHook();
    const { processorsHandlers } = useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError: jest.fn(),
    });

    await processorsHandlers.toggleProcessorEnabled({
      name: "P1",
      enabled: true,
    });

    const patchCall = calls.find((c) => c.method === "PATCH");
    expect(patchCall).toBeDefined();
    expect(JSON.parse(patchCall.body)).toEqual({ enabled: false });
    expect(setters[IDX.setFolderProcessors]).toHaveBeenCalledWith([]);
  });

  test("sets the shared error banner on failure", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "boom" } }, 500)),
    );
    global.document.cookie = "mitto_csrf=test-token";
    const setError = jest.fn();
    const { useFolderProcessorsConfig } = await loadHook();
    const { processorsHandlers } = useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError,
    });

    await processorsHandlers.toggleProcessorEnabled({
      name: "P1",
      enabled: true,
    });
    expect(setError).toHaveBeenCalledWith("Failed to toggle processor: boom");
  });

  test("is a no-op when there is no resolvable folder workspace uuid", async () => {
    global.fetch = jest.fn();
    const { useFolderProcessorsConfig } = await loadHook();
    const { processorsHandlers } = useFolderProcessorsConfig({
      selectedFolder: null,
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => null,
      setError: jest.fn(),
    });
    await processorsHandlers.toggleProcessorEnabled({ name: "P1" });
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderProcessorsConfig — saveProcessorArguments", () => {
  test("sends edited-or-current values for every declared param and clears local edits", async () => {
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      if (method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
        return Promise.resolve(jsonResponse({}));
      }
      return Promise.resolve(jsonResponse({ processors: [] }));
    });
    global.document.cookie = "mitto_csrf=test-token";
    const { useFolderProcessorsConfig, setters } = await loadHook();
    const { processorsHandlers } = useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError: jest.fn(),
    });

    // processorArgEdits initial state is {} (no edits made), so both params
    // fall back to their declared `.value`.
    await processorsHandlers.saveProcessorArguments({
      name: "fmt",
      parameters: [
        { name: "flag", value: "on" },
        { name: "mode", value: "strict" },
      ],
    });

    expect(putCalls).toHaveLength(1);
    expect(putCalls[0].url).toContain(
      "/api/workspaces/ws-1/processors/fmt/arguments",
    );
    expect(JSON.parse(putCalls[0].body)).toEqual({
      arguments: { flag: "on", mode: "strict" },
    });
    // Local edits for this processor are cleared via the functional updater.
    const updater =
      setters[IDX.setProcessorArgEdits].mock.calls[
        setters[IDX.setProcessorArgEdits].mock.calls.length - 1
      ][0];
    expect(updater({ fmt: { flag: "off" }, other: { x: "1" } })).toEqual({
      other: { x: "1" },
    });
  });

  test("sets the shared error banner on failure", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "rejected" } }, 400)),
    );
    global.document.cookie = "mitto_csrf=test-token";
    const setError = jest.fn();
    const { useFolderProcessorsConfig } = await loadHook();
    const { processorsHandlers } = useFolderProcessorsConfig({
      selectedFolder: "myfolder",
      activeTab: "processors",
      groupedWorkspaces: GROUPED,
      getSelectedFolderUuid: () => "ws-1",
      setError,
    });

    await processorsHandlers.saveProcessorArguments({
      name: "fmt",
      parameters: [{ name: "flag", value: "on" }],
    });
    expect(setError).toHaveBeenCalledWith(
      "Failed to save processor arguments: rejected",
    );
  });
});
