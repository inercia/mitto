/**
 * Tests for useWorkspacesSaveCoordinator.js (mitto-7gta.17 slice S6 Test
 * phase).
 *
 * Covers the 1 authFetch/secureFetch->getSdkClient() call site migrated in
 * the Implementation phase: handleSave's main config save
 * (serverConfig.save), including the errorMessage()-wrapped throw that lets
 * the outer catch set the shared error banner exactly as the old
 * `res.ok`-check did. Mirrors the window.preact stub harness established by
 * useFolderPromptsConfig.test.js (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}
global.document.cookie = "mitto_csrf=test-token";

window.preact = {
  useCallback: (fn) => fn,
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

async function loadHook() {
  const configMod = await import("../utils/configCache.js");
  configMod.invalidateConfigCache(); // start each test with a cold cache
  const mod = await import("./useWorkspacesSaveCoordinator.js");
  return mod.useWorkspacesSaveCoordinator;
}

const BASE_PROPS = {
  selectedFolder: null,
  selectedWorkspaceKey: null,
  selectedWorkspace: null,
  groupedWorkspaces: [],
  workspaces: [{ working_dir: "/tmp/ws1", acp_server: "auggie" }],
  editIsDefault: false,
  applyWorkspaceEdits: (ws) => ws,
  getWorkspaceKey: (ws) => ws.working_dir,
  applyFolderEdits: (ws) => ws,
  persistMetadata: jest.fn(() => Promise.resolve()),
  persistUserDataSchema: jest.fn(() => Promise.resolve()),
  shortcutsLoaded: false,
  persistShortcuts: jest.fn(() => Promise.resolve()),
  isNewFolderIncomplete: false,
  setWorkspaces: jest.fn(),
  setNewFolderKey: jest.fn(),
  setSaving: jest.fn(),
  setError: jest.fn(),
  onSave: jest.fn(),
  showToast: jest.fn(),
};

describe("useWorkspacesSaveCoordinator — handleSave", () => {
  test("saves via serverConfig.save(), omitting the web section, then invalidates the cache", async () => {
    global.fetch = jest.fn((url, opts) => {
      if ((opts?.method || "GET") === "POST") {
        return Promise.resolve(jsonResponse({ applied: {} }));
      }
      return Promise.resolve(
        jsonResponse({ web: { auth_enabled: true }, ui: {} }),
      );
    });
    const useWorkspacesSaveCoordinator = await loadHook();
    const props = { ...BASE_PROPS };
    const { handleSave } = useWorkspacesSaveCoordinator(props);
    await handleSave();

    const postCall = global.fetch.mock.calls.find(
      (c) => (c[1]?.method || "GET") === "POST",
    );
    expect(postCall).toBeDefined();
    const body = JSON.parse(postCall[1].body);
    expect(body.web).toBeUndefined();
    expect(body.workspaces).toEqual(props.workspaces);
    expect(body.prompts).toEqual([]);
    expect(props.setError).toHaveBeenCalledWith("");
    expect(props.onSave).toHaveBeenCalled();
    expect(props.showToast).toHaveBeenCalledWith(
      expect.objectContaining({ style: "success", title: "Workspaces saved" }),
    );
  });

  test("a rejected save is wrapped with errorMessage() and surfaced via setError", async () => {
    global.fetch = jest.fn((url, opts) => {
      if ((opts?.method || "GET") === "POST") {
        return Promise.resolve(
          jsonResponse({ error: { message: "config is read-only" } }, 403),
        );
      }
      return Promise.resolve(jsonResponse({ ui: {} }));
    });
    const useWorkspacesSaveCoordinator = await loadHook();
    const setError = jest.fn();
    const { handleSave } = useWorkspacesSaveCoordinator({
      ...BASE_PROPS,
      setError,
    });
    await handleSave();

    // The inner try/catch wraps the SDK rejection as
    // `new Error(errorMessage(err, "Failed to save configuration"))`; since
    // the SDK error carries a message, it wins over the fallback, and the
    // outer catch surfaces that Error's message verbatim (no double prefix).
    expect(setError).toHaveBeenLastCalledWith("config is read-only");
  });

  test("blocks the save when there is an incomplete new folder, without calling the SDK", async () => {
    global.fetch = jest.fn();
    const useWorkspacesSaveCoordinator = await loadHook();
    const setError = jest.fn();
    const { handleSave } = useWorkspacesSaveCoordinator({
      ...BASE_PROPS,
      isNewFolderIncomplete: true,
      setError,
    });
    await handleSave();
    expect(global.fetch).not.toHaveBeenCalled();
    expect(setError).toHaveBeenCalledWith(
      "Please select a folder for the new workspace before saving",
    );
  });
});
