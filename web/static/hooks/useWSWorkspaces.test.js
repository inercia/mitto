/**
 * Tests for useWSWorkspaces.js (mitto-7gta.17 slice S7 Test phase).
 *
 * Covers the 3 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: fetchWorkspaces (workspaces.list), addWorkspace
 * (workspaces.create), and removeWorkspace (workspaces.remove, including its
 * uuid-resolution-from-workspacesRef step and the conversation_count detail
 * passthrough on failure). Mirrors the window.preact stub harness
 * established by useFolderPromptsConfig.test.js (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

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

// Stateful cell harness (mirrors the S3 Test-phase precedent in
// useCreateMode.test.js / useWorkspaceMcpActions.test.js): useState/useRef
// are indexed by call order and persist across re-renders, so a setter
// invoked between two render() calls is visible on the next one — needed
// here because removeWorkspace reads workspacesRef.current, which is only
// synced from the `workspaces` state by an effect this harness invokes
// explicitly between renders.
let cells = [];
let refCells = [];
let callIndex = 0;
let refIndex = 0;
let currentSetters = [];
let currentEffects = [];
window.preact = {
  useState: (initial) => {
    const idx = callIndex++;
    if (!(idx in cells)) cells[idx] = initial;
    const setter = jest.fn((v) => {
      cells[idx] = typeof v === "function" ? v(cells[idx]) : v;
    });
    currentSetters.push(setter);
    return [cells[idx], setter];
  },
  useRef: (initial) => {
    const idx = refIndex++;
    if (!(idx in refCells)) refCells[idx] = { current: initial };
    return refCells[idx];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
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
  cells = [];
  refCells = [];
  callIndex = 0;
  refIndex = 0;
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useWSWorkspaces.js");
  const render = () => {
    callIndex = 0;
    refIndex = 0;
    currentSetters = [];
    currentEffects = [];
    const result = mod.useWSWorkspaces();
    return { ...result, setters: currentSetters, effects: currentEffects };
  };
  return { render };
}

// Runs the ref-sync effect (`useEffect(() => { workspacesRef.current =
// workspaces; }, [workspaces]);`) so workspacesRef reflects the latest
// `workspaces` state — mirrors what a real Preact commit would do. Both the
// mount-fetch effect (`[fetchWorkspaces]`) and this one have a single-item
// deps array, so pick it by declaration order (it is the LAST effect the
// hook registers) rather than by deps shape alone.
function syncWorkspacesRef(hook) {
  const refSyncEffect = hook.effects[hook.effects.length - 1];
  refSyncEffect?.cb();
}

const IDX = {
  setWorkspaces: 0,
  setAcpServers: 1,
};

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
    const { render } = await loadHook();
    const { fetchWorkspaces, setters } = render();
    await fetchWorkspaces();
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/workspaces");
    expect(setters[IDX.setWorkspaces]).toHaveBeenCalledWith([
      { uuid: "u1", working_dir: "/tmp/a" },
    ]);
    expect(setters[IDX.setAcpServers]).toHaveBeenCalledWith([
      { name: "auggie" },
    ]);
  });

  test("missing fields default to empty arrays", async () => {
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({})));
    const { render } = await loadHook();
    const { fetchWorkspaces, setters } = render();
    await fetchWorkspaces();
    expect(setters[IDX.setWorkspaces]).toHaveBeenCalledWith([]);
    expect(setters[IDX.setAcpServers]).toHaveBeenCalledWith([]);
  });

  test("a network failure is swallowed (logged, no throw)", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { render } = await loadHook();
    const { fetchWorkspaces, setters } = render();
    await expect(fetchWorkspaces()).resolves.toBeUndefined();
    expect(setters[IDX.setWorkspaces]).not.toHaveBeenCalled();
  });

  test("mount effect fires fetchWorkspaces", async () => {
    const { render } = await loadHook();
    const { effects } = render();
    // Two effects: fetchWorkspaces on mount, and workspacesRef sync.
    expect(effects.length).toBeGreaterThanOrEqual(1);
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
    const { render } = await loadHook();
    const { addWorkspace } = render();
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
    const { render } = await loadHook();
    const { addWorkspace } = render();
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
    const { render } = await loadHook();
    let hook = render();
    // Populate workspacesRef via the same path the mount effect would use,
    // then re-render so removeWorkspace's useCallback closure (fresh each
    // render()) still reads the SAME workspacesRef instance (useRef persists
    // across the harness's render() calls).
    await hook.fetchWorkspaces();
    hook = render();
    syncWorkspacesRef(hook);
    await hook.removeWorkspace("/tmp/c");
    const deleteCall = calls.find((c) => c.method === "DELETE");
    expect(deleteCall.url).toContain("uuid=u3");
    expect(calls.map((c) => c.method)).toEqual(["GET", "DELETE", "GET"]);
  });

  test("unknown working_dir: throws 'Workspace not found' without fetching", async () => {
    global.fetch = jest.fn();
    const { render } = await loadHook();
    const { removeWorkspace } = render();
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
    const { render } = await loadHook();
    let hook = render();
    await hook.fetchWorkspaces();
    hook = render();
    syncWorkspacesRef(hook);
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
