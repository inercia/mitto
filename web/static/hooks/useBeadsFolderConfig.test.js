/**
 * Reproduction tests for mitto-xdqx:
 *   Workspaces dialog → Tasks tab stuck on "Loading…", UI freezes because
 *   `beadsConfigLoading` is never reset when the dialog is closed / reopened.
 *
 * The bug lives entirely in the effect wiring of
 * `web/static/hooks/useBeadsFolderConfig.js`:
 *
 *   Bug 1 — the folder-reset useEffect (deps=[selectedFolder]) clears data
 *           state but NEVER resets the four loading/saving flags
 *           (beadsConfigLoading, beadsConfigSaving, beadsUpstreamPromptsLoading,
 *           beadsUpstreamSaving). If an in-flight fetch is orphaned (dialog
 *           closed mid-fetch, WebView reconnect, browser cancel), the
 *           `finally { setBeadsConfigLoading(false) }` in `reloadBeadsConfig`
 *           never runs and the flag latches ON — WorkspaceFolderBeadsTab keeps
 *           rendering the "Loading…" spinner forever.
 *
 *   Bug 2 — the load useEffect (deps=[activeTab, selectedFolder]) does NOT
 *           observe `isOpen`. WorkspacesDialog renders `null` while closed
 *           but keeps the hook mounted, so reopening the dialog on the same
 *           folder with activeTab === "beads" is a no-op — the fetch is never
 *           re-issued and the stuck flag from Bug 1 stays visible.
 *
 * Strategy: the hook does `const { useState, useEffect } = window.preact;`
 * so we stub `window.preact` to capture every setter (in declaration order)
 * and every effect (callback + deps). We then invoke the reset effect body
 * directly and assert the four loading/saving setters WERE called with
 * `false`, and assert the load effect's deps INCLUDE `isOpen`. Both
 * assertions currently fail — that is the reproduction.
 */

import {
  describe,
  test,
  expect,
  jest,
} from "../utils/testing/testGlobals.js";

// Minimal environment for the module and the transitive utils barrel it pulls in.
global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

// Positional setter indices matching the useState declaration order in
// `useBeadsFolderConfig.js`. Keep in sync with the source if a state slot
// is added/reordered.
const IDX = {
  setBeadsConfig: 0,
  setBeadsConfigLoading: 1,
  setBeadsConfigError: 2,
  setBeadsConfigSaving: 3,
  setNewBeadsKey: 4,
  setNewBeadsValue: 5,
  setBeadsUpstream: 6,
  setBeadsUpstreamSaving: 7,
  setBeadsPullPrompt: 8,
  setBeadsPushPrompt: 9,
  setBeadsSyncPrompt: 10,
  setBeadsPullPromptArgs: 11,
  setBeadsPushPromptArgs: 12,
  setBeadsSyncPromptArgs: 13,
  setBeadsUpstreamPrompts: 14,
  setBeadsUpstreamPromptsLoading: 15,
};

// The hook destructures `useState`/`useEffect` from `window.preact` at
// MODULE-LOAD time (line 15 of the source). ESM caches the module, so we
// must install a stub once — before the first import — whose `useState`
// and `useEffect` functions push into mutable buckets we reset per test.
let currentSetters = [];
let currentEffects = [];
let currentRefs = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
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

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  currentRefs = [];
  const mod = await import("./useBeadsFolderConfig.js");
  return {
    useBeadsFolderConfig: mod.useBeadsFolderConfig,
    setters: currentSetters,
    effects: currentEffects,
    refs: currentRefs,
  };
}

describe("mitto-xdqx — Workspaces dialog Tasks tab stuck on 'Loading…'", () => {
  test("Bug 1: folder-reset effect resets all four loading/saving flags", async () => {
    const { useBeadsFolderConfig, setters, effects } = await loadHook();
    useBeadsFolderConfig({
      selectedFolder: "agentgateway",
      activeTab: "beads",
      getSelectedFolderDir: () => null,
    });

    // Reset effect is the one whose deps array has exactly one entry
    // (`selectedFolder`); the two load-effects have length 2 and 3.
    const resetEffect = effects.find((e) => e.deps && e.deps.length === 1);
    expect(resetEffect).toBeDefined();

    resetEffect.cb();

    // Bug 1: the current reset body does NOT invoke the four flag setters,
    // so these expectations fail — the "Loading…" spinner never clears.
    expect(setters[IDX.setBeadsConfigLoading]).toHaveBeenCalledWith(false);
    expect(setters[IDX.setBeadsConfigSaving]).toHaveBeenCalledWith(false);
    expect(setters[IDX.setBeadsUpstreamPromptsLoading]).toHaveBeenCalledWith(
      false,
    );
    expect(setters[IDX.setBeadsUpstreamSaving]).toHaveBeenCalledWith(false);
  });

  test("Bug 2: load effect deps include isOpen so it re-fires on dialog reopen", async () => {
    const { useBeadsFolderConfig, effects } = await loadHook();
    // Sentinel value for isOpen — if the hook threads isOpen into the load
    // effect's deps, we should see this exact value in that effect's dep array.
    const isOpenSentinel = "ISOPEN-SENTINEL";
    useBeadsFolderConfig({
      selectedFolder: "agentgateway",
      activeTab: "beads",
      isOpen: isOpenSentinel,
      getSelectedFolderDir: () => null,
    });

    // Identify the load effect: it depends on both activeTab ("beads") and
    // selectedFolder ("agentgateway"); the reset effect has length 1 and the
    // upstream-prompts effect additionally depends on beadsUpstream ("none").
    const loadEffect = effects.find(
      (e) =>
        Array.isArray(e.deps) &&
        e.deps.includes("beads") &&
        e.deps.includes("agentgateway") &&
        !e.deps.includes("none"),
    );
    expect(loadEffect).toBeDefined();

    // Bug 2: currently loadEffect.deps === ["beads","agentgateway"] and does
    // NOT include isOpen, so reopening the dialog on the same folder does
    // not re-issue the fetch. This assertion fails until isOpen is threaded
    // through the hook and added to the deps array.
    expect(loadEffect.deps).toContain(isOpenSentinel);
  });

  test("Bug 3: dialog-close effect force-clears loading flags on isOpen=false", async () => {
    // When the user closes the dialog mid-fetch, the load token bumps and the
    // spinner flags reset, so reopening the dialog never shows a stale
    // "Loading…" (mitto-xdqx follow-up).
    const { useBeadsFolderConfig, setters, effects } = await loadHook();
    useBeadsFolderConfig({
      selectedFolder: "agentgateway",
      activeTab: "beads",
      isOpen: false,
      getSelectedFolderDir: () => null,
    });

    // The dialog-close effect is the one with deps === [false] (single-entry
    // dep array whose only value is the isOpen boolean).
    const closeEffect = effects.find(
      (e) =>
        Array.isArray(e.deps) && e.deps.length === 1 && e.deps[0] === false,
    );
    expect(closeEffect).toBeDefined();

    closeEffect.cb();

    expect(setters[IDX.setBeadsConfigLoading]).toHaveBeenCalledWith(false);
    expect(setters[IDX.setBeadsConfigSaving]).toHaveBeenCalledWith(false);
    expect(setters[IDX.setBeadsUpstreamPromptsLoading]).toHaveBeenCalledWith(
      false,
    );
    expect(setters[IDX.setBeadsUpstreamSaving]).toHaveBeenCalledWith(false);
  });

  test("Bug 3: dialog-close effect is a no-op while dialog is open", async () => {
    const { useBeadsFolderConfig, setters, effects } = await loadHook();
    useBeadsFolderConfig({
      selectedFolder: "agentgateway",
      activeTab: "beads",
      isOpen: true,
      getSelectedFolderDir: () => null,
    });

    const closeEffect = effects.find(
      (e) => Array.isArray(e.deps) && e.deps.length === 1 && e.deps[0] === true,
    );
    expect(closeEffect).toBeDefined();

    // Reset the call counts recorded during hook initialization so we can
    // isolate what the close-effect body itself does (nothing, since isOpen=true).
    setters.forEach((s) => s.mockClear());
    closeEffect.cb();
    expect(setters[IDX.setBeadsConfigLoading]).not.toHaveBeenCalled();
    expect(setters[IDX.setBeadsConfigSaving]).not.toHaveBeenCalled();
    expect(setters[IDX.setBeadsUpstreamPromptsLoading]).not.toHaveBeenCalled();
    expect(setters[IDX.setBeadsUpstreamSaving]).not.toHaveBeenCalled();
  });

  test("Bug 3: folder-reset effect bumps load tokens to invalidate orphaned fetches", async () => {
    const { useBeadsFolderConfig, effects, refs } = await loadHook();
    useBeadsFolderConfig({
      selectedFolder: "agentgateway",
      activeTab: "beads",
      isOpen: true,
      getSelectedFolderDir: () => null,
    });

    // Three refs are declared in the hook (in order): configLoadTokenRef,
    // upstreamLoadTokenRef, upstreamPromptsLoadTokenRef. All start at 0.
    expect(refs.length).toBeGreaterThanOrEqual(3);
    const before = refs.slice(0, 3).map((r) => r.current);

    const resetEffect = effects.find((e) => e.deps && e.deps.length === 1);
    resetEffect.cb();

    // Each token should have been bumped by exactly one so any late-resolving
    // fetch captured under the previous token is now stale and its finally()
    // will not re-latch the loading spinner.
    const after = refs.slice(0, 3).map((r) => r.current);
    expect(after).toEqual(before.map((v) => v + 1));
  });
});
