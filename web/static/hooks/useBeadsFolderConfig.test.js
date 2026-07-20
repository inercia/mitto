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

import { jest } from "@jest/globals";

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
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useBeadsFolderConfig.js");
  return {
    useBeadsFolderConfig: mod.useBeadsFolderConfig,
    setters: currentSetters,
    effects: currentEffects,
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
});
