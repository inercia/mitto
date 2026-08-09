/**
 * Tests for useFolderMetadataConfig.js (mitto-7gta.17 slice S2 Test phase).
 *
 * Covers the folder-selection load effect (GET workspace metadata, with a
 * reset-on-failure branch) and the two persist actions (metadata,
 * user-data-schema), all migrated onto getSdkClient() in slice S2. Mirrors
 * the window.preact stub harness established by useBeadsFolderConfig.test.js;
 * this hook additionally uses useRef, so the stub returns a live ref object.
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentSetters = [];
let currentEffects = [];
let currentRefs = [];
// Positional overrides for the value useState() returns, keyed by call
// index within a single hook invocation (order matches the hook's own
// useState() declaration order). Lets tests simulate "the user already
// edited this field" without a real Preact re-render — the simple stub
// otherwise always returns the caller's `initial` argument.
let stateOverrides = [];
window.preact = {
  useState: (initial) => {
    const i = currentSetters.length;
    const value = i < stateOverrides.length ? stateOverrides[i] : initial;
    const setter = jest.fn();
    currentSetters.push(setter);
    return [value, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  useRef: (initial) => {
    const ref = { current: initial };
    currentRefs.push(ref);
    return ref;
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
  currentRefs = [];
  stateOverrides = [];
  const mod = await import("./useFolderMetadataConfig.js");
  return {
    useFolderMetadataConfig: mod.useFolderMetadataConfig,
    setters: currentSetters,
    effects: currentEffects,
    refs: currentRefs,
  };
}

const IDX = {
  setFolderMetadata: 0,
  setMetadataLoading: 1,
  setEditMetaDescription: 2,
  setEditMetaUrl: 3,
  setEditMetaGroup: 4,
  setEditUserDataFields: 5,
};

const GROUPED = [{ displayName: "myfolder", workspaces: [{ uuid: "ws-1" }] }];

// Two effects share a 1-entry deps array: the ref-sync effect (deps =
// [groupedWorkspaces], an array reference) registered first, and the load
// effect (deps = [selectedFolder], a string) registered second. Distinguish
// by the dep's type rather than position, since Preact would reorder neither
// but a positional index is more fragile to read at a glance.
function findLoadEffect(effects) {
  return effects.find(
    (e) => e.deps && e.deps.length === 1 && !Array.isArray(e.deps[0]),
  );
}

describe("useFolderMetadataConfig — load effect", () => {
  test("loads and populates metadata + user-data-schema fields on success", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({
          description: "desc",
          url: "https://x",
          group: "grp",
          user_data_schema: {
            fields: [{ name: "f1", type: "url", description: "d1" }],
          },
        }),
      ),
    );
    const { useFolderMetadataConfig, setters, effects } = await loadHook();
    useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    const loadEffect = findLoadEffect(effects);
    loadEffect.cb();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    expect(String(global.fetch.mock.calls[0][0])).toContain(
      "/api/workspaces/ws-1/metadata",
    );
    expect(setters[IDX.setMetadataLoading]).toHaveBeenCalledWith(true);
    expect(setters[IDX.setEditMetaDescription]).toHaveBeenLastCalledWith(
      "desc",
    );
    expect(setters[IDX.setEditMetaUrl]).toHaveBeenLastCalledWith("https://x");
    expect(setters[IDX.setEditMetaGroup]).toHaveBeenLastCalledWith("grp");
    expect(setters[IDX.setEditUserDataFields]).toHaveBeenLastCalledWith([
      { name: "f1", type: "url", description: "d1" },
    ]);
    expect(setters[IDX.setMetadataLoading]).toHaveBeenLastCalledWith(false);
  });

  test("resets all fields to empty on a failed load", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "gone" } }, 404)),
    );
    const { useFolderMetadataConfig, setters, effects } = await loadHook();
    useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    findLoadEffect(effects).cb();
    await flush();

    expect(setters[IDX.setFolderMetadata]).toHaveBeenLastCalledWith(null);
    expect(setters[IDX.setEditMetaDescription]).toHaveBeenLastCalledWith("");
    expect(setters[IDX.setEditMetaUrl]).toHaveBeenLastCalledWith("");
    expect(setters[IDX.setEditMetaGroup]).toHaveBeenLastCalledWith("");
    expect(setters[IDX.setEditUserDataFields]).toHaveBeenLastCalledWith([]);
    expect(setters[IDX.setMetadataLoading]).toHaveBeenLastCalledWith(false);
  });

  test("does nothing when no folder is selected", async () => {
    global.fetch = jest.fn();
    const { useFolderMetadataConfig, effects } = await loadHook();
    useFolderMetadataConfig({
      selectedFolder: null,
      groupedWorkspaces: GROUPED,
    });
    findLoadEffect(effects).cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderMetadataConfig — persistMetadata", () => {
  test("PUTs the three edited fields for the folder's workspace uuid", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
        return Promise.resolve(jsonResponse({}));
      }
      return Promise.resolve(jsonResponse({}));
    });
    const { useFolderMetadataConfig } = await loadHook();
    // Simulate "the user already edited the description/url/group fields"
    // via stateOverrides (indices 2/3/4 per the hook's useState() order).
    stateOverrides = [null, false, "New desc", "https://new", "grp2"];
    const { persistMetadata } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await persistMetadata();

    expect(putCalls).toHaveLength(1);
    expect(putCalls[0].url).toContain("/api/workspaces/ws-1/metadata");
    expect(JSON.parse(putCalls[0].body)).toEqual({
      description: "New desc",
      url: "https://new",
      group: "grp2",
    });
  });

  test("skips the request when all three fields are empty", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    global.fetch = jest.fn();
    const { useFolderMetadataConfig } = await loadHook();
    const { persistMetadata } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await persistMetadata();
    // All three edit fields default to "" -> the early-return guard fires.
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("throws a plain Error with the SDK message on failure", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ error: { message: "metadata rejected" } }, 400),
      ),
    );
    const { useFolderMetadataConfig } = await loadHook();
    stateOverrides = [null, false, "New desc", "", ""];
    const { persistMetadata } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await expect(persistMetadata()).rejects.toThrow("metadata rejected");
  });

  test("is a no-op when no folder is selected", async () => {
    global.fetch = jest.fn();
    const { useFolderMetadataConfig } = await loadHook();
    const { persistMetadata } = useFolderMetadataConfig({
      selectedFolder: null,
      groupedWorkspaces: GROUPED,
    });
    await persistMetadata();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useFolderMetadataConfig — persistUserDataSchema", () => {
  test("PUTs the (empty, since no fields were ever set) validated field list", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
        return Promise.resolve(jsonResponse({}));
      }
      return Promise.resolve(jsonResponse({}));
    });
    const { useFolderMetadataConfig } = await loadHook();
    const { persistUserDataSchema } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await persistUserDataSchema();

    expect(putCalls).toHaveLength(1);
    expect(putCalls[0].url).toContain("/api/workspaces/ws-1/user-data-schema");
    expect(JSON.parse(putCalls[0].body)).toEqual({ fields: [] });
  });

  test("filters out fields with an empty/whitespace-only name before sending", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url: String(url), body: opts.body });
      }
      return Promise.resolve(jsonResponse({}));
    });
    const { useFolderMetadataConfig } = await loadHook();
    stateOverrides = [
      null,
      false,
      "",
      "",
      "",
      [
        { name: "valid", type: "string", description: "" },
        { name: "  ", type: "string", description: "" },
        { name: "", type: "url", description: "" },
      ],
    ];
    const { persistUserDataSchema } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await persistUserDataSchema();

    expect(JSON.parse(putCalls[0].body)).toEqual({
      fields: [{ name: "valid", type: "string", description: "" }],
    });
  });

  test("throws a plain Error with the SDK message on failure", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ error: { message: "schema rejected" } }, 400),
      ),
    );
    const { useFolderMetadataConfig } = await loadHook();
    const { persistUserDataSchema } = useFolderMetadataConfig({
      selectedFolder: "myfolder",
      groupedWorkspaces: GROUPED,
    });

    await expect(persistUserDataSchema()).rejects.toThrow("schema rejected");
  });

  test("is a no-op when there is no resolvable folder workspace uuid", async () => {
    global.fetch = jest.fn();
    const { useFolderMetadataConfig } = await loadHook();
    const { persistUserDataSchema } = useFolderMetadataConfig({
      selectedFolder: "ghost",
      groupedWorkspaces: [],
    });
    await persistUserDataSchema();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});
