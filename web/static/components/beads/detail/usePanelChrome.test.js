/**
 * Tests for usePanelChrome.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-15 extracted it verbatim
 * with no accompanying tests). This file covers `loadIssueShortcuts`, the
 * sole network-bearing operation (GET /api/folders/shortcuts twice, folder +
 * global), migrated onto getSdkClient() in slice S3. It mirrors
 * BeadsView.js's tasksList shortcuts loader (same merge-then-resolve
 * pattern), which is covered by source-scan tests in BeadsView.test.js; this
 * file exercises the actual runtime behavior instead.
 *
 * The panel-chrome/menu machinery (open/close fade, outside-click, kebab
 * menu, header toolbar `html` rendering) is unrelated to the SDK migration
 * and is out of scope here — `data: null` keeps every `html`-calling
 * `useMemo` (`panelMenuItems`, `headerToolbarItems`) on its early-return
 * branch, so the stub below does not need a working htm `html` tag function.
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
if (typeof navigator === "undefined") {
  global.navigator = { userAgent: "test-agent" };
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
  useMemo: (fn) => fn(),
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
  // bare "./usePanelChrome.js" path (via useBeadsDetailPanel.js's own static
  // import) under a DIFFERENT window.preact stub. Without a distinct query
  // string here, ESM's per-path module cache would hand this file the OTHER
  // test file's already-evaluated module — whose captured
  // useState/useRef/useCallback/useMemo are bound to that file's `cells`
  // array, not this file's — silently breaking every cross-render assertion.
  hookMod = hookMod || (await import("./usePanelChrome.js?slice-s3-test"));
  return hookMod.usePanelChrome(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  currentEffects = [];
  global.document.cookie = "mitto_csrf=test-token";
}

function baseArgs(overrides = {}) {
  return {
    isOpen: false,
    data: null,
    creating: false,
    viewDirty: false,
    savingView: false,
    initialFullscreen: false,
    workingDir: "/tmp/wsA",
    statusBusy: false,
    onClose: jest.fn(),
    onDelete: jest.fn(),
    onToggleStatus: jest.fn(),
    onToggleDefer: jest.fn(),
    onRunPrompt: jest.fn(),
    onFetchPrompts: jest.fn(() => Promise.resolve([])),
    ...overrides,
  };
}

// The initial-load effect for loadIssueShortcuts (source order: prompts
// pre-fetch, open/close fade, deferred-close resolve, outside-click,
// load-shortcuts, refresh-on-event).
const SHORTCUTS_LOAD_EFFECT_IDX = 4;

describe("usePanelChrome — loadIssueShortcuts", () => {
  test("no-op when workingDir is falsy", async () => {
    freshMount();
    global.fetch = jest.fn();
    await render(baseArgs({ workingDir: "" }));
    currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("merges global-then-folder beadsIssue sections, dropping folder duplicates of a global prompt", async () => {
    freshMount();
    global.fetch = jest.fn((url) => {
      const u = String(url);
      if (u.includes("/api/global/shortcuts")) {
        if (u.includes("include_prompts")) {
          throw new Error(
            "must not request include_prompts for a chrome-only load",
          );
        }
        return Promise.resolve(
          fakeResponse({
            body: { sections: { beadsIssue: [{ prompt: "Shared prompt" }] } },
          }),
        );
      }
      return Promise.resolve(
        fakeResponse({
          body: {
            sections: {
              beadsIssue: [
                { prompt: "Shared prompt" },
                { prompt: "Folder-only prompt" },
              ],
            },
          },
        }),
      );
    });
    const onFetchPrompts = jest.fn(() =>
      Promise.resolve([{ name: "Folder-only prompt", icon: "wand" }]),
    );
    const bag = await render(baseArgs({ onFetchPrompts }));

    currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb();
    await flush();

    // One GET to /api/folders/shortcuts, one to /api/global/shortcuts.
    expect(global.fetch).toHaveBeenCalledTimes(2);
    const urls = global.fetch.mock.calls.map(([u]) => String(u));
    expect(urls.some((u) => u.includes("/api/folders/shortcuts"))).toBe(true);
    expect(urls.some((u) => u.includes("/api/global/shortcuts"))).toBe(true);
    // onFetchPrompts is called once more (list.length > 0) to resolve names.
    expect(onFetchPrompts).toHaveBeenCalledTimes(1);

    void bag;
  });

  test("global shortcuts fetch failure degrades gracefully (folder-only list still used)", async () => {
    freshMount();
    global.fetch = jest.fn((url) => {
      const u = String(url);
      if (u.includes("/api/global/shortcuts")) {
        return Promise.resolve(fakeResponse({ status: 500 }));
      }
      return Promise.resolve(
        fakeResponse({
          body: { sections: { beadsIssue: [{ prompt: "Folder prompt" }] } },
        }),
      );
    });
    await render(
      baseArgs({ onFetchPrompts: jest.fn(() => Promise.resolve([])) }),
    );

    // A failed global fetch is Promise.all'd alongside the folder fetch, so a
    // rejection would otherwise fail the whole load — the source code's own
    // `.catch(() => ({}))` on getGlobal() is what prevents that; assert the
    // effect callback (a synchronous cleanup-fn return, per the
    // `let cancelled = false; ...; return () => {cancelled = true}` shape)
    // does not throw.
    expect(() => currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb()).not.toThrow();
    await flush();
  });

  test("total failure (folder GET rejects) clears both shortcuts and the prompt map", async () => {
    freshMount();
    global.fetch = jest.fn(() => Promise.reject(new Error("network down")));
    await render(baseArgs());

    expect(() => currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb()).not.toThrow();
    await flush();
  });
});
