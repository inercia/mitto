/**
 * Tests for useCreateMode.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-14 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(). Covers handleSave, the sole network-bearing
 * operation (POST /api/issues), including its conditional body-field
 * assembly (title/parent/assignee/notes/dependencies only added when set).
 *
 * Harness mirrors useIssueComments.test.js. handleSave reads every form
 * field from closed-over state (no explicit args), so the stateful
 * useState cell array is required to drive it through a realistic form-fill
 * scenario.
 */

import {
  describe,
  test,
  expect,
  jest,
} from "../../../utils/testing/testGlobals.js";
import { fakeResponse } from "../../../sdk/testing/fake-server.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let cells;
let cellIdx;
let currentEffects;
window.preact = {
  useState: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = initial;
    const setState = (v) => {
      cells[i] = typeof v === "function" ? v(cells[i]) : v;
    };
    return [cells[i], setState];
  },
  useRef: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = { current: initial };
    return cells[i];
  },
  useCallback: (fn) => fn,
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

let hookMod;
async function render(args) {
  cellIdx = 0;
  currentEffects = [];
  // Cache-busting query: useBeadsDetailPanel.test.js transitively imports the
  // bare "./useCreateMode.js" path (via useBeadsDetailPanel.js's own static
  // import) under a DIFFERENT window.preact stub. Without a distinct query
  // string here, ESM's per-path module cache would hand this file the OTHER
  // test file's already-evaluated module — whose captured
  // useState/useRef/useCallback are bound to that file's `cells` array, not
  // this file's — silently breaking every cross-render assertion.
  hookMod = hookMod || (await import("./useCreateMode.js?slice-s3-test"));
  return hookMod.useCreateMode(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  currentEffects = [];
  global.document.cookie = "mitto_csrf=test-token";
}

function baseArgs(overrides = {}) {
  return {
    isCreating: true,
    createParentId: "",
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    onCreated: jest.fn(),
    onClose: jest.fn(),
    ...overrides,
  };
}

describe("useCreateMode — handleSave", () => {
  test("no-op when description is blank (initial state)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.handleSave();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("minimal form: only type/priority/description in the body", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ body: { id: "mitto-new" } })),
    );
    const onCreated = jest.fn();
    const onClose = jest.fn();
    const args = baseArgs({ onCreated, onClose });
    let bag = await render(args);
    bag.setDescription("A new bug");
    bag = await render(args);

    await bag.handleSave();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues");
    expect(String(url)).toContain(encodeURIComponent("/tmp/wsA"));
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({
      type: "task",
      priority: 2,
      description: "A new bug",
    });
    expect(onCreated).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("full form: title/parent/assignee/notes/dependencies all included, trimmed", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs({ createParentId: "mitto-parent" });
    let bag = await render(args);
    bag.setTitle("  My title  ");
    bag.setType("bug");
    bag.setPriority(0);
    bag.setDescription("  desc  ");
    bag.setCreateAssignee("  alice  ");
    bag.setCreateNotes("  some notes  ");
    bag.setCreateDeps([
      { id: "mitto-dep1", type: "blocks" },
      { id: "mitto-dep2" },
    ]);
    bag = await render(args);

    await bag.handleSave();
    await flush();

    const body = JSON.parse(global.fetch.mock.calls[0][1].body);
    expect(body).toEqual({
      type: "bug",
      priority: 0,
      description: "desc",
      title: "My title",
      parent: "mitto-parent",
      assignee: "alice",
      notes: "some notes",
      dependencies: [
        { id: "mitto-dep1", type: "blocks" },
        { id: "mitto-dep2", type: "blocks" },
      ],
    });
  });

  test("failure: error toast, submitting reset, onCreated/onClose NOT called", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const onCreated = jest.fn();
    const onClose = jest.fn();
    const args = baseArgs({ showToast, onCreated, onClose });
    let bag = await render(args);
    bag.setDescription("desc");
    bag = await render(args);

    await bag.handleSave();
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    expect(onCreated).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    bag = await render(args);
    expect(bag.submitting).toBe(false);
  });
});

describe("useCreateMode — addCreateDep / removeCreateDep", () => {
  test("addCreateDep trims the id, dedupes, and clears the draft", async () => {
    freshMount();
    const args = baseArgs();
    let bag = await render(args);
    bag.setCreateNewDepId("  mitto-dep1  ");
    bag.setCreateNewDepType("related");
    bag = await render(args);

    bag.addCreateDep();
    bag = await render(args);
    expect(bag.createDeps).toEqual([{ id: "mitto-dep1", type: "related" }]);
    expect(bag.createNewDepId).toBe("");

    // Re-adding the same id (post-trim) is a no-op.
    bag.setCreateNewDepId("mitto-dep1");
    bag = await render(args);
    bag.addCreateDep();
    bag = await render(args);
    expect(bag.createDeps).toHaveLength(1);
  });

  test("removeCreateDep filters the given id out of the draft list", async () => {
    freshMount();
    const args = baseArgs();
    let bag = await render(args);
    bag.setCreateDeps([
      { id: "mitto-dep1", type: "blocks" },
      { id: "mitto-dep2", type: "blocks" },
    ]);
    bag = await render(args);

    bag.removeCreateDep("mitto-dep1");
    bag = await render(args);
    expect(bag.createDeps).toEqual([{ id: "mitto-dep2", type: "blocks" }]);
  });
});

describe("useCreateMode — reset-on-enter effect", () => {
  test("resets the form fields when isCreating becomes true", async () => {
    freshMount();
    const args = baseArgs({ isCreating: true });
    let bag = await render(args);
    bag.setTitle("stale title");
    bag.setDescription("stale desc");
    bag = await render(args);

    currentEffects[0].cb();
    bag = await render(args);
    expect(bag.title).toBe("");
    expect(bag.description).toBe("");
    expect(bag.type).toBe("task");
    expect(bag.priority).toBe(2);
  });
});
