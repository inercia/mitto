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
import { serializeHiddenModels } from "./../utils/hiddenModels.js";

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
  // Hide-models save coordination (mitto-zu4.4): refs must be objects with a
  // mutable .current, exactly as the shell passes them in production.
  editHiddenModels: [],
  hiddenModelsSaveSeqRef: { current: 0 },
  latestEditHiddenModelsRef: { current: [] },
  setWsHiddenModelsErrors: jest.fn(),
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

  test(
    "blocks the save when there is an incomplete new folder, without calling the SDK",
    async () => {
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
    },
  );

  // mitto-zu4.4 — hidden-models save coordination for the Workspaces dialog.
  // These tests mirror the production wiring: only the selected workspace's
  // applyWorkspaceEdits attaches hidden_models (always explicit; [] clears),
  // untouched workspaces never gain the key, and backend field errors
  // (`workspaces[i].hidden_models[j]`) map back onto the raw draft rows via
  // the snapshot taken from the exact request array.
  describe("hidden-models save coordination (mitto-zu4.4)", () => {
    const TARGET_KEY = "/tmp/ws1";

    // Mirror of useWorkspaceEdits.buildWorkspaceEditsFor: the target
    // workspace always gets an explicit hidden_models array; others pass
    // through untouched (no hidden_models key added).
    function makeApplyWorkspaceEdits(editHiddenModels) {
      return (ws) =>
        ws.working_dir === TARGET_KEY
          ? {
              ...ws,
              hidden_models: serializeHiddenModels(editHiddenModels).rules,
            }
          : ws;
    }

    const HIDDEN_PROPS = (over = {}) => ({
      ...BASE_PROPS,
      selectedWorkspaceKey: TARGET_KEY,
      selectedWorkspace: { uuid: "ws-1", working_dir: TARGET_KEY },
      workspaces: [
        { uuid: "ws-1", working_dir: TARGET_KEY, name: "Alpha" },
        { uuid: "ws-2", working_dir: "/tmp/ws2", name: "Beta" },
      ],
      setWsHiddenModelsErrors: jest.fn(),
      ...over,
    });

    test("saves explicit hidden_models for the selected workspace only (whitespace-only rows dropped) and clears per-workspace errors on success", async () => {
      global.fetch = jest.fn((url, opts) => {
        if ((opts?.method || "GET") === "POST") {
          return Promise.resolve(jsonResponse({ applied: {} }));
        }
        return Promise.resolve(jsonResponse({ ui: {} }));
      });
      const useWorkspacesSaveCoordinator = await loadHook();
      const editHiddenModels = ["(bad", "   ", " .*Terra.*"];
      const setWsHiddenModelsErrors = jest.fn();
      const { handleSave } = useWorkspacesSaveCoordinator(
        HIDDEN_PROPS({
          editHiddenModels,
          applyWorkspaceEdits: makeApplyWorkspaceEdits(editHiddenModels),
          setWsHiddenModelsErrors,
        }),
      );
      await handleSave();

      const postCall = global.fetch.mock.calls.find(
        (c) => (c[1]?.method || "GET") === "POST",
      );
      expect(postCall).toBeDefined();
      const body = JSON.parse(postCall[1].body);
      // Selected workspace: explicit array, whitespace-only row dropped.
      expect(body.workspaces[0].hidden_models).toEqual([
        "(bad",
        " .*Terra.*",
      ]);
      // Untouched workspace must not gain an explicit hidden_models key.
      expect(body.workspaces[1].hidden_models).toBeUndefined();
      // Success clears the per-workspace error map.
      expect(setWsHiddenModelsErrors).toHaveBeenCalledWith({});
    });

    test("maps a backend workspaces[i].hidden_models[j] error back onto the matching draft row", async () => {
      global.fetch = jest.fn((url, opts) => {
        if ((opts?.method || "GET") === "POST") {
          return Promise.resolve(
            jsonResponse(
              {
                error: {
                  message: "Invalid hidden model pattern",
                  details: { field: "workspaces[0].hidden_models[1]" },
                },
              },
              400,
            ),
          );
        }
        return Promise.resolve(jsonResponse({ ui: {} }));
      });
      const useWorkspacesSaveCoordinator = await loadHook();
      // Draft rows include the whitespace-only row, so request index 1 maps
      // back to draft row index 2 via the saved rowIndexes.
      const editHiddenModels = ["(bad", "   ", " .*Terra.*"];
      const latestEditHiddenModelsRef = { current: editHiddenModels };
      const setWsHiddenModelsErrors = jest.fn();
      const setError = jest.fn();
      const { handleSave } = useWorkspacesSaveCoordinator(
        HIDDEN_PROPS({
          editHiddenModels,
          latestEditHiddenModelsRef,
          applyWorkspaceEdits: makeApplyWorkspaceEdits(editHiddenModels),
          setWsHiddenModelsErrors,
          setError,
        }),
      );
      await handleSave();

      // The wrapped error's message still reaches the shared banner...
      expect(setError).toHaveBeenLastCalledWith("Invalid hidden model pattern");
      // ...but the hook surfaces the field error through the per-workspace
      // error map (an updater keyed by the workspace key), and the failure
      // must NOT clear the existing map.
      expect(setWsHiddenModelsErrors).toHaveBeenCalledTimes(1);
      const updater = setWsHiddenModelsErrors.mock.calls[0][0];
      const result = updater({ "other-key": { rowIndex: 0 } });
      expect(result).toEqual({
        "other-key": { rowIndex: 0 },
        [TARGET_KEY]: {
          rowIndex: 2,
          message: "Invalid hidden model pattern",
          rowValue: " .*Terra.*",
        },
      });
    });

    test("ignores hidden-models errors that target a different request index", async () => {
      global.fetch = jest.fn((url, opts) => {
        if ((opts?.method || "GET") === "POST") {
          return Promise.resolve(
            jsonResponse(
              {
                error: {
                  message: "Invalid hidden model pattern",
                  details: { field: "workspaces[1].hidden_models[0]" },
                },
              },
              400,
            ),
          );
        }
        return Promise.resolve(jsonResponse({ ui: {} }));
      });
      const useWorkspacesSaveCoordinator = await loadHook();
      const editHiddenModels = ["(bad"];
      const setWsHiddenModelsErrors = jest.fn();
      const { handleSave } = useWorkspacesSaveCoordinator(
        HIDDEN_PROPS({
          editHiddenModels,
          latestEditHiddenModelsRef: { current: editHiddenModels },
          applyWorkspaceEdits: makeApplyWorkspaceEdits(editHiddenModels),
          setWsHiddenModelsErrors,
        }),
      );
      await handleSave();

      // parsed.index (1) != snapshot.index (0): no per-workspace mapping.
      expect(setWsHiddenModelsErrors).not.toHaveBeenCalled();
    });

    test("does not map hidden-models errors when a newer save has started (stale seq guard)", async () => {
      let resolveSave;
      global.fetch = jest.fn((url, opts) => {
        if ((opts?.method || "GET") === "POST") {
          return new Promise((resolve) => {
            resolveSave = () =>
              resolve(
                jsonResponse(
                  {
                    error: {
                      message: "Invalid hidden model pattern",
                      details: { field: "workspaces[0].hidden_models[0]" },
                    },
                  },
                  400,
                ),
              );
          });
        }
        return Promise.resolve(jsonResponse({ ui: {} }));
      });
      const useWorkspacesSaveCoordinator = await loadHook();
      const hiddenModelsSaveSeqRef = { current: 0 };
      const editHiddenModels = ["(bad"];
      const setWsHiddenModelsErrors = jest.fn();
      const { handleSave } = useWorkspacesSaveCoordinator(
        HIDDEN_PROPS({
          editHiddenModels,
          hiddenModelsSaveSeqRef,
          latestEditHiddenModelsRef: { current: editHiddenModels },
          applyWorkspaceEdits: makeApplyWorkspaceEdits(editHiddenModels),
          setWsHiddenModelsErrors,
        }),
      );
      const pending = handleSave();
      // Let the save request reach the (pending) fetch mock, then simulate a
      // newer save bumping the sequence before the first one settles.
      await new Promise((r) => setTimeout(r, 10));
      hiddenModelsSaveSeqRef.current += 1;
      resolveSave();
      await pending;
      expect(setWsHiddenModelsErrors).not.toHaveBeenCalled();
    });
  });
});
