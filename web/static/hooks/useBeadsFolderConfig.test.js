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

    // Three token refs are declared first in the hook (in order):
    // configLoadTokenRef, upstreamLoadTokenRef, upstreamPromptsLoadTokenRef.
    // All start at 0. Six additional refs mirror the upstream-prompt values.
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

// -----------------------------------------------------------------------------
// Regression tests for the disabled-buttons-after-save bug: user modifies the
// Tasks-tab prompt selection in the Workspaces dialog and the BeadsView
// pull/push/sync buttons render disabled because their `pullPromptName` /
// `pushPromptName` / `syncPromptName` state is empty. Root cause: the three
// save handlers (saveBeadsPromptName, saveBeadsUpstream, saveBeadsPromptArgs)
// used to PUT the FULL upstream body reading the untouched fields from a
// render-time closure — so when the user changed a select before the initial
// reloadBeadsUpstream GET had resolved, the closure held "" for the other two
// prompt names and the backend persisted a wiped Beads block. The fix routes
// every save through refs mirroring the six upstream-prompt values.
// -----------------------------------------------------------------------------
describe("saveBeadsPromptName / saveBeadsUpstream / saveBeadsPromptArgs read from refs, not closure", () => {
  async function primeHookWithLoadedPrompts({ isOpen = true } = {}) {
    const { useBeadsFolderConfig, refs, setters } = await loadHook();
    const result = useBeadsFolderConfig({
      selectedFolder: "myfolder",
      activeTab: "beads",
      isOpen,
      getSelectedFolderDir: () => "/tmp/myfolder",
    });
    // Ref layout: [0..2] = load tokens, [3..8] = prompt/args mirrors, in the
    // order: pull, push, sync, pullArgs, pushArgs, syncArgs. Simulate a
    // completed reloadBeadsUpstream by populating the mirror refs directly —
    // this is what the GET response handler now writes into.
    refs[3].current = "PullOne";
    refs[4].current = "PushOne";
    refs[5].current = "SyncOne";
    refs[6].current = { A: "1" };
    refs[7].current = { B: "2" };
    refs[8].current = { C: "3" };
    return { result, refs, setters };
  }

  test("saveBeadsPromptName sends the OTHER two prompt names from refs, not empty closure state", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      if (method === "PUT") {
        putCalls.push({ url, body: opts && opts.body });
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ upstream: "prompts" }),
        });
      }
      // Never-resolving GET simulates the pre-fix race where the initial
      // reloadBeadsUpstream has not yet populated state at the time of PUT.
      return new Promise(() => {});
    });

    const { result } = await primeHookWithLoadedPrompts();
    await result.beadsHandlers.saveBeadsPromptName("push_prompt", "PushTwo");

    expect(putCalls).toHaveLength(1);
    const body = JSON.parse(putCalls[0].body);
    expect(body.upstream).toBe("prompts");
    expect(body.push_prompt).toBe("PushTwo");
    // These two assertions are the regression: the closure would have read
    // "" from render-1 state, but refs preserve the LATEST committed values.
    expect(body.pull_prompt).toBe("PullOne");
    expect(body.sync_prompt).toBe("SyncOne");
    expect(body.pull_prompt_args).toEqual({ A: "1" });
    expect(body.push_prompt_args).toEqual({ B: "2" });
    expect(body.sync_prompt_args).toEqual({ C: "3" });
  });

  test("saveBeadsUpstream(prompts) preserves prompt names + args from refs", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url, body: opts.body });
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({
            upstream: "prompts",
            pull_prompt: "PullOne",
            push_prompt: "PushOne",
            sync_prompt: "SyncOne",
          }),
        });
      }
      return new Promise(() => {});
    });

    const { result } = await primeHookWithLoadedPrompts();
    await result.beadsHandlers.saveBeadsUpstream("prompts");

    expect(putCalls).toHaveLength(1);
    const body = JSON.parse(putCalls[0].body);
    expect(body.upstream).toBe("prompts");
    expect(body.pull_prompt).toBe("PullOne");
    expect(body.push_prompt).toBe("PushOne");
    expect(body.sync_prompt).toBe("SyncOne");
  });

  test("saveBeadsPromptArgs sends the OTHER prompt names + arg maps from refs", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url, body: opts.body });
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ upstream: "prompts" }),
        });
      }
      return new Promise(() => {});
    });

    const { result } = await primeHookWithLoadedPrompts();
    await result.beadsHandlers.saveBeadsPromptArgs("push_prompt", {
      NEW: "yes",
    });

    expect(putCalls).toHaveLength(1);
    const body = JSON.parse(putCalls[0].body);
    expect(body.upstream).toBe("prompts");
    expect(body.push_prompt_args).toEqual({ NEW: "yes" });
    expect(body.pull_prompt).toBe("PullOne");
    expect(body.push_prompt).toBe("PushOne");
    expect(body.sync_prompt).toBe("SyncOne");
    expect(body.pull_prompt_args).toEqual({ A: "1" });
    expect(body.sync_prompt_args).toEqual({ C: "3" });
  });

  test("saveBeadsPromptName updates its own ref so a subsequent save reads the latest value", async () => {
    global.document.cookie = "mitto_csrf=test-token";
    const putCalls = [];
    global.fetch = jest.fn((url, opts) => {
      if (opts && opts.method === "PUT") {
        putCalls.push({ url, body: opts.body });
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ upstream: "prompts" }),
        });
      }
      return new Promise(() => {});
    });

    const { result, refs } = await primeHookWithLoadedPrompts();

    await result.beadsHandlers.saveBeadsPromptName("pull_prompt", "PullTwo");
    // Ref for pull is updated so a subsequent save for push does not
    // reintroduce the previous pull value.
    expect(refs[3].current).toBe("PullTwo");

    await result.beadsHandlers.saveBeadsPromptName("push_prompt", "PushTwo");
    expect(putCalls).toHaveLength(2);
    const body2 = JSON.parse(putCalls[1].body);
    expect(body2.pull_prompt).toBe("PullTwo");
    expect(body2.push_prompt).toBe("PushTwo");
    expect(body2.sync_prompt).toBe("SyncOne");
  });
});
