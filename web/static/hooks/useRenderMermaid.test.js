/**
 * Tests for useRenderMermaid.js (mitto-xbz).
 *
 * The hook destructures `useEffect` from `window.preact` at module-load
 * time, so we install a minimal stub that records the registered effect
 * before the first import, mirroring the pattern in
 * useVisibleInterval.test.js. We then invoke the effect body ourselves to
 * exercise the enabled/ref/deps gating without a real Preact render tree.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  jest,
} from "../utils/testing/testGlobals.js";

global.window = global.window || {};

let currentEffects = [];
window.preact = {
  ...(window.preact || {}),
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

async function loadHook() {
  currentEffects = [];
  const mod = await import("./useRenderMermaid.js");
  return mod.useRenderMermaid;
}

// Runs the hook and immediately invokes the single effect it registers,
// mirroring what Preact would do on mount/update.
function mount(useRenderMermaid, ref, deps, enabled) {
  currentEffects = [];
  useRenderMermaid(ref, deps, enabled);
  expect(currentEffects).toHaveLength(1);
  expect(currentEffects[0].deps).toBe(deps);
  currentEffects[0].cb();
}

beforeEach(() => {
  delete window.renderMermaidDiagrams;
});

describe("useRenderMermaid — calls window.renderMermaidDiagrams", () => {
  test("calls it with ref.current when enabled and the container is mounted", async () => {
    const useRenderMermaid = await loadHook();
    const fn = jest.fn();
    window.renderMermaidDiagrams = fn;
    const container = {};
    const ref = { current: container };

    mount(useRenderMermaid, ref, ["dep"], true);

    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(container);
  });

  test("defaults enabled to true when the third argument is omitted", async () => {
    const useRenderMermaid = await loadHook();
    const fn = jest.fn();
    window.renderMermaidDiagrams = fn;
    const ref = { current: {} };

    currentEffects = [];
    useRenderMermaid(ref, ["dep"]);
    currentEffects[0].cb();

    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("useRenderMermaid — guards", () => {
  test("does not call the renderer when enabled is false", async () => {
    const useRenderMermaid = await loadHook();
    const fn = jest.fn();
    window.renderMermaidDiagrams = fn;
    const ref = { current: {} };

    mount(useRenderMermaid, ref, ["dep"], false);

    expect(fn).not.toHaveBeenCalled();
  });

  test("does not call the renderer when ref.current is null (container not mounted)", async () => {
    const useRenderMermaid = await loadHook();
    const fn = jest.fn();
    window.renderMermaidDiagrams = fn;
    const ref = { current: null };

    mount(useRenderMermaid, ref, ["dep"], true);

    expect(fn).not.toHaveBeenCalled();
  });

  test("does not throw and does not call the renderer when ref itself is null/undefined", async () => {
    const useRenderMermaid = await loadHook();
    const fn = jest.fn();
    window.renderMermaidDiagrams = fn;

    expect(() => mount(useRenderMermaid, null, ["dep"], true)).not.toThrow();
    expect(fn).not.toHaveBeenCalled();

    expect(() =>
      mount(useRenderMermaid, undefined, ["dep"], true),
    ).not.toThrow();
    expect(fn).not.toHaveBeenCalled();
  });

  test("does not throw when window.renderMermaidDiagrams is not a function", async () => {
    const useRenderMermaid = await loadHook();
    const ref = { current: {} };
    // window.renderMermaidDiagrams left undefined by beforeEach.

    expect(() => mount(useRenderMermaid, ref, ["dep"], true)).not.toThrow();
  });
});
