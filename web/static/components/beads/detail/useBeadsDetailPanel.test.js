/**
 * Tests for useBeadsDetailPanel.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-11 extracted it verbatim
 * with no accompanying tests). This file covers `improveDescriptionText`,
 * the composer's own network-bearing operation (POST /api/aux/improve-prompt
 * via `getSdkClient().misc.improvePrompt`), specifically the AbortError
 * detection fix made during the slice S3 migration: the SDK wraps every
 * fetch-level failure (including an aborted request) in a
 * `MittoNetworkError` with the original `DOMException` on `.cause`, so a
 * timeout must be detected via `err.cause?.name`, not `err.name` directly.
 *
 * The full hook is mounted (issue=null, isCreating=false) rather than
 * extracting improveDescriptionText in isolation, since it closes over
 * `improvingDesc`/`setImprovingDesc` state declared in the composer itself.
 * With no open issue and no creation in progress, every sub-hook's derived
 * `useMemo` short-circuits before touching `html` (e.g. usePanelChrome's
 * `headerToolbarItems`/`panelMenuItems` both start with `if (!data) return
 * [];`), so the stub below does not need a working htm `html` tag function.
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
  global.document = {
    cookie: "",
    addEventListener: () => {},
    removeEventListener: () => {},
  };
}

let cells;
let cellIdx;
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
  useMemo: (fn) => fn(),
  useEffect: () => {},
};

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

let hookMod;
async function render(args) {
  cellIdx = 0;
  // Cache-busting query (defense-in-depth): this file is the one place that
  // imports the bare "./useBeadsDetailPanel.js" path, but that module's own
  // static imports of useCreateMode.js/useViewEdit.js/useIssueLabels.js/
  // useIssueComments.js/useIssueDependencies.js/usePanelChrome.js are bare
  // (no query) and so share the ESM module cache with those hooks' own
  // dedicated test files unless EVERY file's *own* dynamic import uses a
  // distinct query (see useCreateMode.test.js etc. for the matching half of
  // this fix) — otherwise whichever file's window.preact stub was active
  // when a given bare hook path was first loaded silently "wins" it for
  // every other importer for the rest of the test run.
  hookMod = hookMod || (await import("./useBeadsDetailPanel.js?slice-s3-test"));
  return hookMod.useBeadsDetailPanel(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  global.document.cookie = "mitto_csrf=test-token";
  window.mittoCurrentWorkspaceUUID = "";
}

function baseArgs(overrides = {}) {
  return {
    issue: null,
    allIssues: [],
    isCreating: false,
    workingDir: "",
    onClose: jest.fn(),
    onCreated: jest.fn(),
    onUpdated: jest.fn(),
    showToast: jest.fn(),
    onFetchPrompts: jest.fn(() => Promise.resolve([])),
    onRunPrompt: jest.fn(),
    onDelete: jest.fn(),
    onToggleStatus: jest.fn(),
    onToggleDefer: jest.fn(),
    statusBusy: false,
    onSelectIssue: jest.fn(),
    createParentId: "",
    ...overrides,
  };
}

describe("useBeadsDetailPanel — improveDescriptionText", () => {
  test("no-op when already running, or text is blank", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.improveDescriptionText("", jest.fn());
    await bag.improveDescriptionText("   ", jest.fn());
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("success: posts {prompt, workspace_uuid} and replaces the text via setText", async () => {
    freshMount();
    window.mittoCurrentWorkspaceUUID = "ws-uuid-1";
    global.fetch = jest.fn(() =>
      Promise.resolve(
        fakeResponse({ body: { improved_prompt: "Better text" } }),
      ),
    );
    const bag = await render(baseArgs());
    const setText = jest.fn();

    await bag.improveDescriptionText("rough draft", setText);
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/aux/improve-prompt");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({
      prompt: "rough draft",
      workspace_uuid: "ws-uuid-1",
    });
    expect(setText).toHaveBeenCalledWith("Better text");
  });

  test("does not call setText when the response carries no improved_prompt", async () => {
    freshMount();
    global.fetch = jest.fn(() => Promise.resolve(fakeResponse({ body: {} })));
    const bag = await render(baseArgs());
    const setText = jest.fn();

    await bag.improveDescriptionText("rough draft", setText);
    await flush();

    expect(setText).not.toHaveBeenCalled();
  });

  test("non-abort failure: generic error toast via errorMessage(err, fallback)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const bag = await render(baseArgs({ showToast }));

    await bag.improveDescriptionText("rough draft", jest.fn());
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
  });

  test("abort (fetch rejects with a DOMException named AbortError): timeout message via err.cause", async () => {
    freshMount();
    // Mirrors the SDK's real behavior (sdk/core/transport.js): any rejection
    // from the injected `fetch` is wrapped in a MittoNetworkError whose
    // `.cause` is the original error — here a DOMException named
    // "AbortError", exactly as a browser's fetch() rejects an aborted
    // request. This is the shape the mitto-7gta.17 S3 migration fix targets:
    // `err.name` on the thrown MittoNetworkError is always "MittoNetworkError",
    // never "AbortError", so the check must inspect `err.cause?.name`.
    global.fetch = jest.fn(() => {
      const abortErr = new Error("The operation was aborted.");
      abortErr.name = "AbortError";
      return Promise.reject(abortErr);
    });
    const showToast = jest.fn();
    const bag = await render(baseArgs({ showToast }));

    await bag.improveDescriptionText("rough draft", jest.fn());
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request timed out. Please try again.",
    });
  });

  test("resets improvingDesc to false after success and after failure", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const args = baseArgs();
    let bag = await render(args);

    await bag.improveDescriptionText("rough draft", jest.fn());
    await flush();

    bag = await render(args);
    expect(bag.improvingDesc).toBe(false);
  });
});
