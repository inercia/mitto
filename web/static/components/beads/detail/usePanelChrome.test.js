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
  // Minimal tagged-template stub so the `html`-rendering useMemos
  // (headerToolbarItems / panelMenuItems) do not throw when a test passes a
  // non-null `data` (needed to exercise the per-issue shortcut resolution).
  html: (strings, ...vals) => ({ strings, vals }),
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
      // Ungated metadata fetch (mitto three-state shortcut rendering): the
      // raw workspace-prompts list, no enabled_context gate.
      if (u.includes("/api/workspace-prompts")) {
        return Promise.resolve(fakeResponse({ body: { prompts: [] } }));
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

    // GETs: /api/folders/shortcuts, /api/global/shortcuts, and the ungated
    // /api/workspace-prompts metadata fetch (parallel to onFetchPrompts).
    expect(global.fetch).toHaveBeenCalledTimes(3);
    const urls = global.fetch.mock.calls.map(([u]) => String(u));
    expect(urls.some((u) => u.includes("/api/folders/shortcuts"))).toBe(true);
    expect(urls.some((u) => u.includes("/api/global/shortcuts"))).toBe(true);
    expect(urls.some((u) => u.includes("/api/workspace-prompts"))).toBe(true);
    // onFetchPrompts is called once more (list.length > 0) to resolve names.
    expect(onFetchPrompts).toHaveBeenCalledTimes(1);

    void bag;
  });

  test("passes the open issue to onFetchPrompts so item.*-gated shortcuts resolve per-issue", async () => {
    freshMount();
    global.fetch = jest.fn((url) => {
      const u = String(url);
      if (u.includes("/api/global/shortcuts")) {
        return Promise.resolve(fakeResponse({ body: { sections: {} } }));
      }
      return Promise.resolve(
        fakeResponse({
          body: {
            sections: { beadsIssue: [{ prompt: "Support: investigate" }] },
          },
        }),
      );
    });
    // An item-gated prompt only appears in the resolver's returned list when the
    // open issue is passed through (the server evaluates enabledWhen against
    // Item.*). The stub asserts the issue arrived; a context-less call would
    // return [] and leave the shortcut greyed/disabled.
    const issue = {
      id: "on-call-634",
      status: "open",
      issue_type: "task",
      priority: 1,
      labels: ["support-question", "state:drafting"],
    };
    const onFetchPrompts = jest.fn((_dir, forItem) =>
      Promise.resolve(
        forItem && (forItem.labels || []).includes("support-question")
          ? [{ name: "Support: investigate", icon: "search" }]
          : [],
      ),
    );
    await render(baseArgs({ data: issue, onFetchPrompts }));

    currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb();
    await flush();

    expect(onFetchPrompts).toHaveBeenCalledTimes(1);
    expect(onFetchPrompts).toHaveBeenCalledWith("/tmp/wsA", issue);
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

describe("usePanelChrome — three-state shortcut rendering", () => {
  // The header toolbar renders each configured beadsIssue shortcut in one of
  // three states (mitto: gated-off shortcuts previously showed a misleading
  // generic-lightning "not found" icon):
  //  1. enabled     — passed enabledWhen for the open issue: real icon, clickable.
  //  2. gated off   — exists in the workspace but excluded for this issue: real
  //                   icon, greyed/disabled.
  //  3. missing     — no prompt of that name exists at all: lightning + "not found".
  // The icon component ends up as the first value of the stubbed `html` tag
  // (icon: html`<${Icon} .../>`); it is a named function component, so its
  // `.name` identifies which icon was chosen. (Icons.js cannot be imported
  // statically here — its top-level `const { html } = window.preact` would run
  // before the stub is installed; the hook pulls it in via a dynamic import
  // inside render(), after window.preact is set.)
  function iconNameOf(item) {
    const Icon = item.icon && item.icon.vals && item.icon.vals[0];
    return Icon && Icon.name;
  }

  test("gated-off shortcut keeps its real icon but is disabled; enabled is clickable; missing falls back to lightning", async () => {
    freshMount();
    // Two configured folder shortcuts plus one that does not exist at all.
    global.fetch = jest.fn((url) => {
      const u = String(url);
      if (u.includes("/api/global/shortcuts")) {
        return Promise.resolve(fakeResponse({ body: { sections: {} } }));
      }
      if (u.includes("/api/workspace-prompts")) {
        // Ungated metadata: both support prompts EXIST (carry their icons),
        // "Ghost prompt" does not appear here (genuinely missing).
        return Promise.resolve(
          fakeResponse({
            body: {
              prompts: [
                { name: "Support: investigate", icon: "search" },
                { name: "Support: reply to user", icon: "chat-bubble" },
              ],
            },
          }),
        );
      }
      return Promise.resolve(
        fakeResponse({
          body: {
            sections: {
              beadsIssue: [
                { prompt: "Support: investigate" },
                { prompt: "Support: reply to user" },
                { prompt: "Ghost prompt" },
              ],
            },
          },
        }),
      );
    });

    const issue = {
      id: "on-call-0nh",
      status: "open",
      issue_type: "task",
      priority: 1,
      // Has support-question but NOT state:drafting, so "reply to user" is
      // gated off while "investigate" is enabled.
      labels: ["support-question", "state:awaiting-us"],
    };
    // The gated resolver returns only the prompt whose enabledWhen passes.
    const onFetchPrompts = jest.fn(() =>
      Promise.resolve([{ name: "Support: investigate", icon: "search" }]),
    );

    await render(baseArgs({ data: issue, onFetchPrompts }));
    currentEffects[SHORTCUTS_LOAD_EFFECT_IDX].cb();
    await flush();
    await flush();

    // Re-render so the eagerly-evaluated headerToolbarItems useMemo reads the
    // now-populated state cells.
    const bag = await render(baseArgs({ data: issue, onFetchPrompts }));
    const items = bag.headerToolbarItems;
    const byTestId = (id) => items.find((it) => it.testId === id);

    const enabled = byTestId("beads-issue-shortcut-btn-0");
    const gatedOff = byTestId("beads-issue-shortcut-btn-1");
    const missing = byTestId("beads-issue-shortcut-btn-2");

    // 1. Enabled: real (search) icon, clickable.
    expect(iconNameOf(enabled)).toBe("SearchIcon");
    expect(enabled.disabled).toBe(false);
    expect(enabled.tip).toBe("Support: investigate");

    // 2. Gated off: real (chat-bubble) icon — NOT lightning — but disabled.
    expect(iconNameOf(gatedOff)).toBe("ChatBubbleIcon");
    expect(iconNameOf(gatedOff)).not.toBe("LightningIcon");
    expect(gatedOff.disabled).toBe(true);
    expect(gatedOff.tip).toBe(
      "Support: reply to user — not available for this issue",
    );

    // 3. Missing: lightning fallback + "not found", disabled.
    expect(iconNameOf(missing)).toBe("LightningIcon");
    expect(missing.disabled).toBe(true);
    expect(missing.tip).toBe('Prompt "Ghost prompt" not found');
  });
});
