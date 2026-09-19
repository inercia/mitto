/**
 * Tests for useWSWorkspaces.js (mitto-7gta.17 slice S7 Test phase; updated
 * mitto-sus.11 for the workspacesStore migration).
 *
 * Covers the 3 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: fetchWorkspaces (workspaces.list), addWorkspace
 * (workspaces.create), and removeWorkspace (workspaces.remove, including its
 * uuid-resolution-from-the-store step and the conversation_count detail
 * passthrough on failure). Mirrors the window.preact stub harness
 * established by useFolderPromptsConfig.test.js (slice S2).
 *
 * The workspaces/acpServers data itself no longer lives in this hook's own
 * useState (mitto-sus.11) -- it's written directly to the module-level
 * stores/workspacesStore.js, so assertions read it back via that store's
 * getters instead of inspecting useState setter calls.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";
import {
  getWorkspaces,
  getAcpServers,
  _resetWorkspacesStoreForTests,
} from "../stores/workspacesStore.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}
// Every migrated handler here that issues a state-changing request needs a
// CSRF token; pre-seed the cookie so browserCookieAuth's authorize() never
// needs its own network round trip (the test-local fetch mocks below only
// shape the endpoint under test).
global.document.cookie = "mitto_csrf=test-token";

let currentEffects = [];
window.preact = {
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  useCallback: (fn) => fn,
};

beforeEach(() => {
  _resetWorkspacesStoreForTests();
});

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
  currentEffects = [];
  const mod = await import("./useWSWorkspaces.js");
  return {
    ...mod.useWSWorkspaces(),
    effects: currentEffects,
  };
}

describe("useWSWorkspaces — fetchWorkspaces", () => {
  test("success: GETs /api/workspaces and stores workspaces + acp_servers", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({
          workspaces: [{ uuid: "u1", working_dir: "/tmp/a" }],
          acp_servers: [{ name: "auggie" }],
        }),
      ),
    );
    const { fetchWorkspaces } = await loadHook();
    await fetchWorkspaces();
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/workspaces");
    expect(getWorkspaces()).toEqual([{ uuid: "u1", working_dir: "/tmp/a" }]);
    expect(getAcpServers()).toEqual([{ name: "auggie" }]);
  });

  test("missing fields default to empty arrays", async () => {
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({})));
    const { fetchWorkspaces } = await loadHook();
    await fetchWorkspaces();
    expect(getWorkspaces()).toEqual([]);
    expect(getAcpServers()).toEqual([]);
  });

  test("a network failure is swallowed (logged, no throw, no store change)", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { fetchWorkspaces } = await loadHook();
    await expect(fetchWorkspaces()).resolves.toBeUndefined();
    expect(getWorkspaces()).toEqual([]);
  });

  test("mount effect fires fetchWorkspaces", async () => {
    const { effects } = await loadHook();
    expect(effects).toHaveLength(1);
    expect(effects[0].deps).toEqual([expect.any(Function)]);
  });
});

describe("useWSWorkspaces — addWorkspace", () => {
  test("success: POSTs {working_dir, acp_server}, refreshes, and returns {workspace}", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ method, url: String(url), body: opts && opts.body });
      if (method === "POST")
        return Promise.resolve(
          jsonResponse({ uuid: "u2", working_dir: "/tmp/b" }, 201),
        );
      return Promise.resolve(jsonResponse({ workspaces: [], acp_servers: [] }));
    });
    const { addWorkspace } = await loadHook();
    const result = await addWorkspace("/tmp/b", "auggie");
    expect(result).toEqual({
      workspace: { uuid: "u2", working_dir: "/tmp/b" },
    });
    const postCall = calls.find((c) => c.method === "POST");
    expect(postCall.url).toContain("/api/workspaces");
    expect(JSON.parse(postCall.body)).toEqual({
      working_dir: "/tmp/b",
      acp_server: "auggie",
    });
    expect(calls.map((c) => c.method)).toEqual(["POST", "GET"]);
  });

  test("failure: returns {error} via errorMessage(), does not throw", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "bad path" } }, 400)),
    );
    const { addWorkspace } = await loadHook();
    const result = await addWorkspace("/tmp/bad", "auggie");
    expect(result).toEqual({ error: "bad path" });
  });
});

describe("useWSWorkspaces — removeWorkspace", () => {
  test("resolves the uuid from the already-fetched workspaces list, then DELETEs and refreshes", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ method, url: String(url) });
      if (method === "DELETE") return Promise.resolve(jsonResponse(null, 204));
      return Promise.resolve(
        jsonResponse({
          workspaces: [{ uuid: "u3", working_dir: "/tmp/c" }],
          acp_servers: [],
        }),
      );
    });
    const hook = await loadHook();
    // Populate the store via the same path the mount effect would use;
    // removeWorkspace reads the CURRENT store contents via getWorkspaces()
    // directly (no ref/re-render needed, unlike the pre-mitto-sus.11 shape).
    await hook.fetchWorkspaces();
    await hook.removeWorkspace("/tmp/c");
    const deleteCall = calls.find((c) => c.method === "DELETE");
    expect(deleteCall.url).toContain("uuid=u3");
    expect(calls.map((c) => c.method)).toEqual(["GET", "DELETE", "GET"]);
  });

  test("unknown working_dir: throws 'Workspace not found' without fetching", async () => {
    global.fetch = jest.fn();
    const { removeWorkspace } = await loadHook();
    await expect(removeWorkspace("/tmp/ghost")).rejects.toThrow(
      "Workspace not found",
    );
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("a 409 conflict's details.conversation_count is copied onto the rethrown error", async () => {
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      if (method === "DELETE")
        return Promise.resolve(
          jsonResponse(
            {
              error: {
                code: "conflict",
                message: "in use",
                details: { conversation_count: 3 },
              },
            },
            409,
          ),
        );
      return Promise.resolve(
        jsonResponse({
          workspaces: [{ uuid: "u4", working_dir: "/tmp/d" }],
          acp_servers: [],
        }),
      );
    });
    const hook = await loadHook();
    await hook.fetchWorkspaces();
    let caught;
    try {
      await hook.removeWorkspace("/tmp/d");
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeDefined();
    expect(caught.conversationCount).toBe(3);
  });
});
